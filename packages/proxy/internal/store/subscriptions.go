package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrConflict = errors.New("store version conflict")

type SubscriptionRecord struct {
	ID         string
	Name       string
	SourceType string
	// SourceClass is the operator-declared taxonomy that selects the probe
	// policy (see source_policy.go). It is a property of the source, not of a
	// node, because the replacement cost of a member is decided by where the
	// member came from.
	SourceClass            string
	SourceConfigEncrypted  []byte
	Enabled                bool
	RefreshIntervalSeconds int
	RefreshCron            string
	LastSuccessSnapshotID  string
	ConsecutiveFailures    int
	LastFailureJSON        string
	LastRefreshAttemptAt   *time.Time
	NextRefreshAt          *time.Time
	Version                int
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (s *Store) CreateSubscription(ctx context.Context, record SubscriptionRecord) (SubscriptionRecord, error) {
	// A row written without an explicit class gets the default rather than a
	// CHECK failure: the column is NOT NULL with a closed vocabulary, and an
	// internal caller that predates the column must keep working.
	record.SourceClass = NormalizeSourceClass(record.SourceClass)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO subscriptions(
    id,
    name,
    source_type,
    source_class,
    source_config_encrypted,
    enabled,
    refresh_interval_seconds,
    refresh_cron,
    last_success_snapshot_id,
    consecutive_failures,
    last_refresh_attempt_at,
    next_refresh_at,
    version,
    created_at,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)
`,
		record.ID,
		record.Name,
		record.SourceType,
		record.SourceClass,
		record.SourceConfigEncrypted,
		boolToInteger(record.Enabled),
		record.RefreshIntervalSeconds,
		record.RefreshCron,
		record.LastSuccessSnapshotID,
		record.ConsecutiveFailures,
		nullableTimeString(record.LastRefreshAttemptAt),
		nullableTimeString(record.NextRefreshAt),
		record.Version,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return SubscriptionRecord{}, ErrConflict
		}
		return SubscriptionRecord{}, fmt.Errorf("create subscription: %w", err)
	}
	return record, nil
}

func (s *Store) GetSubscription(ctx context.Context, id string) (SubscriptionRecord, error) {
	row := s.db.QueryRowContext(ctx, subscriptionSelect+" WHERE id = ?", id)
	record, err := scanSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionRecord{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionRecord{}, fmt.Errorf("get subscription: %w", err)
	}
	return record, nil
}

func (s *Store) ListSubscriptions(ctx context.Context, limit, offset int) ([]SubscriptionRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx, subscriptionSelect+" ORDER BY created_at DESC, id ASC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()

	records := make([]SubscriptionRecord, 0, limit)
	for rows.Next() {
		record, err := scanSubscription(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions: %w", err)
	}
	return records, nil
}

func (s *Store) UpdateSubscription(
	ctx context.Context,
	record SubscriptionRecord,
	expectedVersion int,
) (SubscriptionRecord, error) {
	record.SourceClass = NormalizeSourceClass(record.SourceClass)
	result, err := s.db.ExecContext(ctx, `
UPDATE subscriptions
SET
    name = ?,
    source_type = ?,
    source_class = ?,
    source_config_encrypted = ?,
    enabled = ?,
    refresh_interval_seconds = ?,
    refresh_cron = ?,
    next_refresh_at = NULLIF(?, ''),
    version = version + 1,
    updated_at = ?
WHERE id = ? AND version = ?
`,
		record.Name,
		record.SourceType,
		record.SourceClass,
		record.SourceConfigEncrypted,
		boolToInteger(record.Enabled),
		record.RefreshIntervalSeconds,
		record.RefreshCron,
		nullableTimeString(record.NextRefreshAt),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		record.ID,
		expectedVersion,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return SubscriptionRecord{}, ErrConflict
		}
		return SubscriptionRecord{}, fmt.Errorf("update subscription: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return SubscriptionRecord{}, fmt.Errorf("read subscription update result: %w", err)
	}
	if rowsAffected != 1 {
		if _, getErr := s.GetSubscription(ctx, record.ID); errors.Is(getErr, ErrNotFound) {
			return SubscriptionRecord{}, ErrNotFound
		}
		return SubscriptionRecord{}, ErrConflict
	}
	return s.GetSubscription(ctx, record.ID)
}

func (s *Store) DeleteSubscription(ctx context.Context, id string, expectedVersion int) error {
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin subscription delete: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	result, err := transaction.ExecContext(ctx, "DELETE FROM subscriptions WHERE id = ? AND version = ?", id, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read subscription delete result: %w", err)
	}
	if rowsAffected != 1 {
		var existingID string
		queryErr := transaction.QueryRowContext(ctx, "SELECT id FROM subscriptions WHERE id = ?", id).Scan(&existingID)
		if errors.Is(queryErr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if queryErr != nil {
			return fmt.Errorf("check subscription after delete conflict: %w", queryErr)
		}
		return ErrConflict
	}

	// Agent Note: ../../.agents/notes/implemented/bug-fix/2026-10-15-subscription-refresh-must-not-retire-residential.md
	// 共用清扫判据 (origin 分流), 判据只有一处 —— 见 retireDisappearedNodesSQL 的说明。
	retiredAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := transaction.ExecContext(ctx, retireDisappearedNodesSQL, retiredAt); err != nil {
		return fmt.Errorf("retire orphaned nodes after subscription delete: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit subscription delete: %w", err)
	}
	committed = true
	return nil
}

const subscriptionSelect = `
SELECT
    id,
    name,
    source_type,
    source_class,
    source_config_encrypted,
    enabled,
    refresh_interval_seconds,
    refresh_cron,
    COALESCE(last_success_snapshot_id, ''),
    consecutive_failures,
    last_failure_json,
    last_refresh_attempt_at,
    next_refresh_at,
    version,
    created_at,
    updated_at
FROM subscriptions`

type scanner interface {
	Scan(...any) error
}

func scanSubscription(source scanner) (SubscriptionRecord, error) {
	var record SubscriptionRecord
	var enabled int
	var lastRefreshAttemptAt sql.NullString
	var nextRefreshAt sql.NullString
	var createdAt string
	var updatedAt string
	if err := source.Scan(
		&record.ID,
		&record.Name,
		&record.SourceType,
		&record.SourceClass,
		&record.SourceConfigEncrypted,
		&enabled,
		&record.RefreshIntervalSeconds,
		&record.RefreshCron,
		&record.LastSuccessSnapshotID,
		&record.ConsecutiveFailures,
		&record.LastFailureJSON,
		&lastRefreshAttemptAt,
		&nextRefreshAt,
		&record.Version,
		&createdAt,
		&updatedAt,
	); err != nil {
		return SubscriptionRecord{}, err
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return SubscriptionRecord{}, fmt.Errorf("parse subscription created_at: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return SubscriptionRecord{}, fmt.Errorf("parse subscription updated_at: %w", err)
	}
	parsedLastRefreshAttemptAt, err := parseNullableTime(lastRefreshAttemptAt)
	if err != nil {
		return SubscriptionRecord{}, fmt.Errorf("parse subscription last_refresh_attempt_at: %w", err)
	}
	parsedNextRefreshAt, err := parseNullableTime(nextRefreshAt)
	if err != nil {
		return SubscriptionRecord{}, fmt.Errorf("parse subscription next_refresh_at: %w", err)
	}
	record.Enabled = enabled != 0
	record.LastRefreshAttemptAt = parsedLastRefreshAttemptAt
	record.NextRefreshAt = parsedNextRefreshAt
	record.CreatedAt = parsedCreatedAt
	record.UpdatedAt = parsedUpdatedAt
	return record, nil
}

func boolToInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func nullableTimeString(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
