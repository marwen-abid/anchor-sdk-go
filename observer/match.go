package observer

import (
	"context"
	"errors"
	"log"

	"github.com/marwen-abid/anchor-sdk-go/anchor"
)

// AutoMatchPayments automatically matches incoming Stellar payments to pending
// withdrawals by extracting the transfer ID from the payment's memo field.
//
// This function simplifies the common use case where:
// 1. User initiates withdrawal, receives transfer ID
// 2. User sends Stellar payment to anchor's distribution account with memo=transferID
// 3. Observer detects payment and calls tm.NotifyPaymentReceived() automatically
//
// AutoMatchPayments registers a payment handler with the observer that:
// - Filters for payments to the distribution account
// - Extracts memo as the transfer ID
// - Calls tm.NotifyPaymentReceived(ctx, transferID, details) on match
// - Logs errors but does not crash on processing failures
//
// The observer must already be configured with a cursor and handlers before
// calling AutoMatchPayments. The registered handler will be called for each
// payment streamed from Horizon.
//
// Example usage:
//
//	obs := observer.NewHorizonObserver(
//	    "https://horizon.stellar.org",
//	    observer.WithCursor("now"),
//	)
//	err := observer.AutoMatchPayments(obs, tm, "GBBD47UZQ...")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	obs.Start(ctx) // blocks until context cancelled
func AutoMatchPayments(obs Observer, tm *anchor.TransferManager, distributionAccount string) error {
	if obs == nil {
		return errors.New("observer is nil")
	}
	if tm == nil {
		return errors.New("transfer manager is nil")
	}
	if distributionAccount == "" {
		return errors.New("distribution account is empty")
	}

	// Register a payment handler that matches payments to the distribution account
	obs.OnPayment(
		func(evt PaymentEvent) error {
			// Filter: Only process payments to the distribution account
			if evt.To != distributionAccount {
				return nil
			}

			// Extract transfer ID from memo
			transferID := evt.Memo
			if transferID == "" {
				log.Printf("Payment %s: received to distribution account but has no memo, skipping", evt.ID)
				return nil
			}

			// Call NotifyPaymentReceived to transition withdrawal
			ctx := context.Background()
			details := anchor.PaymentReceivedDetails{
				StellarTxHash: evt.TransactionHash,
				Amount:        evt.Amount,
				AssetCode:     evt.Asset,
			}

			if err := tm.NotifyPaymentReceived(ctx, transferID, details); err != nil {
				log.Printf("Payment %s: failed to notify transfer %s: %v", evt.ID, transferID, err)
				// Don't crash - log error and continue processing
				return nil
			}

			log.Printf("Payment %s: matched transfer %s, amount %s %s", evt.ID, transferID, evt.Amount, evt.Asset)
			return nil
		},
		WithDestination(distributionAccount),
	)

	return nil
}
