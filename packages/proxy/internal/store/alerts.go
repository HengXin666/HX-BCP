package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type AlertRecord struct {
	ID             string
	Rule           string
	TargetID       string
	TargetName     string
	Severity       string
	Status         string
	Message        string
	FiredAt        time.Time
	ResolvedAt     *time.Time
	LastNotifiedAt *time.Time
	NotifyCount    int
	Acknowledged   bool
}

type AlertSettingsRecord struct {
	Enabled             bool
	SMTPConfigEncrypted []byte
	UpdatedAt           time.Time
}

// SubscriptionHealthRow feeds the subscription alert detectors.
type SubscriptionHealthRow struct {
	ID                  string
	Name                string
	ConsecutiveFailures int
	ActiveNodeCount     *int
}

func (s *Store) CreateAlert(ctx context.Context, record AlertRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO alerts (id, rule, target_id, target_name, severity, status, message, fired_at, resolved_at, last_notified_at, notify_count, acknowledged)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		record.ID,
		record.Rule,
		record.TargetID,
		record.TargetName,
		record.Severity,
		record.Status,
		record.Message,
		record.FiredAt.UTC().Format(time.RFC3339Nano),
		nullableTimeValue(record.ResolvedAt),
		nullableTimeValue(record.LastNotifiedAt),
		record.NotifyCount,
		boolToInteger(record.Acknowledged),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrConflict
		}
		return fmt.Errorf("create alert: %w", err)
	}
	return nil
}

func (s *Store) ListAlerts(ctx context.Context, status string, limit int) ([]AlertRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query := `
SELECT id, rule, target_id, target_name, severity, status, message, fired_at, resolved_at, last_notified_at, notify_count, acknowledged
FROM alerts`
	arguments := make([]any, 0, 2)
	if status != "" {
		query += ` WHERE status = ?`
		arguments = append(arguments, status)
	}
	query += ` ORDER BY fired_at DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	records := make([]AlertRecord, 0)
	for rows.Next() {
		record, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alerts: %w", err)
	}
	return records, nil
}

func (s *Store) ResolveAlert(ctx context.Context, id string, resolvedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE alerts SET status = 'resolved', resolved_at = ? WHERE id = ? AND status = 'firing'
`, resolvedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolve alert result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkAlertNotified(ctx context.Context, id string, notifiedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE alerts SET last_notified_at = ?, notify_count = notify_count + 1 WHERE id = ?
`, notifiedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("mark alert notified: %w", err)
	}
	return nil
}

func (s *Store) AcknowledgeAlert(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE alerts SET acknowledged = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("acknowledge alert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("acknowledge alert result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneResolvedAlerts keeps the alert history bounded.
func (s *Store) PruneResolvedAlerts(ctx context.Context, olderThan time.Time) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM alerts WHERE status = 'resolved' AND resolved_at IS NOT NULL AND resolved_at < ?
`, olderThan.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("prune resolved alerts: %w", err)
	}
	return nil
}

func (s *Store) GetAlertSettings(ctx context.Context) (AlertSettingsRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT enabled, smtp_config_encrypted, updated_at FROM alert_settings WHERE id = 1
`)
	var record AlertSettingsRecord
	var enabled int
	var updatedAt string
	if err := row.Scan(&enabled, &record.SMTPConfigEncrypted, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return AlertSettingsRecord{}, ErrNotFound
	} else if err != nil {
		return AlertSettingsRecord{}, fmt.Errorf("get alert settings: %w", err)
	}
	record.Enabled = enabled == 1
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return AlertSettingsRecord{}, fmt.Errorf("parse alert settings updated_at: %w", err)
	}
	record.UpdatedAt = parsed
	return record, nil
}

func (s *Store) UpsertAlertSettings(ctx context.Context, record AlertSettingsRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO alert_settings (id, enabled, smtp_config_encrypted, updated_at)
VALUES (1, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    enabled = excluded.enabled,
    smtp_config_encrypted = excluded.smtp_config_encrypted,
    updated_at = excluded.updated_at
`,
		boolToInteger(record.Enabled),
		record.SMTPConfigEncrypted,
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert alert settings: %w", err)
	}
	return nil
}

// ListSubscriptionHealth returns enabled subscriptions with their failure
// counters and the node count of the current active snapshot.
func (s *Store) ListSubscriptionHealth(ctx context.Context) ([]SubscriptionHealthRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
    s.id, s.name, s.consecutive_failures,
    (
        SELECT ss.node_count FROM subscription_snapshots ss
        WHERE ss.subscription_id = s.id AND ss.status = 'active'
        ORDER BY ss.fetched_at DESC LIMIT 1
    )
FROM subscriptions s
WHERE s.enabled = 1
ORDER BY s.id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("list subscription health: %w", err)
	}
	defer rows.Close()
	items := make([]SubscriptionHealthRow, 0)
	for rows.Next() {
		var item SubscriptionHealthRow
		var nodeCount sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Name, &item.ConsecutiveFailures, &nodeCount); err != nil {
			return nil, fmt.Errorf("scan subscription health: %w", err)
		}
		if nodeCount.Valid {
			value := int(nodeCount.Int64)
			item.ActiveNodeCount = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscription health: %w", err)
	}
	return items, nil
}

func scanAlert(rows *sql.Rows) (AlertRecord, error) {
	var record AlertRecord
	var firedAt string
	var resolvedAt, lastNotifiedAt sql.NullString
	var acknowledged int
	if err := rows.Scan(
		&record.ID,
		&record.Rule,
		&record.TargetID,
		&record.TargetName,
		&record.Severity,
		&record.Status,
		&record.Message,
		&firedAt,
		&resolvedAt,
		&lastNotifiedAt,
		&record.NotifyCount,
		&acknowledged,
	); err != nil {
		return AlertRecord{}, fmt.Errorf("scan alert: %w", err)
	}
	record.Acknowledged = acknowledged == 1
	parsed, err := time.Parse(time.RFC3339Nano, firedAt)
	if err != nil {
		return AlertRecord{}, fmt.Errorf("parse alert fired_at: %w", err)
	}
	record.FiredAt = parsed
	if resolvedAt.Valid && resolvedAt.String != "" {
		value, err := time.Parse(time.RFC3339Nano, resolvedAt.String)
		if err != nil {
			return AlertRecord{}, fmt.Errorf("parse alert resolved_at: %w", err)
		}
		record.ResolvedAt = &value
	}
	if lastNotifiedAt.Valid && lastNotifiedAt.String != "" {
		value, err := time.Parse(time.RFC3339Nano, lastNotifiedAt.String)
		if err != nil {
			return AlertRecord{}, fmt.Errorf("parse alert last_notified_at: %w", err)
		}
		record.LastNotifiedAt = &value
	}
	return record, nil
}

func nullableTimeValue(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
