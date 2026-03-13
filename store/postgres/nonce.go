package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
)

// NonceStore is a PostgreSQL implementation of anchorsdk.NonceStore.
type NonceStore struct {
	pool *pgxpool.Pool
}

// NewNonceStore creates a new PostgreSQL-backed nonce store.
func NewNonceStore(pool *pgxpool.Pool) *NonceStore {
	return &NonceStore{pool: pool}
}

func (s *NonceStore) Add(ctx context.Context, nonce string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO nonces (nonce, expires_at) VALUES ($1, $2)`,
		nonce, expiresAt,
	)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errors.New("nonce already exists")
	}
	return err
}

func (s *NonceStore) Consume(ctx context.Context, nonce string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE nonces SET consumed = TRUE WHERE nonce = $1 AND consumed = FALSE AND expires_at > now()`,
		nonce,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

var _ anchorsdk.NonceStore = (*NonceStore)(nil)
