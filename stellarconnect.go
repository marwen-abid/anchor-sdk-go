// Package anchorsdk provides a Go SDK for implementing Stellar anchor services.
// It handles SEP-10 authentication, SEP-24 interactive flows, SEP-6 transfer protocols,
// and payment observation while delegating key signing, persistence, and business logic
// to the developer.
package anchorsdk

import (
	"context"
	"time"
)

// Signer is the minimal contract for proving identity and authorizing actions.
// The SDK does not manage keys, wallet connections, or signing infrastructure.
// The caller provides a Signer; the SDK uses it.
type Signer interface {
	// PublicKey returns the Stellar address (G...) identifying this signer.
	PublicKey() string

	// SignTransaction signs a Stellar transaction envelope (base64 XDR).
	// The networkPassphrase is required for computing the correct transaction hash.
	// Returns the signed envelope as base64 XDR.
	SignTransaction(ctx context.Context, xdr, networkPassphrase string) (string, error)
}

// MessageSigner is an optional extension for SEP-45 smart contract wallet auth.
// If a Signer also implements MessageSigner, the SDK will route to SEP-45
// when the anchor supports it.
type MessageSigner interface {
	Signer
	SignMessage(ctx context.Context, message string) (string, error)
}

// TransferStore is the persistence interface for transfer records.
// The SDK calls these methods internally during state transitions.
// The developer implements this interface against their own database.
type TransferStore interface {
	// Save persists a new transfer record.
	Save(ctx context.Context, transfer *Transfer) error

	// FindByID retrieves a transfer by its unique identifier.
	FindByID(ctx context.Context, id string) (*Transfer, error)

	// FindByAccount returns all transfers for a given Stellar account,
	// ordered by creation time descending.
	FindByAccount(ctx context.Context, account string) ([]*Transfer, error)

	// Update applies partial updates to an existing transfer.
	// Only non-nil fields in the update are applied.
	Update(ctx context.Context, id string, update *TransferUpdate) error

	// List returns transfers matching the given filters.
	List(ctx context.Context, filters TransferFilters) ([]*Transfer, error)
}

