package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CFAccountRecord is a Cloudflare account whose API token feeds the fleet.
type CFAccountRecord struct {
	ID                string
	Email             string
	CFAccountID       string
	APITokenEncrypted string
	Status            string // active | banned
	BannedAt          string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// FleetWorkerRecord tracks per-worker fleet health state.
type FleetWorkerRecord struct {
	AccountID    string
	WorkerName   string
	CanonicalURL string
	ProviderID   string
	Failures     int
	LastCheck    string
	LastDetail   string
	CreatedAt    time.Time
}

// ListCFAccounts returns all fleet CF accounts ordered by creation.
func (s *Store) ListCFAccounts(ctx context.Context) ([]CFAccountRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, email, cf_account_id, api_token_encrypted, status, banned_at, created_at, updated_at
FROM cf_accounts ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list cf accounts: %w", err)
	}
	defer rows.Close()
	records := make([]CFAccountRecord, 0)
	for rows.Next() {
		record, err := scanCFAccount(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cf accounts: %w", err)
	}
	return records, nil
}

func (s *Store) CreateCFAccount(
	ctx context.Context, record CFAccountRecord,
) (CFAccountRecord, error) {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	record.UpdatedAt = record.CreatedAt
	_, err := s.db.ExecContext(ctx, `
INSERT INTO cf_accounts(id, email, cf_account_id, api_token_encrypted, status, banned_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Email, record.CFAccountID, record.APITokenEncrypted,
		record.Status, record.BannedAt,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return CFAccountRecord{}, ErrConflict
		}
		return CFAccountRecord{}, fmt.Errorf("create cf account: %w", err)
	}
	return record, nil
}

func (s *Store) GetCFAccount(ctx context.Context, id string) (CFAccountRecord, error) {
	record, err := scanCFAccount(s.db.QueryRowContext(ctx, `
SELECT id, email, cf_account_id, api_token_encrypted, status, banned_at, created_at, updated_at
FROM cf_accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return CFAccountRecord{}, ErrNotFound
	}
	if err != nil {
		return CFAccountRecord{}, fmt.Errorf("get cf account: %w", err)
	}
	return record, nil
}

// SetCFAccountStatus marks an account active/banned.
func (s *Store) SetCFAccountStatus(ctx context.Context, id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	bannedAt := ""
	if status == "banned" {
		bannedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE cf_accounts SET status = ?, banned_at = ?, updated_at = ? WHERE id = ?`,
		status, bannedAt, now, id)
	if err != nil {
		return fmt.Errorf("set cf account status: %w", err)
	}
	return nil
}

func (s *Store) DeleteCFAccount(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM cf_accounts WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete cf account: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM fleet_workers WHERE account_id = ?", id); err != nil {
		return fmt.Errorf("delete fleet workers: %w", err)
	}
	return nil
}

// ListFleetWorkers returns health state for one account.
func (s *Store) ListFleetWorkers(ctx context.Context, accountID string) ([]FleetWorkerRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT account_id, worker_name, canonical_url, provider_id, failures, last_check, last_detail, created_at
FROM fleet_workers WHERE account_id = ? ORDER BY created_at ASC, worker_name ASC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list fleet workers: %w", err)
	}
	defer rows.Close()
	records := make([]FleetWorkerRecord, 0)
	for rows.Next() {
		record, err := scanFleetWorker(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fleet workers: %w", err)
	}
	return records, nil
}

func (s *Store) UpsertFleetWorker(ctx context.Context, record FleetWorkerRecord) error {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO fleet_workers(account_id, worker_name, canonical_url, provider_id, failures, last_check, last_detail, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, worker_name) DO UPDATE SET
    canonical_url = excluded.canonical_url,
    provider_id = excluded.provider_id,
    failures = excluded.failures,
    last_check = excluded.last_check,
    last_detail = excluded.last_detail`,
		record.AccountID, record.WorkerName, record.CanonicalURL, record.ProviderID,
		record.Failures, record.LastCheck, record.LastDetail,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert fleet worker: %w", err)
	}
	return nil
}

func (s *Store) DeleteFleetWorker(ctx context.Context, accountID, workerName string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM fleet_workers WHERE account_id = ? AND worker_name = ?",
		accountID, workerName)
	if err != nil {
		return fmt.Errorf("delete fleet worker: %w", err)
	}
	return nil
}

func scanCFAccount(scanner interface{ Scan(...any) error }) (CFAccountRecord, error) {
	var record CFAccountRecord
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&record.ID, &record.Email, &record.CFAccountID, &record.APITokenEncrypted,
		&record.Status, &record.BannedAt, &createdAt, &updatedAt,
	); err != nil {
		return CFAccountRecord{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return record, nil
}

func scanFleetWorker(scanner interface{ Scan(...any) error }) (FleetWorkerRecord, error) {
	var record FleetWorkerRecord
	var createdAt string
	if err := scanner.Scan(
		&record.AccountID, &record.WorkerName, &record.CanonicalURL, &record.ProviderID,
		&record.Failures, &record.LastCheck, &record.LastDetail, &createdAt,
	); err != nil {
		return FleetWorkerRecord{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return record, nil
}

// ReplaceChannelProviders rewrites the channel→provider links (aggregation).
func (s *Store) ReplaceChannelProviders(ctx context.Context, channelID string, providerIDs []string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM channel_providers WHERE channel_id = ?", channelID); err != nil {
		return fmt.Errorf("clear channel providers: %w", err)
	}
	for _, providerID := range providerIDs {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO channel_providers(channel_id, provider_id) VALUES (?, ?)`,
			channelID, providerID); err != nil {
			return fmt.Errorf("link channel provider: %w", err)
		}
	}
	return nil
}

// ListChannelProviders returns the provider ids aggregated under a channel.
func (s *Store) ListChannelProviders(ctx context.Context, channelID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT provider_id FROM channel_providers WHERE channel_id = ? ORDER BY provider_id",
		channelID)
	if err != nil {
		return nil, fmt.Errorf("list channel providers: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// ListChannelProvidersForChannels resolves the aggregated provider ids of many
// channels in one query so the channel list view avoids a per-channel round
// trip. Channels without links map to an empty (non-nil) slice.
func (s *Store) ListChannelProvidersForChannels(ctx context.Context, channelIDs []string) (map[string][]string, error) {
	if len(channelIDs) == 0 {
		return map[string][]string{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(channelIDs)), ",")
	arguments := make([]any, len(channelIDs))
	for index, id := range channelIDs {
		arguments[index] = id
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT channel_id, provider_id
FROM channel_providers
WHERE channel_id IN (`+placeholders+`)
ORDER BY channel_id ASC, provider_id ASC
`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list channel providers for channels: %w", err)
	}
	defer rows.Close()
	links := make(map[string][]string, len(channelIDs))
	for _, id := range channelIDs {
		links[id] = []string{}
	}
	for rows.Next() {
		var channelID, providerID string
		if err := rows.Scan(&channelID, &providerID); err != nil {
			return nil, err
		}
		links[channelID] = append(links[channelID], providerID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return links, nil
}
