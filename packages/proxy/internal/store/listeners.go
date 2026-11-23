package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ListenerRecord struct {
	ID           string
	Name         string
	Kind         string
	BindAddress  string
	Port         int
	ProxyGroupID string
	// NodeID pins this listener's egress to one node, addressed by the
	// control-plane node id a proxy group would also use. It is the direct form
	// of "send this service out through that node": without it the only way to
	// name a single node was to build a one-member group for it, which is why
	// this installation held 57 groups that pinned only 28 distinct nodes.
	//
	// See .agents/notes/implemented/feature/2026-09-26-listener-pins-a-node.md
	// for why this is single-valued and why the column must stay nullable.
	// It is a nullable reference, not NOT NULL DEFAULT '': SQLite treats NULL in
	// a foreign-key column as "no parent", while an empty string is compared as
	// a real value and fails the constraint. Empty means "not pinned" and is
	// stored as NULL.
	NodeID              string
	AuthMode            string
	AuthConfigEncrypted []byte
	TransportJSON       string
	PublicEndpointJSON  string
	ShareToken          string
	// SharedInbound marks this listener as a member of an aggregate entry
	// point ("standard" or "websocket"). Empty keeps the historical dedicated
	// port behaviour.
	SharedInbound string
	Enabled       bool
	Version       int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (s *Store) CreateListener(ctx context.Context, record ListenerRecord) (ListenerRecord, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO listeners(
    id, name, listener_type, kind, bind_address, port, proxy_group_id,
    auth_policy_encrypted, auth_mode, auth_config_encrypted,
    transport_json, public_endpoint_json, share_token, shared_inbound,
    enabled, version, created_at, updated_at, node_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))
`,
		record.ID,
		record.Name,
		record.Kind,
		record.Kind,
		record.BindAddress,
		record.Port,
		record.ProxyGroupID,
		record.AuthConfigEncrypted,
		record.AuthMode,
		record.AuthConfigEncrypted,
		record.TransportJSON,
		record.PublicEndpointJSON,
		record.ShareToken,
		record.SharedInbound,
		boolToInteger(record.Enabled),
		record.Version,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		record.NodeID,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ListenerRecord{}, ErrConflict
		}
		return ListenerRecord{}, fmt.Errorf("create listener: %w", err)
	}
	return record, nil
}

func (s *Store) GetListener(ctx context.Context, id string) (ListenerRecord, error) {
	record, err := scanListener(s.db.QueryRowContext(ctx, listenerSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return ListenerRecord{}, ErrNotFound
	}
	if err != nil {
		return ListenerRecord{}, fmt.Errorf("get listener: %w", err)
	}
	return record, nil
}

func (s *Store) ListListeners(ctx context.Context) ([]ListenerRecord, error) {
	rows, err := s.db.QueryContext(ctx, listenerSelect+" ORDER BY created_at ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list listeners: %w", err)
	}
	defer rows.Close()
	records := make([]ListenerRecord, 0)
	for rows.Next() {
		record, err := scanListener(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listener: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate listeners: %w", err)
	}
	return records, nil
}

func (s *Store) UpdateListener(
	ctx context.Context,
	record ListenerRecord,
	expectedVersion int,
) (ListenerRecord, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE listeners
SET
    name = ?, listener_type = ?, kind = ?, bind_address = ?, port = ?, proxy_group_id = ?,
    auth_policy_encrypted = ?, auth_mode = ?, auth_config_encrypted = ?,
    transport_json = ?, public_endpoint_json = ?, shared_inbound = ?, enabled = ?,
    node_id = NULLIF(?, ''), version = version + 1, updated_at = ?
WHERE id = ? AND version = ?
`,
		record.Name,
		record.Kind,
		record.Kind,
		record.BindAddress,
		record.Port,
		record.ProxyGroupID,
		record.AuthConfigEncrypted,
		record.AuthMode,
		record.AuthConfigEncrypted,
		record.TransportJSON,
		record.PublicEndpointJSON,
		record.SharedInbound,
		boolToInteger(record.Enabled),
		record.NodeID,
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		record.ID,
		expectedVersion,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ListenerRecord{}, ErrConflict
		}
		return ListenerRecord{}, fmt.Errorf("update listener: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return ListenerRecord{}, fmt.Errorf("read listener update result: %w", err)
	}
	if rowsAffected != 1 {
		if _, getErr := s.GetListener(ctx, record.ID); errors.Is(getErr, ErrNotFound) {
			return ListenerRecord{}, ErrNotFound
		}
		return ListenerRecord{}, ErrConflict
	}
	return s.GetListener(ctx, record.ID)
}

func (s *Store) DeleteListener(ctx context.Context, id string, expectedVersion int) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM listeners WHERE id = ? AND version = ?", id, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete listener: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read listener delete result: %w", err)
	}
	if rowsAffected == 1 {
		return nil
	}
	if _, getErr := s.GetListener(ctx, id); errors.Is(getErr, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

const listenerSelect = `
SELECT
    id, name, kind, bind_address, port, proxy_group_id, auth_mode,
    auth_config_encrypted, transport_json, public_endpoint_json,
    share_token, shared_inbound, enabled, version, created_at, updated_at,
    COALESCE(node_id, '')
FROM listeners`

// GetListenerByShareToken resolves the public subscription-export token.
func (s *Store) GetListenerByShareToken(ctx context.Context, token string) (ListenerRecord, error) {
	record, err := scanListener(s.db.QueryRowContext(ctx, listenerSelect+" WHERE share_token = ?", token))
	if errors.Is(err, sql.ErrNoRows) {
		return ListenerRecord{}, ErrNotFound
	}
	if err != nil {
		return ListenerRecord{}, fmt.Errorf("get listener by share token: %w", err)
	}
	return record, nil
}

// RotateListenerShareToken replaces the share token and bumps the version so
// previously distributed subscription links stop working.
func (s *Store) RotateListenerShareToken(ctx context.Context, id, token string) (ListenerRecord, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE listeners
SET share_token = ?, version = version + 1, updated_at = ?
WHERE id = ?
`, token, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		if isUniqueConstraint(err) {
			return ListenerRecord{}, ErrConflict
		}
		return ListenerRecord{}, fmt.Errorf("rotate listener share token: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return ListenerRecord{}, fmt.Errorf("read listener rotate result: %w", err)
	}
	if rowsAffected != 1 {
		return ListenerRecord{}, ErrNotFound
	}
	return s.GetListener(ctx, id)
}

func scanListener(source scanner) (ListenerRecord, error) {
	var record ListenerRecord
	var enabled int
	var createdAt string
	var updatedAt string
	if err := source.Scan(
		&record.ID,
		&record.Name,
		&record.Kind,
		&record.BindAddress,
		&record.Port,
		&record.ProxyGroupID,
		&record.AuthMode,
		&record.AuthConfigEncrypted,
		&record.TransportJSON,
		&record.PublicEndpointJSON,
		&record.ShareToken,
		&record.SharedInbound,
		&enabled,
		&record.Version,
		&createdAt,
		&updatedAt,
		&record.NodeID,
	); err != nil {
		return ListenerRecord{}, err
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ListenerRecord{}, fmt.Errorf("parse listener created_at: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ListenerRecord{}, fmt.Errorf("parse listener updated_at: %w", err)
	}
	record.Enabled = enabled != 0
	record.CreatedAt = parsedCreatedAt
	record.UpdatedAt = parsedUpdatedAt
	return record, nil
}
