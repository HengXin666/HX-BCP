package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ResidentialClientSessionRecord is one caller-controlled logical session on
// a residential channel. The proxy password stays encrypted outside the data
// plane compiler and the token-authenticated session API.
type ResidentialClientSessionRecord struct {
	ChannelID             string
	SessionID             string
	AuthUsername          string
	AuthPasswordEncrypted []byte
	SessionIndex          int
	DeclaredIndex         int
	NodeFingerprint       string
	RouteMode             string
	RotateCount           int
	LastRotatedAt         *time.Time
	AllocatedAt           *time.Time
	ExpiresAt             *time.Time
	CountryCode           string
	LastUsedAt            *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
	// AllocVersion is a per-session monotonic counter bumped on every
	// allocation, rotation, route or release change. Consumers use it as a
	// compare-and-swap token: a rotation submitted with a stale version is
	// rejected instead of silently double-rotating the same IP window.
	AllocVersion int
	// Lease fields implement the exclusive-ownership window of the unified
	// integration standard. A node with an unexpired lease is owned by
	// LeaseHolder and may only be rotated or re-routed by the holder holding
	// LeaseID.
	LeaseID        string
	LeaseHolder    string
	LeaseExpiresAt *time.Time
}

// ResidentialClientRouteRecord is the compiler-facing view. NodeFingerprint
// is resolved from the channel pool's stable slot ordering.
type ResidentialClientRouteRecord struct {
	ResidentialClientSessionRecord
	ListenerID       string
	DirectListenerID string
	UpstreamGroup    string
	ChannelEnabled   bool
}

// normalizedDeclaredIndex maps an unset ordinal onto the sentinel the schema
// uses for on-demand sessions. Declared ordinals start at 1, so a zero value
// from an older caller must not collide in the partial unique index that
// covers declared_index >= 0.
func normalizedDeclaredIndex(index int) int {
	if index < 1 {
		return -1
	}
	return index
}

func (s *Store) CreateResidentialClientSession(
	ctx context.Context,
	record ResidentialClientSessionRecord,
) (ResidentialClientSessionRecord, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO residential_client_sessions(
		channel_id, session_id, auth_username, auth_password_encrypted,
		session_index, declared_index, node_fingerprint, route_mode, rotate_count, last_rotated_at,
		allocated_at, expires_at, created_at, updated_at, country_code, last_used_at,
		alloc_version, lease_id, lease_holder, lease_expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, record.ChannelID, record.SessionID, record.AuthUsername, record.AuthPasswordEncrypted,
		record.SessionIndex, normalizedDeclaredIndex(record.DeclaredIndex),
		record.NodeFingerprint, record.RouteMode, record.RotateCount,
		nullableTimeString(record.LastRotatedAt),
		nullableTimeString(record.AllocatedAt), nullableTimeString(record.ExpiresAt),
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano), record.CountryCode,
		nullableTimeString(record.LastUsedAt),
		record.AllocVersion, record.LeaseID, record.LeaseHolder,
		nullableTimeString(record.LeaseExpiresAt))
	if err != nil {
		if isUniqueConstraint(err) {
			return ResidentialClientSessionRecord{}, ErrConflict
		}
		return ResidentialClientSessionRecord{}, fmt.Errorf("create residential client session: %w", err)
	}
	return record, nil
}

func (s *Store) GetResidentialClientSession(
	ctx context.Context,
	channelID, sessionID string,
) (ResidentialClientSessionRecord, error) {
	record, err := scanResidentialClientSession(s.db.QueryRowContext(ctx, `
	SELECT channel_id, session_id, auth_username, auth_password_encrypted,
	       session_index, declared_index, node_fingerprint, route_mode, rotate_count, last_rotated_at,
	       allocated_at, expires_at, created_at, updated_at, country_code, last_used_at,
	       alloc_version, lease_id, lease_holder, lease_expires_at
FROM residential_client_sessions
WHERE channel_id = ? AND session_id = ?
`, channelID, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return ResidentialClientSessionRecord{}, ErrNotFound
	}
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("get residential client session: %w", err)
	}
	return record, nil
}

