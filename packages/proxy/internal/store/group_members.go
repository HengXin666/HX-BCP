package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// GroupMemberRecord is one node a group carried at the last successful apply.
type GroupMemberRecord struct {
	GroupID    string
	NodeFP     string
	JoinedAt   time.Time
	LastKeptAt time.Time
	Reason     string
}

// ListGroupMembers returns group id -> node fingerprint -> joined_at.
func (s *Store) ListGroupMembers(ctx context.Context) (map[string]map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id, node_fp, joined_at FROM group_members`)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	defer rows.Close()
	result := make(map[string]map[string]time.Time)
	for rows.Next() {
		var groupID, fingerprint, joined string
		if err := rows.Scan(&groupID, &fingerprint, &joined); err != nil {
			return nil, fmt.Errorf("scan group member: %w", err)
		}
		at, _ := time.Parse(time.RFC3339Nano, joined)
		if result[groupID] == nil {
			result[groupID] = make(map[string]time.Time)
		}
		result[groupID][fingerprint] = at
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group members: %w", err)
	}
	return result, nil
}

// ReplaceGroupMembers converges the stored membership onto members (group id
// -> node fingerprints) in one transaction: newcomers join at now, kept members
// keep joined_at and get last_kept_at = now, and members that left are
// removed. Groups absent from members (disabled or deleted) are cleared, so the
// table never describes a group Mihomo no longer serves.
func (s *Store) ReplaceGroupMembers(ctx context.Context, members map[string][]string, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group members: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS current_group_members(group_id TEXT NOT NULL, node_fp TEXT NOT NULL, PRIMARY KEY (group_id, node_fp))`); err != nil {
		return fmt.Errorf("prepare group members: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM current_group_members`); err != nil {
		return fmt.Errorf("prepare group members: %w", err)
	}
	groupIDs := make([]string, 0, len(members))
	for groupID := range members {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Strings(groupIDs)
	for _, groupID := range groupIDs {
		for _, fingerprint := range members[groupID] {
			if fingerprint == "" {
				continue
			}
			if _, err := transaction.ExecContext(ctx, `INSERT OR IGNORE INTO current_group_members(group_id, node_fp) VALUES (?, ?)`, groupID, fingerprint); err != nil {
				return fmt.Errorf("stage group member: %w", err)
			}
		}
	}
	statements := []string{
		`DELETE FROM group_members WHERE NOT EXISTS (
            SELECT 1 FROM current_group_members c WHERE c.group_id = group_members.group_id AND c.node_fp = group_members.node_fp)`,
		`UPDATE group_members SET last_kept_at = ?1, reason = 'kept'`,
		`INSERT INTO group_members(group_id, node_fp, joined_at, last_kept_at, reason)
            SELECT c.group_id, c.node_fp, ?1, ?1, 'selected' FROM current_group_members c
            WHERE EXISTS (SELECT 1 FROM proxy_groups g WHERE g.id = c.group_id)
            ON CONFLICT(group_id, node_fp) DO NOTHING`,
		`DELETE FROM current_group_members`,
	}
	for index, statement := range statements {
		var execErr error
		if index == 1 || index == 2 {
			_, execErr = transaction.ExecContext(ctx, statement, stamp)
		} else {
			_, execErr = transaction.ExecContext(ctx, statement)
		}
		if execErr != nil {
			return fmt.Errorf("replace group members: %w", execErr)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit group members: %w", err)
	}
	return nil
}
