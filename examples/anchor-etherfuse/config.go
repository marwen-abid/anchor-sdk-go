package main

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration for the Etherfuse anchor example.
type Config struct {
	// Etherfuse API
	EtherfuseAPIKey        string
	EtherfuseAPIURL        string
	EtherfuseWebhookSecret string

	// Stellar Anchor
	AnchorSecret      string
	AnchorDomain      string
	AnchorPort        int
	JWTSecret         string
	NetworkPassphrase string
	HorizonURL        string

	// Asset Issuers
	USDCIssuer  string
	CETESIssuer string
}

// LoadConfig reads configuration from a .env file (if present) and environment
// variables. Environment variables take precedence over .env file values.
// It searches for .env in the current directory and in the package directory.
func LoadConfig() (*Config, error) {
	loadDotEnv(".env")
	loadDotEnv("examples/anchor-etherfuse/.env")

	cfg := &Config{
		EtherfuseAPIKey:        getEnv("ETHERFUSE_API_KEY", ""),
		EtherfuseAPIURL:        getEnv("ETHERFUSE_API_URL", "https://api.sand.etherfuse.com"),
		EtherfuseWebhookSecret: getEnv("ETHERFUSE_WEBHOOK_SECRET", ""),
		AnchorSecret:           getEnv("ANCHOR_SECRET", "SAPCL3RTB7VB3VQXIVIM4P6AH5C7ZQDHY772GOCAWASACCFFWOMQVP4S"),
		AnchorDomain:           getEnv("ANCHOR_DOMAIN", "localhost:8000"),
		AnchorPort:             getEnvInt("ANCHOR_PORT", 8000),
		JWTSecret:              getEnv("JWT_SECRET", "test-jwt-secret-key-for-development"),
		NetworkPassphrase:      getEnv("NETWORK_PASSPHRASE", "Test SDF Network ; September 2015"),
		HorizonURL:             getEnv("HORIZON_URL", "https://horizon-testnet.stellar.org"),
		USDCIssuer:             getEnv("USDC_ISSUER", "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"),
		CETESIssuer:            getEnv("CETES_ISSUER", "GC3CW7EDYRTWQ635VDIGY6S4ZUF5L6TQ7AA4MWS7LEQDBLUSZXV7UPS4"),
	}

	if cfg.EtherfuseAPIKey == "" {
		return nil, errors.New("ETHERFUSE_API_KEY is required (set in .env or environment)")
	}

	return cfg, nil
}

// loadDotEnv reads a .env file and sets environment variables for any keys
// not already set in the environment. Silently ignores missing files.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Only set if not already in environment
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
