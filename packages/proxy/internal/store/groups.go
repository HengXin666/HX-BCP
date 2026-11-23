package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ProxyGroupRecord struct {
	ID             string
	Name           string
	Strategy       string
	SourceSpecJSON string
	// RulePipelineJSON is the per-group rule pipeline.
	RulePipelineJSON string
	Enabled          bool
	EmptyBehavior    string
	FallbackTargetID string
	// DialerProxyGroupID names the group this group's egress is tunnelled
	// through. It is a group-level chain: every member this group selects is
	// dialled through that group. Mihomo rejects dialer-proxy on a proxy-group,
	// so the compiler turns this into derived per-member proxies instead of a
	// group key; see .agents/notes/implemented/architecture/
	// 2026-09-16-mihomo-dialer-proxy-is-node-level.md.
	DialerProxyGroupID string
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (s *Store) CreateProxyGroup(ctx context.Context, record ProxyGroupRecord) (ProxyGroupRecord, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO proxy_groups(
    id, name, strategy, source_spec_json, rule_pipeline_json, enabled,
    empty_behavior, fallback_target_id, dialer_proxy_group_id, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)
`,
		record.ID,
		record.Name,
		record.Strategy,
		record.SourceSpecJSON,
		record.RulePipelineJSON,
		boolToInteger(record.Enabled),
		record.EmptyBehavior,
		record.FallbackTargetID,
		record.DialerProxyGroupID,
		record.Version,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ProxyGroupRecord{}, ErrConflict
		}
		return ProxyGroupRecord{}, fmt.Errorf("create proxy group: %w", err)
	}
	return record, nil
}

func (s *Store) GetProxyGroup(ctx context.Context, id string) (ProxyGroupRecord, error) {
	record, err := scanProxyGroup(s.db.QueryRowContext(ctx, proxyGroupSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProxyGroupRecord{}, ErrNotFound
	}
	if err != nil {
		return ProxyGroupRecord{}, fmt.Errorf("get proxy group: %w", err)
	}
	return record, nil
}

func (s *Store) ListProxyGroups(ctx context.Context) ([]ProxyGroupRecord, error) {
	rows, err := s.db.QueryContext(ctx, proxyGroupSelect+" ORDER BY created_at ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list proxy groups: %w", err)
	}
	defer rows.Close()
	records := make([]ProxyGroupRecord, 0)
	for rows.Next() {
		record, err := scanProxyGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proxy group: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy groups: %w", err)
	}
	return records, nil
}

func (s *Store) UpdateProxyGroup(
	ctx context.Context,
	record ProxyGroupRecord,
	expectedVersion int,
) (ProxyGroupRecord, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE proxy_groups
SET
    name = ?, strategy = ?, source_spec_json = ?, rule_pipeline_json = ?,
    enabled = ?, empty_behavior = ?, fallback_target_id = NULLIF(?, ''),
    dialer_proxy_group_id = NULLIF(?, ''),
    version = version + 1, updated_at = ?
WHERE id = ? AND version = ?
`,
		record.Name,
		record.Strategy,
		record.SourceSpecJSON,
		record.RulePipelineJSON,
		boolToInteger(record.Enabled),
		record.EmptyBehavior,
		record.FallbackTargetID,
		record.DialerProxyGroupID,
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		record.ID,
		expectedVersion,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ProxyGroupRecord{}, ErrConflict
		}
		return ProxyGroupRecord{}, fmt.Errorf("update proxy group: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return ProxyGroupRecord{}, fmt.Errorf("read proxy group update result: %w", err)
	}
	if rowsAffected != 1 {
		if _, getErr := s.GetProxyGroup(ctx, record.ID); errors.Is(getErr, ErrNotFound) {
			return ProxyGroupRecord{}, ErrNotFound
		}
		return ProxyGroupRecord{}, ErrConflict
	}
	return s.GetProxyGroup(ctx, record.ID)
}

func (s *Store) DeleteProxyGroup(ctx context.Context, id string, expectedVersion int) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM proxy_groups WHERE id = ? AND version = ?", id, expectedVersion)
	if err != nil {
		if isForeignKeyConstraint(err) {
			return ErrConflict
		}
		return fmt.Errorf("delete proxy group: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read proxy group delete result: %w", err)
	}
	if rowsAffected == 1 {
		return nil
	}
	if _, getErr := s.GetProxyGroup(ctx, id); errors.Is(getErr, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

const proxyGroupSelect = `
SELECT
    id, name, strategy, source_spec_json, rule_pipeline_json, enabled,
    empty_behavior, COALESCE(fallback_target_id, ''),
    COALESCE(dialer_proxy_group_id, ''), version, created_at, updated_at
FROM proxy_groups`

func scanProxyGroup(source scanner) (ProxyGroupRecord, error) {
	var record ProxyGroupRecord
	var enabled int
	var createdAt string
	var updatedAt string
	if err := source.Scan(
		&record.ID,
		&record.Name,
		&record.Strategy,
		&record.SourceSpecJSON,
		&record.RulePipelineJSON,
		&enabled,
		&record.EmptyBehavior,
		&record.FallbackTargetID,
		&record.DialerProxyGroupID,
		&record.Version,
		&createdAt,
		&updatedAt,
	); err != nil {
		return ProxyGroupRecord{}, err
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ProxyGroupRecord{}, fmt.Errorf("parse proxy group created_at: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ProxyGroupRecord{}, fmt.Errorf("parse proxy group updated_at: %w", err)
	}
	record.Enabled = enabled != 0
	record.CreatedAt = parsedCreatedAt
	record.UpdatedAt = parsedUpdatedAt
	return record, nil
}

func isForeignKeyConstraint(err error) bool {
	return err != nil && (containsFold(err.Error(), "foreign key constraint failed") || containsFold(err.Error(), "constraint failed"))
}

func containsFold(value, fragment string) bool {
	if len(fragment) > len(value) {
		return false
	}
	for index := 0; index+len(fragment) <= len(value); index++ {
		match := true
		for offset := range fragment {
			left := value[index+offset]
			right := fragment[offset]
			if left >= 'A' && left <= 'Z' {
				left += 'a' - 'A'
			}
			if right >= 'A' && right <= 'Z' {
				right += 'a' - 'A'
			}
			if left != right {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
