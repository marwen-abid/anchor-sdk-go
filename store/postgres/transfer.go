package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
)

// TransferStore is a PostgreSQL implementation of anchorsdk.TransferStore.
type TransferStore struct {
	pool *pgxpool.Pool
}

// NewTransferStore creates a new PostgreSQL-backed transfer store.
func NewTransferStore(pool *pgxpool.Pool) *TransferStore {
	return &TransferStore{pool: pool}
}

const transferColumns = `id, kind, mode, status, asset_code, asset_issuer, account, amount,
	interactive_token, interactive_url, external_ref, stellar_tx_hash,
	message, metadata, created_at, updated_at, completed_at`

// scanTransfer scans a single transfer row into a Transfer struct.
func scanTransfer(row pgx.Row) (*anchorsdk.Transfer, error) {
	var t anchorsdk.Transfer
	var metadataJSON []byte

	err := row.Scan(
		&t.ID, &t.Kind, &t.Mode, &t.Status,
		&t.AssetCode, &t.AssetIssuer, &t.Account, &t.Amount,
		&t.InteractiveToken, &t.InteractiveURL, &t.ExternalRef, &t.StellarTxHash,
		&t.Message, &metadataJSON, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &t.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return &t, nil
}

// scanTransfers scans multiple transfer rows.
func scanTransfers(rows pgx.Rows) ([]*anchorsdk.Transfer, error) {
	defer rows.Close()
	var result []*anchorsdk.Transfer
	for rows.Next() {
		var t anchorsdk.Transfer
		var metadataJSON []byte

		err := rows.Scan(
			&t.ID, &t.Kind, &t.Mode, &t.Status,
			&t.AssetCode, &t.AssetIssuer, &t.Account, &t.Amount,
			&t.InteractiveToken, &t.InteractiveURL, &t.ExternalRef, &t.StellarTxHash,
			&t.Message, &metadataJSON, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt,
		)
		if err != nil {
			return nil, err
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &t.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}

		result = append(result, &t)
	}
	return result, rows.Err()
}

func (s *TransferStore) Save(ctx context.Context, transfer *anchorsdk.Transfer) error {
	metadataJSON, err := json.Marshal(transfer.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO transfers (id, kind, mode, status, asset_code, asset_issuer, account, amount,
			interactive_token, interactive_url, external_ref, stellar_tx_hash,
			message, metadata, created_at, updated_at, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		transfer.ID, transfer.Kind, transfer.Mode, transfer.Status,
		transfer.AssetCode, transfer.AssetIssuer, transfer.Account, transfer.Amount,
		transfer.InteractiveToken, transfer.InteractiveURL, transfer.ExternalRef, transfer.StellarTxHash,
		transfer.Message, metadataJSON, transfer.CreatedAt, transfer.UpdatedAt, transfer.CompletedAt,
	)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errors.New("transfer already exists")
	}
	return err
}

func (s *TransferStore) FindByID(ctx context.Context, id string) (*anchorsdk.Transfer, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+transferColumns+` FROM transfers WHERE id = $1`, id)

	t, err := scanTransfer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("transfer not found")
	}
	return t, err
}

func (s *TransferStore) FindByAccount(ctx context.Context, account string) ([]*anchorsdk.Transfer, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+transferColumns+` FROM transfers WHERE account = $1 ORDER BY created_at DESC`, account)
	if err != nil {
		return nil, err
	}
	return scanTransfers(rows)
}

func (s *TransferStore) Update(ctx context.Context, id string, update *anchorsdk.TransferUpdate) error {
	setClauses := []string{"updated_at = now()"}
	args := []any{}
	argIdx := 1

	if update.Status != nil {
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, string(*update.Status))
		argIdx++
	}
	if update.Amount != nil {
		setClauses = append(setClauses, fmt.Sprintf("amount = $%d", argIdx))
		args = append(args, *update.Amount)
		argIdx++
	}
	if update.ExternalRef != nil {
		setClauses = append(setClauses, fmt.Sprintf("external_ref = $%d", argIdx))
		args = append(args, *update.ExternalRef)
		argIdx++
	}
	if update.StellarTxHash != nil {
		setClauses = append(setClauses, fmt.Sprintf("stellar_tx_hash = $%d", argIdx))
		args = append(args, *update.StellarTxHash)
		argIdx++
	}
	if update.InteractiveToken != nil {
		setClauses = append(setClauses, fmt.Sprintf("interactive_token = $%d", argIdx))
		args = append(args, *update.InteractiveToken)
		argIdx++
	}
	if update.InteractiveURL != nil {
		setClauses = append(setClauses, fmt.Sprintf("interactive_url = $%d", argIdx))
		args = append(args, *update.InteractiveURL)
		argIdx++
	}
	if update.Message != nil {
		setClauses = append(setClauses, fmt.Sprintf("message = $%d", argIdx))
		args = append(args, *update.Message)
		argIdx++
	}
	if update.Metadata != nil {
		metadataJSON, err := json.Marshal(update.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
		setClauses = append(setClauses, fmt.Sprintf("metadata = $%d", argIdx))
		args = append(args, metadataJSON)
		argIdx++
	}
	if update.CompletedAt != nil {
		setClauses = append(setClauses, fmt.Sprintf("completed_at = $%d", argIdx))
		args = append(args, *update.CompletedAt)
		argIdx++
	}

	query := fmt.Sprintf("UPDATE transfers SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), argIdx)
	args = append(args, id)

	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("transfer not found")
	}
	return nil
}

func (s *TransferStore) List(ctx context.Context, filters anchorsdk.TransferFilters) ([]*anchorsdk.Transfer, error) {
	whereClauses := []string{}
	args := []any{}
	argIdx := 1

	if filters.Account != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("account = $%d", argIdx))
		args = append(args, filters.Account)
		argIdx++
	}
	if filters.AssetCode != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("asset_code = $%d", argIdx))
		args = append(args, filters.AssetCode)
		argIdx++
	}
	if filters.Status != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, string(*filters.Status))
		argIdx++
	}
	if filters.Kind != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("kind = $%d", argIdx))
		args = append(args, string(*filters.Kind))
		argIdx++
	}

	query := `SELECT ` + transferColumns + ` FROM transfers`
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += " ORDER BY created_at DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filters.Limit)
		argIdx++ //nolint:ineffassign // keep argIdx consistent for future clauses
	}
	if filters.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filters.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanTransfers(rows)
}

var _ anchorsdk.TransferStore = (*TransferStore)(nil)
