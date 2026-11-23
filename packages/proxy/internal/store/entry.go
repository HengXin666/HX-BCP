package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EntryTenantRecord is one client account of the unified entry.
type EntryTenantRecord struct {
	ID             string
	Name           string
	SecretSealed   []byte
	Pools          []string
	ResiQuotaBytes int64
	ResiUsedBytes  int64
	TotalBytes     int64
	Enabled        bool
	Note           string
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NodeExitProfileRecord is the last measured exit of one node.
type NodeExitProfileRecord struct {
	NodeID      string
	ExitIP      string
	CountryCode string
	City        string
	ASN         int
	ASName      string
	Residential bool
	Mobile      bool
	Hosting     bool
	Success     bool
	Error       string
	SampledAt   time.Time
}

const entryTenantColumns = `id, name, secret_sealed, pools, resi_quota_bytes, resi_used_bytes,
    total_bytes, enabled, note, version, created_at, updated_at`

// CreateEntryTenant inserts a tenant, a duplicate name is ErrConflict.
func (s *Store) CreateEntryTenant(ctx context.Context, record EntryTenantRecord) (EntryTenantRecord, error) {
	now := time.Now().UTC()
	record.CreatedAt, record.UpdatedAt, record.Version = now, now, 1
	_, err := s.db.ExecContext(ctx, `
INSERT INTO entry_tenants(`+entryTenantColumns+`)
VALUES (?, ?, ?, ?, ?, 0, 0, ?, ?, 1, ?, ?)`,
		record.ID, record.Name, record.SecretSealed, strings.Join(record.Pools, ","),
		record.ResiQuotaBytes, boolToInteger(record.Enabled), record.Note,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return EntryTenantRecord{}, ErrConflict
		}
		return EntryTenantRecord{}, fmt.Errorf("insert entry tenant: %w", err)
	}
	return record, nil
}

// ListEntryTenants returns every tenant ordered by name.
func (s *Store) ListEntryTenants(ctx context.Context) ([]EntryTenantRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryTenantColumns+` FROM entry_tenants ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list entry tenants: %w", err)
	}
	defer rows.Close()
	items := make([]EntryTenantRecord, 0)
	for rows.Next() {
		record, err := scanEntryTenant(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	return items, rows.Err()
}

// GetEntryTenant reads one tenant by id.
func (s *Store) GetEntryTenant(ctx context.Context, id string) (EntryTenantRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+entryTenantColumns+` FROM entry_tenants WHERE id = ?`, id)
	record, err := scanEntryTenant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EntryTenantRecord{}, ErrNotFound
	}
	return record, err
}

// UpdateEntryTenant writes the editable fields under an optimistic version check.
// A nil SecretSealed keeps the stored one.
func (s *Store) UpdateEntryTenant(ctx context.Context, record EntryTenantRecord) (EntryTenantRecord, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE entry_tenants SET
    secret_sealed = COALESCE(?, secret_sealed),
    pools = ?, resi_quota_bytes = ?, enabled = ?, note = ?,
    version = version + 1, updated_at = ?
WHERE id = ? AND version = ?`,
		nullableBytes(record.SecretSealed), strings.Join(record.Pools, ","), record.ResiQuotaBytes,
		boolToInteger(record.Enabled), record.Note, now.Format(time.RFC3339Nano), record.ID, record.Version)
	if err != nil {
		return EntryTenantRecord{}, fmt.Errorf("update entry tenant: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		if _, getErr := s.GetEntryTenant(ctx, record.ID); getErr != nil {
			return EntryTenantRecord{}, getErr
		}
		return EntryTenantRecord{}, ErrConflict
	}
	return s.GetEntryTenant(ctx, record.ID)
}

