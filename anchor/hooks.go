// Package anchor provides anchor-specific implementations for the Stellar Anchor SDK,
// including authentication, transfer management, and lifecycle event handling.
package anchor

import (
	"sync"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
)

// HookEvent represents a named lifecycle event that anchors can subscribe to.
type HookEvent string

// Hook event constants represent the lifecycle events that anchors can react to.
const (
	HookDepositInitiated             HookEvent = "deposit:initiated"
	HookDepositKYCComplete           HookEvent = "deposit:kyc_complete"
	HookDepositFundsReceived         HookEvent = "deposit:funds_received"
	HookWithdrawalInitiated          HookEvent = "withdrawal:initiated"
	HookWithdrawalStellarPaymentSent HookEvent = "withdrawal:stellar_payment_sent"
	HookTransferStatusChanged        HookEvent = "transfer:status_changed"
)

// HookRegistry manages lifecycle event handlers for transfer state changes.
// It implements the observer pattern, allowing anchors to register callbacks
// that execute sequentially when transfer lifecycle events occur.
//
// Handlers are stored per event and execute in registration order.
// The registry is thread-safe for concurrent registration and triggering.
type HookRegistry struct {
	handlers map[HookEvent][]func(*anchorsdk.Transfer)
	mu       sync.RWMutex
}

// NewHookRegistry creates a new lifecycle hook registry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		handlers: make(map[HookEvent][]func(*anchorsdk.Transfer)),
	}
}

// On registers a handler function for a specific lifecycle event.
// Multiple handlers can be registered for the same event and will execute
// sequentially in registration order when the event is triggered.
//
// The handler receives a pointer to the Transfer that triggered the event.
// Handlers should be quick, non-blocking operations. If a handler panics,
// the panic will propagate and prevent subsequent handlers from executing.
func (r *HookRegistry) On(event HookEvent, handler func(*anchorsdk.Transfer)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.handlers[event] = append(r.handlers[event], handler)
}

// Trigger executes all registered handlers for a specific lifecycle event,
// passing the transfer that triggered the event. Handlers execute sequentially
// in registration order.
//
// If any handler panics, the panic propagates to the caller and subsequent
// handlers do not execute.
func (r *HookRegistry) Trigger(event HookEvent, transfer *anchorsdk.Transfer) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handlers, ok := r.handlers[event]
	if !ok {
		return
	}

	for _, handler := range handlers {
		handler(transfer)
	}
}
