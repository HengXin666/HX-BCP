package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type NodeQualityResult struct {
	NodeID       string
	CheckedAt    time.Time
	Success      bool
	LatencyMS    *int
	TestURL      string
	ErrorCode    string
	ErrorMessage string
}

type NodeQualityCheckRecord struct {
	NodeQualityResult
	ID int64
}

func (s *Store) RecordNodeHealthResult(ctx context.Context, result NodeQualityResult) error {
	var latency any
	if result.LatencyMS != nil {
		latency = *result.LatencyMS
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO node_quality_checks(
    node_id, checked_at, success, latency_ms, test_url, error_code, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?)
`, result.NodeID, result.CheckedAt.UTC().Format(time.RFC3339Nano), boolToInteger(result.Success), latency, result.TestURL, result.ErrorCode, result.ErrorMessage)
	if isForeignKeyConstraint(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("insert node health result: %w", err)
	}
	return nil
}

func (s *Store) ListLatestNodeHealthResults(ctx context.Context, nodeIDs []string) ([]NodeQualityCheckRecord, error) {
	if len(nodeIDs) == 0 {
		return []NodeQualityCheckRecord{}, nil
	}
	if len(nodeIDs) > 1000 {
		return nil, errors.New("at most 1000 node health result sets can be listed")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(nodeIDs)), ",")
	arguments := make([]any, len(nodeIDs))
	for index, id := range nodeIDs {
		arguments[index] = id
	}
	// The window function picks the newest row per (node, test_url) in one
	// pass over the matching checks, replacing the previous per-row NOT EXISTS
	// scan that degraded as quality history accumulated.
	rows, err := s.db.QueryContext(ctx, `
SELECT id, node_id, checked_at, success, latency_ms, test_url, error_code, error_message
FROM (
    SELECT q.id, q.node_id, q.checked_at, q.success, q.latency_ms,
           q.test_url, q.error_code, q.error_message,
           ROW_NUMBER() OVER (
               PARTITION BY q.node_id, q.test_url
               ORDER BY q.checked_at DESC, q.id DESC
           ) AS position
    FROM node_quality_checks q
    WHERE q.node_id IN (`+placeholders+`)
)
WHERE position = 1
ORDER BY node_id, test_url
`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list latest node health results: %w", err)
	}
	defer rows.Close()
	records := make([]NodeQualityCheckRecord, 0)
	for rows.Next() {
		var record NodeQualityCheckRecord
		var checkedAt string
		var success int
		var latency sql.NullInt64
		if err := rows.Scan(&record.ID, &record.NodeID, &checkedAt, &success, &latency, &record.TestURL, &record.ErrorCode, &record.ErrorMessage); err != nil {
			return nil, fmt.Errorf("scan latest node health result: %w", err)
		}
		record.Success = success == 1
		if latency.Valid {
			value := int(latency.Int64)
			record.LatencyMS = &value
		}
		record.CheckedAt, err = time.Parse(time.RFC3339Nano, checkedAt)
		if err != nil {
			return nil, fmt.Errorf("parse latest node health result time: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest node health results: %w", err)
	}
	return records, nil
}

func (s *Store) RecordNodeQualityResult(ctx context.Context, result NodeQualityResult) (NodeRecord, error) {
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return NodeRecord{}, fmt.Errorf("begin node quality result: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	var latency any
	if result.LatencyMS != nil {
		latency = *result.LatencyMS
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO node_quality_checks(
    node_id, checked_at, success, latency_ms, test_url, error_code, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?)
`,
		result.NodeID,
		result.CheckedAt.UTC().Format(time.RFC3339Nano),
		boolToInteger(result.Success),
		latency,
		result.TestURL,
		result.ErrorCode,
		result.ErrorMessage,
	); err != nil {
		if isForeignKeyConstraint(err) {
			return NodeRecord{}, ErrNotFound
		}
		return NodeRecord{}, fmt.Errorf("insert node quality result: %w", err)
	}

	if result.Success {
		state := "healthy"
		if result.LatencyMS != nil && *result.LatencyMS > 1500 {
			state = "degraded"
		}
		// A success clears the whole failure schedule: the consecutive-failure
		// counter resets, the exponential backoff is dropped, and a paused
		// probe is resumed. This is what makes a residential node come back on
		// its own, and what stops a pool member from staying paused after a
		// genuine recovery.
		updateResult, err := transaction.ExecContext(ctx, `
UPDATE nodes
SET
    lifecycle_state = CASE WHEN lifecycle_state = 'disabled' THEN 'disabled' ELSE ? END,
    last_checked_at = ?,
    last_latency_ms = ?,
    consecutive_probe_failures = 0,
    next_probe_at = NULL,
    probe_paused = 0,
    version = version + 1
WHERE id = ? AND lifecycle_state <> 'retired'
`, state, result.CheckedAt.UTC().Format(time.RFC3339Nano), latency, result.NodeID)
		if err != nil {
			return NodeRecord{}, fmt.Errorf("update successful node quality: %w", err)
		}
		if err := requireOneRow(updateResult); err != nil {
			return NodeRecord{}, err
		}
	} else {
		// The failure rule is per source class, not global. Reading the current
		// counter first (rather than incrementing inside the UPDATE) is what
		// lets the threshold, the retirement decision and the backoff all be
		// derived from ONE snapshot of the same counter — computing them in SQL
		// and in Go would be two copies of the same judgement, which is the
		// divergence this repository has already been burned by.
		classes, err := nodeSourceClasses(ctx, transaction, result.NodeID)
		if err != nil {
			return NodeRecord{}, err
		}
		policy := NodePolicyFor(classes)

		var previousFailures int
		var lifecycleState string
		if err := transaction.QueryRowContext(ctx, `
SELECT consecutive_probe_failures, lifecycle_state FROM nodes WHERE id = ?
`, result.NodeID).Scan(&previousFailures, &lifecycleState); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return NodeRecord{}, ErrNotFound
			}
			return NodeRecord{}, fmt.Errorf("read node failure counter: %w", err)
		}
		failures := previousFailures + 1

		nextState := "degraded"
		switch {
		case lifecycleState == "disabled":
			// An administrator switched this node off; a probe result must not
			// switch it back on.
			nextState = "disabled"
		case policy.ShouldRetire(failures):
			nextState = "retired"
		case lifecycleState == "quarantined":
			// Quarantine is the conservative state. A failure never lifts it:
			// only a success or an explicit administrator enable does. Without
			// this, a class that never quarantines (residential) would demote
			// an already-quarantined node back to 'degraded' — which a group
			// with a 'degraded' filter would then route through.
			nextState = "quarantined"
		case policy.ShouldQuarantine(failures):
			nextState = "quarantined"
		}

		// probe_paused is the terminal brake. It is set when the class declares
		// that a member past this point is spent (see ClassPolicies): for a
		// 5000-member disposable pool, re-probing a specific dead member is
		// pure waste — the next refresh brings replacements.
		paused := 0
		if nextState == "quarantined" && !policy.KeepsQuarantinedProbe() {
			paused = 1
		}
		if nextState == "retired" {
			paused = 1
		}

		// Exponential backoff, persisted rather than slept: the due-node query
		// enforces it, so one slow node cannot stall a whole batch. A paused or
		// retired node gets no schedule at all.
		var nextProbeAt any
		var retiredAt any
		if nextState == "retired" {
			retiredAt = result.CheckedAt.UTC().Format(time.RFC3339Nano)
		}
		if paused == 0 {
			nextProbeAt = policy.NextProbeAt(result.CheckedAt.UTC(), failures).UTC().Format(time.RFC3339Nano)
		}

		updateResult, err := transaction.ExecContext(ctx, `
UPDATE nodes
SET
    lifecycle_state = ?,
    last_checked_at = ?,
    last_latency_ms = NULL,
    consecutive_probe_failures = ?,
    next_probe_at = ?,
    probe_paused = ?,
    retired_at = ?,
    version = version + 1
WHERE id = ? AND lifecycle_state <> 'retired'
`,
			nextState,
			result.CheckedAt.UTC().Format(time.RFC3339Nano),
			failures,
			nextProbeAt,
			paused,
			retiredAt,
			result.NodeID,
		)
		if err != nil {
			return NodeRecord{}, fmt.Errorf("update failed node quality: %w", err)
		}
		if err := requireOneRow(updateResult); err != nil {
			return NodeRecord{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return NodeRecord{}, fmt.Errorf("commit node quality result: %w", err)
	}
	committed = true
	return s.GetNode(ctx, result.NodeID)
}

