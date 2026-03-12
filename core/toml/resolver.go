package toml

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/marwen-abid/anchor-sdk-go/core/net"
	"github.com/marwen-abid/anchor-sdk-go/errors"
)

const (
	defaultCacheTTL   = 5 * time.Minute
	wellKnownPath     = "/.well-known/stellar.toml"
	maxCurrencyArrays = 100
	maxTomlSize       = 1024 * 1024
)

type cacheEntry struct {
	info      *AnchorInfo
	fetchedAt time.Time
}

type Resolver struct {
	client   *net.Client
	cache    map[string]*cacheEntry
	cacheTTL time.Duration
	mu       sync.RWMutex
}

func NewResolver(client *net.Client) *Resolver {
	return &Resolver{
		client:   client,
		cache:    make(map[string]*cacheEntry),
		cacheTTL: defaultCacheTTL,
	}
}

func (r *Resolver) Resolve(ctx context.Context, domain string) (*AnchorInfo, error) {
	r.mu.RLock()
	entry, exists := r.cache[domain]
	r.mu.RUnlock()

	if exists && time.Since(entry.fetchedAt) < r.cacheTTL {
		return entry.info, nil
	}

	url := "https://" + strings.TrimPrefix(domain, "https://")
	url = strings.TrimSuffix(url, "/") + wellKnownPath

	resp, err := r.client.Get(ctx, url)
	if err != nil {
		return nil, errors.NewCoreError(errors.TOML_FETCH_FAILED, "failed to fetch stellar.toml from "+domain, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, errors.NewCoreError(errors.TOML_FETCH_FAILED, fmt.Sprintf("stellar.toml fetch returned status %d", resp.StatusCode), nil)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTomlSize))
	if err != nil {
		return nil, errors.NewCoreError(errors.TOML_FETCH_FAILED, "failed to read stellar.toml response", err)
	}

	info := r.parse(string(body))

	if info.SigningKey != "" && !strings.HasPrefix(info.SigningKey, "G") {
		return nil, errors.NewCoreError(errors.TOML_SIGNING_KEY_MISMATCH, "invalid SIGNING_KEY format: "+info.SigningKey, nil)
	}

	r.mu.Lock()
	r.cache[domain] = &cacheEntry{
		info:      info,
		fetchedAt: time.Now(),
	}
	r.mu.Unlock()

	return info, nil
}

func (r *Resolver) parse(content string) *AnchorInfo {
	info := &AnchorInfo{}
	lines := strings.Split(content, "\n")

	var inCurrencies bool
	var currentCurrency *CurrencyInfo

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "[[CURRENCIES]]") {
			if currentCurrency != nil && currentCurrency.Code != "" {
				info.Currencies = append(info.Currencies, *currentCurrency)
				if len(info.Currencies) >= maxCurrencyArrays {
					break
				}
			}
			inCurrencies = true
			currentCurrency = &CurrencyInfo{}
			continue
		}

		if strings.HasPrefix(line, "[[") || strings.HasPrefix(line, "[") {
			if currentCurrency != nil && currentCurrency.Code != "" {
				info.Currencies = append(info.Currencies, *currentCurrency)
			}
			inCurrencies = false
			currentCurrency = nil
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"'")

		if inCurrencies && currentCurrency != nil {
			switch key {
			case "code":
				currentCurrency.Code = value
			case "issuer":
				currentCurrency.Issuer = value
			case "status":
				currentCurrency.Status = value
			case "display_decimals":
				_, _ = fmt.Sscanf(value, "%d", &currentCurrency.DisplayDecimals)
			case "anchor_asset_type":
				currentCurrency.AnchorAssetType = value
			case "description":
				currentCurrency.Description = value
			}
		} else {
			switch key {
			case "NETWORK_PASSPHRASE":
				info.NetworkPassphrase = value
			case "SIGNING_KEY":
				info.SigningKey = value
			case "WEB_AUTH_ENDPOINT":
				info.WebAuthEndpoint = value
			case "TRANSFER_SERVER":
				info.TransferServerSep6 = value
			case "TRANSFER_SERVER_SEP0024":
				info.TransferServerSep24 = value
			}
		}
	}

	if currentCurrency != nil && currentCurrency.Code != "" {
		info.Currencies = append(info.Currencies, *currentCurrency)
	}

	return info
}
