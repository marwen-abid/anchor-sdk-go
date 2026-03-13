package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/errors"
)

// Session represents an authenticated connection to a Stellar anchor.
// It contains the JWT token and expiration information for making
// authenticated API requests to the anchor's services.
type Session struct {
	// HomeDomain is the anchor's domain (e.g., "testanchor.stellar.org")
	HomeDomain string

	// Account is the Stellar account address (G...) that was authenticated
	Account string

	// JWT is the authentication token to use in Authorization: Bearer headers
	JWT string

	// ExpiresAt indicates when the JWT token expires
	ExpiresAt time.Time

	// client is the parent Client that created this session (private, for internal use)
	client *Client
}

// IsValid returns true if the session has not expired.
func (s *Session) IsValid() bool {
	return time.Now().Before(s.ExpiresAt)
}

// Login authenticates with an anchor using SEP-10 Web Authentication.
// It performs the following steps:
//  1. Discovers the anchor's WEB_AUTH_ENDPOINT via stellar.toml
//  2. Fetches an authentication challenge from the anchor
//  3. Signs the challenge transaction using the provided signer
//  4. Submits the signed transaction back to the anchor
//  5. Receives and returns a JWT token in a Session
//
// The returned Session contains the JWT token for making authenticated
// requests to the anchor's services (SEP-24, SEP-6, etc.).
func (c *Client) Login(ctx context.Context, account, homeDomain string, signer anchorsdk.Signer) (*Session, error) {
	// Step 1: Discover anchor's WEB_AUTH_ENDPOINT via stellar.toml
	anchorInfo, err := c.tomlResolver.Resolve(ctx, homeDomain)
	if err != nil {
		return nil, errors.NewClientError(
			errors.AUTH_UNSUPPORTED,
			"failed to resolve stellar.toml for "+homeDomain,
			err,
		)
	}

	if anchorInfo.WebAuthEndpoint == "" {
		return nil, errors.NewClientError(
			errors.AUTH_UNSUPPORTED,
			fmt.Sprintf("anchor %s does not provide WEB_AUTH_ENDPOINT in stellar.toml", homeDomain),
			nil,
		)
	}

	// Step 2: Fetch authentication challenge from the anchor
	challengeURL := fmt.Sprintf("%s?account=%s", anchorInfo.WebAuthEndpoint, account)
	resp, err := c.httpClient.Get(ctx, challengeURL)
	if err != nil {
		return nil, errors.NewClientError(
			errors.CHALLENGE_FETCH_FAILED,
			"failed to fetch challenge from "+challengeURL,
			err,
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.NewClientError(
			errors.CHALLENGE_FETCH_FAILED,
			fmt.Sprintf("challenge request returned status %d: %s", resp.StatusCode, string(body)),
			nil,
		)
	}

	// Parse challenge response
	var challengeResp struct {
		Transaction       string `json:"transaction"`
		NetworkPassphrase string `json:"network_passphrase"`
	}

	if err = json.NewDecoder(resp.Body).Decode(&challengeResp); err != nil {
		return nil, errors.NewClientError(
			errors.CHALLENGE_INVALID,
			"failed to decode challenge response JSON",
			err,
		)
	}

	// Validate network passphrase matches client configuration
	if challengeResp.NetworkPassphrase != c.networkPassphrase {
		return nil, errors.NewClientError(
			errors.CHALLENGE_INVALID,
			fmt.Sprintf("network passphrase mismatch: expected %s, got %s", c.networkPassphrase, challengeResp.NetworkPassphrase),
			nil,
		)
	}

	// Step 3: Sign the challenge transaction using the provided signer
	signedXDR, err := signer.SignTransaction(ctx, challengeResp.Transaction, c.networkPassphrase)
	if err != nil {
		return nil, errors.NewClientError(
			errors.SIGNER_ERROR,
			"failed to sign challenge transaction",
			err,
		)
	}

	// Step 4: Submit the signed transaction back to the anchor
	submitPayload := map[string]string{
		"transaction": signedXDR,
	}

	submitBody, err := json.Marshal(submitPayload)
	if err != nil {
		return nil, errors.NewClientError(
			errors.AUTH_REJECTED,
			"failed to marshal submit payload",
			err,
		)
	}

	submitResp, err := c.httpClient.Post(ctx, anchorInfo.WebAuthEndpoint, bytes.NewReader(submitBody))
	if err != nil {
		return nil, errors.NewClientError(
			errors.AUTH_REJECTED,
			"failed to submit signed challenge",
			err,
		)
	}
	defer func() { _ = submitResp.Body.Close() }()

	if submitResp.StatusCode != 200 {
		body, _ := io.ReadAll(submitResp.Body)
		return nil, errors.NewClientError(
			errors.AUTH_REJECTED,
			fmt.Sprintf("auth submission returned status %d: %s", submitResp.StatusCode, string(body)),
			nil,
		)
	}

	// Step 5: Parse the JWT token from the response
	var tokenResp struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(submitResp.Body).Decode(&tokenResp); err != nil {
		return nil, errors.NewClientError(
			errors.AUTH_REJECTED,
			"failed to decode token response JSON",
			err,
		)
	}

	// Parse JWT to extract expiration time (simple parsing, assumes HS256 JWT)
	// For v1, we set a default expiration of 24 hours from now
	// TODO: In v2, parse the JWT exp claim properly
	expiresAt := time.Now().Add(24 * time.Hour)

	return &Session{
		HomeDomain: homeDomain,
		Account:    account,
		JWT:        tokenResp.Token,
		ExpiresAt:  expiresAt,
		client:     c,
	}, nil
}

// Deposit initiates a deposit with the anchor using SEP-24 interactive flow.
// It discovers the TRANSFER_SERVER_SEP0024 endpoint, makes a POST request to
// /transactions/deposit/interactive, and returns a TransferProcess for polling status.
//
// The amount parameter is optional; pass empty string to let the user specify
// the amount in the interactive flow.
func (s *Session) Deposit(ctx context.Context, assetCode, amount string) (*TransferProcess, error) {
	return s.initiateTransfer(ctx, "deposit", assetCode, amount)
}

// Withdraw initiates a withdrawal with the anchor using SEP-24 interactive flow.
// It discovers the TRANSFER_SERVER_SEP0024 endpoint, makes a POST request to
// /transactions/withdraw/interactive, and returns a TransferProcess for polling status.
//
// The amount parameter is optional; pass empty string to let the user specify
// the amount in the interactive flow.
func (s *Session) Withdraw(ctx context.Context, assetCode, amount string) (*TransferProcess, error) {
	return s.initiateTransfer(ctx, "withdrawal", assetCode, amount)
}

// initiateTransfer is the common implementation for Deposit and Withdraw.
func (s *Session) initiateTransfer(ctx context.Context, kind, assetCode, amount string) (*TransferProcess, error) {
	anchorInfo, err := s.client.tomlResolver.Resolve(ctx, s.HomeDomain)
	if err != nil {
		return nil, errors.NewClientError(
			errors.AUTH_UNSUPPORTED,
			"failed to resolve stellar.toml for "+s.HomeDomain,
			err,
		)
	}

	if anchorInfo.TransferServerSep24 == "" {
		return nil, errors.NewClientError(
			errors.AUTH_UNSUPPORTED,
			fmt.Sprintf("anchor %s does not provide TRANSFER_SERVER_SEP0024 in stellar.toml", s.HomeDomain),
			nil,
		)
	}

	payload := map[string]string{
		"asset_code": assetCode,
		"account":    s.Account,
	}
	if amount != "" {
		payload["amount"] = amount
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.NewClientError(
			errors.TRANSFER_INIT_FAILED,
			"failed to marshal transfer request payload",
			err,
		)
	}

	endpoint := fmt.Sprintf("%s/transactions/%s/interactive", anchorInfo.TransferServerSep24, kind)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, errors.NewClientError(
			errors.TRANSFER_INIT_FAILED,
			"failed to create transfer request",
			err,
		)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.JWT)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.NewClientError(
			errors.TRANSFER_INIT_FAILED,
			"failed to initiate "+kind,
			err,
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.NewClientError(
			errors.TRANSFER_INIT_FAILED,
			fmt.Sprintf("%s request returned status %d: %s", kind, resp.StatusCode, string(body)),
			nil,
		)
	}

	var transferResp struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		ID   string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&transferResp); err != nil {
		return nil, errors.NewClientError(
			errors.TRANSFER_INIT_FAILED,
			"failed to decode transfer response JSON",
			err,
		)
	}

	process := &TransferProcess{
		ID:             transferResp.ID,
		Status:         anchorsdk.StatusInteractive,
		InteractiveURL: transferResp.URL,
		session:        s,
		endpoint:       anchorInfo.TransferServerSep24,
	}

	if process.onInteractive != nil {
		process.onInteractive(transferResp.URL)
	}

	return process, nil
}
