package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/stellar/go-stellar-sdk/keypair"
)

// GenerateNonce generates a cryptographically secure random nonce and returns it as a base64-encoded string.
// The length parameter specifies the number of random bytes to generate.
// For SEP-10 compatibility, use 48 bytes which encodes to 64 characters in base64.
func GenerateNonce(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("nonce length must be positive, got %d", length)
	}

	nonce := make([]byte, length)
	_, err := rand.Read(nonce)
	if err != nil {
		return "", fmt.Errorf("failed to generate random nonce: %w", err)
	}

	encodedNonce := base64.RawURLEncoding.EncodeToString(nonce)
	return encodedNonce, nil
}

// VerifySignature verifies a Stellar signature against a message using the provided public key.
// It returns true if the signature is valid, false otherwise.
// The publicKey parameter should be a valid Stellar public key string (starting with 'G').
// The message and signature parameters should be the original message data and the signature to verify.
func VerifySignature(publicKey, message, signature string) (bool, error) {
	kp, err := keypair.ParseAddress(publicKey)
	if err != nil {
		return false, fmt.Errorf("failed to parse public key: %w", err)
	}

	err = kp.Verify([]byte(message), []byte(signature))
	if err != nil {
		return false, nil
	}

	return true, nil
}

// HashSHA256 computes the SHA256 hash of the provided data and returns it as a byte slice.
func HashSHA256(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}
