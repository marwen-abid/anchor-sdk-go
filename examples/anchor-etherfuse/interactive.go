package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"math/big"
	"net/http"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/anchor"
)

//go:embed templates/interactive.html
var interactiveTemplate embed.FS

// interactivePageData is passed to the HTML template for all steps.
type interactivePageData struct {
	Token           string
	Kind            string // "deposit" or "withdrawal"
	Step            string // "onboard", "kyc-pending", "amount", "quote-confirm", "deposit-instructions", "withdrawal-pending", "kyc-rejected", "error"
	Amount          string
	AssetCode       string
	AvailableAssets []string // asset codes available from Etherfuse

	// Etherfuse onboarding
	OnboardingURL string

	// Quote confirmation
	QuoteID       string
	ExchangeRate  string
	SourceAmount  string
	DestAmount    string
	DestAmountFee string
	FeeAmount     string

	// Deposit instructions
	DepositClabe  string
	DepositAmount string
	OrderID       string

	// Error display
	ErrorMessage string
}

// handleGetInteractive displays the interactive KYC/transfer flow.
// Routes the user to the appropriate step based on their Etherfuse state.
func handleGetInteractive(
	tm *anchor.TransferManager,
	ef *EtherfuseClient,
	store anchorsdk.TransferStore,
) http.HandlerFunc {
	tmpl := template.Must(template.ParseFS(interactiveTemplate, "templates/interactive.html"))

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, `{"error":"missing token parameter"}`, http.StatusBadRequest)
			return
		}

		transfer, err := tm.PeekInteractiveToken(r.Context(), token)
		if err != nil {
			log.Printf("Invalid token: %v", err)
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		// Build available assets list from supportedAssets
		var available []string
		for symbol := range supportedAssets {
			available = append(available, symbol)
		}

		data := interactivePageData{
			Token:           token,
			Kind:            string(transfer.Kind),
			AssetCode:       transfer.AssetCode,
			Amount:          transfer.Amount,
			AvailableAssets: available,
		}

		// Determine step based on Etherfuse KYC status
		customerID := DeterministicCustomerID(transfer.Account)
		kycStatus, err := ef.GetKYCStatus(r.Context(), customerID, transfer.Account)
		if err != nil {
			// Customer not found or error — needs onboarding
			data.Step = "onboard"
		} else {
			switch kycStatus.Status {
			case "approved":
				data.Step = "amount"
			case "proposed":
				data.Step = "kyc-pending"
			case "rejected":
				data.Step = "kyc-rejected"
				data.ErrorMessage = kycStatus.CurrentRejectionReason
			default:
				data.Step = "onboard"
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("Failed to render template: %v", err)
		}
	}
}

// handlePostOnboard generates an Etherfuse onboarding URL and redirects the user.
func handlePostOnboard(
	tm *anchor.TransferManager,
	ef *EtherfuseClient,
	store anchorsdk.TransferStore,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			writeJSONError(w, "missing token", http.StatusBadRequest)
			return
		}

		transfer, err := tm.PeekInteractiveToken(r.Context(), token)
		if err != nil {
			writeJSONError(w, "invalid token", http.StatusUnauthorized)
			return
		}

		customerID := DeterministicCustomerID(transfer.Account)
		bankAccountID := DeterministicBankAccountID(transfer.Account)

		url, err := ef.GetOnboardingURL(r.Context(), customerID, bankAccountID, transfer.Account)
		if err != nil {
			log.Printf("Failed to get onboarding URL: %v", err)
			writeJSONError(w, "failed to generate onboarding URL", http.StatusInternalServerError)
			return
		}

		// Store customer IDs in transfer metadata
		if err := mergeMetadata(r.Context(), store, transfer.ID, map[string]any{
			"etherfuse_customer_id":     customerID,
			"etherfuse_bank_account_id": bankAccountID,
		}); err != nil {
			log.Printf("Failed to store customer metadata: %v", err)
		}

		// Return JSON with the onboarding URL (JS will open it)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"onboarding_url": url,
		})
	}
}

// handleKYCPoll returns the current KYC status as JSON for AJAX polling.
func handleKYCPoll(
	tm *anchor.TransferManager,
	ef *EtherfuseClient,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			writeJSONError(w, "missing token", http.StatusBadRequest)
			return
		}

		transfer, err := tm.PeekInteractiveToken(r.Context(), token)
		if err != nil {
			writeJSONError(w, "invalid token", http.StatusUnauthorized)
			return
		}

		customerID := DeterministicCustomerID(transfer.Account)
		kycStatus, err := ef.GetKYCStatus(r.Context(), customerID, transfer.Account)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "not_started"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": kycStatus.Status})
	}
}

