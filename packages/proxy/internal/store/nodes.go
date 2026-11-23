package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type NodeImportRecord struct {
	ID                       string
	Fingerprint              string
	DisplayName              string
	Protocol                 string
	CanonicalConfigEncrypted []byte
	SourceName               string
}

type NodeRecord struct {
	ID                       string
	Fingerprint              string
	DisplayName              string
	Protocol                 string
	LifecycleState           string
	FirstSeenAt              time.Time
	LastSeenAt               time.Time
	RetiredAt                *time.Time
	LastCheckedAt            *time.Time
	LastLatencyMS            *int
	LastErrorCode            string
	LastErrorMessage         string
	ConsecutiveProbeFailures int
	// NextProbeAt is when the exponential backoff allows the next probe. Nil
	// means "no backoff in force", i.e. the steady-state interval applies.
	NextProbeAt *time.Time
	// ProbePaused marks a node the policy has stopped probing (a spent pool
	// member, or a retired node). It is the visible half of the decision that
	// used to be implicit in "we keep probing everything forever".
	ProbePaused bool
	Version     int
	SourceCount int
}

type NodeSourceRecord struct {
	NodeID           string
	SubscriptionID   string
	SubscriptionName string
	SourceName       string
}

type NodeFilter struct {
	Search   string
	Protocol string
	State    string
	Limit    int
	Offset   int
}

