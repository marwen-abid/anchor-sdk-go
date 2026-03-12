package observer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
	"github.com/stellar/go-stellar-sdk/protocols/horizon/base"
	"github.com/stellar/go-stellar-sdk/protocols/horizon/operations"

	"github.com/marwen-abid/anchor-sdk-go/errors"
)

// HorizonObserver implements Observer by streaming payment operations from Horizon.
// It provides cursor management for resumability, reconnection with exponential backoff,
// and filtering capabilities.
type HorizonObserver struct {
	horizonURL  string
	client      *horizonclient.Client
	handlers    []handlerEntry
	cursor      string
	cursorSaver func(string) error

	// Reconnection backoff settings
	initialBackoff time.Duration
	maxBackoff     time.Duration

	// Synchronization
	mu       sync.RWMutex
	stopChan chan struct{}
	stopOnce sync.Once
	running  bool
}

// ObserverOption is a function that configures a HorizonObserver.
type ObserverOption func(*HorizonObserver)

// WithCursor sets the starting cursor for streaming.
// Use "now" to start from the current ledger (skip historical payments).
// Use a specific paging_token to resume from a previous position.
func WithCursor(cursor string) ObserverOption {
	return func(h *HorizonObserver) {
		h.cursor = cursor
	}
}

// WithCursorSaver sets a callback that's called after each payment is processed.
// This allows the caller to persist the cursor for resumability across restarts.
// The callback receives the paging_token of the last successfully processed payment.
func WithCursorSaver(saver func(string) error) ObserverOption {
	return func(h *HorizonObserver) {
		h.cursorSaver = saver
	}
}

// WithReconnectBackoff sets the initial and maximum backoff durations for reconnection.
// Default is 1s initial, 60s max with exponential growth.
func WithReconnectBackoff(initial, maxDur time.Duration) ObserverOption {
	return func(h *HorizonObserver) {
		h.initialBackoff = initial
		h.maxBackoff = maxDur
	}
}

// NewHorizonObserver creates a new HorizonObserver that streams from the given Horizon URL.
// The default cursor is "now" (skip historical payments), but can be overridden with WithCursor.
func NewHorizonObserver(horizonURL string, opts ...ObserverOption) *HorizonObserver {
	obs := &HorizonObserver{
		horizonURL:     horizonURL,
		client:         &horizonclient.Client{HorizonURL: horizonURL},
		handlers:       make([]handlerEntry, 0),
		cursor:         "now",
		initialBackoff: 1 * time.Second,
		maxBackoff:     60 * time.Second,
		stopChan:       make(chan struct{}),
	}

	for _, opt := range opts {
		opt(obs)
	}

	return obs
}

// OnPayment registers a handler for payment events with optional filters.
// Multiple handlers can be registered. Filters are ANDed together.
// Handlers are called sequentially for each matching payment.
func (h *HorizonObserver) OnPayment(handler PaymentHandler, filters ...PaymentFilter) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.handlers = append(h.handlers, handlerEntry{
		handler: handler,
		filters: filters,
	})
}