func (s *Store) ListResidentialClientSessions(
	ctx context.Context,
	channelID string,
) ([]ResidentialClientSessionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
	SELECT channel_id, session_id, auth_username, auth_password_encrypted,
	       session_index, declared_index, node_fingerprint, route_mode, rotate_count, last_rotated_at,
	       allocated_at, expires_at, created_at, updated_at, country_code, last_used_at,
	       alloc_version, lease_id, lease_holder, lease_expires_at
FROM residential_client_sessions
WHERE channel_id = ?
ORDER BY created_at ASC, session_id ASC
`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list residential client sessions: %w", err)
	}
	defer rows.Close()
	records := make([]ResidentialClientSessionRecord, 0)
	for rows.Next() {
		record, err := scanResidentialClientSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan residential client session: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate residential client sessions: %w", err)
	}
	return records, nil
}

// ListResidentialClientSessionsForChannels returns every client session of the
// given channels in one query, keyed by channel id. Channels without sessions
// map to an empty (non-nil) slice. This lets the channel list view render
// allocation counts without a query per channel.
func (s *Store) ListResidentialClientSessionsForChannels(
	ctx context.Context,
	channelIDs []string,
) (map[string][]ResidentialClientSessionRecord, error) {
	if len(channelIDs) == 0 {
		return map[string][]ResidentialClientSessionRecord{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(channelIDs)), ",")
	arguments := make([]any, len(channelIDs))
	for index, id := range channelIDs {
		arguments[index] = id
	}
	rows, err := s.db.QueryContext(ctx, `
	SELECT channel_id, session_id, auth_username, auth_password_encrypted,
	       session_index, declared_index, node_fingerprint, route_mode, rotate_count, last_rotated_at,
	       allocated_at, expires_at, created_at, updated_at, country_code, last_used_at,
	       alloc_version, lease_id, lease_holder, lease_expires_at
FROM residential_client_sessions
WHERE channel_id IN (`+placeholders+`)
ORDER BY channel_id ASC, created_at ASC, session_id ASC
`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list residential client sessions for channels: %w", err)
	}
	defer rows.Close()
	byChannel := make(map[string][]ResidentialClientSessionRecord, len(channelIDs))
	for _, id := range channelIDs {
		byChannel[id] = []ResidentialClientSessionRecord{}
	}
	for rows.Next() {
		record, err := scanResidentialClientSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan residential client session for channel: %w", err)
		}
		byChannel[record.ChannelID] = append(byChannel[record.ChannelID], record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate residential client sessions for channels: %w", err)
	}
	return byChannel, nil
}

// ListResidentialClientRoutes uses the node assigned directly to each logical
// session. The pool-index join is retained only for pre-v19 compatibility.
func (s *Store) ListResidentialClientRoutes(ctx context.Context) ([]ResidentialClientRouteRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
WITH pooled AS (
    SELECT origin_ref AS channel_id, fingerprint,
           ROW_NUMBER() OVER (PARTITION BY origin_ref ORDER BY display_name ASC, id ASC) - 1 AS session_index
    FROM nodes
    WHERE origin = ? AND lifecycle_state NOT IN ('disabled', 'retired')
)
SELECT cs.channel_id, cs.session_id, cs.auth_username, cs.auth_password_encrypted,
       cs.session_index, cs.declared_index, cs.node_fingerprint, cs.route_mode,
       cs.rotate_count, cs.last_rotated_at,
	       cs.allocated_at, cs.expires_at, cs.created_at, cs.updated_at, cs.country_code,
       cs.alloc_version, cs.lease_id, cs.lease_holder, cs.lease_expires_at,
       c.listener_id, COALESCE(c.direct_listener_id, ''),
       COALESCE(NULLIF(cs.node_fingerprint, ''), p.fingerprint, ''),
       COALESCE(upstream.name, ''), c.enabled
FROM residential_client_sessions cs
JOIN residential_channels c ON c.id = cs.channel_id
JOIN residential_providers provider ON provider.id = c.provider_id
LEFT JOIN proxy_groups upstream
       ON upstream.id = provider.upstream_proxy_group_id AND upstream.enabled = 1
LEFT JOIN pooled p ON p.channel_id = cs.channel_id AND p.session_index = cs.session_index
ORDER BY cs.channel_id ASC, cs.session_id ASC
`, ResidentialOrigin)
	if err != nil {
		return nil, fmt.Errorf("list residential client routes: %w", err)
	}
	defer rows.Close()
	records := make([]ResidentialClientRouteRecord, 0)
	for rows.Next() {
		var record ResidentialClientRouteRecord
		var lastRotatedAt, allocatedAt, expiresAt, leaseExpiresAt sql.NullString
		var createdAt, updatedAt string
		var enabled int
		if err := rows.Scan(
			&record.ChannelID, &record.SessionID, &record.AuthUsername,
			&record.AuthPasswordEncrypted, &record.SessionIndex, &record.DeclaredIndex,
			&record.NodeFingerprint, &record.RouteMode,
			&record.RotateCount, &lastRotatedAt, &allocatedAt, &expiresAt,
			&createdAt, &updatedAt, &record.CountryCode,
			&record.AllocVersion, &record.LeaseID, &record.LeaseHolder, &leaseExpiresAt,
			&record.ListenerID, &record.DirectListenerID, &record.NodeFingerprint,
			&record.UpstreamGroup, &enabled,
		); err != nil {
			return nil, fmt.Errorf("scan residential client route: %w", err)
		}
		record.LastRotatedAt, err = parseNullableTime(lastRotatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse residential client route rotation time: %w", err)
		}
		record.AllocatedAt, err = parseNullableTime(allocatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse residential client route allocation time: %w", err)
		}
		record.ExpiresAt, err = parseNullableTime(expiresAt)
		if err != nil {
			return nil, fmt.Errorf("parse residential client route expiry time: %w", err)
		}
		record.LeaseExpiresAt, err = parseNullableTime(leaseExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("parse residential client route lease expiry time: %w", err)
		}
		record.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		record.ChannelEnabled = enabled != 0
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate residential client routes: %w", err)
	}
	return records, nil
}

