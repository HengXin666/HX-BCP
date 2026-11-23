package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ResidentialOrigin marks node rows that were materialized from a residential
// provider session pool rather than imported from a subscription snapshot.
const ResidentialOrigin = "residential"

// ResidentialDialerProxyGroupIDKey is stored inside an encrypted residential
// node config. The Mihomo compiler resolves the stable group ID to its current
// display name before emitting dialer-proxy.
const ResidentialDialerProxyGroupIDKey = "hx_dialer_proxy_group_id"

// ResidentialWsPxySessionIDKey is stored inside an encrypted residential node
// config so the control plane can DELETE the matching HX-CF-WsPxy session.
// The Mihomo compiler strips it before emitting YAML.
const ResidentialWsPxySessionIDKey = "hx_wspxy_session_id"

// ResidentialSessionNode is one pooled gateway session rendered as a node.
type ResidentialSessionNode struct {
	ID                       string
	Fingerprint              string
	DisplayName              string
	Protocol                 string
	CanonicalConfigEncrypted []byte
}

// UpsertResidentialSessionNode persists one node allocated for one logical
// client session. It does not remove any other channel node.
func (s *Store) UpsertResidentialSessionNode(
	ctx context.Context,
	channelID string,
	session ResidentialSessionNode,
	now time.Time,
) (string, error) {
	if strings.TrimSpace(channelID) == "" {
		return "", fmt.Errorf("residential channel id is required")
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO nodes(
    id, fingerprint, display_name, protocol, canonical_config_encrypted,
    lifecycle_state, first_seen_at, last_seen_at, retired_at, version, origin, origin_ref
) VALUES (?, ?, ?, ?, ?, 'candidate', ?, ?, NULL, 1, ?, ?)
ON CONFLICT(fingerprint) DO UPDATE SET
    display_name = excluded.display_name,
    protocol = excluded.protocol,
    canonical_config_encrypted = excluded.canonical_config_encrypted,
    lifecycle_state = 'candidate', last_seen_at = excluded.last_seen_at,
    retired_at = NULL, origin = excluded.origin, origin_ref = excluded.origin_ref,
    version = nodes.version + 1
`, session.ID, session.Fingerprint, session.DisplayName, session.Protocol,
		session.CanonicalConfigEncrypted, timestamp, timestamp, ResidentialOrigin, channelID)
	if err != nil {
		return "", fmt.Errorf("upsert residential session node: %w", err)
	}
	var id string
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM nodes WHERE fingerprint = ?", session.Fingerprint).Scan(&id); err != nil {
		return "", fmt.Errorf("resolve residential session node: %w", err)
	}
	return id, nil
}

func (s *Store) DeleteResidentialSessionNode(ctx context.Context, channelID, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM nodes
WHERE origin = ? AND origin_ref = ? AND fingerprint = ?
  AND NOT EXISTS (
      SELECT 1
      FROM residential_client_sessions sessions
      WHERE sessions.channel_id = ?
        AND sessions.node_fingerprint = nodes.fingerprint
  )
`, ResidentialOrigin, channelID, fingerprint, channelID)
	if err != nil {
		return fmt.Errorf("delete residential session node: %w", err)
	}
	return nil
}

