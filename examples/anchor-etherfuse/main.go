// Package main implements an anchor server integrated with Etherfuse FX Ramp APIs.
//
// This example demonstrates:
//   - Serving stellar.toml with USDC and CETES assets (SEP-1)
//   - SEP-10 Web Authentication with challenge/response flow
//   - SEP-24 Interactive deposit (MXN -> USDC/CETES via Etherfuse onramp)
//   - SEP-24 Interactive withdrawal (USDC/CETES -> MXN via Etherfuse offramp)
//   - Etherfuse webhook processing for order status updates
//
// Run with: ETHERFUSE_API_KEY=xxx go run ./examples/anchor-etherfuse
// Or copy .env.example to .env and configure it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/anchor"
	"github.com/marwen-abid/anchor-sdk-go/core/account"
	"github.com/marwen-abid/anchor-sdk-go/core/toml"
	"github.com/marwen-abid/anchor-sdk-go/observer"
	"github.com/marwen-abid/anchor-sdk-go/signers"
	"github.com/marwen-abid/anchor-sdk-go/store/memory"
	pgstore "github.com/marwen-abid/anchor-sdk-go/store/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

const jwtExpiry = 24 * time.Hour

// In-memory cursor persistence for observer stream resumability
var currentCursor string = "now"

// challengeResponse represents GET /auth response with SEP-10 challenge.
type challengeResponse struct {
	Transaction       string `json:"transaction"`
	NetworkPassphrase string `json:"network_passphrase"`
}

// authRequest represents POST /auth request with signed challenge.
type authRequest struct {
	Transaction string `json:"transaction"`
}

