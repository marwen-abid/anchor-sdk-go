package account

import (
	"context"
	"fmt"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
)

// HorizonAccountFetcher implements anchorsdk.AccountFetcher using a Horizon server.
type HorizonAccountFetcher struct {
	client *horizonclient.Client
}

// NewHorizonAccountFetcher creates an AccountFetcher backed by the given Horizon URL.
func NewHorizonAccountFetcher(horizonURL string) *HorizonAccountFetcher {
	return &HorizonAccountFetcher{
		client: &horizonclient.Client{HorizonURL: horizonURL},
	}
}

// FetchSigners returns the signers and thresholds for a Stellar account.
func (f *HorizonAccountFetcher) FetchSigners(_ context.Context, accountID string) ([]anchorsdk.AccountSigner, anchorsdk.AccountThresholds, error) {
	account, err := f.client.AccountDetail(horizonclient.AccountRequest{
		AccountID: accountID,
	})
	if err != nil {
		return nil, anchorsdk.AccountThresholds{}, fmt.Errorf("failed to fetch account %s: %w", accountID, err)
	}

	signers := make([]anchorsdk.AccountSigner, len(account.Signers))
	for i, s := range account.Signers {
		signers[i] = anchorsdk.AccountSigner{
			Key:    s.Key,
			Weight: s.Weight,
		}
	}

	thresholds := anchorsdk.AccountThresholds{
		Low:    account.Thresholds.LowThreshold,
		Medium: account.Thresholds.MedThreshold,
		High:   account.Thresholds.HighThreshold,
	}

	return signers, thresholds, nil
}
