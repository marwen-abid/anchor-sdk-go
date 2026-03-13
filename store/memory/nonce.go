// Package memory provides in-memory implementations of store interfaces.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
)

// nonceEntry represents a stored nonce with its expiration and consumption state.
type nonceEntry struct {
	ExpiresAt time.Time
	Consumed  bool
}

// NonceStore is an in-memory implementation of anchorsdk.NonceStore.
// It stores nonces with expiration times and tracks consumption state.
// Access is protected by sync.RWMutex for thread safety.
type NonceStore struct {
	nonces map[string]nonceEntry
	mu     sync.RWMutex
}

// NewNonceStore creates a new in-memory nonce store.
func NewNonceStore() *NonceStore {
	return &NonceStore{
		nonces: make(map[string]nonceEntry),
	}
}

// Add records a nonce as issued with the given expiration time.
// Returns an error if the nonce already exists.
func (s *NonceStore) Add(ctx context.Context, nonce string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.nonces[nonce]; exists {
		return errors.New("nonce already exists")
	}

	s.nonces[nonce] = nonceEntry{
		ExpiresAt: expiresAt,
		Consumed:  false,
	}
	return nil
}

// Consume marks a nonce as used and returns true if successful.
// Returns false if the nonce was not found, was already consumed, or has expired.
// Performs lazy cleanup of expired nonces during operation.
func (s *NonceStore) Consume(ctx context.Context, nonce string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Lazy cleanup: remove expired nonces
	now := time.Now()
	for key, entry := range s.nonces {
		if now.After(entry.ExpiresAt) {
			delete(s.nonces, key)
		}
	}

	// Check if nonce exists and is valid
	entry, exists := s.nonces[nonce]
	if !exists {
		return false, nil
	}

	// Check if already consumed
	if entry.Consumed {
		return false, nil
	}

	// Check if expired
	if now.After(entry.ExpiresAt) {
		delete(s.nonces, nonce)
		return false, nil
	}

	// Mark as consumed
	entry.Consumed = true
	s.nonces[nonce] = entry
	return true, nil
}

// Verify that NonceStore implements anchorsdk.NonceStore
var _ anchorsdk.NonceStore = (*NonceStore)(nil)