func (s *Store) UpdateResidentialClientSessionRoute(
	ctx context.Context,
	channelID, sessionID, routeMode string,
	sessionIndex int,
	rotatedAt *time.Time,
) (ResidentialClientSessionRecord, error) {
	rotateIncrement := 0
	if rotatedAt != nil {
		rotateIncrement = 1
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_client_sessions
SET route_mode = ?, session_index = ?,
    node_fingerprint = CASE WHEN ? = 'residential' THEN node_fingerprint ELSE '' END,
    allocated_at = CASE WHEN ? = 'residential' THEN allocated_at ELSE '' END,
    expires_at = CASE WHEN ? = 'residential' THEN expires_at ELSE '' END,
    rotate_count = rotate_count + ?, last_rotated_at = ?, updated_at = ?,
    alloc_version = alloc_version + 1
WHERE channel_id = ? AND session_id = ?
`, routeMode, sessionIndex, routeMode, routeMode, routeMode, rotateIncrement, nullableTimeString(rotatedAt),
		now.Format(time.RFC3339Nano), channelID, sessionID)
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("update residential client session route: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("read residential client session update result: %w", err)
	}
	if rowsAffected != 1 {
		return ResidentialClientSessionRecord{}, ErrNotFound
	}
	return s.GetResidentialClientSession(ctx, channelID, sessionID)
}

func (s *Store) UpdateResidentialClientSessionAllocation(
	ctx context.Context,
	channelID, sessionID, fingerprint string,
	allocatedAt time.Time,
	expiresAt *time.Time,
	rotated bool,
) (ResidentialClientSessionRecord, error) {
	increment := 0
	if rotated {
		increment = 1
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_client_sessions
SET route_mode = 'residential', session_index = -1, node_fingerprint = ?,
    allocated_at = ?, expires_at = ?, rotate_count = rotate_count + ?,
    last_rotated_at = CASE WHEN ? = 1 THEN ? ELSE last_rotated_at END,
    updated_at = ?, alloc_version = alloc_version + 1
WHERE channel_id = ? AND session_id = ?
`, fingerprint, allocatedAt.UTC().Format(time.RFC3339Nano), nullableTimeString(expiresAt),
		increment, increment, allocatedAt.UTC().Format(time.RFC3339Nano),
		allocatedAt.UTC().Format(time.RFC3339Nano), channelID, sessionID)
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("update residential client allocation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("read residential client allocation result: %w", err)
	}
	if rows != 1 {
		return ResidentialClientSessionRecord{}, ErrNotFound
	}
	return s.GetResidentialClientSession(ctx, channelID, sessionID)
}