// handlePostQuote creates an Etherfuse quote and renders the confirmation page.
func handlePostQuote(
	tm *anchor.TransferManager,
	ef *EtherfuseClient,
	store anchorsdk.TransferStore,
	assetIdentifiers map[string]string, // maps asset code (e.g. "USDC") to Etherfuse identifier
) http.HandlerFunc {
	tmpl := template.Must(template.ParseFS(interactiveTemplate, "templates/interactive.html"))

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			writeJSONError(w, "missing token", http.StatusBadRequest)
			return
		}

		transfer, err := tm.PeekInteractiveToken(r.Context(), token)
		if err != nil {
			writeJSONError(w, "invalid token", http.StatusUnauthorized)
			return
		}

		if err := r.ParseForm(); err != nil {
			writeJSONError(w, "invalid form data", http.StatusBadRequest)
			return
		}

		amount := r.FormValue("amount")
		assetCode := r.FormValue("asset_code")
		if amount == "" || assetCode == "" {
			renderError(w, tmpl, token, transfer, "Amount and asset are required")
			return
		}

		assetID, ok := assetIdentifiers[assetCode]
		if !ok {
			renderError(w, tmpl, token, transfer, "Unsupported asset: "+assetCode)
			return
		}

		customerID := DeterministicCustomerID(transfer.Account)
		quoteID := DeterministicQuoteID(transfer.ID)

		var quoteReq QuoteRequest
		if transfer.Kind == anchorsdk.KindDeposit {
			// Onramp: MXN → crypto
			quoteReq = QuoteRequest{
				QuoteID:    quoteID,
				CustomerID: customerID,
				QuoteAssets: QuoteAssets{
					Type:        "onramp",
					SourceAsset: "MXN",
					TargetAsset: assetID,
				},
				SourceAmount: amount,
			}
		} else {
			// Offramp: crypto → MXN
			quoteReq = QuoteRequest{
				QuoteID:    quoteID,
				CustomerID: customerID,
				QuoteAssets: QuoteAssets{
					Type:        "offramp",
					SourceAsset: assetID,
					TargetAsset: "MXN",
				},
				SourceAmount: amount,
			}
		}

		quote, err := ef.CreateQuote(r.Context(), quoteReq)
		if err != nil {
			log.Printf("Failed to create quote: %v", err)
			renderError(w, tmpl, token, transfer, "Failed to get exchange rate. Please try again.")
			return
		}

		// Store quote in metadata
		if err := mergeMetadata(r.Context(), store, transfer.ID, map[string]any{
			"etherfuse_quote_id":      quote.QuoteID,
			"etherfuse_exchange_rate": quote.ExchangeRate,
			"etherfuse_asset_code":    assetCode,
			"etherfuse_asset_id":      assetID,
		}); err != nil {
			log.Printf("Failed to store quote metadata: %v", err)
		}

		// Update transfer amount
		if err := store.Update(r.Context(), transfer.ID, &anchorsdk.TransferUpdate{
			Amount: &amount,
		}); err != nil {
			log.Printf("Failed to update transfer amount: %v", err)
		}

		destAmountAfterFee := quote.DestinationAmountAfterFee
		if destAmountAfterFee == "" {
			destAmountAfterFee = subtractDecimal(quote.DestinationAmount, quote.FeeAmount)
		}

		data := interactivePageData{
			Token:         token,
			Kind:          string(transfer.Kind),
			Step:          "quote-confirm",
			AssetCode:     assetCode,
			QuoteID:       quote.QuoteID,
			ExchangeRate:  quote.ExchangeRate,
			SourceAmount:  quote.SourceAmount,
			DestAmount:    quote.DestinationAmount,
			DestAmountFee: destAmountAfterFee,
			FeeAmount:     quote.FeeAmount,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("Failed to render template: %v", err)
		}
	}
}

