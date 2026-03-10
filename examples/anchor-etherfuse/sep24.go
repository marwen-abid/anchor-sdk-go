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

	stellarconnect "github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/anchor"
	"github.com/stellar/go/keypair"
)

// supportedAssets is the set of asset codes supported by this anchor.
var supportedAssets = map[string]bool{"USDC": true, "CETES": true}

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
	Transaction *etherfuseTransactionResponse `json:"transaction"`
}

// SEP-24 Transactions list response
type sep24TransactionsResponse struct {
	Transactions []*etherfuseTransactionResponse `json:"transactions"`
}

// etherfuseTransactionResponse extends TransferStatusResponse with SEP-24
// withdrawal fields populated from Etherfuse burnTransaction data.
type etherfuseTransactionResponse struct {
	ID                    string     `json:"id"`
	Kind                  string     `json:"kind"`
	Status                string     `json:"status"`
	StatusETA             int        `json:"status_eta,omitempty"`
	MoreInfoURL           string     `json:"more_info_url"`
	AmountIn              string     `json:"amount_in,omitempty"`
	AmountOut             string     `json:"amount_out,omitempty"`
	AmountFee             string     `json:"amount_fee,omitempty"`
	To                    string     `json:"to,omitempty"`
	From                  string     `json:"from,omitempty"`
	StartedAt             time.Time  `json:"started_at"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	TxHash                string     `json:"stellar_transaction_id,omitempty"`
	ExternalTxID          string     `json:"external_transaction_id,omitempty"`
	Message               string     `json:"message,omitempty"`
	WithdrawAnchorAccount string     `json:"withdraw_anchor_account,omitempty"`
	WithdrawMemo          string     `json:"withdraw_memo,omitempty"`
	WithdrawMemoType      string     `json:"withdraw_memo_type,omitempty"`
}

// mapStatusToSEP24 maps internal SDK statuses to SEP-24 spec statuses.
// For Etherfuse withdrawals in pending_external with withdraw details available,
// it returns pending_user_transfer_start so wallets prompt the user to send payment.
func mapStatusToSEP24(transfer *stellarconnect.Transfer) string {
	status := string(transfer.Status)

	// Etherfuse withdrawal: once withdraw details are available, present as
	// pending_user_transfer_start so the wallet knows to prompt the user.
	if transfer.Kind == stellarconnect.KindWithdrawal && status == "pending_external" {
		if transfer.Metadata != nil {
			if _, ok := transfer.Metadata["etherfuse_withdraw_anchor_account"]; ok {
				return "pending_user_transfer_start"
			}
		}
	}

	switch status {
	case "interactive", "initiating":
		return "incomplete"
	case "failed", "denied", "cancelled":
		return "error"
	default:
		return status
	}
}

// buildTransactionResponse creates an etherfuseTransactionResponse from a
// Transfer and its status response, enriching it with Etherfuse metadata.
func buildTransactionResponse(transfer *stellarconnect.Transfer, baseURL string) *etherfuseTransactionResponse {
	moreInfo := strings.TrimRight(baseURL, "/") + "/transaction/" + transfer.ID
	resp := &etherfuseTransactionResponse{
		ID:           transfer.ID,
		Kind:         string(transfer.Kind),
		Status:       mapStatusToSEP24(transfer),
		MoreInfoURL:  moreInfo,
		AmountIn:     transfer.Amount,
		AmountOut:    transfer.Amount,
		StartedAt:    transfer.CreatedAt,
		CompletedAt:  transfer.CompletedAt,
		TxHash:       transfer.StellarTxHash,
		ExternalTxID: transfer.ExternalRef,
		Message:      transfer.Message,
	}

	if transfer.Kind == stellarconnect.KindDeposit {
		resp.To = transfer.Account
	} else if transfer.Kind == stellarconnect.KindWithdrawal {
		resp.From = transfer.Account
	}

	// Enrich with Etherfuse metadata
	if transfer.Metadata != nil {
		if wa, ok := transfer.Metadata["etherfuse_withdraw_anchor_account"].(string); ok {
			resp.WithdrawAnchorAccount = wa
		}
		if wm, ok := transfer.Metadata["etherfuse_withdraw_memo"].(string); ok {
			resp.WithdrawMemo = wm
			resp.WithdrawMemoType = "text" // default
		}
		if wmt, ok := transfer.Metadata["etherfuse_withdraw_memo_type"].(string); ok {
			resp.WithdrawMemoType = wmt
		}
		if fee, ok := transfer.Metadata["etherfuse_fee_amount"].(string); ok {
			resp.AmountFee = fee
		}
	}

	return resp
}

// handleSEP24Info returns asset information for SEP-24 deposits and withdrawals.
// The info is built dynamically from the supported assets discovered at startup.
func handleSEP24Info() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deposit := map[string]assetInfo{}
		withdraw := map[string]assetInfo{}
		for symbol := range supportedAssets {
			info := assetInfo{
				Enabled:    true,
				FeeFixed:   0,
				FeePercent: 0.20,
				MinAmount:  1,
				MaxAmount:  100000,
			}
			deposit[symbol] = info
			withdraw[symbol] = info
		}
		response := sep24InfoResponse{
			Deposit:  deposit,
			Withdraw: withdraw,
			Fee:      feeInfo{Enabled: true},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}

// handleDepositInteractive initiates an interactive deposit flow.
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
		if strings.TrimSpace(account) == "" {
			account = claims.Subject
		}
		if _, err := keypair.ParseAddress(account); err != nil {
			writeJSONError(w, "invalid account", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(amount) == "" {
			amount = "0"
		}

		req := anchor.DepositRequest{
			Account:   account,
			AssetCode: assetCode,
			Amount:    amount,
			Mode:      stellarconnect.ModeInteractive,
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
		if strings.TrimSpace(account) == "" {
			account = claims.Subject
		}
		if strings.TrimSpace(amount) == "" {
			amount = "0"
		}

		req := anchor.WithdrawalRequest{
			Account:   account,
			AssetCode: assetCode,
			Amount:    amount,
			Dest:      dest,
			Mode:      stellarconnect.ModeInteractive,
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

// handleGetTransaction returns the status of a single transfer.
func handleGetTransaction(tm *anchor.TransferManager, store stellarconnect.TransferStore, baseURL string) http.HandlerFunc {
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

		if id != "" {
			transfer, err := store.FindByID(context.Background(), id)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "transfer not found"})
				return
			}
			resp := buildTransactionResponse(transfer, baseURL)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(sep24TransactionResponse{Transaction: resp})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "transfer not found"})
	}
}

// handleGetTransactions returns a list of transfers for the authenticated account.
func handleGetTransactions(store stellarconnect.TransferStore, baseURL string) http.HandlerFunc {
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

		if assetCode != "" && !supportedAssets[assetCode] {
			writeJSONError(w, "unsupported asset_code", http.StatusBadRequest)
			return
		}

		filters := stellarconnect.TransferFilters{Account: claims.Subject}
		if strings.TrimSpace(assetCode) != "" {
			filters.AssetCode = assetCode
		}
		if kind == "deposit" {
			k := stellarconnect.KindDeposit
			filters.Kind = &k
		} else if kind == "withdrawal" {
			k := stellarconnect.KindWithdrawal
			filters.Kind = &k
		}

		transfers, err := store.List(context.Background(), filters)
		if err != nil {
			writeJSONError(w, "failed to list transfers", http.StatusInternalServerError)
			return
		}

		if noOlderThan != "" {
			if cutoff, err := time.Parse(time.RFC3339, noOlderThan); err == nil {
				filtered := make([]*stellarconnect.Transfer, 0, len(transfers))
				for _, t := range transfers {
					if !t.CreatedAt.Before(cutoff) {
						filtered = append(filtered, t)
					}
				}
				transfers = filtered
			}
		}

		sort.Slice(transfers, func(i, j int) bool {
			return transfers[i].CreatedAt.After(transfers[j].CreatedAt)
		})

		if limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit < len(transfers) {
				transfers = transfers[:limit]
			}
		}

		responses := make([]*etherfuseTransactionResponse, 0, len(transfers))
		for _, transfer := range transfers {
			responses = append(responses, buildTransactionResponse(transfer, baseURL))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(sep24TransactionsResponse{Transactions: responses})
	}
}

// handleMoreInfo serves the more_info_url page for a transaction.
func handleMoreInfo(store stellarconnect.TransferStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "missing transaction id", http.StatusBadRequest)
			return
		}

		transfer, err := store.FindByID(context.Background(), id)
		if err != nil {
			http.Error(w, "transaction not found", http.StatusNotFound)
			return
		}

		status := mapStatusToSEP24(transfer)

		orderID := ""
		if transfer.Metadata != nil {
			if oid, ok := transfer.Metadata["etherfuse_order_id"].(string); ok {
				orderID = oid
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `<html><head><style>
body{font-family:system-ui,sans-serif;max-width:520px;margin:40px auto;padding:0 16px;background:#f5f5f5;color:#1a1a1a}
.card{background:#fff;border-radius:12px;padding:28px;box-shadow:0 2px 12px rgba(0,0,0,0.08)}
h1{font-size:20px;margin-bottom:16px}
.row{display:flex;justify-content:space-between;padding:10px 0;border-bottom:1px solid #f3f4f6;font-size:14px}
.row:last-child{border-bottom:none}
.label{color:#6b7280}
.value{font-weight:600;word-break:break-all;max-width:60%%;text-align:right}
.copy-btn{display:block;margin-top:16px;padding:10px;background:#f3f4f6;border:none;border-radius:6px;cursor:pointer;font-size:13px;width:100%%}
</style></head><body><div class="card">
<h1>Transaction Details</h1>
<div class="row"><span class="label">Transaction ID</span><span class="value">%s</span></div>
<div class="row"><span class="label">Kind</span><span class="value">%s</span></div>
<div class="row"><span class="label">Status</span><span class="value">%s</span></div>
<div class="row"><span class="label">Etherfuse Order ID</span><span class="value">%s</span></div>
<button class="copy-btn" onclick="navigator.clipboard.writeText('%s').then(()=>this.textContent='Copied!')">Copy Order ID</button>
</div></body></html>`,
			transfer.ID, string(transfer.Kind), status, orderID, orderID)
	}
}

// parseDepositRequest parses deposit request from JSON, form-urlencoded, or multipart/form-data.
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
	// Handles both application/x-www-form-urlencoded and multipart/form-data
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			return "", "", "", err
		}
	}
	return r.FormValue("asset_code"), r.FormValue("account"), r.FormValue("amount"), nil
}

// parseWithdrawRequest parses withdrawal request from JSON, form-urlencoded, or multipart/form-data.
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
	// Handles both application/x-www-form-urlencoded and multipart/form-data
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			return "", "", "", "", err
		}
	}
	return r.FormValue("asset_code"), r.FormValue("account"), r.FormValue("amount"), r.FormValue("dest"), nil
}