// Start begins streaming payment operations from Horizon.
// This method blocks until the context is cancelled or Stop() is called.
// It automatically reconnects with exponential backoff on stream failures.
func (h *HorizonObserver) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return errors.NewObserverError(errors.STREAM_ERROR, "observer already running", nil)
	}
	h.running = true
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		h.running = false
		h.mu.Unlock()
	}()

	// Exponential backoff state
	backoff := h.initialBackoff
	attempt := 0

	for {
		// Check if stopped or context cancelled
		select {
		case <-h.stopChan:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Get current cursor
		h.mu.RLock()
		currentCursor := h.cursor
		h.mu.RUnlock()

		// Create operation request for streaming payments
		opRequest := horizonclient.OperationRequest{
			Cursor: currentCursor,
			Order:  horizonclient.OrderAsc,
		}

		// Start streaming
		err := h.client.StreamPayments(ctx, opRequest, func(op operations.Operation) {
			// Reset backoff on successful stream
			backoff = h.initialBackoff
			attempt = 0

			// Convert operation to PaymentEvent
			evt := h.convertToPaymentEvent(op)
			if evt == nil {
				// Not a payment type we recognize, skip
				return
			}

			// Process the event through handlers
			h.processEvent(*evt)

			// Update cursor
			h.mu.Lock()
			h.cursor = evt.Cursor
			h.mu.Unlock()

			// Save cursor if callback provided
			if h.cursorSaver != nil {
				if err := h.cursorSaver(evt.Cursor); err != nil {
					// Log error but continue streaming
					// Production implementation might want better error handling
					fmt.Printf("observer: failed to save cursor: %v\n", err)
				}
			}
		})

		// If stream ended, check reason
		if err == nil {
			// Normal shutdown
			return nil
		}

		// Check if we should stop
		select {
		case <-h.stopChan:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Stream error - reconnect with backoff
		fmt.Printf("observer: stream error (attempt %d): %v, reconnecting in %v\n", attempt, err, backoff)

		// Wait for backoff period or until stopped
		select {
		case <-time.After(backoff):
			// Continue to retry
		case <-h.stopChan:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}

		// Increase backoff exponentially: 1s, 2s, 4s, 8s, ..., max 60s
		attempt++
		backoff *= 2
		if backoff > h.maxBackoff {
			backoff = h.maxBackoff
		}
	}
}

// Stop gracefully stops streaming. It's safe to call Stop multiple times.
func (h *HorizonObserver) Stop() error {
	h.stopOnce.Do(func() {
		close(h.stopChan)
	})
	return nil
}

// convertToPaymentEvent converts a Horizon operation to a PaymentEvent.
// Returns nil if the operation is not a payment type.
func (h *HorizonObserver) convertToPaymentEvent(op operations.Operation) *PaymentEvent {
	// Get base operation data
	baseOp := op.GetBase()

	// Build base event
	evt := &PaymentEvent{
		ID:              baseOp.ID,
		Cursor:          baseOp.PT, // PT is the paging_token field
		TransactionHash: baseOp.TransactionHash,
	}

	// Type-specific conversion
	switch op.GetType() {
	case "payment":
		// Payment operation
		payment, ok := op.(operations.Payment)
		if !ok {
			return nil
		}
		evt.From = payment.From
		evt.To = payment.To
		evt.Amount = payment.Amount
		evt.Asset = h.formatAsset(payment.Asset)

	case "create_account":
		// CreateAccount is also a payment (funds the new account)
		create, ok := op.(operations.CreateAccount)
		if !ok {
			return nil
		}
		evt.From = create.Funder
		evt.To = create.Account
		evt.Amount = create.StartingBalance
		evt.Asset = "native"

	case "path_payment_strict_send", "path_payment_strict_receive", "path_payment":
		// Path payments
		// Note: The actual type might vary, we'll handle the base case
		// For v1, we'll extract what we can from the base operation
		evt.From = baseOp.SourceAccount
		// Path payments are complex - for v1 we'll just capture what we can
		// Production implementation would need to handle PathPayment types specifically
		return nil // Skip path payments for v1 simplicity

	case "account_merge":
		// Account merge transfers all funds
		merge, ok := op.(operations.AccountMerge)
		if !ok {
			return nil
		}
		evt.From = merge.Account
		evt.To = merge.Into
		evt.Asset = "native"
		// Note: Amount is not directly available in account_merge, would need to query effects
		evt.Amount = "0" // Placeholder

	default:
		// Not a payment type
		return nil
	}

	return evt
}

// formatAsset formats an asset for display.
// Native XLM returns "native", issued assets return "CODE:ISSUER".
func (h *HorizonObserver) formatAsset(asset base.Asset) string {
	if asset.Type == "native" {
		return "native"
	}
	return fmt.Sprintf("%s:%s", asset.Code, asset.Issuer)
}

// processEvent runs all registered handlers for the given event if it passes their filters.
func (h *HorizonObserver) processEvent(evt PaymentEvent) { //nolint:gocritic
	h.mu.RLock()
	handlers := h.handlers
	h.mu.RUnlock()

	for _, entry := range handlers {
		// Check all filters (AND logic)
		passesFilters := true
		for _, filter := range entry.filters {
			if !filter(evt) {
				passesFilters = false
				break
			}
		}

		if !passesFilters {
			continue
		}

		// Call handler
		if err := entry.handler(evt); err != nil {
			// Log error but continue processing other handlers
			fmt.Printf("observer: handler error: %v\n", err)
		}
	}
}

// Compile-time interface check
var _ Observer = (*HorizonObserver)(nil)
