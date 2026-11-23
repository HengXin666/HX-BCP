package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type NodeConfigRecord struct {
	ID                       string
	Fingerprint              string
	DisplayName              string
	Protocol                 string
	LifecycleState           string
	CanonicalConfigEncrypted []byte
}

type GroupNodeCandidate struct {
	NodeConfigRecord
	LastLatencyMS   *int
	SubscriptionIDs []string
}

func (s *Store) ListNodeConfigs(ctx context.Context, ids []string) ([]NodeConfigRecord, error) {
	conditions := []string{"lifecycle_state NOT IN ('disabled', 'retired')"}
	arguments := make([]any, 0, len(ids))
	if len(ids) > 0 {
		placeholders := make([]string, len(ids))
		for index, id := range ids {
			placeholders[index] = "?"
			arguments = append(arguments, id)
		}
		conditions = append(conditions, "id IN ("+strings.Join(placeholders, ",")+")")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, fingerprint, display_name, protocol, lifecycle_state, canonical_config_encrypted
FROM nodes
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY first_seen_at ASC, id ASC
`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list node configs: %w", err)
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
			return nil, fmt.Errorf("scan node config: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node configs: %w", err)
	}
	return records, nil
}

// ListGroupNodeCandidates returns every node a proxy group may select from.
// Subscription nodes are only offered while they belong to an active snapshot.
// Residential session nodes have no snapshot at all, so they are unioned in
// with an empty subscription id and stay selectable through their own origin.
func (s *Store) ListGroupNodeCandidates(ctx context.Context) ([]GroupNodeCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
    n.id, n.fingerprint, n.display_name, n.protocol, n.lifecycle_state,
    n.canonical_config_encrypted, n.last_latency_ms, sn.subscription_id
FROM nodes n
JOIN subscription_nodes sn ON sn.node_id = n.id
JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id AND ss.status = 'active'
WHERE n.lifecycle_state NOT IN ('disabled', 'retired')
UNION
SELECT
    n.id, n.fingerprint, n.display_name, n.protocol, n.lifecycle_state,
    n.canonical_config_encrypted, n.last_latency_ms, ''
FROM nodes n
WHERE n.origin = 'residential' AND n.lifecycle_state NOT IN ('disabled', 'retired')
ORDER BY 1 ASC, 8 ASC
`)
	if err != nil {
		return nil, fmt.Errorf("list proxy group node candidates: %w", err)
	}
	defer rows.Close()

	items := make([]GroupNodeCandidate, 0)
	byID := make(map[string]int)
	for rows.Next() {
		var record GroupNodeCandidate
		var latency sql.NullInt64
		var subscriptionID string
		if err := rows.Scan(
			&record.ID,
			&record.Fingerprint,
			&record.DisplayName,
			&record.Protocol,
			&record.LifecycleState,
			&record.CanonicalConfigEncrypted,
			&latency,
			&subscriptionID,
		); err != nil {
			return nil, fmt.Errorf("scan proxy group node candidate: %w", err)
		}
		if index, exists := byID[record.ID]; exists {
			if subscriptionID != "" {
				items[index].SubscriptionIDs = append(items[index].SubscriptionIDs, subscriptionID)
			}
			continue
		}
		if latency.Valid {
			value := int(latency.Int64)
			record.LastLatencyMS = &value
		}
		// Residential session nodes have no subscription; keep the slice empty
		// so subscription predicates never match them accidentally.
		record.SubscriptionIDs = []string{}
		if subscriptionID != "" {
			record.SubscriptionIDs = []string{subscriptionID}
		}
		byID[record.ID] = len(items)
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy group node candidates: %w", err)
	}
	return items, nil
}