// RestoreResidentialClientSessionState restores runtime routing fields after a
// failed data-plane apply. It intentionally leaves credentials and identity
// untouched.
func (s *Store) RestoreResidentialClientSessionState(
	ctx context.Context,
	record ResidentialClientSessionRecord,
) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_client_sessions
SET route_mode = ?, session_index = ?, node_fingerprint = ?, rotate_count = ?,
    last_rotated_at = ?, allocated_at = ?, expires_at = ?, updated_at = ?,
    alloc_version = ?, lease_id = ?, lease_holder = ?, lease_expires_at = ?
WHERE channel_id = ? AND session_id = ?
`, record.RouteMode, record.SessionIndex, record.NodeFingerprint, record.RotateCount,
		nullableTimeString(record.LastRotatedAt), nullableTimeString(record.AllocatedAt),
		nullableTimeString(record.ExpiresAt), record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		record.AllocVersion, record.LeaseID, record.LeaseHolder,
		nullableTimeString(record.LeaseExpiresAt),
		record.ChannelID, record.SessionID)
	if err != nil {
		return fmt.Errorf("restore residential client session state: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read residential client session restore result: %w", err)
	}
	if rowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

// ClearResidentialClientSessionAllocation drops a session's residential node
// while keeping it in residential route mode. Idle release uses this so the
// published node name and its credentials survive; the next request
// reallocates an exit IP transparently.
func (s *Store) ClearResidentialClientSessionAllocation(
	ctx context.Context,
	channelID, sessionID string,
) (ResidentialClientSessionRecord, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_client_sessions
SET node_fingerprint = '', allocated_at = '', expires_at = '', updated_at = ?,
    alloc_version = alloc_version + 1, lease_id = '', lease_holder = '', lease_expires_at = ''
WHERE channel_id = ? AND session_id = ?
`, time.Now().UTC().Format(time.RFC3339Nano), channelID, sessionID)
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("clear residential client allocation: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("read residential client clear result: %w", err)
	}
	if rowsAffected != 1 {
		return ResidentialClientSessionRecord{}, ErrNotFound
	}
	return s.GetResidentialClientSession(ctx, channelID, sessionID)
}