// authResponse represents POST /auth response with JWT token.
type authResponse struct {
	Token string `json:"token"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	signer, err := signers.FromSecret(cfg.AnchorSecret)
	if err != nil {
		return fmt.Errorf("failed to create signer: %w", err)
	}

	var nonceStore anchorsdk.NonceStore
	var transferStore anchorsdk.TransferStore

	switch cfg.StoreType {
	case "postgres":
		var pool *pgxpool.Pool
		pool, err = pgstore.Connect(context.Background(), cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("failed to connect to postgres: %w", err)
		}
		defer pool.Close()
		nonceStore = pgstore.NewNonceStore(pool)
		transferStore = pgstore.NewTransferStore(pool)
		log.Printf("Using PostgreSQL store")
	default:
		nonceStore = memory.NewNonceStore()
		transferStore = memory.NewTransferStore()
		log.Printf("Using in-memory store")
	}

	jwtIssuer, jwtVerifier := anchor.NewHMACJWT(
		[]byte(cfg.JWTSecret),
		cfg.AnchorDomain,
		jwtExpiry,
	)

	accountFetcher := account.NewHorizonAccountFetcher(cfg.HorizonURL)

	authIssuer, err := anchor.NewAuthIssuer(anchor.AuthConfig{
		Domain:            cfg.AnchorDomain,
		NetworkPassphrase: cfg.NetworkPassphrase,
		Signer:            signer,
		NonceStore:        nonceStore,
		JWTIssuer:         jwtIssuer,
		JWTVerifier:       jwtVerifier,
		AccountFetcher:    accountFetcher,
	})
	if err != nil {
		return fmt.Errorf("failed to create auth issuer: %w", err)
	}

	baseURL := "http://" + cfg.AnchorDomain
	transferConfig := anchor.Config{
		Domain:              cfg.AnchorDomain,
		InteractiveBaseURL:  baseURL + "/interactive",
		DistributionAccount: signer.PublicKey(),
		BaseURL:             baseURL,
	}
	transferManager := anchor.NewTransferManager(transferStore, transferConfig, nil)

	// Etherfuse client
	etherfuseClient := NewEtherfuseClient(cfg.EtherfuseAPIKey, cfg.EtherfuseAPIURL)

	// Fetch available asset identifiers from Etherfuse at startup.
	// This ensures we use the exact identifiers Etherfuse expects for quotes.
	assetIdentifiers := map[string]string{}
	efAssets, err := etherfuseClient.GetAssets(context.Background(), signer.PublicKey())
	if err != nil {
		log.Printf("WARNING: Failed to fetch Etherfuse assets: %v", err)
		log.Printf("WARNING: Falling back to configured issuers")
		assetIdentifiers["USDC"] = "USDC:" + cfg.USDCIssuer
		assetIdentifiers["CETES"] = "CETES:" + cfg.CETESIssuer
	} else {
		for _, a := range efAssets {
			assetIdentifiers[a.Symbol] = a.Identifier
			log.Printf("Etherfuse asset: %s -> %s", a.Symbol, a.Identifier)
		}
	}

	// Update supported assets to match what Etherfuse actually offers
	supportedAssets = map[string]bool{}
	for symbol := range assetIdentifiers {
		supportedAssets[symbol] = true
	}

	// Observer for auto-matching Stellar payments to pending withdrawals
	distributionAccount := signer.PublicKey()
	obs := observer.NewHorizonObserver(
		cfg.HorizonURL,
		observer.WithCursor(currentCursor),
		observer.WithCursorSaver(func(cursor string) error {
			currentCursor = cursor
			return nil
		}),
	)

	if err := observer.AutoMatchPayments(obs, transferManager, distributionAccount); err != nil {
		return fmt.Errorf("failed to setup auto-matching: %w", err)
	}

	// SEP-1: stellar.toml — build currencies from Etherfuse assets
	assetDescriptions := map[string][2]string{
		"USDC":  {"USD Coin on Stellar", "USD Coin bridged via Etherfuse FX Ramp"},
		"CETES": {"Mexican Government Treasury Certificates", "CETES tokenized on Stellar via Etherfuse"},
	}
	var currencies []toml.CurrencyInfo
	for symbol, identifier := range assetIdentifiers {
		// Parse issuer from "CODE:ISSUER" format
		parts := strings.SplitN(identifier, ":", 2)
		issuer := ""
		if len(parts) == 2 {
			issuer = parts[1]
		}
		desc := assetDescriptions[symbol]
		currencies = append(currencies, toml.CurrencyInfo{
			Code:            symbol,
			Issuer:          issuer,
			Status:          "test",
			DisplayDecimals: 2,
			AnchorAssetType: "fiat",
			IsAssetAnchored: true,
			Desc:            desc[0],
			Description:     desc[1],
		})
	}
	anchorInfo := &toml.AnchorInfo{
		NetworkPassphrase:   cfg.NetworkPassphrase,
		SigningKey:          signer.PublicKey(),
		WebAuthEndpoint:     baseURL + "/auth",
		TransferServerSep24: baseURL + "/sep24",
		Currencies:          currencies,
	}
	tomlPublisher := toml.NewPublisher(anchorInfo)

	// Start observer
	go func() {
		if err := obs.Start(context.Background()); err != nil {
			log.Printf("Observer stopped: %v", err)
		}
	}()
	log.Printf("Observer started watching %s", distributionAccount)

	// Start order poller (handles lifecycle when webhooks can't reach localhost)
	startOrderPoller(etherfuseClient, transferManager, transferStore, cfg.NetworkPassphrase)

	// HTTP routes
	mux := http.NewServeMux()

	// SEP-1: Discovery
	mux.HandleFunc("/.well-known/stellar.toml", tomlPublisher.Handler())

	// SEP-10: Authentication
	mux.HandleFunc("GET /auth", handleGetChallenge(authIssuer, cfg.NetworkPassphrase))
	mux.HandleFunc("POST /auth", handlePostChallenge(authIssuer))

	// SEP-24: Info
	mux.HandleFunc("GET /sep24/info", handleSEP24Info())

	// SEP-24: Interactive deposit/withdrawal
	mux.Handle("POST /sep24/transactions/deposit/interactive", authIssuer.RequireAuth(handleDepositInteractive(transferManager)))
	mux.Handle("POST /sep24/transactions/withdraw/interactive", authIssuer.RequireAuth(handleWithdrawInteractive(transferManager)))

	// SEP-24: Transaction status
	mux.Handle("GET /sep24/transaction", authIssuer.RequireAuth(handleGetTransaction(transferManager, transferStore, baseURL)))
	mux.Handle("GET /sep24/transactions", authIssuer.RequireAuth(handleGetTransactions(transferStore, baseURL)))
	mux.HandleFunc("GET /transaction/{id}", handleMoreInfo(transferStore))

	// Interactive flow (multi-step Etherfuse KYC + quote + order)
	mux.HandleFunc("GET /interactive", handleGetInteractive(transferManager, etherfuseClient, transferStore))
	mux.HandleFunc("POST /interactive/onboard", handlePostOnboard(transferManager, etherfuseClient, transferStore))
	mux.HandleFunc("GET /interactive/kyc-poll", handleKYCPoll(transferManager, etherfuseClient))
	mux.HandleFunc("POST /interactive/quote", handlePostQuote(transferManager, etherfuseClient, transferStore, assetIdentifiers))
	mux.HandleFunc("POST /interactive/order", handlePostOrder(transferManager, etherfuseClient, transferStore))

	// Etherfuse webhooks
	mux.HandleFunc("POST /webhooks/etherfuse", handleWebhook(transferManager, transferStore, cfg.EtherfuseWebhookSecret, cfg.NetworkPassphrase))

	handler := corsMiddleware(mux)

	addr := fmt.Sprintf(":%d", cfg.AnchorPort)
	log.Printf("Etherfuse Anchor started on %s", addr)
	log.Printf("Stellar.toml: http://localhost:%d/.well-known/stellar.toml", cfg.AnchorPort)
	log.Printf("SEP-10 Auth:  http://localhost:%d/auth", cfg.AnchorPort)
	log.Printf("SEP-24:       http://localhost:%d/sep24/info", cfg.AnchorPort)
	log.Printf("Etherfuse API: %s", cfg.EtherfuseAPIURL)
	log.Printf("Webhook:      http://localhost:%d/webhooks/etherfuse", cfg.AnchorPort)

	return http.ListenAndServe(addr, handler)
}

// corsMiddleware adds CORS headers to all responses and handles OPTIONS preflight requests.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeJSONError writes a JSON error response with the given status code and message.
func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// handleGetChallenge returns a SEP-10 challenge transaction for the given account.
func handleGetChallenge(authIssuer *anchor.AuthIssuer, networkPassphrase string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct := r.URL.Query().Get("account")
		if acct == "" {
			writeJSONError(w, "missing account parameter", http.StatusBadRequest)
			return
		}

		challengeXDR, err := authIssuer.CreateChallenge(context.Background(), acct)
		if err != nil {
			log.Printf("Failed to create challenge: %v", err)
			writeJSONError(w, "failed to create challenge", http.StatusBadRequest)
			return
		}

		response := challengeResponse{
			Transaction:       challengeXDR,
			NetworkPassphrase: networkPassphrase,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}

// handlePostChallenge verifies a signed SEP-10 challenge and returns a JWT token.
func handlePostChallenge(authIssuer *anchor.AuthIssuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var transaction string

		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/x-www-form-urlencoded") {
			if err := r.ParseForm(); err != nil {
				writeJSONError(w, "failed to parse form data", http.StatusBadRequest)
				return
			}
			transaction = r.FormValue("transaction")
		} else {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeJSONError(w, "failed to read request body", http.StatusBadRequest)
				return
			}
			defer func() { _ = r.Body.Close() }()

			var req authRequest
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONError(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			transaction = req.Transaction
		}

		if transaction == "" {
			writeJSONError(w, "missing transaction", http.StatusBadRequest)
			return
		}

		token, err := authIssuer.VerifyChallenge(context.Background(), transaction)
		if err != nil {
			log.Printf("Failed to verify challenge: %v", err)
			writeJSONError(w, "challenge verification failed", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(authResponse{Token: token})
	}
}
