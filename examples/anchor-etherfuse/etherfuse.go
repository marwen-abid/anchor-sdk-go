package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// EtherfuseClient wraps all Etherfuse FX Ramp API interactions.
type EtherfuseClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewEtherfuseClient creates a new Etherfuse API client.
func NewEtherfuseClient(apiKey, baseURL string) *EtherfuseClient {
	return &EtherfuseClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// --- Onboarding ---

type onboardingURLRequest struct {
	CustomerID    string `json:"customerId"`
	BankAccountID string `json:"bankAccountId"`
	PublicKey     string `json:"publicKey"`
	Blockchain    string `json:"blockchain"`
}

type onboardingURLResponse struct {
	PresignedURL string `json:"presigned_url"`
}

// GetOnboardingURL generates a presigned URL for customer onboarding.
// The URL is valid for 15 minutes.
func (c *EtherfuseClient) GetOnboardingURL(ctx context.Context, customerID, bankAccountID, publicKey string) (string, error) {
	req := onboardingURLRequest{
		CustomerID:    customerID,
		BankAccountID: bankAccountID,
		PublicKey:     publicKey,
		Blockchain:    "stellar",
	}
	var resp onboardingURLResponse
	if err := c.post(ctx, "/ramp/onboarding-url", req, &resp); err != nil {
		return "", fmt.Errorf("get onboarding URL: %w", err)
	}
	return resp.PresignedURL, nil
}

// --- Quotes ---

// QuoteAssets defines the asset pair for a quote.
type QuoteAssets struct {
	Type        string `json:"type"` // "onramp" or "offramp"
	SourceAsset string `json:"sourceAsset"`
	TargetAsset string `json:"targetAsset"`
}

// QuoteRequest for POST /ramp/quote.
type QuoteRequest struct {
	QuoteID      string      `json:"quoteId"`
	CustomerID   string      `json:"customerId"`
	Blockchain   string      `json:"blockchain"`
	QuoteAssets  QuoteAssets `json:"quoteAssets"`
	SourceAmount string      `json:"sourceAmount"`
}

// QuoteResponse from POST /ramp/quote.
type QuoteResponse struct {
	QuoteID                   string `json:"quoteId"`
	SourceAmount              string `json:"sourceAmount"`
	DestinationAmount         string `json:"destinationAmount"`
	ExchangeRate              string `json:"exchangeRate"`
	FeeBps                    string `json:"feeBps"`
	FeeAmount                 string `json:"feeAmount"`
	DestinationAmountAfterFee string `json:"destinationAmountAfterFee"`
	ExpiresAt                 string `json:"expiresAt"`
}

// CreateQuote creates a quote for an onramp or offramp conversion.
// Quotes expire after 2 minutes.
func (c *EtherfuseClient) CreateQuote(ctx context.Context, req QuoteRequest) (*QuoteResponse, error) { //nolint:gocritic
	req.Blockchain = "stellar"
	var resp QuoteResponse
	if err := c.post(ctx, "/ramp/quote", req, &resp); err != nil {
		return nil, fmt.Errorf("create quote: %w", err)
	}
	return &resp, nil
}

// --- Orders ---

// OrderRequest for POST /ramp/order.
type OrderRequest struct {
	OrderID       string `json:"orderId"`
	BankAccountID string `json:"bankAccountId"`
	PublicKey     string `json:"publicKey"`
	QuoteID       string `json:"quoteId"`
	UseAnchor     bool   `json:"useAnchor,omitempty"`
}

// OnrampOrderResult from a deposit order.
type OnrampOrderResult struct {
	OrderID       string      `json:"orderId"`
	DepositClabe  string      `json:"depositClabe"`
	DepositAmount json.Number `json:"depositAmount"`
}

// OfframpOrderResult from a withdrawal order.
type OfframpOrderResult struct {
	OrderID               string `json:"orderId"`
	WithdrawAnchorAccount string `json:"withdrawAnchorAccount,omitempty"`
	WithdrawMemo          string `json:"withdrawMemo,omitempty"`
	WithdrawMemoType      string `json:"withdrawMemoType,omitempty"`
}

// orderResponse wraps the discriminated union response from POST /ramp/order.
type orderResponse struct {
	Onramp  *OnrampOrderResult  `json:"onramp,omitempty"`
	Offramp *OfframpOrderResult `json:"offramp,omitempty"`
}

// CreateOnrampOrder creates a deposit order (MXN → crypto).
// Returns the CLABE number and amount for the user to send MXN via SPEI.
func (c *EtherfuseClient) CreateOnrampOrder(ctx context.Context, req OrderRequest) (*OnrampOrderResult, error) {
	var resp orderResponse
	if err := c.post(ctx, "/ramp/order", req, &resp); err != nil {
		return nil, fmt.Errorf("create onramp order: %w", err)
	}
	if resp.Onramp == nil {
		return nil, errors.New("unexpected response: missing onramp field")
	}
	return resp.Onramp, nil
}

// CreateOfframpOrder creates a withdrawal order (crypto → MXN).
// UseAnchor is set to true to receive withdraw anchor details directly in the response.
func (c *EtherfuseClient) CreateOfframpOrder(ctx context.Context, req OrderRequest) (*OfframpOrderResult, error) {
	req.UseAnchor = true
	var resp orderResponse
	if err := c.post(ctx, "/ramp/order", req, &resp); err != nil {
		return nil, fmt.Errorf("create offramp order: %w", err)
	}
	if resp.Offramp == nil {
		return nil, errors.New("unexpected response: missing offramp field")
	}
	return resp.Offramp, nil
}

// --- KYC Status ---

// KYCStatus from GET /ramp/customer/{id}/kyc/{pubkey}.
type KYCStatus struct {
	CustomerID             string `json:"customerId"`
	WalletPublicKey        string `json:"walletPublicKey"`
	Status                 string `json:"status"` // "not_started", "proposed", "approved", "rejected"
	CurrentRejectionReason string `json:"currentRejectionReason"`
}

// GetKYCStatus checks the KYC verification status for a customer.
func (c *EtherfuseClient) GetKYCStatus(ctx context.Context, customerID, publicKey string) (*KYCStatus, error) {
	path := fmt.Sprintf("/ramp/customer/%s/kyc/%s", customerID, publicKey)
	var resp KYCStatus
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("get KYC status: %w", err)
	}
	return &resp, nil
}

