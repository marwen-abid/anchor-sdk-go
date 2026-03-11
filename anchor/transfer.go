package anchor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	corecrypto "github.com/marwen-abid/anchor-sdk-go/core/crypto"
	"github.com/marwen-abid/anchor-sdk-go/errors"
)

const (
	interactiveTokenLength = 32
)

type Config struct {
	Domain              string
	InteractiveBaseURL  string
	DistributionAccount string
	BaseURL             string
}

type TransferManager struct {
	store         anchorsdk.TransferStore
	config        Config
	hooks         *HookRegistry
	tokenMu       sync.Mutex
	tokenToID     map[string]string
	transferMu    sync.Mutex
	transferLocks map[string]*sync.Mutex
}

func NewTransferManager(store anchorsdk.TransferStore, config Config, hooks *HookRegistry) *TransferManager {
	if hooks == nil {
		hooks = NewHookRegistry()
	}
	return &TransferManager{
		store:         store,
		config:        config,
		hooks:         hooks,
		tokenToID:     make(map[string]string),
		transferLocks: make(map[string]*sync.Mutex),
	}
}

// lockForTransfer returns a per-transfer mutex, creating one if needed.
func (tm *TransferManager) lockForTransfer(id string) *sync.Mutex {
	tm.transferMu.Lock()
	defer tm.transferMu.Unlock()
	mu, ok := tm.transferLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		tm.transferLocks[id] = mu
	}
	return mu
}

type DepositRequest struct {
	Account   string
	AssetCode string
	Amount    string
	Mode      anchorsdk.TransferMode
	Metadata  map[string]any
}

type DepositResult struct {
	ID             string
	InteractiveURL string
	Instructions   string
	ETA            int
}

type WithdrawalRequest struct {
	Account   string
	AssetCode string
	Amount    string
	Mode      anchorsdk.TransferMode
	Dest      string
	DestExtra string
	Metadata  map[string]any
}

type WithdrawalResult struct {
	ID              string
	InteractiveURL  string
	StellarAccount  string
	StellarMemo     string
	StellarMemoType string
	ETA             int
}

type FundsReceivedDetails struct {
	ExternalRef string
	Amount      string
}

type PaymentSentDetails struct {
	StellarTxHash string
}

type PaymentReceivedDetails struct {
	StellarTxHash string
	Amount        string
	AssetCode     string
}

type DisbursementDetails struct {
	ExternalRef string
}

