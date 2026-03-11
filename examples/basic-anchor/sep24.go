package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/anchor"
	"github.com/stellar/go/keypair"
)

// supportedAssets is the set of asset codes supported by this example anchor.
var supportedAssets = map[string]bool{"USDC": true}

// SEP-24 Info response structure
type sep24InfoResponse struct {
	Deposit  map[string]assetInfo `json:"deposit"`
	Withdraw map[string]assetInfo `json:"withdraw"`
	Fee      feeInfo              `json:"fee"`
}

type feeInfo struct {
	Enabled bool `json:"enabled"`
}

type assetInfo struct {
	Enabled    bool    `json:"enabled"`
	FeeFixed   float64 `json:"fee_fixed"`
	FeePercent float64 `json:"fee_percent"`
	MinAmount  float64 `json:"min_amount"`
	MaxAmount  float64 `json:"max_amount"`
}

// SEP-24 Interactive response structure
type sep24InteractiveResponse struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	ID   string `json:"id"`
}

// SEP-24 single transaction response wrapper (per SEP-24 spec)
type sep24TransactionResponse struct {
	Transaction *anchor.TransferStatusResponse `json:"transaction"`
}

// SEP-24 Transactions list response
type sep24TransactionsResponse struct {
	Transactions []*anchor.TransferStatusResponse `json:"transactions"`
}

// mapStatusToSEP24 maps internal SDK statuses to SEP-24 spec statuses.
func mapStatusToSEP24(status string) string {
	switch status {
	case "interactive", "initiating":
		return "incomplete"
	case "failed", "denied", "cancelled":
		return "error"
	default:
		return status
	}
}

// handleSEP24Info returns asset information for SEP-24 deposits and withdrawals.
// No authentication required per SEP-24 spec.
func handleSEP24Info() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := sep24InfoResponse{
			Deposit: map[string]assetInfo{
				"USDC": {
					Enabled:    true,
					FeeFixed:   0,
					FeePercent: 0,
					MinAmount:  0.1,
					MaxAmount:  10000,
				},
			},
			Withdraw: map[string]assetInfo{
				"USDC": {
					Enabled:    true,
					FeeFixed:   0,
					FeePercent: 0,
					MinAmount:  0.1,
					MaxAmount:  10000,
				},
			},
			Fee: feeInfo{Enabled: false},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}

// handleDepositInteractive initiates an interactive deposit flow.
// Requires JWT authentication.
func handleDepositInteractive(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			writeJSONError(w, "authentication required", http.StatusForbidden)
			return
		}

		assetCode, account, amount, err := parseDepositRequest(r)
		if err != nil {
			writeJSONError(w, "invalid request format", http.StatusBadRequest)
			return
		}

		if strings.TrimSpace(assetCode) == "" {
			writeJSONError(w, "asset_code is required", http.StatusBadRequest)
			return
		}

		if !supportedAssets[assetCode] {
			writeJSONError(w, "unsupported asset_code", http.StatusBadRequest)
			return
		}

		// Use account from JWT claims if not provided
		if strings.TrimSpace(account) == "" {
			account = claims.Subject
		}

		// Validate account format
		if _, err := keypair.ParseAddress(account); err != nil {
			writeJSONError(w, "invalid account", http.StatusBadRequest)
			return
		}

		// Amount is optional for interactive deposits
		if strings.TrimSpace(amount) == "" {
			amount = "0"
		}

		req := anchor.DepositRequest{
			Account:   account,
			AssetCode: assetCode,
			Amount:    amount,
			Mode:      anchorsdk.ModeInteractive,
		}

		result, err := tm.InitiateDeposit(context.Background(), req)
		if err != nil {
			writeJSONError(w, "failed to initiate deposit", http.StatusInternalServerError)
			return
		}

		response := sep24InteractiveResponse{
			Type: "interactive_customer_info_needed",
			URL:  result.InteractiveURL,
			ID:   result.ID,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}

// handleWithdrawInteractive initiates an interactive withdrawal flow.
// Requires JWT authentication.
func handleWithdrawInteractive(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			writeJSONError(w, "authentication required", http.StatusForbidden)
			return
		}

		assetCode, account, amount, dest, err := parseWithdrawRequest(r)
		if err != nil {
			writeJSONError(w, "invalid request format", http.StatusBadRequest)
			return
		}

		if strings.TrimSpace(assetCode) == "" {
			writeJSONError(w, "asset_code is required", http.StatusBadRequest)
			return
		}

		if !supportedAssets[assetCode] {
			writeJSONError(w, "unsupported asset_code", http.StatusBadRequest)
			return
		}

		// Use account from JWT claims if not provided
		if strings.TrimSpace(account) == "" {
			account = claims.Subject
		}

		// Amount is optional for interactive withdrawals
		if strings.TrimSpace(amount) == "" {
			amount = "0"
		}

		req := anchor.WithdrawalRequest{
			Account:   account,
			AssetCode: assetCode,
			Amount:    amount,
			Dest:      dest,
			Mode:      anchorsdk.ModeInteractive,
		}

		result, err := tm.InitiateWithdrawal(context.Background(), req)
		if err != nil {
			writeJSONError(w, "failed to initiate withdrawal", http.StatusInternalServerError)
			return
		}

		response := sep24InteractiveResponse{
			Type: "interactive_customer_info_needed",
			URL:  result.InteractiveURL,
			ID:   result.ID,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}