// DeleteEntryTenant removes a tenant.
func (s *Store) DeleteEntryTenant(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM entry_tenants WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete entry tenant: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

// AddEntryTenantUsage adds measured bytes to a tenant's counters.
func (s *Store) AddEntryTenantUsage(ctx context.Context, id string, resiBytes, totalBytes int64) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE entry_tenants SET resi_used_bytes = resi_used_bytes + ?, total_bytes = total_bytes + ?
WHERE id = ?`, resiBytes, totalBytes, id)
	if err != nil {
		return fmt.Errorf("add entry tenant usage: %w", err)
	}
	return nil
}

// ResetEntryTenantUsage zeroes the residential counter, for a new billing period.
func (s *Store) ResetEntryTenantUsage(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE entry_tenants SET resi_used_bytes = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("reset entry tenant usage: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanEntryTenant(row rowScanner) (EntryTenantRecord, error) {
	var record EntryTenantRecord
	var pools, createdAt, updatedAt string
	var enabled int
	if err := row.Scan(&record.ID, &record.Name, &record.SecretSealed, &pools, &record.ResiQuotaBytes,
		&record.ResiUsedBytes, &record.TotalBytes, &enabled, &record.Note, &record.Version,
		&createdAt, &updatedAt); err != nil {
		return EntryTenantRecord{}, err
	}
	record.Enabled = enabled != 0
	if pools != "" {
		record.Pools = strings.Split(pools, ",")
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return record, nil
}

// UpsertNodeExitProfile stores the latest exit measurement of a node. A node
// deleted meanwhile is ignored.
func (s *Store) UpsertNodeExitProfile(ctx context.Context, record NodeExitProfileRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO node_exit_profiles(node_id, exit_ip, country_code, city, asn, as_name,
    residential, mobile, hosting, success, error, sampled_at)
SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM nodes WHERE id = ?)
ON CONFLICT(node_id) DO UPDATE SET
    exit_ip = excluded.exit_ip, country_code = excluded.country_code, city = excluded.city,
    asn = excluded.asn, as_name = excluded.as_name, residential = excluded.residential,
    mobile = excluded.mobile, hosting = excluded.hosting, success = excluded.success,
    error = excluded.error, sampled_at = excluded.sampled_at`,
		record.NodeID, record.ExitIP, record.CountryCode, record.City, record.ASN, record.ASName,
		boolToInteger(record.Residential), boolToInteger(record.Mobile), boolToInteger(record.Hosting),
		boolToInteger(record.Success), record.Error, record.SampledAt.UTC().Format(time.RFC3339Nano), record.NodeID)
	if err != nil {
		return fmt.Errorf("upsert node exit profile: %w", err)
	}
	return nil
}

// ListNodeExitProfiles returns every stored exit measurement.
func (s *Store) ListNodeExitProfiles(ctx context.Context) ([]NodeExitProfileRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT node_id, exit_ip, country_code, city, asn, as_name, residential, mobile, hosting,
    success, error, sampled_at
FROM node_exit_profiles`)
	if err != nil {
		return nil, fmt.Errorf("list node exit profiles: %w", err)
	}
	defer rows.Close()
	items := make([]NodeExitProfileRecord, 0)
	for rows.Next() {
		var record NodeExitProfileRecord
		var residential, mobile, hosting, success int
		var sampledAt string
		if err := rows.Scan(&record.NodeID, &record.ExitIP, &record.CountryCode, &record.City, &record.ASN,
			&record.ASName, &residential, &mobile, &hosting, &success, &record.Error, &sampledAt); err != nil {
			return nil, fmt.Errorf("scan node exit profile: %w", err)
		}
		record.Residential, record.Mobile, record.Hosting = residential != 0, mobile != 0, hosting != 0
		record.Success = success != 0
		record.SampledAt, _ = time.Parse(time.RFC3339Nano, sampledAt)
		items = append(items, record)
	}
	return items, rows.Err()
}

// ListNodeSourceClasses maps every node held by an active subscription snapshot
// to the classes of the sources that hold it. Nodes no subscription holds, such
// as residential channel sessions, are absent.
func (s *Store) ListNodeSourceClasses(ctx context.Context) (map[string][]SourceClass, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT sn.node_id, s.source_class
FROM subscription_nodes sn
JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id AND ss.status = 'active'
JOIN subscriptions s ON s.id = sn.subscription_id`)
	if err != nil {
		return nil, fmt.Errorf("list node source classes: %w", err)
	}
	defer rows.Close()
	classes := make(map[string][]SourceClass)
	for rows.Next() {
		var nodeID, class string
		if err := rows.Scan(&nodeID, &class); err != nil {
			return nil, fmt.Errorf("scan node source class: %w", err)
		}
		classes[nodeID] = append(classes[nodeID], SourceClass(class))
	}
	return classes, rows.Err()
}

func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}
