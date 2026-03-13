package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
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
