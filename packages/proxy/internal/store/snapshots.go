package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SubscriptionSnapshotRecord struct {
	ID                string
	SubscriptionID    string
	ContentHash       string
	FetchedAt         time.Time
	NodeCount         int
	Status            string
	ArtifactPath      string
	ParseSummaryJSON  string
	FetchMetadataJSON string
	NextRefreshAt     time.Time
	CreatedAt         time.Time
}

func (s *Store) GetSubscriptionSnapshotByHash(
	ctx context.Context,
	subscriptionID string,
	contentHash string,
) (SubscriptionSnapshotRecord, error) {
	row := s.db.QueryRowContext(ctx, snapshotSelect+" WHERE subscription_id = ? AND content_hash = ?", subscriptionID, contentHash)
	record, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionSnapshotRecord{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionSnapshotRecord{}, fmt.Errorf("get subscription snapshot by hash: %w", err)
	}
	return record, nil
}

func (s *Store) CommitSubscriptionSnapshot(
	ctx context.Context,
	record SubscriptionSnapshotRecord,
) error {
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	if _, err := transaction.ExecContext(ctx, `
UPDATE subscription_snapshots
SET status = 'retired'
WHERE subscription_id = ? AND status = 'active'
`, record.SubscriptionID); err != nil {
		return fmt.Errorf("retire previous subscription snapshot: %w", err)
	}
	// Agent Note: ../../.agents/notes/implemented/bug-fix/2026-10-28-snapshot-retire-sweep-cost.md
	if err := pruneInactiveSnapshotLinks(ctx, transaction, record.SubscriptionID, record.ID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO subscription_snapshots(
    id,
    subscription_id,
    content_hash,
    fetched_at,
    node_count,
    status,
    artifact_path,
    parse_summary_json,
    fetch_metadata_json,
    created_at
) VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)
`,
		record.ID,
		record.SubscriptionID,
		record.ContentHash,
		record.FetchedAt.UTC().Format(time.RFC3339Nano),
		record.NodeCount,
		record.ArtifactPath,
		record.ParseSummaryJSON,
		record.FetchMetadataJSON,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		if isUniqueConstraint(err) {
			return ErrConflict
		}
		return fmt.Errorf("insert subscription snapshot: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
UPDATE subscriptions
SET
    last_success_snapshot_id = ?,
    consecutive_failures = 0,
    last_failure_json = '',
    last_refresh_attempt_at = ?,
    next_refresh_at = ?,
    updated_at = ?
WHERE id = ?
`,
		record.ID,
		record.FetchedAt.UTC().Format(time.RFC3339Nano),
		record.NextRefreshAt.UTC().Format(time.RFC3339Nano),
		record.FetchedAt.UTC().Format(time.RFC3339Nano),
		record.SubscriptionID,
	); err != nil {
		return fmt.Errorf("activate subscription snapshot: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit subscription snapshot: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) ActivateSubscriptionSnapshot(
	ctx context.Context,
	subscriptionID string,
	snapshotID string,
	refreshedAt time.Time,
	nextRefreshAt time.Time,
	fetchMetadataJSON string,
) error {
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin snapshot activation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	if _, err := transaction.ExecContext(ctx, `
UPDATE subscription_snapshots
SET status = 'retired'
WHERE subscription_id = ? AND status = 'active' AND id <> ?
`, subscriptionID, snapshotID); err != nil {
		return fmt.Errorf("retire active subscription snapshot: %w", err)
	}
	if err := pruneInactiveSnapshotLinks(ctx, transaction, subscriptionID, snapshotID); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `
UPDATE subscription_snapshots
SET
    status = 'active',
    fetched_at = ?,
    fetch_metadata_json = ?
WHERE id = ? AND subscription_id = ?
`,
		refreshedAt.UTC().Format(time.RFC3339Nano),
		fetchMetadataJSON,
		snapshotID,
		subscriptionID,
	)
	if err != nil {
		return fmt.Errorf("activate existing subscription snapshot: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read snapshot activation result: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	if _, err := transaction.ExecContext(ctx, `
UPDATE subscriptions
SET
    last_success_snapshot_id = ?,
    consecutive_failures = 0,
    last_failure_json = '',
    last_refresh_attempt_at = ?,
    next_refresh_at = ?,
    updated_at = ?
WHERE id = ?
`,
		snapshotID,
		refreshedAt.UTC().Format(time.RFC3339Nano),
		nextRefreshAt.UTC().Format(time.RFC3339Nano),
		refreshedAt.UTC().Format(time.RFC3339Nano),
		subscriptionID,
	); err != nil {
		return fmt.Errorf("update subscription after snapshot activation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit snapshot activation: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) MarkSubscriptionRefreshFailed(
	ctx context.Context,
	subscriptionID string,
	failedAt time.Time,
	nextRefreshAt time.Time,
	failureJSON string,
) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE subscriptions
SET
    consecutive_failures = consecutive_failures + 1,
    last_failure_json = ?,
    last_refresh_attempt_at = ?,
    next_refresh_at = ?,
    updated_at = ?
WHERE id = ?
`,
		failureJSON,
		failedAt.UTC().Format(time.RFC3339Nano),
		nextRefreshAt.UTC().Format(time.RFC3339Nano),
		failedAt.UTC().Format(time.RFC3339Nano),
		subscriptionID,
	)
	if err != nil {
		return fmt.Errorf("mark subscription refresh failed: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read failed refresh result: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetSubscriptionSnapshot(
	ctx context.Context,
	id string,
) (SubscriptionSnapshotRecord, error) {
	row := s.db.QueryRowContext(ctx, snapshotSelect+" WHERE id = ?", id)
	record, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionSnapshotRecord{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionSnapshotRecord{}, fmt.Errorf("get subscription snapshot: %w", err)
	}
	return record, nil
}

const snapshotSelect = `
SELECT
    id,
    subscription_id,
    content_hash,
    fetched_at,
    node_count,
    status,
    COALESCE(artifact_path, ''),
    parse_summary_json,
    fetch_metadata_json,
    created_at
FROM subscription_snapshots`

func scanSnapshot(source scanner) (SubscriptionSnapshotRecord, error) {
	var record SubscriptionSnapshotRecord
	var fetchedAt string
	var createdAt string
	if err := source.Scan(
		&record.ID,
		&record.SubscriptionID,
		&record.ContentHash,
		&fetchedAt,
		&record.NodeCount,
		&record.Status,
		&record.ArtifactPath,
		&record.ParseSummaryJSON,
		&record.FetchMetadataJSON,
		&createdAt,
	); err != nil {
		return SubscriptionSnapshotRecord{}, err
	}
	parsedFetchedAt, err := time.Parse(time.RFC3339Nano, fetchedAt)
	if err != nil {
		return SubscriptionSnapshotRecord{}, fmt.Errorf("parse snapshot fetched_at: %w", err)
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return SubscriptionSnapshotRecord{}, fmt.Errorf("parse snapshot created_at: %w", err)
	}
	record.FetchedAt = parsedFetchedAt
	record.CreatedAt = parsedCreatedAt
	return record, nil
}