// TouchResidentialClientSession records that a caller used this session. Idle
// release uses it to decide which allocations can be returned to the vendor.
// It is runtime state and does not bump any configuration version.
func (s *Store) TouchResidentialClientSession(
	ctx context.Context,
	channelID, sessionID string,
	usedAt time.Time,
) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_client_sessions
SET last_used_at = ?
WHERE channel_id = ? AND session_id = ?
`, usedAt.UTC().Format(time.RFC3339Nano), channelID, sessionID)
	if err != nil {
		return fmt.Errorf("touch residential client session: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read residential client session touch result: %w", err)
	}
	if rowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

// SetResidentialClientSessionDeclaredIndex assigns a declared ordinal to a
// session so its published node name stays stable across rotations.
func (s *Store) SetResidentialClientSessionDeclaredIndex(
	ctx context.Context,
	channelID, sessionID string,
	declaredIndex int,
) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_client_sessions
SET declared_index = ?, updated_at = ?
WHERE channel_id = ? AND session_id = ?
`, normalizedDeclaredIndex(declaredIndex), time.Now().UTC().Format(time.RFC3339Nano), channelID, sessionID)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrConflict
		}
		return fmt.Errorf("set residential client declared index: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read residential client declared index result: %w", err)
	}
	if rowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteResidentialClientSession(ctx context.Context, channelID, sessionID string) error {
	result, err := s.db.ExecContext(ctx, `
DELETE FROM residential_client_sessions WHERE channel_id = ? AND session_id = ?
`, channelID, sessionID)
	if err != nil {
		return fmt.Errorf("delete residential client session: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read residential client session delete result: %w", err)
	}
	if rowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

type residentialClientSessionScanner interface {
	Scan(...any) error
}

func scanResidentialClientSession(scanner residentialClientSessionScanner) (ResidentialClientSessionRecord, error) {
	var record ResidentialClientSessionRecord
	var lastRotatedAt, allocatedAt, expiresAt, lastUsedAt, leaseExpiresAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&record.ChannelID, &record.SessionID, &record.AuthUsername,
		&record.AuthPasswordEncrypted, &record.SessionIndex, &record.DeclaredIndex,
		&record.NodeFingerprint, &record.RouteMode,
		&record.RotateCount, &lastRotatedAt, &allocatedAt, &expiresAt, &createdAt, &updatedAt,
		&record.CountryCode, &lastUsedAt,
		&record.AllocVersion, &record.LeaseID, &record.LeaseHolder, &leaseExpiresAt,
	); err != nil {
		return ResidentialClientSessionRecord{}, err
	}
	var err error
	record.LastRotatedAt, err = parseNullableTime(lastRotatedAt)
	if err != nil {
		return ResidentialClientSessionRecord{}, err
	}
	record.AllocatedAt, err = parseNullableTime(allocatedAt)
	if err != nil {
		return ResidentialClientSessionRecord{}, err
	}
	record.ExpiresAt, err = parseNullableTime(expiresAt)
	if err != nil {
		return ResidentialClientSessionRecord{}, err
	}
	record.LastUsedAt, err = parseNullableTime(lastUsedAt)
	if err != nil {
		return ResidentialClientSessionRecord{}, err
	}
	record.LeaseExpiresAt, err = parseNullableTime(leaseExpiresAt)
	if err != nil {
		return ResidentialClientSessionRecord{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return record, nil
}

// SetResidentialClientSessionLease writes the exclusive-ownership lease of a
// declared node. An empty leaseID releases the lease; otherwise holder,
// leaseID and expiry are set as one atomic update.
func (s *Store) SetResidentialClientSessionLease(
	ctx context.Context,
	channelID, sessionID, holder, leaseID string,
	expiresAt *time.Time,
) (ResidentialClientSessionRecord, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE residential_client_sessions
SET lease_id = ?, lease_holder = ?, lease_expires_at = ?, updated_at = ?
WHERE channel_id = ? AND session_id = ?
`, leaseID, holder, nullableTimeString(expiresAt),
		time.Now().UTC().Format(time.RFC3339Nano), channelID, sessionID)
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("set residential client session lease: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return ResidentialClientSessionRecord{}, fmt.Errorf("read residential client session lease result: %w", err)
	}
	if rowsAffected != 1 {
		return ResidentialClientSessionRecord{}, ErrNotFound
	}
	return s.GetResidentialClientSession(ctx, channelID, sessionID)
}