// ReplaceResidentialSessionPool atomically swaps the pooled session nodes that
// belong to one channel. Nodes still referenced by the channel's proxy group
// keep their identity across refreshes when the fingerprint is unchanged, so
// traffic history and quality records survive a pool top-up.
//
// The whole replacement runs in one transaction: a failed refresh leaves the
// previous pool in place rather than emptying the channel.
func (s *Store) ReplaceResidentialSessionPool(
	ctx context.Context,
	channelID string,
	sessions []ResidentialSessionNode,
	now time.Time,
) ([]string, error) {
	if strings.TrimSpace(channelID) == "" {
		return nil, fmt.Errorf("residential channel id is required")
	}
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin residential pool replace: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	timestamp := now.UTC().Format(time.RFC3339Nano)
	keep := make(map[string]struct{}, len(sessions))
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO nodes(
    id, fingerprint, display_name, protocol, canonical_config_encrypted,
    lifecycle_state, first_seen_at, last_seen_at, retired_at, version,
    origin, origin_ref
) VALUES (?, ?, ?, ?, ?, 'candidate', ?, ?, NULL, 1, ?, ?)
ON CONFLICT(fingerprint) DO UPDATE SET
    display_name = excluded.display_name,
    protocol = excluded.protocol,
    canonical_config_encrypted = excluded.canonical_config_encrypted,
    lifecycle_state = CASE
        WHEN nodes.lifecycle_state = 'disabled' THEN 'disabled'
        WHEN nodes.lifecycle_state = 'retired' THEN 'candidate'
        ELSE nodes.lifecycle_state
    END,
    last_seen_at = excluded.last_seen_at,
    retired_at = NULL,
    origin = excluded.origin,
    origin_ref = excluded.origin_ref,
    version = nodes.version + 1
`,
			session.ID,
			session.Fingerprint,
			session.DisplayName,
			session.Protocol,
			session.CanonicalConfigEncrypted,
			timestamp,
			timestamp,
			ResidentialOrigin,
			channelID,
		); err != nil {
			return nil, fmt.Errorf("upsert residential session node: %w", err)
		}
		var nodeID string
		if err := transaction.QueryRowContext(
			ctx,
			"SELECT id FROM nodes WHERE fingerprint = ?",
			session.Fingerprint,
		).Scan(&nodeID); err != nil {
			return nil, fmt.Errorf("resolve residential session node id: %w", err)
		}
		keep[nodeID] = struct{}{}
		ids = append(ids, nodeID)
	}

	rows, err := transaction.QueryContext(ctx, `
SELECT id FROM nodes WHERE origin = ? AND origin_ref = ?
`, ResidentialOrigin, channelID)
	if err != nil {
		return nil, fmt.Errorf("list existing residential session nodes: %w", err)
	}
	stale := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan existing residential session node: %w", err)
		}
		if _, retained := keep[id]; !retained {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate existing residential session nodes: %w", err)
	}
	rows.Close()

	for _, id := range stale {
		if _, err := transaction.ExecContext(ctx, "DELETE FROM nodes WHERE id = ?", id); err != nil {
			return nil, fmt.Errorf("delete retired residential session node: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("commit residential pool replace: %w", err)
	}
	committed = true
	return ids, nil
}

// ListResidentialSessionNodes returns one channel's pooled nodes in stable
// creation order so the rotation index maps to a deterministic session.
func (s *Store) ListResidentialSessionNodes(
	ctx context.Context,
	channelID string,
) ([]NodeConfigRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, fingerprint, display_name, protocol, lifecycle_state, canonical_config_encrypted
FROM nodes
WHERE origin = ? AND origin_ref = ? AND lifecycle_state NOT IN ('disabled', 'retired')
ORDER BY display_name ASC, id ASC
`, ResidentialOrigin, channelID)
	if err != nil {
		return nil, fmt.Errorf("list residential session nodes: %w", err)
	}
	defer rows.Close()
	records := make([]NodeConfigRecord, 0)
	for rows.Next() {
		var record NodeConfigRecord
		if err := rows.Scan(
			&record.ID,
			&record.Fingerprint,
			&record.DisplayName,
			&record.Protocol,
			&record.LifecycleState,
			&record.CanonicalConfigEncrypted,
		); err != nil {
			return nil, fmt.Errorf("scan residential session node: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate residential session nodes: %w", err)
	}
	return records, nil
}

// CountResidentialSessionNodes returns the pooled node count per channel in a
// single grouped query. The channel list view only needs pool sizes, so this
// avoids materializing every encrypted node config just to count them.
func (s *Store) CountResidentialSessionNodes(ctx context.Context, channelIDs []string) (map[string]int, error) {
	if len(channelIDs) == 0 {
		return map[string]int{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(channelIDs)), ",")
	arguments := make([]any, len(channelIDs))
	for index, id := range channelIDs {
		arguments[index] = id
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT origin_ref, COUNT(*)
FROM nodes
WHERE origin = ? AND origin_ref IN (`+placeholders+`) AND lifecycle_state NOT IN ('disabled', 'retired')
GROUP BY origin_ref
`, append([]any{ResidentialOrigin}, arguments...)...)
	if err != nil {
		return nil, fmt.Errorf("count residential session nodes: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int, len(channelIDs))
	for _, id := range channelIDs {
		counts[id] = 0
	}
	for rows.Next() {
		var channelID string
		var count int
		if err := rows.Scan(&channelID, &count); err != nil {
			return nil, fmt.Errorf("scan residential session node count: %w", err)
		}
		counts[channelID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate residential session node counts: %w", err)
	}
	return counts, nil
}

// DeleteResidentialSessionPool removes every pooled node of one channel. It is
// used when a channel is deleted, after the group and listener are gone.
func (s *Store) DeleteResidentialSessionPool(ctx context.Context, channelID string) error {
	if _, err := s.db.ExecContext(
		ctx,
		"DELETE FROM nodes WHERE origin = ? AND origin_ref = ?",
		ResidentialOrigin,
		channelID,
	); err != nil {
		return fmt.Errorf("delete residential session pool: %w", err)
	}
	return nil
}