func (s *Store) ListActiveNodeSources(ctx context.Context, nodeIDs []string) ([]NodeSourceRecord, error) {
	if len(nodeIDs) == 0 {
		return []NodeSourceRecord{}, nil
	}
	placeholders := make([]string, len(nodeIDs))
	arguments := make([]any, len(nodeIDs))
	for index, id := range nodeIDs {
		placeholders[index] = "?"
		arguments[index] = id
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT sn.node_id, sn.subscription_id, s.name, sn.source_name
FROM subscription_nodes sn
JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id AND ss.status = 'active'
JOIN subscriptions s ON s.id = sn.subscription_id
WHERE sn.node_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY s.name ASC, sn.subscription_id ASC
`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list active node sources: %w", err)
	}
	defer rows.Close()
	records := make([]NodeSourceRecord, 0)
	for rows.Next() {
		var record NodeSourceRecord
		if err := rows.Scan(&record.NodeID, &record.SubscriptionID, &record.SubscriptionName, &record.SourceName); err != nil {
			return nil, fmt.Errorf("scan active node source: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active node sources: %w", err)
	}
	return records, nil
}

// retireDisappearedNodesSQL retires every node that no longer belongs to any
// ACTIVE subscription snapshot.
//
// Agent Note: ../../.agents/notes/implemented/bug-fix/2026-10-15-subscription-refresh-must-not-retire-residential.md
//
// ⚠️ The origin guard is load-bearing, not defensive. A node whose origin is
// 'residential' is materialized by the residential channel's own allocation
// path (UpsertResidentialSessionNode / ReplaceResidentialSessionPool) and is
// NEVER a member of subscription_nodes. Without the guard this statement
// retires it on the next subscription refresh — the only exit of a sticky
// residential channel disappears, the group is republished as REJECT, and
// every consumer of that group fails until the next rotation happens to
// allocate a fresh node. That was the 2026-10-15 gh-pool incident: the hourly
// refresh at 21:26:47 retired the live session node and registrations died
// with "组 residential-linkup 内无可选节点" for the following ~10 minutes.
//
// Residential node lifetime belongs to the residential service: it retires a
// node when the session that owns it is deleted or replaced, never because a
// subscription snapshot did not mention it.
const retireDisappearedNodesSQL = `
UPDATE nodes
SET lifecycle_state = 'retired', retired_at = ?, version = version + 1
WHERE lifecycle_state NOT IN ('disabled', 'retired')
  AND origin <> 'residential'
  AND NOT EXISTS (
      SELECT 1
      FROM subscription_nodes sn
      JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id
      WHERE sn.node_id = nodes.id AND ss.status = 'active'
  )
`

func (s *Store) CommitParsedSubscriptionSnapshot(
	ctx context.Context,
	record SubscriptionSnapshotRecord,
	nodes []NodeImportRecord,
) error {
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin parsed snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	if err := retireActiveSnapshot(ctx, transaction, record.SubscriptionID); err != nil {
		return err
	}
	if err := insertSnapshot(ctx, transaction, record); err != nil {
		return err
	}
	for _, node := range nodes {
		if err := upsertSnapshotNode(ctx, transaction, record, node); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, retireDisappearedNodesSQL, record.FetchedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("retire disappeared nodes: %w", err)
	}
	if err := activateSubscriptionAfterSnapshot(ctx, transaction, record); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit parsed subscription snapshot: %w", err)
	}
	committed = true
	return nil
}

func retireActiveSnapshot(ctx context.Context, transaction *sql.Tx, subscriptionID string) error {
	if _, err := transaction.ExecContext(ctx, `
UPDATE subscription_snapshots
SET status = 'retired'
WHERE subscription_id = ? AND status = 'active'
`, subscriptionID); err != nil {
		return fmt.Errorf("retire previous subscription snapshot: %w", err)
	}
	return pruneInactiveSnapshotLinks(ctx, transaction, subscriptionID, "")
}

// pruneInactiveSnapshotLinks drops the node links of snapshots that are no
// longer active for one subscription. keepSnapshotID is preserved even when it
// is not active yet, because the caller is about to (re)activate it and re-add
// its links.
//
// Every reader of subscription_nodes filters on the ACTIVE snapshot (the retire
// sweep, source_count, node sources, group candidates, source classes), so a
// retired snapshot's links are unreachable weight. They were not free: on the
// reported database 82,129 of 84,632 links (97%) belonged to retired snapshots,
// and the retire sweep had to walk them for every candidate node.
//
// Agent Note: ../../.agents/notes/implemented/bug-fix/2026-10-28-snapshot-retire-sweep-cost.md
func pruneInactiveSnapshotLinks(ctx context.Context, transaction *sql.Tx, subscriptionID, keepSnapshotID string) error {
	if _, err := transaction.ExecContext(ctx, `
DELETE FROM subscription_nodes
WHERE snapshot_id IN (
    SELECT id FROM subscription_snapshots
    WHERE subscription_id = ? AND status <> 'active' AND id <> ?
)
`, subscriptionID, keepSnapshotID); err != nil {
		return fmt.Errorf("prune inactive snapshot links: %w", err)
	}
	return nil
}

func insertSnapshot(ctx context.Context, transaction *sql.Tx, record SubscriptionSnapshotRecord) error {
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO subscription_snapshots(
    id, subscription_id, content_hash, fetched_at, node_count, status,
    artifact_path, parse_summary_json, fetch_metadata_json, created_at
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
	return nil
}

func upsertSnapshotNode(
	ctx context.Context,
	transaction *sql.Tx,
	snapshot SubscriptionSnapshotRecord,
	node NodeImportRecord,
) error {
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO nodes(
    id, fingerprint, display_name, protocol, canonical_config_encrypted,
    lifecycle_state, first_seen_at, last_seen_at, retired_at, version
) VALUES (?, ?, ?, ?, ?, 'candidate', ?, ?, NULL, 1)
ON CONFLICT(fingerprint) DO UPDATE SET
    display_name = excluded.display_name,
    protocol = excluded.protocol,
    canonical_config_encrypted = excluded.canonical_config_encrypted,
    lifecycle_state = CASE
        WHEN nodes.lifecycle_state = 'disabled' THEN 'disabled'
        WHEN nodes.lifecycle_state = 'retired' THEN 'candidate'
        ELSE nodes.lifecycle_state
    END,
    -- Reviving from 'retired' means the endpoint came back in a fresh
    -- observation, so the old failure schedule must not survive: otherwise a
    -- pool member retired at 5 failures would reappear already paused and
    -- never be probed again. A node that is merely still present keeps its
    -- counter, so a flapping endpoint cannot dodge quarantine by reappearing.
    consecutive_probe_failures = CASE
        WHEN nodes.lifecycle_state = 'retired' THEN 0
        ELSE nodes.consecutive_probe_failures
    END,
    next_probe_at = CASE
        WHEN nodes.lifecycle_state = 'retired' THEN NULL
        ELSE nodes.next_probe_at
    END,
    probe_paused = CASE
        WHEN nodes.lifecycle_state = 'retired' THEN 0
        ELSE nodes.probe_paused
    END,
    last_seen_at = excluded.last_seen_at,
    retired_at = NULL,
    version = nodes.version + 1
`,
		node.ID,
		node.Fingerprint,
		node.DisplayName,
		node.Protocol,
		node.CanonicalConfigEncrypted,
		snapshot.FetchedAt.UTC().Format(time.RFC3339Nano),
		snapshot.FetchedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("upsert parsed node: %w", err)
	}
	var nodeID string
	if err := transaction.QueryRowContext(ctx, `SELECT id FROM nodes WHERE fingerprint = ?`, node.Fingerprint).Scan(&nodeID); err != nil {
		return fmt.Errorf("resolve parsed node id: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO subscription_nodes(subscription_id, snapshot_id, node_id, source_name)
VALUES (?, ?, ?, ?)
`, snapshot.SubscriptionID, snapshot.ID, nodeID, node.SourceName); err != nil {
		return fmt.Errorf("link parsed node to snapshot: %w", err)
	}
	return nil
}

func activateSubscriptionAfterSnapshot(ctx context.Context, transaction *sql.Tx, record SubscriptionSnapshotRecord) error {
	result, err := transaction.ExecContext(ctx, `
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
	)
	if err != nil {
		return fmt.Errorf("activate subscription snapshot: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read subscription activation result: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ActivateParsedSubscriptionSnapshot(
	ctx context.Context,
	record SubscriptionSnapshotRecord,
	nodes []NodeImportRecord,
	fetchMetadataJSON string,
) error {
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin parsed snapshot activation: %w", err)
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
`, record.SubscriptionID, record.ID); err != nil {
		return fmt.Errorf("retire active parsed snapshot: %w", err)
	}
	if err := pruneInactiveSnapshotLinks(ctx, transaction, record.SubscriptionID, record.ID); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `
UPDATE subscription_snapshots
SET
    status = 'active',
    fetched_at = ?,
    node_count = ?,
    parse_summary_json = ?,
    fetch_metadata_json = ?
WHERE id = ? AND subscription_id = ?
`,
		record.FetchedAt.UTC().Format(time.RFC3339Nano),
		record.NodeCount,
		record.ParseSummaryJSON,
		fetchMetadataJSON,
		record.ID,
		record.SubscriptionID,
	)
	if err != nil {
		return fmt.Errorf("activate parsed snapshot: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read parsed snapshot activation result: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM subscription_nodes WHERE snapshot_id = ?`, record.ID); err != nil {
		return fmt.Errorf("clear parsed snapshot nodes: %w", err)
	}
	for _, node := range nodes {
		if err := upsertSnapshotNode(ctx, transaction, record, node); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, retireDisappearedNodesSQL, record.FetchedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("retire disappeared nodes after activation: %w", err)
	}
	if err := activateSubscriptionAfterSnapshot(ctx, transaction, record); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit parsed snapshot activation: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) ListNodes(ctx context.Context, filter NodeFilter) ([]NodeRecord, error) {
	if filter.Limit < 1 || filter.Limit > 1000 {
		filter.Limit = 200
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	where, arguments := nodeFilterWhere(filter)
	arguments = append(arguments, filter.Limit, filter.Offset)
	query := `
SELECT
    n.id,
    n.fingerprint,
    n.display_name,
    n.protocol,
    n.lifecycle_state,
    n.first_seen_at,
    n.last_seen_at,
    n.retired_at,
    n.last_checked_at,
    n.last_latency_ms,
    n.consecutive_probe_failures,
    n.next_probe_at,
    n.probe_paused,
    n.version,
    (SELECT qc.error_code FROM node_quality_checks qc
     WHERE qc.node_id = n.id ORDER BY qc.checked_at DESC LIMIT 1) AS last_error_code,
    (SELECT qc.error_message FROM node_quality_checks qc
     WHERE qc.node_id = n.id ORDER BY qc.checked_at DESC LIMIT 1) AS last_error_message,
    COUNT(DISTINCT CASE WHEN ss.status = 'active' THEN sn.subscription_id END) AS source_count
FROM nodes n
LEFT JOIN subscription_nodes sn ON sn.node_id = n.id
LEFT JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id
WHERE ` + where + `
GROUP BY n.id
ORDER BY
    CASE n.lifecycle_state
        WHEN 'healthy' THEN 0
        WHEN 'candidate' THEN 1
        WHEN 'degraded' THEN 2
        WHEN 'quarantined' THEN 3
        WHEN 'disabled' THEN 4
        ELSE 5
    END,
    n.last_seen_at DESC,
    n.display_name ASC
LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	items := make([]NodeRecord, 0)
	for rows.Next() {
		record, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return items, nil
}

// nodeFilterWhere turns a NodeFilter into a reusable WHERE clause over the
// aliased nodes table. It is shared by ListNodes and CountNodes so a count can
// never answer a different question than the page it describes.
func nodeFilterWhere(filter NodeFilter) (string, []any) {
	conditions := []string{"1 = 1"}
	arguments := make([]any, 0, 3)
	if search := strings.TrimSpace(filter.Search); search != "" {
		conditions = append(conditions, "(n.display_name LIKE ? ESCAPE '\\' OR n.fingerprint LIKE ? ESCAPE '\\')")
		pattern := "%" + escapeLike(search) + "%"
		arguments = append(arguments, pattern, pattern)
	}
	if protocol := strings.TrimSpace(filter.Protocol); protocol != "" {
		conditions = append(conditions, "n.protocol = ?")
		arguments = append(arguments, protocol)
	}
	if state := strings.TrimSpace(filter.State); state != "" {
		conditions = append(conditions, "n.lifecycle_state = ?")
		arguments = append(arguments, state)
	}
	return strings.Join(conditions, " AND "), arguments
}

// CountNodes reports how many nodes match the filter, ignoring pagination.
//
// It exists so a list client that needs the whole inventory can learn the size
// from the first page and fetch the remaining pages concurrently. The nodes
// list is the control plane's largest collection (thousands of rows), and a
// serial offset walk pays a full query per page: measured on a 2750-node
// inventory, three sequential 1000-row pages cost ~1.1s while the same three
// pages fetched in parallel cost one page plus a count.
func (s *Store) CountNodes(ctx context.Context, filter NodeFilter) (int, error) {
	where, arguments := nodeFilterWhere(filter)
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM nodes n WHERE `+where, arguments...)
	var total int
	if err := row.Scan(&total); err != nil {
		return 0, fmt.Errorf("count nodes: %w", err)
	}
	return total, nil
}

func (s *Store) GetNode(ctx context.Context, id string) (NodeRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT
    n.id,
    n.fingerprint,
    n.display_name,
    n.protocol,
    n.lifecycle_state,
    n.first_seen_at,
    n.last_seen_at,
    n.retired_at,
    n.last_checked_at,
    n.last_latency_ms,
    n.consecutive_probe_failures,
    n.next_probe_at,
    n.probe_paused,
    n.version,
    (SELECT qc.error_code FROM node_quality_checks qc
     WHERE qc.node_id = n.id ORDER BY qc.checked_at DESC LIMIT 1) AS last_error_code,
    (SELECT qc.error_message FROM node_quality_checks qc
     WHERE qc.node_id = n.id ORDER BY qc.checked_at DESC LIMIT 1) AS last_error_message,
    COUNT(DISTINCT CASE WHEN ss.status = 'active' THEN sn.subscription_id END) AS source_count
FROM nodes n
LEFT JOIN subscription_nodes sn ON sn.node_id = n.id
LEFT JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id
WHERE n.id = ?
GROUP BY n.id
`, id)
	record, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRecord{}, ErrNotFound
	}
	if err != nil {
		return NodeRecord{}, fmt.Errorf("get node: %w", err)
	}
	return record, nil
}

func scanNode(source scanner) (NodeRecord, error) {
	var record NodeRecord
	var firstSeen string
	var lastSeen string
	var retired sql.NullString
	var lastChecked sql.NullString
	var lastLatency sql.NullInt64
	var nextProbeAt sql.NullString
	var probePaused int
	var lastErrorCode sql.NullString
	var lastErrorMessage sql.NullString
	if err := source.Scan(
		&record.ID,
		&record.Fingerprint,
		&record.DisplayName,
		&record.Protocol,
		&record.LifecycleState,
		&firstSeen,
		&lastSeen,
		&retired,
		&lastChecked,
		&lastLatency,
		&record.ConsecutiveProbeFailures,
		&nextProbeAt,
		&probePaused,
		&record.Version,
		&lastErrorCode,
		&lastErrorMessage,
		&record.SourceCount,
	); err != nil {
		return NodeRecord{}, err
	}
	record.ProbePaused = probePaused != 0
	if nextProbeAt.Valid && nextProbeAt.String != "" {
		parsedNextProbeAt, err := time.Parse(time.RFC3339Nano, nextProbeAt.String)
		if err != nil {
			return NodeRecord{}, fmt.Errorf("parse node next_probe_at: %w", err)
		}
		record.NextProbeAt = &parsedNextProbeAt
	}
	record.LastErrorCode = lastErrorCode.String
	record.LastErrorMessage = lastErrorMessage.String
	parsedFirstSeen, err := time.Parse(time.RFC3339Nano, firstSeen)
	if err != nil {
		return NodeRecord{}, fmt.Errorf("parse node first_seen_at: %w", err)
	}
	parsedLastSeen, err := time.Parse(time.RFC3339Nano, lastSeen)
	if err != nil {
		return NodeRecord{}, fmt.Errorf("parse node last_seen_at: %w", err)
	}
	record.FirstSeenAt = parsedFirstSeen
	record.LastSeenAt = parsedLastSeen
	if retired.Valid {
		parsedRetired, err := time.Parse(time.RFC3339Nano, retired.String)
		if err != nil {
			return NodeRecord{}, fmt.Errorf("parse node retired_at: %w", err)
		}
		record.RetiredAt = &parsedRetired
	}
	if lastChecked.Valid {
		parsedLastChecked, err := time.Parse(time.RFC3339Nano, lastChecked.String)
		if err != nil {
			return NodeRecord{}, fmt.Errorf("parse node last_checked_at: %w", err)
		}
		record.LastCheckedAt = &parsedLastChecked
	}
	if lastLatency.Valid {
		latency := int(lastLatency.Int64)
		record.LastLatencyMS = &latency
	}
	return record, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}

// SetNodeLifecycleState performs an administrator lifecycle transition.
// Only 'disabled' (admin off) and 'candidate' (re-enable, waiting for a new
// probe) are valid targets, and retired nodes cannot be changed.
//
// Re-enabling is an explicit operator override of the probe policy, so it also
// clears the failure schedule: a manual 'candidate' means "probe this now and
// judge it fresh", which is not true if the old backoff or pause survived.
// This is the same override path a mis-quarantined residential exit needs after
// the operator reclassifies its source.
func (s *Store) SetNodeLifecycleState(ctx context.Context, id, state string) error {
	if state != "disabled" && state != "candidate" {
		return fmt.Errorf("unsupported lifecycle transition to %q", state)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE nodes
SET lifecycle_state = ?,
    version = version + 1,
    consecutive_probe_failures = CASE WHEN ? = 'candidate' THEN 0 ELSE consecutive_probe_failures END,
    next_probe_at = CASE WHEN ? = 'candidate' THEN NULL ELSE next_probe_at END,
    probe_paused = CASE WHEN ? = 'candidate' THEN 0 ELSE probe_paused END,
    retired_at = CASE WHEN ? = 'candidate' THEN NULL ELSE retired_at END
WHERE id = ? AND lifecycle_state <> 'retired'
`, state, state, state, state, state, id)
	if err != nil {
		return fmt.Errorf("set node lifecycle state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read node lifecycle update result: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}
