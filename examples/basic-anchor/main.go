// Package main implements a standalone example anchor server with SEP-1 and SEP-10 support.
//
// This example demonstrates:
// - Serving stellar.toml at /.well-known/stellar.toml (SEP-1)
// - SEP-10 Web Authentication with challenge/response flow
// - CORS middleware for cross-origin requests
//
// Run with: go run ./examples/anchor
// Test with: curl http://localhost:8000/.well-known/stellar.toml
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/marwen-abid/anchor-sdk-go/anchor"
	"github.com/marwen-abid/anchor-sdk-go/core/account"
	"github.com/marwen-abid/anchor-sdk-go/core/toml"
	"github.com/marwen-abid/anchor-sdk-go/observer"
	"github.com/marwen-abid/anchor-sdk-go/signers"
	"github.com/marwen-abid/anchor-sdk-go/store/memory"
)

const (
	// Test anchor secret key (DO NOT use in production)
	testAnchorSecret = "SAPCL3RTB7VB3VQXIVIM4P6AH5C7ZQDHY772GOCAWASACCFFWOMQVP4S"
	// JWT secret for HMAC signing (DO NOT use in production)
	testJWTSecret = "test-jwt-secret-key-for-development"
	// Domain for this anchor (used in SEP-10)
	testDomain = "localhost:8000"
	// Testnet network passphrase
	testNetworkPassphrase = "Test SDF Network ; September 2015"
	// JWT token expiration duration
	jwtExpiry = 24 * time.Hour
	// Testnet Horizon URL
	horizonURL = "https://horizon-testnet.stellar.org"
)

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
	port := flag.Int("port", 8000, "Port to listen on")
	flag.Parse()

	signer, err := signers.FromSecret(testAnchorSecret)
	if err != nil {
		log.Fatalf("Failed to create signer: %v", err)
	}

	nonceStore := memory.NewNonceStore()

	jwtIssuer, jwtVerifier := anchor.NewHMACJWT(
		[]byte(testJWTSecret),
		testDomain,
		jwtExpiry,
	)

	accountFetcher := account.NewHorizonAccountFetcher(horizonURL)

	authIssuer, err := anchor.NewAuthIssuer(anchor.AuthConfig{
		Domain:            testDomain,
		NetworkPassphrase: testNetworkPassphrase,
		Signer:            signer,
		NonceStore:        nonceStore,
		JWTIssuer:         jwtIssuer,
		JWTVerifier:       jwtVerifier,
		AccountFetcher:    accountFetcher,
	})
	if err != nil {
		log.Fatalf("Failed to create auth issuer: %v", err)
	}

	transferStore := memory.NewTransferStore()
	transferConfig := anchor.Config{
		Domain:              testDomain,
		InteractiveBaseURL:  fmt.Sprintf("http://%s/interactive", testDomain),
		DistributionAccount: signer.PublicKey(),
		BaseURL:             "http://" + testDomain,
	}
	transferManager := anchor.NewTransferManager(transferStore, transferConfig, nil)

	distributionAccount := signer.PublicKey()
	obs := observer.NewHorizonObserver(
		horizonURL,
		observer.WithCursor(currentCursor),
		observer.WithCursorSaver(func(cursor string) error {
			currentCursor = cursor
			return nil
		}),
	)

	if err := observer.AutoMatchPayments(obs, transferManager, distributionAccount); err != nil {
		log.Fatalf("Failed to setup auto-matching: %v", err)
	}

	anchorInfo := &toml.AnchorInfo{
		NetworkPassphrase:   testNetworkPassphrase,
		SigningKey:          signer.PublicKey(),
		WebAuthEndpoint:     fmt.Sprintf("http://%s/auth", testDomain),
		TransferServerSep6:  fmt.Sprintf("http://%s/sep6", testDomain),
		TransferServerSep24: fmt.Sprintf("http://%s/sep24", testDomain),
		Currencies: []toml.CurrencyInfo{
			{
				Code:            "USDC",
				Issuer:          "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
				Status:          "test",
				DisplayDecimals: 2,
				AnchorAssetType: "fiat",
				IsAssetAnchored: true,
				Desc:            "Test USDC token for development",
				Description:     "Test USDC token for development",
			},
		},
	}
	tomlPublisher := toml.NewPublisher(anchorInfo)

	go func() {
		if err := obs.Start(context.Background()); err != nil {
			log.Printf("Observer stopped: %v", err)
		}
	}()

	log.Printf("Observer started watching %s", distributionAccount)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/stellar.toml", tomlPublisher.Handler())
	mux.HandleFunc("GET /auth", handleGetChallenge(authIssuer))
	mux.HandleFunc("POST /auth", handlePostChallenge(authIssuer))
	mux.HandleFunc("GET /sep24/info", handleSEP24Info())
	mux.Handle("POST /sep24/transactions/deposit/interactive", authIssuer.RequireAuth(handleDepositInteractive(transferManager)))
	mux.Handle("POST /sep24/transactions/withdraw/interactive", authIssuer.RequireAuth(handleWithdrawInteractive(transferManager)))
	mux.Handle("GET /sep24/transaction", authIssuer.RequireAuth(handleGetTransaction(transferManager)))
	mux.Handle("GET /sep24/transactions", authIssuer.RequireAuth(handleGetTransactions(transferStore, transferConfig.BaseURL)))
	mux.HandleFunc("GET /transaction/{id}", handleMoreInfo(transferManager))
	mux.HandleFunc("GET /interactive", handleGetInteractive(transferManager))
	mux.HandleFunc("POST /interactive", handlePostInteractive(transferManager))
	mux.HandleFunc("GET /sep6/info", handleSEP6Info())
	mux.Handle("GET /sep6/deposit", authIssuer.RequireAuth(handleSEP6Deposit(transferManager)))
	mux.Handle("GET /sep6/withdraw", authIssuer.RequireAuth(handleSEP6Withdraw(transferManager)))
	mux.Handle("GET /sep6/transaction", authIssuer.RequireAuth(handleSEP6Transaction(transferManager)))
	mux.Handle("GET /sep6/transactions", authIssuer.RequireAuth(handleSEP6Transactions(transferStore, transferConfig.BaseURL)))

	handler := corsMiddleware(mux)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Example anchor started on %s", addr)
	log.Printf("Stellar.toml: http://localhost:%d/.well-known/stellar.toml", *port)
	log.Printf("SEP-10 Auth: http://localhost:%d/auth", *port)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
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
func handleGetChallenge(authIssuer *anchor.AuthIssuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct := r.URL.Query().Get("account")
		if acct == "" {
			writeJSONError(w, "missing account parameter", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		challengeXDR, err := authIssuer.CreateChallenge(ctx, acct)
		if err != nil {
			log.Printf("Failed to create challenge: %v", err)
			writeJSONError(w, "failed to create challenge", http.StatusBadRequest)
			return
		}

		response := challengeResponse{
			Transaction:       challengeXDR,
			NetworkPassphrase: testNetworkPassphrase,
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

		ctx := context.Background()
		token, err := authIssuer.VerifyChallenge(ctx, transaction)
		if err != nil {
			log.Printf("Failed to verify challenge: %v", err)
			writeJSONError(w, "challenge verification failed", http.StatusBadRequest)
			return
		}

		response := authResponse{
			Token: token,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}
