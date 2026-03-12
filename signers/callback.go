package signers

import (
	"context"

	"github.com/marwen-abid/anchor-sdk-go"
)

// callbackSigner wraps a custom signing function for external signing services.
type callbackSigner struct {
	publicKey string
	signFunc  func(context.Context, string, string) (string, error)
}

// FromCallback creates a Signer from a public key and an arbitrary signing function.
// The signFunc receives (ctx, xdr, networkPassphrase) and returns (signedXDR, error).
// Intended for wrapping HSMs, custodial APIs, or any external signing service.
func FromCallback(
	publicKey string,
	signFunc func(context.Context, string, string) (string, error),
) anchorsdk.Signer {
	return &callbackSigner{
		publicKey: publicKey,
		signFunc:  signFunc,
	}
}

// PublicKey returns the Stellar address (G...) for this signer.
func (s *callbackSigner) PublicKey() string {
	return s.publicKey
}

// SignTransaction signs a Stellar transaction envelope (base64 XDR) by delegating to the callback function.
// Returns the signed envelope as base64 XDR.
func (s *callbackSigner) SignTransaction(ctx context.Context, xdr, networkPassphrase string) (string, error) {
	return s.signFunc(ctx, xdr, networkPassphrase)
}
