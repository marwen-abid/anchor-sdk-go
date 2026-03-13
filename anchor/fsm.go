// Package anchor provides SEP-24 transfer state machine validation.
//
// The finite state machine (FSM) enforces legal state transitions for
// SEP-24 interactive transfers according to RFC Section 4.6. It validates
// that a requested transition from one TransferStatus to another is allowed
// by the protocol specification.
package anchor

import (
	"fmt"

	"github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/errors"
)

// legalTransitions defines the allowed state transitions for SEP-24 transfers.
// Each key is a "from" state, and the value is a set of valid "to" states.
//
// Terminal states (completed, failed, denied, cancelled, expired) have no outgoing transitions.
var legalTransitions = map[anchorsdk.TransferStatus]map[anchorsdk.TransferStatus]bool{
	anchorsdk.StatusInitiating: {
		anchorsdk.StatusInteractive:              true,
		anchorsdk.StatusPendingUserTransferStart: true,
		anchorsdk.StatusPendingExternal:          true,
		anchorsdk.StatusFailed:                   true,
		anchorsdk.StatusDenied:                   true,
	},
	anchorsdk.StatusInteractive: {
		anchorsdk.StatusPendingUserTransferStart: true,
		anchorsdk.StatusPendingExternal:          true,
		anchorsdk.StatusFailed:                   true,
		anchorsdk.StatusExpired:                  true,
	},
	anchorsdk.StatusPendingUserTransferStart: {
		anchorsdk.StatusPendingExternal: true,
		anchorsdk.StatusPendingStellar:  true,
		anchorsdk.StatusFailed:          true,
		anchorsdk.StatusCancelled:       true,
	},
	anchorsdk.StatusPendingExternal: {
		anchorsdk.StatusPendingStellar: true,
		anchorsdk.StatusFailed:         true,
		anchorsdk.StatusCancelled:      true,
	},
	anchorsdk.StatusPendingStellar: {
		anchorsdk.StatusCompleted: true,
		anchorsdk.StatusFailed:    true,
	},
	anchorsdk.StatusPaymentRequired: {
		anchorsdk.StatusPendingStellar: true,
		anchorsdk.StatusFailed:         true,
	},
	// Terminal states have no outgoing transitions
	anchorsdk.StatusCompleted: {},
	anchorsdk.StatusFailed:    {},
	anchorsdk.StatusDenied:    {},
	anchorsdk.StatusCancelled: {},
	anchorsdk.StatusExpired:   {},
}

// ValidateTransition checks if a state transition from "from" to "to" is legal
// according to SEP-24 protocol rules (RFC Section 4.6).
//
// Returns nil if the transition is valid, or an error with code TRANSITION_INVALID
// if the transition is not allowed.
//
// Example:
//
//	err := ValidateTransition(StatusInitiating, StatusInteractive)
//	if err != nil {
//	    // Handle illegal transition
//	}
func ValidateTransition(from, to anchorsdk.TransferStatus) error {
	// Check if the "from" state exists in the transition map
	validToStates, exists := legalTransitions[from]
	if !exists {
		return errors.NewAnchorError(
			errors.TRANSITION_INVALID,
			fmt.Sprintf("unknown source state: %s", from),
			nil,
		)
	}

	// Check if the "to" state is in the set of valid transitions
	if !validToStates[to] {
		return errors.NewAnchorError(
			errors.TRANSITION_INVALID,
			fmt.Sprintf("illegal transition from %s to %s", from, to),
			nil,
		)
	}

	return nil
}