// handleGetTransaction returns the status of a single transfer by ID, stellar_transaction_id, or external_transaction_id.
// Requires JWT authentication.
func handleGetTransaction(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}

		id := r.URL.Query().Get("id")
		stellarTxID := r.URL.Query().Get("stellar_transaction_id")
		externalTxID := r.URL.Query().Get("external_transaction_id")

		if strings.TrimSpace(id) == "" && strings.TrimSpace(stellarTxID) == "" && strings.TrimSpace(externalTxID) == "" {
			http.Error(w, `{"error":"id, stellar_transaction_id, or external_transaction_id parameter is required"}`, http.StatusBadRequest)
			return
		}

		// Lookup by id
		if id != "" {
			status, err := tm.GetStatus(context.Background(), id)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "transfer not found"})
				return
			}
			status.Status = mapStatusToSEP24(status.Status)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(sep24TransactionResponse{Transaction: status})
			return
		}

		// For stellar_transaction_id or external_transaction_id, we need to search through transfers
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "transfer not found"})
	}
}

// handleGetTransactions returns a list of transfers for the authenticated account.
// Requires JWT authentication.
func handleGetTransactions(store anchorsdk.TransferStore, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}

		assetCode := r.URL.Query().Get("asset_code")
		kind := r.URL.Query().Get("kind")
		limitStr := r.URL.Query().Get("limit")
		noOlderThan := r.URL.Query().Get("no_older_than")

		// Validate asset_code if provided
		if assetCode != "" && !supportedAssets[assetCode] {
			writeJSONError(w, "unsupported asset_code", http.StatusBadRequest)
			return
		}

		filters := anchorsdk.TransferFilters{
			Account: claims.Subject,
		}
		if strings.TrimSpace(assetCode) != "" {
			filters.AssetCode = assetCode
		}
		if kind == "deposit" {
			k := anchorsdk.KindDeposit
			filters.Kind = &k
		} else if kind == "withdrawal" {
			k := anchorsdk.KindWithdrawal
			filters.Kind = &k
		}

		transfers, err := store.List(context.Background(), filters)
		if err != nil {
			writeJSONError(w, "failed to list transfers", http.StatusInternalServerError)
			return
		}

		// Filter by no_older_than
		if noOlderThan != "" {
			if cutoff, err := time.Parse(time.RFC3339, noOlderThan); err == nil {
				filtered := make([]*anchorsdk.Transfer, 0, len(transfers))
				for _, t := range transfers {
					if !t.CreatedAt.Before(cutoff) {
						filtered = append(filtered, t)
					}
				}
				transfers = filtered
			}
		}

		// Sort by created_at descending
		sort.Slice(transfers, func(i, j int) bool {
			return transfers[i].CreatedAt.After(transfers[j].CreatedAt)
		})

		// Apply limit
		if limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit < len(transfers) {
				transfers = transfers[:limit]
			}
		}

		// Convert Transfer objects to TransferStatusResponse objects
		responses := make([]*anchor.TransferStatusResponse, 0, len(transfers))
		for _, transfer := range transfers {
			moreInfo := strings.TrimRight(baseURL, "/") + "/transaction/" + transfer.ID
			resp := &anchor.TransferStatusResponse{
				ID:           transfer.ID,
				Kind:         string(transfer.Kind),
				Status:       mapStatusToSEP24(string(transfer.Status)),
				MoreInfoURL:  moreInfo,
				AmountIn:     transfer.Amount,
				AmountOut:    transfer.Amount,
				StartedAt:    transfer.CreatedAt,
				CompletedAt:  transfer.CompletedAt,
				TxHash:       transfer.StellarTxHash,
				ExternalTxID: transfer.ExternalRef,
				Message:      transfer.Message,
			}
			if transfer.Kind == anchorsdk.KindDeposit {
				resp.To = transfer.Account
			} else if transfer.Kind == anchorsdk.KindWithdrawal {
				resp.From = transfer.Account
			}
			responses = append(responses, resp)
		}

		response := sep24TransactionsResponse{
			Transactions: responses,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}

// handleMoreInfo serves the more_info_url page for a transaction.
// No authentication required — this is a public info page.
func handleMoreInfo(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "missing transaction id", http.StatusBadRequest)
			return
		}

		status, err := tm.GetStatus(context.Background(), id)
		if err != nil {
			http.Error(w, "transaction not found", http.StatusNotFound)
			return
		}
		status.Status = mapStatusToSEP24(status.Status)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "<html><body><h1>Transaction %s</h1><p>Status: %s</p><p>Kind: %s</p></body></html>",
			status.ID, status.Status, status.Kind)
	}
}

// parseDepositRequest parses deposit request from either JSON or FormData
func parseDepositRequest(r *http.Request) (assetCode, account, amount string, err error) {
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			AssetCode string `json:"asset_code"`
			Account   string `json:"account"`
			Amount    string `json:"amount"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return "", "", "", err
		}
		return req.AssetCode, req.Account, req.Amount, nil
	}
	// FormData parsing
	if err := r.ParseForm(); err != nil {
		return "", "", "", err
	}
	return r.FormValue("asset_code"), r.FormValue("account"), r.FormValue("amount"), nil
}

// parseWithdrawRequest parses withdrawal request from either JSON or FormData
func parseWithdrawRequest(r *http.Request) (assetCode, account, amount, dest string, err error) {
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			AssetCode string `json:"asset_code"`
			Account   string `json:"account"`
			Amount    string `json:"amount"`
			Dest      string `json:"dest"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return "", "", "", "", err
		}
		return req.AssetCode, req.Account, req.Amount, req.Dest, nil
	}
	// FormData parsing
	if err := r.ParseForm(); err != nil {
		return "", "", "", "", err
	}
	return r.FormValue("asset_code"), r.FormValue("account"), r.FormValue("amount"), r.FormValue("dest"), nil
}
