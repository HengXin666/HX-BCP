package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// APIKeyRecord is a persistent machine credential for the management API.
// Only the key hash is stored; the plaintext key is returned exactly once at
// creation time.
type APIKeyRecord struct {
	ID         string
	Name       string
	KeyHash    string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

func (s *Store) CreateAPIKey(ctx context.Context, record APIKeyRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO api_keys (id, name, key_hash, created_at, last_used_at)
VALUES (?, ?, ?, ?, ?)
`,
		record.ID,
		record.Name,
		record.KeyHash,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.LastUsedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("create api key: %w", err)
	}
	return nil
}

func (s *Store) GetAPIKeyByHash(ctx context.Context, keyHash string) (APIKeyRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, key_hash, created_at, last_used_at
FROM api_keys WHERE key_hash = ?
`, keyHash)
	var record APIKeyRecord
	var createdAt, lastUsedAt string
	if err := row.Scan(&record.ID, &record.Name, &record.KeyHash, &createdAt, &lastUsedAt); errors.Is(err, sql.ErrNoRows) {
		return APIKeyRecord{}, ErrNotFound
	} else if err != nil {
		return APIKeyRecord{}, fmt.Errorf("get api key: %w", err)
	}
	var err error
	if record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return APIKeyRecord{}, fmt.Errorf("parse api key created_at: %w", err)
	}
	if record.LastUsedAt, err = time.Parse(time.RFC3339Nano, lastUsedAt); err != nil {
		return APIKeyRecord{}, fmt.Errorf("parse api key last_used_at: %w", err)
	}
	return record, nil
}

func (s *Store) TouchAPIKey(ctx context.Context, id string, lastUsedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE api_keys SET last_used_at = ? WHERE id = ?
`, lastUsedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}

func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKeyRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, key_hash, created_at, last_used_at
FROM api_keys ORDER BY created_at DESC
`)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()
	records := make([]APIKeyRecord, 0)
	for rows.Next() {
		var record APIKeyRecord
		var createdAt, lastUsedAt string
		if err := rows.Scan(&record.ID, &record.Name, &record.KeyHash, &createdAt, &lastUsedAt); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		if record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("parse api key created_at: %w", err)
		}
		if record.LastUsedAt, err = time.Parse(time.RFC3339Nano, lastUsedAt); err != nil {
			return nil, fmt.Errorf("parse api key last_used_at: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api keys: %w", err)
	}
	return records, nil
}

func (s *Store) DeleteAPIKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	return nil
}