type TransferStatusResponse struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	StatusETA    int        `json:"status_eta,omitempty"`
	MoreInfoURL  string     `json:"more_info_url"`
	AmountIn     string     `json:"amount_in,omitempty"`
	AmountOut    string     `json:"amount_out,omitempty"`
	To           string     `json:"to,omitempty"`
	From         string     `json:"from,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	TxHash       string     `json:"stellar_transaction_id,omitempty"`
	ExternalTxID string     `json:"external_transaction_id,omitempty"`
	Message      string     `json:"message,omitempty"`
}

func (tm *TransferManager) InitiateDeposit(ctx context.Context, req DepositRequest) (*DepositResult, error) {
	if tm.store == nil {
		return nil, errors.NewAnchorError(errors.STORE_ERROR, "transfer store not configured", nil)
	}
	if strings.TrimSpace(req.Account) == "" || strings.TrimSpace(req.AssetCode) == "" || strings.TrimSpace(req.Amount) == "" {
		return nil, errors.NewAnchorError(errors.TRANSFER_INIT_FAILED, "account, asset_code, and amount are required", nil)
	}

	id, err := corecrypto.GenerateNonce(16)
	if err != nil {
		return nil, errors.NewAnchorError(errors.TRANSFER_INIT_FAILED, "failed to generate transfer ID", err)
	}

	now := time.Now()
	transfer := &anchorsdk.Transfer{
		ID:        id,
		Kind:      anchorsdk.KindDeposit,
		Mode:      req.Mode,
		Status:    anchorsdk.StatusInitiating,
		AssetCode: req.AssetCode,
		Account:   req.Account,
		Amount:    req.Amount,
		Metadata:  req.Metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if req.Mode == anchorsdk.ModeInteractive {
		token, url, err := tm.generateInteractiveURL(id)
		if err != nil {
			return nil, err
		}
		transfer.InteractiveToken = token
		transfer.InteractiveURL = url
		transfer.Status = anchorsdk.StatusInteractive
	}

	if err := tm.store.Save(ctx, transfer); err != nil {
		return nil, errors.NewAnchorError(errors.STORE_ERROR, "failed to save transfer", err)
	}

	if transfer.Mode == anchorsdk.ModeInteractive {
		tm.hooks.Trigger(HookDepositInitiated, transfer)
		return &DepositResult{ID: transfer.ID, InteractiveURL: transfer.InteractiveURL}, nil
	}

	if err := tm.transition(ctx, transfer.ID, anchorsdk.StatusPendingExternal, ""); err != nil {
		return nil, err
	}
	tm.hooks.Trigger(HookDepositInitiated, transfer)
	return &DepositResult{ID: transfer.ID, Instructions: "deposit initiated", ETA: 0}, nil
}

func (tm *TransferManager) InitiateWithdrawal(ctx context.Context, req WithdrawalRequest) (*WithdrawalResult, error) {
	if tm.store == nil {
		return nil, errors.NewAnchorError(errors.STORE_ERROR, "transfer store not configured", nil)
	}
	if strings.TrimSpace(req.Account) == "" || strings.TrimSpace(req.AssetCode) == "" || strings.TrimSpace(req.Amount) == "" {
		return nil, errors.NewAnchorError(errors.TRANSFER_INIT_FAILED, "account, asset_code, and amount are required", nil)
	}

	id, err := corecrypto.GenerateNonce(16)
	if err != nil {
		return nil, errors.NewAnchorError(errors.TRANSFER_INIT_FAILED, "failed to generate transfer ID", err)
	}

	now := time.Now()
	transfer := &anchorsdk.Transfer{
		ID:        id,
		Kind:      anchorsdk.KindWithdrawal,
		Mode:      req.Mode,
		Status:    anchorsdk.StatusInitiating,
		AssetCode: req.AssetCode,
		Account:   req.Account,
		Amount:    req.Amount,
		Metadata:  req.Metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if req.Mode == anchorsdk.ModeInteractive {
		token, url, err := tm.generateInteractiveURL(id)
		if err != nil {
			return nil, err
		}
		transfer.InteractiveToken = token
		transfer.InteractiveURL = url
		transfer.Status = anchorsdk.StatusInteractive
	} else {
		transfer.Status = anchorsdk.StatusPaymentRequired
	}

	if err := tm.store.Save(ctx, transfer); err != nil {
		return nil, errors.NewAnchorError(errors.STORE_ERROR, "failed to save transfer", err)
	}

	tm.hooks.Trigger(HookWithdrawalInitiated, transfer)

	result := &WithdrawalResult{
		ID:              transfer.ID,
		InteractiveURL:  transfer.InteractiveURL,
		StellarAccount:  tm.config.DistributionAccount,
		StellarMemo:     transfer.ID,
		StellarMemoType: "text",
	}
	return result, nil
}

func (tm *TransferManager) CompleteInteractive(ctx context.Context, transferID string, data map[string]any) error {
	transfer, err := tm.store.FindByID(ctx, transferID)
	if err != nil {
		return errors.NewAnchorError(errors.STORE_ERROR, "failed to load transfer", err)
	}
	if transfer.Mode != anchorsdk.ModeInteractive {
		return errors.NewAnchorError(errors.TRANSITION_INVALID, "transfer not in interactive mode", nil)
	}

	next := anchorsdk.StatusPendingExternal
	if transfer.Kind == anchorsdk.KindDeposit {
		next = anchorsdk.StatusPendingUserTransferStart
	}
	return tm.transition(ctx, transferID, next, "")
}

// PeekInteractiveToken validates the token without consuming it.
// Use this for GET requests that display the interactive form.
func (tm *TransferManager) PeekInteractiveToken(ctx context.Context, token string) (*anchorsdk.Transfer, error) {
	tm.tokenMu.Lock()
	transferID, ok := tm.tokenToID[token]
	tm.tokenMu.Unlock()
	if !ok {
		return nil, errors.NewAnchorError(errors.INTERACTIVE_TOKEN_INVALID, "interactive token invalid", nil)
	}
	transfer, err := tm.store.FindByID(ctx, transferID)
	if err != nil {
		return nil, errors.NewAnchorError(errors.STORE_ERROR, "failed to load transfer", err)
	}
	return transfer, nil
}

// ConsumeInteractiveToken validates and deletes the token.
// Use this for POST requests that finalize the interactive flow.
func (tm *TransferManager) ConsumeInteractiveToken(ctx context.Context, token string) (*anchorsdk.Transfer, error) {
	tm.tokenMu.Lock()
	transferID, ok := tm.tokenToID[token]
	if ok {
		delete(tm.tokenToID, token)
	}
	tm.tokenMu.Unlock()
	if !ok {
		return nil, errors.NewAnchorError(errors.INTERACTIVE_TOKEN_INVALID, "interactive token invalid", nil)
	}
	transfer, err := tm.store.FindByID(ctx, transferID)
	if err != nil {
		return nil, errors.NewAnchorError(errors.STORE_ERROR, "failed to load transfer", err)
	}
	return transfer, nil
}

// VerifyInteractiveToken validates and deletes the token (alias for ConsumeInteractiveToken).
func (tm *TransferManager) VerifyInteractiveToken(ctx context.Context, token string) (*anchorsdk.Transfer, error) {
	return tm.ConsumeInteractiveToken(ctx, token)
}

func (tm *TransferManager) NotifyFundsReceived(ctx context.Context, transferID string, details FundsReceivedDetails) error {
	update := &anchorsdk.TransferUpdate{ExternalRef: &details.ExternalRef}
	if strings.TrimSpace(details.Amount) != "" {
		update.Amount = &details.Amount
	}
	return tm.updateAndTransition(ctx, transferID, update, anchorsdk.StatusPendingStellar, HookDepositFundsReceived)
}

func (tm *TransferManager) NotifyPaymentSent(ctx context.Context, transferID string, details PaymentSentDetails) error {
	update := &anchorsdk.TransferUpdate{StellarTxHash: &details.StellarTxHash}
	completedAt := time.Now()
	update.CompletedAt = &completedAt
	return tm.updateAndTransition(ctx, transferID, update, anchorsdk.StatusCompleted, HookTransferStatusChanged)
}

func (tm *TransferManager) NotifyPaymentReceived(ctx context.Context, transferID string, details PaymentReceivedDetails) error {
	update := &anchorsdk.TransferUpdate{StellarTxHash: &details.StellarTxHash}
	return tm.updateAndTransition(ctx, transferID, update, anchorsdk.StatusPendingStellar, HookWithdrawalStellarPaymentSent)
}

func (tm *TransferManager) NotifyDisbursementSent(ctx context.Context, transferID string, details DisbursementDetails) error {
	update := &anchorsdk.TransferUpdate{ExternalRef: &details.ExternalRef}
	completedAt := time.Now()
	update.CompletedAt = &completedAt
	return tm.updateAndTransition(ctx, transferID, update, anchorsdk.StatusCompleted, HookTransferStatusChanged)
}

func (tm *TransferManager) Deny(ctx context.Context, transferID string, reason string) error {
	return tm.transition(ctx, transferID, anchorsdk.StatusDenied, reason)
}

func (tm *TransferManager) Cancel(ctx context.Context, transferID string, reason string) error {
	return tm.transition(ctx, transferID, anchorsdk.StatusCancelled, reason)
}

func (tm *TransferManager) GetStatus(ctx context.Context, transferID string) (*TransferStatusResponse, error) {
	transfer, err := tm.store.FindByID(ctx, transferID)
	if err != nil {
		return nil, errors.NewAnchorError(errors.STORE_ERROR, "failed to load transfer", err)
	}
	baseURL := tm.config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}
	moreInfo := fmt.Sprintf("%s/transaction/%s", strings.TrimRight(baseURL, "/"), transfer.ID)
	resp := &TransferStatusResponse{
		ID:           transfer.ID,
		Kind:         string(transfer.Kind),
		Status:       string(transfer.Status),
		MoreInfoURL:  moreInfo,
		AmountIn:     transfer.Amount,
		AmountOut:    transfer.Amount,
		StartedAt:    transfer.CreatedAt,
		CompletedAt:  transfer.CompletedAt,
		TxHash:       transfer.StellarTxHash,
		ExternalTxID: transfer.ExternalRef,
		Message:      transfer.Message,
	}
	// SEP-24: deposits require "to" (user's Stellar account), withdrawals require "from"
	if transfer.Kind == anchorsdk.KindDeposit {
		resp.To = transfer.Account
	} else if transfer.Kind == anchorsdk.KindWithdrawal {
		resp.From = transfer.Account
	}
	return resp, nil
}

func (tm *TransferManager) updateAndTransition(ctx context.Context, transferID string, update *anchorsdk.TransferUpdate, next anchorsdk.TransferStatus, hook HookEvent) error {
	mu := tm.lockForTransfer(transferID)
	mu.Lock()
	defer mu.Unlock()

	transfer, err := tm.store.FindByID(ctx, transferID)
	if err != nil {
		return errors.NewAnchorError(errors.STORE_ERROR, "failed to load transfer", err)
	}
	if err := ValidateTransition(transfer.Status, next); err != nil {
		return err
	}
	update.Status = &next
	if err := tm.store.Update(ctx, transferID, update); err != nil {
		return errors.NewAnchorError(errors.STORE_ERROR, "failed to update transfer", err)
	}
	updated, err := tm.store.FindByID(ctx, transferID)
	if err == nil {
		tm.hooks.Trigger(hook, updated)
		tm.hooks.Trigger(HookTransferStatusChanged, updated)
	}
	return nil
}

func (tm *TransferManager) transition(ctx context.Context, transferID string, next anchorsdk.TransferStatus, message string) error {
	mu := tm.lockForTransfer(transferID)
	mu.Lock()
	defer mu.Unlock()

	transfer, err := tm.store.FindByID(ctx, transferID)
	if err != nil {
		return errors.NewAnchorError(errors.STORE_ERROR, "failed to load transfer", err)
	}
	if err := ValidateTransition(transfer.Status, next); err != nil {
		return err
	}
	update := &anchorsdk.TransferUpdate{Status: &next}
	if strings.TrimSpace(message) != "" {
		update.Message = &message
	}
	if next == anchorsdk.StatusCompleted {
		completedAt := time.Now()
		update.CompletedAt = &completedAt
	}
	if err := tm.store.Update(ctx, transferID, update); err != nil {
		return errors.NewAnchorError(errors.STORE_ERROR, "failed to update transfer", err)
	}
	updated, err := tm.store.FindByID(ctx, transferID)
	if err == nil {
		tm.hooks.Trigger(HookTransferStatusChanged, updated)
	}
	return nil
}

func (tm *TransferManager) generateInteractiveURL(transferID string) (string, string, error) {
	token, err := corecrypto.GenerateNonce(interactiveTokenLength)
	if err != nil {
		return "", "", errors.NewAnchorError(errors.INTERACTIVE_TOKEN_INVALID, "failed to generate interactive token", err)
	}
	tm.tokenMu.Lock()
	tm.tokenToID[token] = transferID
	tm.tokenMu.Unlock()
	base := strings.TrimRight(tm.config.InteractiveBaseURL, "/")
	if base == "" {
		baseURL := tm.config.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:8000"
		}
		base = strings.TrimRight(baseURL, "/") + "/interactive"
	}
	url := fmt.Sprintf("%s?token=%s", base, token)
	return token, url, nil
}

func isTerminal(status anchorsdk.TransferStatus) bool {
	switch status {
	case anchorsdk.StatusCompleted,
		anchorsdk.StatusFailed,
		anchorsdk.StatusDenied,
		anchorsdk.StatusCancelled,
		anchorsdk.StatusExpired:
		return true
	default:
		return false
	}
}
