package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ResidentialProviderRecord stores one dynamic residential proxy vendor
// account. Gateway credentials stay encrypted; the username template decides
// how session, region and TTL parameters are encoded into the gateway login.
type ResidentialProviderRecord struct {
	ID                   string
	Name                 string
	Vendor               string
	Protocol             string
	GatewayHost          string
	GatewayPort          int
	UpstreamProxyGroupID string
	// APIURL is a v14 compatibility field for api-list providers. New values are
	// stored in the encrypted credentials envelope by the residential service;
	// an edit upgrades legacy rows and clears this column.
	APIURL               string
	CredentialsEncrypted []byte
	UsernameTemplate     string
	RotationMode         string
	SessionTTLSeconds    int
	PoolSize             int
	SessionExpiryPolicy  string
	DefaultRegion        string
	DefaultRegionMode    string
	DefaultRandomRegions string
	Enabled              bool
	Version              int
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func residentialDefaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// nullableString maps an empty optional foreign key onto SQL NULL so a
// REFERENCES constraint is not evaluated against the empty string.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) CreateResidentialProvider(
	ctx context.Context,
	record ResidentialProviderRecord,
) (ResidentialProviderRecord, error) {
	if record.SessionExpiryPolicy == "" {
		record.SessionExpiryPolicy = "rotate"
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO residential_providers(
    id, name, vendor, protocol, gateway_host, gateway_port,
    api_url, upstream_proxy_group_id, credentials_encrypted, username_template, rotation_mode,
    session_ttl_seconds, pool_size, session_expiry_policy, default_region, default_region_mode, default_random_regions, enabled, version,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		record.ID,
		record.Name,
		record.Vendor,
		record.Protocol,
		record.GatewayHost,
		record.GatewayPort,
		record.APIURL,
		record.UpstreamProxyGroupID,
		record.CredentialsEncrypted,
		record.UsernameTemplate,
		record.RotationMode,
		record.SessionTTLSeconds,
		record.PoolSize,
		record.SessionExpiryPolicy,
		record.DefaultRegion,
		residentialDefaultString(record.DefaultRegionMode, "fixed"),
		residentialDefaultString(record.DefaultRandomRegions, "[]"),
		boolToInteger(record.Enabled),
		record.Version,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ResidentialProviderRecord{}, ErrConflict
		}
		return ResidentialProviderRecord{}, fmt.Errorf("create residential provider: %w", err)
	}
	return record, nil
}

func (s *Store) GetResidentialProvider(ctx context.Context, id string) (ResidentialProviderRecord, error) {
	record, err := scanResidentialProvider(s.db.QueryRowContext(ctx, residentialProviderSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return ResidentialProviderRecord{}, ErrNotFound
	}
	if err != nil {
		return ResidentialProviderRecord{}, fmt.Errorf("get residential provider: %w", err)
	}
	return record, nil
}

func (s *Store) ListResidentialProviders(ctx context.Context) ([]ResidentialProviderRecord, error) {
	rows, err := s.db.QueryContext(ctx, residentialProviderSelect+" ORDER BY created_at ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list residential providers: %w", err)
	}
	defer rows.Close()
	records := make([]ResidentialProviderRecord, 0)
	for rows.Next() {
		record, err := scanResidentialProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan residential provider: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate residential providers: %w", err)
	}
	return records, nil
}

func (s *Store) UpdateResidentialProvider(
	ctx context.Context,
	record ResidentialProviderRecord,
	expectedVersion int,
) (ResidentialProviderRecord, error) {
	if record.SessionExpiryPolicy == "" {
		record.SessionExpiryPolicy = "rotate"
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_providers
SET
    name = ?, vendor = ?, protocol = ?, gateway_host = ?, gateway_port = ?,
    api_url = ?, upstream_proxy_group_id = NULLIF(?, ''), credentials_encrypted = ?, username_template = ?, rotation_mode = ?,
    session_ttl_seconds = ?, pool_size = ?, session_expiry_policy = ?, default_region = ?, default_region_mode = ?, default_random_regions = ?, enabled = ?,
    version = version + 1, updated_at = ?
WHERE id = ? AND version = ?
`,
		record.Name,
		record.Vendor,
		record.Protocol,
		record.GatewayHost,
		record.GatewayPort,
		record.APIURL,
		record.UpstreamProxyGroupID,
		record.CredentialsEncrypted,
		record.UsernameTemplate,
		record.RotationMode,
		record.SessionTTLSeconds,
		record.PoolSize,
		record.SessionExpiryPolicy,
		record.DefaultRegion,
		residentialDefaultString(record.DefaultRegionMode, "fixed"),
		residentialDefaultString(record.DefaultRandomRegions, "[]"),
		boolToInteger(record.Enabled),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		record.ID,
		expectedVersion,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ResidentialProviderRecord{}, ErrConflict
		}
		return ResidentialProviderRecord{}, fmt.Errorf("update residential provider: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return ResidentialProviderRecord{}, fmt.Errorf("read residential provider update result: %w", err)
	}
	if rowsAffected != 1 {
		if _, getErr := s.GetResidentialProvider(ctx, record.ID); errors.Is(getErr, ErrNotFound) {
			return ResidentialProviderRecord{}, ErrNotFound
		}
		return ResidentialProviderRecord{}, ErrConflict
	}
	return s.GetResidentialProvider(ctx, record.ID)
}

func (s *Store) DeleteResidentialProvider(ctx context.Context, id string, expectedVersion int) error {
	result, err := s.db.ExecContext(
		ctx,
		"DELETE FROM residential_providers WHERE id = ? AND version = ?",
		id,
		expectedVersion,
	)
	if err != nil {
		if isForeignKeyConstraint(err) {
			return ErrConflict
		}
		return fmt.Errorf("delete residential provider: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read residential provider delete result: %w", err)
	}
	if rowsAffected == 1 {
		return nil
	}
	if _, getErr := s.GetResidentialProvider(ctx, id); errors.Is(getErr, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

const residentialProviderSelect = `
SELECT
    id, name, vendor, protocol, gateway_host, gateway_port,
    api_url, COALESCE(upstream_proxy_group_id, ''), credentials_encrypted, username_template, rotation_mode,
    session_ttl_seconds, pool_size, session_expiry_policy, default_region, default_region_mode, default_random_regions, enabled, version,
    created_at, updated_at
FROM residential_providers`

func scanResidentialProvider(source scanner) (ResidentialProviderRecord, error) {
	var record ResidentialProviderRecord
	var enabled int
	var createdAt string
	var updatedAt string
	if err := source.Scan(
		&record.ID,
		&record.Name,
		&record.Vendor,
		&record.Protocol,
		&record.GatewayHost,
		&record.GatewayPort,
		&record.APIURL,
		&record.UpstreamProxyGroupID,
		&record.CredentialsEncrypted,
		&record.UsernameTemplate,
		&record.RotationMode,
		&record.SessionTTLSeconds,
		&record.PoolSize,
		&record.SessionExpiryPolicy,
		&record.DefaultRegion,
		&record.DefaultRegionMode,
		&record.DefaultRandomRegions,
		&enabled,
		&record.Version,
		&createdAt,
		&updatedAt,
	); err != nil {
		return ResidentialProviderRecord{}, err
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ResidentialProviderRecord{}, fmt.Errorf("parse residential provider created_at: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ResidentialProviderRecord{}, fmt.Errorf("parse residential provider updated_at: %w", err)
	}
	record.Enabled = enabled != 0
	record.CreatedAt = parsedCreatedAt
	record.UpdatedAt = parsedUpdatedAt
	return record, nil
}