// nodeSourceClasses returns the source classes of every source that currently
// owns this node. The per-class failure policy it feeds is specified in
// .agents/notes/implemented/feature/2026-09-26-source-class-drives-probe-policy.md. In production that is exactly one source; the plural form
// exists because a node's policy must remain well defined when an operator
// deliberately shares one endpoint between two sources.
//
// It runs inside the caller's transaction so the policy is derived from the
// same database snapshot as the failure counter it is applied to. A source
// deleted concurrently cannot make the policy read a half-updated world.
func nodeSourceClasses(ctx context.Context, transaction *sql.Tx, nodeID string) ([]SourceClass, error) {
	rows, err := transaction.QueryContext(ctx, `
SELECT DISTINCT s.source_class
FROM subscription_nodes sn
JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id AND ss.status = 'active'
JOIN subscriptions s ON s.id = sn.subscription_id
WHERE sn.node_id = ?
`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("read node source classes: %w", err)
	}
	defer rows.Close()
	classes := make([]SourceClass, 0, 2)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan node source class: %w", err)
		}
		classes = append(classes, SourceClass(value))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node source classes: %w", err)
	}
	return classes, nil
}

func requireOneRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read node quality update result: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

// DueNodeIDs lists the nodes whose next probe is allowed now. The caller passes
// the cutoff for the steady-state interval; nodes carrying an explicit
// next_probe_at (i.e. ones backing off after failures) are additionally gated by
// their own schedule, and nodes marked probe_paused are never returned.
//
// Putting the backoff here rather than sleeping in the probe loop matters: a
// single dead node backing off for six hours must not occupy a worker or delay
// the rest of the batch.
func (s *Store) DueNodeIDs(ctx context.Context, before time.Time, limit int) ([]string, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id
FROM nodes
WHERE lifecycle_state NOT IN ('disabled', 'retired')
  AND probe_paused = 0
  AND (last_checked_at IS NULL OR last_checked_at <= ?)
  AND (next_probe_at IS NULL OR next_probe_at <= ?)
ORDER BY COALESCE(next_probe_at, last_checked_at, first_seen_at) ASC, id ASC
LIMIT ?
`, before.UTC().Format(time.RFC3339Nano), before.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("list due node checks: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan due node check: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due node checks: %w", err)
	}
	return ids, nil
}

func (s *Store) GetNodeConfig(ctx context.Context, id string) (NodeConfigRecord, error) {
	var record NodeConfigRecord
	err := s.db.QueryRowContext(ctx, `
SELECT id, fingerprint, display_name, protocol, lifecycle_state, canonical_config_encrypted
FROM nodes
WHERE id = ?
`, id).Scan(
		&record.ID,
		&record.Fingerprint,
		&record.DisplayName,
		&record.Protocol,
		&record.LifecycleState,
		&record.CanonicalConfigEncrypted,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeConfigRecord{}, ErrNotFound
	}
	if err != nil {
		return NodeConfigRecord{}, fmt.Errorf("get node config: %w", err)
	}
	return record, nil
}