// handlePostOrder creates the Etherfuse order, consumes the token, and completes
// the interactive flow. This is the terminal step.
func handlePostOrder(
	tm *anchor.TransferManager,
	ef *EtherfuseClient,
	store anchorsdk.TransferStore,
) http.HandlerFunc {
	tmpl := template.Must(template.ParseFS(interactiveTemplate, "templates/interactive.html"))

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			writeJSONError(w, "missing token", http.StatusBadRequest)
			return
		}

		// Peek first to get transfer details
		transfer, err := tm.PeekInteractiveToken(r.Context(), token)
		if err != nil {
			writeJSONError(w, "invalid token", http.StatusUnauthorized)
			return
		}

		if err := r.ParseForm(); err != nil {
			writeJSONError(w, "invalid form data", http.StatusBadRequest)
			return
		}

		quoteID := r.FormValue("quote_id")
		if quoteID == "" {
			renderError(w, tmpl, token, transfer, "Missing quote ID")
			return
		}

		bankAccountID := DeterministicBankAccountID(transfer.Account)
		orderID := DeterministicOrderID(transfer.ID)

		orderReq := OrderRequest{
			OrderID:       orderID,
			BankAccountID: bankAccountID,
			PublicKey:     transfer.Account,
			QuoteID:       quoteID,
		}

		ctx := r.Context()
		data := interactivePageData{
			Token:     token,
			Kind:      string(transfer.Kind),
			AssetCode: transfer.AssetCode,
		}

		if transfer.Kind == anchorsdk.KindDeposit {
			// Create onramp order
			result, err := ef.CreateOnrampOrder(ctx, orderReq)
			if err != nil {
				log.Printf("Failed to create onramp order: %v", err)
				renderError(w, tmpl, token, transfer, "Failed to create order. Please try again.")
				return
			}

			// Order created successfully — now consume token and complete interactive
			if _, err := tm.ConsumeInteractiveToken(ctx, token); err != nil {
				log.Printf("Failed to consume token: %v", err)
			}

			// Store order details in metadata
			if err := mergeMetadata(ctx, store, transfer.ID, map[string]any{
				"etherfuse_order_id":       result.OrderID,
				"etherfuse_deposit_clabe":  result.DepositClabe,
				"etherfuse_deposit_amount": result.DepositAmount.String(),
			}); err != nil {
				log.Printf("Failed to store order metadata: %v", err)
			}

			if err := tm.CompleteInteractive(ctx, transfer.ID, map[string]any{
				"etherfuse_order_id": result.OrderID,
			}); err != nil {
				log.Printf("Failed to complete interactive: %v", err)
			}

			data.Step = "deposit-instructions"
			data.DepositClabe = result.DepositClabe
			data.DepositAmount = result.DepositAmount.String()
			data.OrderID = result.OrderID

		} else {
			// Create offramp order
			result, err := ef.CreateOfframpOrder(ctx, orderReq)
			if err != nil {
				log.Printf("Failed to create offramp order: %v", err)
				renderError(w, tmpl, token, transfer, "Failed to create order. Please try again.")
				return
			}

			// Order created successfully — now consume token and complete interactive
			if _, err := tm.ConsumeInteractiveToken(ctx, token); err != nil {
				log.Printf("Failed to consume token: %v", err)
			}

			// Store order details in metadata
			meta := map[string]any{"etherfuse_order_id": result.OrderID}
			if result.WithdrawAnchorAccount != "" {
				meta["etherfuse_withdraw_anchor_account"] = result.WithdrawAnchorAccount
				meta["etherfuse_withdraw_memo"] = result.WithdrawMemo
				meta["etherfuse_withdraw_memo_type"] = result.WithdrawMemoType
			}
			if err := mergeMetadata(ctx, store, transfer.ID, meta); err != nil {
				log.Printf("Failed to store order metadata: %v", err)
			}

			if err := tm.CompleteInteractive(ctx, transfer.ID, map[string]any{
				"etherfuse_order_id": result.OrderID,
			}); err != nil {
				log.Printf("Failed to complete interactive: %v", err)
			}

			data.Step = "withdrawal-pending"
			data.OrderID = result.OrderID
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("Failed to render template: %v", err)
		}
	}
}

// DeterministicQuoteID generates a deterministic UUID for a quote from a transfer ID.
func DeterministicQuoteID(transferID string) string {
	return uuidV5([16]byte{0x7c, 0xa7, 0xb8, 0x12, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}, transferID)
}

// DeterministicOrderID generates a deterministic UUID for an order from a transfer ID.
func DeterministicOrderID(transferID string) string {
	return uuidV5([16]byte{0x8d, 0xa7, 0xb8, 0x13, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}, transferID)
}

// subtractDecimal computes a - b for decimal strings, returning a string.
// Returns "0" if either input is invalid.
func subtractDecimal(a, b string) string {
	ra, ok := new(big.Rat).SetString(a)
	if !ok {
		return "0"
	}
	rb, ok := new(big.Rat).SetString(b)
	if !ok {
		return "0"
	}
	result := new(big.Rat).Sub(ra, rb)
	return result.FloatString(7)
}

// renderError renders the template with an error message.
func renderError(w http.ResponseWriter, tmpl *template.Template, token string, transfer *anchorsdk.Transfer, msg string) {
	data := interactivePageData{
		Token:        token,
		Kind:         string(transfer.Kind),
		Step:         "error",
		ErrorMessage: msg,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("Failed to render error template: %v", err)
	}
}