// --- Assets ---

// EtherfuseAsset from GET /ramp/assets.
type EtherfuseAsset struct {
	Symbol     string `json:"symbol"`
	Identifier string `json:"identifier"` // "CODE:ISSUER" format for use in quotes
	Name       string `json:"name"`
}

type assetsResponse struct {
	Assets []EtherfuseAsset `json:"assets"`
}

// GetAssets returns the list of rampable assets on Stellar.
func (c *EtherfuseClient) GetAssets(ctx context.Context, wallet string) ([]EtherfuseAsset, error) {
	path := "/ramp/assets?blockchain=stellar&currency=mxn&wallet=" + wallet
	var resp assetsResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("get assets: %w", err)
	}
	return resp.Assets, nil
}

// --- Order Details ---

// OrderDetails from GET /ramp/order/{orderId}.
type OrderDetails struct {
	OrderID               string      `json:"orderId"`
	CustomerID            string      `json:"customerId"`
	OrderType             string      `json:"orderType"` // "onramp" or "offramp"
	Status                string      `json:"status"`    // "created", "funded", "completed", "failed", "refunded", "canceled"
	BurnTransaction       string      `json:"burnTransaction,omitempty"`
	ConfirmedTxSignature  string      `json:"confirmedTxSignature,omitempty"`
	AmountInFiat          json.Number `json:"amountInFiat,omitempty"`
	AmountInTokens        json.Number `json:"amountInTokens,omitempty"`
	WithdrawAnchorAccount string      `json:"withdrawAnchorAccount,omitempty"`
	WithdrawMemo          string      `json:"withdrawMemo,omitempty"`
	WithdrawMemoType      string      `json:"withdrawMemoType,omitempty"`
}

// GetOrder fetches the current state of an order by ID.
func (c *EtherfuseClient) GetOrder(ctx context.Context, orderID string) (*OrderDetails, error) {
	path := "/ramp/order/" + orderID
	var resp OrderDetails
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	return &resp, nil
}

// --- HTTP helpers ---

func (c *EtherfuseClient) post(ctx context.Context, path string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

func (c *EtherfuseClient) get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

// --- Deterministic UUID v5 ---

// Fixed namespaces for deterministic UUID generation.
var (
	customerNamespace    = [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	bankAccountNamespace = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
)

// DeterministicCustomerID generates a deterministic UUID v5 from a Stellar
// public key. The same account always produces the same customer ID.
func DeterministicCustomerID(stellarAccount string) string {
	return uuidV5(customerNamespace, stellarAccount)
}

// DeterministicBankAccountID generates a deterministic UUID v5 for the bank
// account associated with a Stellar public key.
func DeterministicBankAccountID(stellarAccount string) string {
	return uuidV5(bankAccountNamespace, stellarAccount)
}

// uuidV5 implements UUID v5 (SHA-1 name-based) without external dependencies.
func uuidV5(namespace [16]byte, name string) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)

	// Set version (5) and variant (RFC 4122) bits.
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // variant RFC 4122

	var buf [16]byte
	copy(buf[:], sum[:16])
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(buf[0:4]),
		binary.BigEndian.Uint16(buf[4:6]),
		binary.BigEndian.Uint16(buf[6:8]),
		binary.BigEndian.Uint16(buf[8:10]),
		buf[10:16],
	)
}