// Transfer is the canonical transfer record.
type Transfer struct {
	ID               string
	Kind             TransferKind   // "deposit" | "withdrawal"
	Mode             TransferMode   // "interactive" | "api"
	Status           TransferStatus // Set by SDK state machine, never by developer
	AssetCode        string
	AssetIssuer      string
	Account          string // Stellar account
	Amount           string // Decimal string
	InteractiveToken string // One-time token for interactive flows
	InteractiveURL   string
	ExternalRef      string // Banking/payment reference
	StellarTxHash    string // On-chain transaction hash
	Message          string // Human-readable status message
	Metadata         map[string]any
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

// TransferUpdate contains the mutable fields for a transfer update.
// Only non-zero-value fields are applied. Status is always set by the SDK.
type TransferUpdate struct {
	Status           *TransferStatus
	Amount           *string
	ExternalRef      *string
	StellarTxHash    *string
	InteractiveToken *string
	InteractiveURL   *string
	Message          *string
	Metadata         map[string]any
	CompletedAt      *time.Time
}

// TransferFilters for listing transfers.
type TransferFilters struct {
	Account   string
	AssetCode string
	Status    *TransferStatus
	Kind      *TransferKind
	Limit     int
	Offset    int
}

// TransferStatus represents the current state in the transfer lifecycle.
type TransferStatus string

const (
	// StatusInitiating is the initial state when a transfer is first created.
	StatusInitiating TransferStatus = "initiating"

	// StatusInteractive indicates the user must complete KYC or provide info via web UI.
	StatusInteractive TransferStatus = "interactive"

	// StatusPendingUserTransferStart means the anchor is waiting for the user to
	// send funds (for deposits) or initiate withdrawal.
	StatusPendingUserTransferStart TransferStatus = "pending_user_transfer_start"

	// StatusPendingExternal means the anchor is processing the off-chain leg
	// (bank transfer, fiat payment, etc).
	StatusPendingExternal TransferStatus = "pending_external"

	// StatusPendingStellar means the on-chain Stellar transaction is in progress.
	StatusPendingStellar TransferStatus = "pending_stellar"

	// StatusPaymentRequired means the user must send a Stellar payment to proceed.
	StatusPaymentRequired TransferStatus = "payment_required"

	// StatusCompleted is a terminal state indicating successful completion.
	StatusCompleted TransferStatus = "completed"

	// StatusFailed is a terminal state indicating an unrecoverable error.
	StatusFailed TransferStatus = "failed"

	// StatusDenied is a terminal state indicating the transfer was rejected
	// by compliance or business logic.
	StatusDenied TransferStatus = "denied"

	// StatusCancelled is a terminal state indicating the transfer was cancelled
	// by the user or system.
	StatusCancelled TransferStatus = "cancelled"

	// StatusExpired is a terminal state indicating the transfer timed out
	// before completion.
	StatusExpired TransferStatus = "expired"
)

// TransferKind distinguishes deposits from withdrawals.
type TransferKind string

const (
	// KindDeposit represents an off-chain to on-chain transfer.
	KindDeposit TransferKind = "deposit"

	// KindWithdrawal represents an on-chain to off-chain transfer.
	KindWithdrawal TransferKind = "withdrawal"
)

// TransferMode distinguishes interactive flows from direct API calls.
type TransferMode string

const (
	// ModeInteractive requires user interaction via a web UI for KYC, payment details, etc.
	ModeInteractive TransferMode = "interactive"

	// ModeAPI is a programmatic flow with no user interaction.
	ModeAPI TransferMode = "api"
)

// NonceStore tracks challenge nonces for replay protection.
// Nonces are added when a challenge is issued and consumed when verified.
type NonceStore interface {
	// Add records a nonce as issued. It should be retrievable until it
	// expires or is consumed.
	Add(ctx context.Context, nonce string, expiresAt time.Time) error

	// Consume marks a nonce as used. Returns false if the nonce was not
	// found or was already consumed.
	Consume(ctx context.Context, nonce string) (bool, error)
}

// JWTIssuer creates authentication tokens after successful SEP-10 verification.
type JWTIssuer interface {
	Issue(ctx context.Context, claims JWTClaims) (string, error)
}

// JWTVerifier validates and decodes authentication tokens.
type JWTVerifier interface {
	Verify(ctx context.Context, token string) (*JWTClaims, error)
}

// JWTClaims are the standard claims for a Stellar Anchor auth token.
type JWTClaims struct {
	Subject    string // Stellar address (G...)
	Issuer     string // Anchor domain
	IssuedAt   time.Time
	ExpiresAt  time.Time
	AuthMethod string // "sep10" | "sep45"
	Memo       string // Optional memo from auth challenge
}

// AccountSigner represents a signer registered on a Stellar account.
type AccountSigner struct {
	Key    string // Public key (G...)
	Weight int32  // Signing weight
}

// AccountThresholds represents the threshold levels for a Stellar account.
type AccountThresholds struct {
	Low    byte
	Medium byte
	High   byte
}

// AccountFetcher retrieves account signer information from the Stellar network.
// Implementations may use Horizon, Soroban RPC, or any other data source.
// The SDK uses this during SEP-10 challenge verification to validate
// that signatures meet the account's medium threshold.
type AccountFetcher interface {
	// FetchSigners returns the signers and thresholds for a Stellar account.
	// Returns an error if the account cannot be found or the lookup fails.
	FetchSigners(ctx context.Context, accountID string) ([]AccountSigner, AccountThresholds, error)
}

// PaymentEvent represents an incoming or outgoing Stellar payment
// detected by the Observer.
type PaymentEvent struct {
	ID              string
	From            string
	To              string
	Asset           string
	Amount          string
	Memo            string
	Cursor          string
	TransactionHash string
}

// PaymentHandler is a callback invoked when the Observer detects
// a payment matching the registered filters.
type PaymentHandler func(event PaymentEvent) error

// Observer watches the Stellar network for events relevant to anchor operations.
// It wraps Horizon streaming or Soroban RPC and emits typed events through
// registered handlers.
type Observer interface {
	// OnPayment registers a handler for payment events with optional filters.
	// Multiple handlers can be registered; they execute sequentially.
	OnPayment(handler PaymentHandler, filters ...PaymentFilter)

	// Start begins watching the network. Blocks until ctx is cancelled.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the observer.
	Stop() error
}

// PaymentFilter narrows which payments trigger a handler.
type PaymentFilter func(PaymentEvent) bool
