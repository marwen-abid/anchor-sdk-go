package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/anchor"
)

// SEP-6 Info response structure (same as SEP-24)
type sep6InfoResponse struct {
	Deposit  map[string]sep6AssetInfo `json:"deposit"`
	Withdraw map[string]sep6AssetInfo `json:"withdraw"`
}

type sep6AssetInfo struct {
	Enabled    bool                   `json:"enabled"`
	FeeFixed   float64                `json:"fee_fixed"`
	FeePercent float64                `json:"fee_percent"`
	MinAmount  float64                `json:"min_amount"`
	MaxAmount  float64                `json:"max_amount"`
	Fields     map[string]interface{} `json:"fields,omitempty"`
}

// SEP-6 Deposit response structure
type sep6DepositResponse struct {
	How          string                 `json:"how"`
	ID           string                 `json:"id"`
	Instructions map[string]interface{} `json:"instructions,omitempty"`
	ETA          int                    `json:"eta,omitempty"`
}

// SEP-6 Withdrawal response structure
type sep6WithdrawResponse struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	MemoType  string `json:"memo_type"`
	Memo      string `json:"memo"`
	ETA       int    `json:"eta,omitempty"`
}

// handleSEP6Info returns asset information for SEP-6 deposits and withdrawals.
// No authentication required per SEP-6 spec.
func handleSEP6Info() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := sep6InfoResponse{
			Deposit: map[string]sep6AssetInfo{
				"USDC": {
					Enabled:    true,
					FeeFixed:   0,
					FeePercent: 0,
					MinAmount:  0.1,
					MaxAmount:  10000,
					Fields:     map[string]interface{}{},
				},
			},
			Withdraw: map[string]sep6AssetInfo{
				"USDC": {
					Enabled:    true,
					FeeFixed:   0,
					FeePercent: 0,
					MinAmount:  0.1,
					MaxAmount:  10000,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}

// handleSEP6Deposit initiates a non-interactive deposit flow.
// Requires JWT authentication. Returns mock banking instructions.
func handleSEP6Deposit(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}

		// Parse query parameters
		assetCode := r.URL.Query().Get("asset_code")
		amount := r.URL.Query().Get("amount")

		// Use account from JWT claims for security
		account := claims.Subject

		if strings.TrimSpace(assetCode) == "" {
			http.Error(w, `{"error":"asset_code is required"}`, http.StatusBadRequest)
			return
		}

		// Default amount if not provided
		if strings.TrimSpace(amount) == "" {
			amount = "0"
		}

		req := anchor.DepositRequest{
			Account:   account,
			AssetCode: assetCode,
			Amount:    amount,
			Mode:      anchorsdk.ModeAPI,
		}

		result, err := tm.InitiateDeposit(context.Background(), req)
		if err != nil {
			http.Error(w, `{"error":"failed to initiate deposit"}`, http.StatusInternalServerError)
			return
		}

		// Return mock banking instructions
		response := sep6DepositResponse{
			How: "bank_transfer",
			ID:  result.ID,
			Instructions: map[string]interface{}{
				"organization.bank_account_number": "1234567890",
				"organization.bank_routing_number": "987654321",
				"organization.bank_name":           "Example Bank",
			},
			ETA: 0,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}

// handleSEP6Withdraw initiates a non-interactive withdrawal flow.
// Requires JWT authentication. Returns mock instructions with transfer ID.
func handleSEP6Withdraw(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}

		// Parse query parameters
		assetCode := r.URL.Query().Get("asset_code")
		amount := r.URL.Query().Get("amount")
		dest := r.URL.Query().Get("dest")

		// Use account from JWT claims for security
		account := claims.Subject

		if strings.TrimSpace(assetCode) == "" {
			http.Error(w, `{"error":"asset_code is required"}`, http.StatusBadRequest)
			return
		}

		if strings.TrimSpace(amount) == "" {
			http.Error(w, `{"error":"amount is required"}`, http.StatusBadRequest)
			return
		}

		req := anchor.WithdrawalRequest{
			Account:   account,
			AssetCode: assetCode,
			Amount:    amount,
			Dest:      dest,
			Mode:      anchorsdk.ModeAPI,
		}

		result, err := tm.InitiateWithdrawal(context.Background(), req)
		if err != nil {
			http.Error(w, `{"error":"failed to initiate withdrawal"}`, http.StatusInternalServerError)
			return
		}

		response := sep6WithdrawResponse{
			ID:        result.ID,
			AccountID: result.StellarAccount,
			MemoType:  result.StellarMemoType,
			Memo:      result.StellarMemo,
			ETA:       0,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}

// handleSEP6Transaction returns the status of a single transfer by ID.
// Requires JWT authentication. Reuses SEP-24 handler logic.
func handleSEP6Transaction(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}

		id := r.URL.Query().Get("id")
		if strings.TrimSpace(id) == "" {
			http.Error(w, `{"error":"id parameter is required"}`, http.StatusBadRequest)
			return
		}

		status, err := tm.GetStatus(context.Background(), id)
		if err != nil {
			http.Error(w, `{"error":"transfer not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(status)
	}
}

// handleSEP6Transactions returns a list of transfers for the authenticated account.
// Requires JWT authentication. Supports optional asset_code filter.
func handleSEP6Transactions(store anchorsdk.TransferStore, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := anchor.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}

		assetCode := r.URL.Query().Get("asset_code")

		filters := anchorsdk.TransferFilters{
			Account: claims.Subject,
		}
		if strings.TrimSpace(assetCode) != "" {
			filters.AssetCode = assetCode
		}

		transfers, err := store.List(context.Background(), filters)
		if err != nil {
			http.Error(w, `{"error":"failed to list transfers"}`, http.StatusInternalServerError)
			return
		}

		// Convert Transfer objects to TransferStatusResponse objects
		responses := make([]*anchor.TransferStatusResponse, 0, len(transfers))
		for _, transfer := range transfers {
			moreInfo := strings.TrimRight(baseURL, "/") + "/transaction/" + transfer.ID
			resp := &anchor.TransferStatusResponse{
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
			responses = append(responses, resp)
		}

		response := sep24TransactionsResponse{
			Transactions: responses,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}
