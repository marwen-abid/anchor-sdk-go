package signers

import (
	"context"
	"errors"
	"fmt"

	"github.com/marwen-abid/anchor-sdk-go"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
)

// keypairSigner wraps a stellar/go keypair for signing transactions.
type keypairSigner struct {
	kp *keypair.Full
}

// FromSecret creates a Signer from a Stellar secret key (S...).
// Intended for server-side use (exchanges, backends, bots).
// Returns an error if the secret key is invalid.
func FromSecret(secret string) (anchorsdk.Signer, error) {
	kp, err := keypair.ParseFull(secret)
	if err != nil {
		return nil, fmt.Errorf("invalid secret key: %w", err)
	}
	return &keypairSigner{kp: kp}, nil
}

// PublicKey returns the Stellar address (G...) for this keypair.
func (s *keypairSigner) PublicKey() string {
	return s.kp.Address()
}

// SignTransaction signs a Stellar transaction envelope (base64 XDR).
// It parses the XDR, signs the transaction hash with the keypair, and returns
// the signed envelope as base64 XDR.
func (s *keypairSigner) SignTransaction(ctx context.Context, xdr, networkPassphrase string) (string, error) {
	parsed, err := txnbuild.TransactionFromXDR(xdr)
	if err != nil {
		return "", fmt.Errorf("failed to parse transaction XDR: %w", err)
	}

	tx, ok := parsed.Transaction()
	if !ok {
		return "", errors.New("expected a Transaction, got a FeeBumpTransaction")
	}

	signedTx, err := tx.Sign(networkPassphrase, s.kp)
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	return signedTx.Base64()
}
