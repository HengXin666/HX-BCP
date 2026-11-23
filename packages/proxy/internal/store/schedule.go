package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) ClaimDueSubscriptions(
	ctx context.Context,
	now time.Time,
	leaseUntil time.Time,
	limit int,
) ([]string, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin subscription refresh claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	nowText := now.UTC().Format(time.RFC3339Nano)
	rows, err := transaction.QueryContext(ctx, `
SELECT id
FROM subscriptions
WHERE
    enabled = 1
    AND (next_refresh_at IS NULL OR next_refresh_at <= ?)
ORDER BY COALESCE(next_refresh_at, created_at) ASC, id ASC
LIMIT ?
`, nowText, limit)
	if err != nil {
		return nil, fmt.Errorf("select due subscriptions: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan due subscription: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate due subscriptions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close due subscription rows: %w", err)
	}

	leaseText := leaseUntil.UTC().Format(time.RFC3339Nano)
	claimed := make([]string, 0, len(ids))
	for _, id := range ids {
		result, err := transaction.ExecContext(ctx, `
UPDATE subscriptions
SET next_refresh_at = ?
WHERE
    id = ?
    AND enabled = 1
    AND (next_refresh_at IS NULL OR next_refresh_at <= ?)
`, leaseText, id, nowText)
		if err != nil {
			return nil, fmt.Errorf("claim subscription %q: %w", id, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read subscription claim result: %w", err)
		}
		if rowsAffected == 1 {
			claimed = append(claimed, id)
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("commit subscription refresh claim: %w", err)
	}
	committed = true
	return claimed, nil
}
