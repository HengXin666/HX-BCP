package store

import (
	"context"
	"testing"
	"time"
)

// TestSubscriptionNodesLookupIndexExists pins the index the retire sweep needs.
//
// The sweep asks, per candidate node, "does any ACTIVE snapshot still mention
// me?". Without an index that starts with node_id, SQLite walked the covering
// index subscription_nodes_subscription once per node, so it was O(nodes x
// links): 16s of CPU for one 2199-node refresh (measured 2026-10-28). The
// index alone took that same commit to 183ms.
func TestSubscriptionNodesLookupIndexExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := openTestStore(t)

	rows, err := storage.db.QueryContext(ctx, `
SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'subscription_nodes'
`)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer rows.Close()
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}
	if !names["subscription_nodes_node"] {
		t.Fatalf("subscription_nodes(node_id) index missing, indexes = %v", names)
	}
}

// TestRefreshingASubscriptionDropsRetiredSnapshotLinks pins the retention rule
// that keeps that sweep cheap: a snapshot that is no longer active has no
// reachable links, because every reader filters on the ACTIVE snapshot.
//
// Before the fix 97% of the links in the reported database (82,129 of 84,632)
// belonged to retired snapshots and every refresh walked them.
func TestRefreshingASubscriptionDropsRetiredSnapshotLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := openTestStore(t)
	now := time.Now().UTC()

	if _, err := storage.db.ExecContext(ctx, SUBSCRIPTION_SEED_SQL, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	first := SubscriptionSnapshotRecord{
		ID: "snap-retention-1", SubscriptionID: "sub-1", ContentHash: "hash-1",
		FetchedAt: now, NodeCount: 1, CreatedAt: now,
	}
	if err := storage.CommitParsedSubscriptionSnapshot(ctx, first, []NodeImportRecord{{
		ID: "node-retention-1", Fingerprint: "fp-retention-1", DisplayName: "kept",
		Protocol: "http", CanonicalConfigEncrypted: []byte{0x00}, SourceName: "src",
	}}); err != nil {
		t.Fatalf("first commit: %v", err)
	}

	second := SubscriptionSnapshotRecord{
		ID: "snap-retention-2", SubscriptionID: "sub-1", ContentHash: "hash-2",
		FetchedAt: now.Add(time.Minute), NodeCount: 1, CreatedAt: now.Add(time.Minute),
	}
	if err := storage.CommitParsedSubscriptionSnapshot(ctx, second, []NodeImportRecord{{
		ID: "node-retention-1", Fingerprint: "fp-retention-1", DisplayName: "kept",
		Protocol: "http", CanonicalConfigEncrypted: []byte{0x00}, SourceName: "src",
	}}); err != nil {
		t.Fatalf("second commit: %v", err)
	}

	var retiredLinks, activeLinks int
	if err := storage.db.QueryRowContext(ctx, `
SELECT
    COALESCE(SUM(CASE WHEN ss.status <> 'active' THEN 1 ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN ss.status = 'active' THEN 1 ELSE 0 END), 0)
FROM subscription_nodes sn
JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id
	`).Scan(&retiredLinks, &activeLinks); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if retiredLinks != 0 {
		t.Fatalf("retired snapshot still holds %d node links, want 0", retiredLinks)
	}
	if activeLinks != 1 {
		t.Fatalf("active snapshot holds %d node links, want 1", activeLinks)
	}

	// The node is still a member of the active snapshot, so the sweep must not
	// have retired it. Pruning is a link decision, never a lifecycle one.
	node, err := storage.GetNode(ctx, "node-retention-1")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if node.LifecycleState == "retired" {
		t.Fatalf("pruning retired a node that is still in the active snapshot: %+v", node)
	}
}

// TestActivatingASnapshotDropsRetiredSnapshotLinks covers the second writer.
// ActivateParsedSubscriptionSnapshot is the path a not-modified / identical
// hash refresh takes, and it must prune the same way.
func TestActivatingASnapshotDropsRetiredSnapshotLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := openTestStore(t)
	now := time.Now().UTC()

	if _, err := storage.db.ExecContext(ctx, SUBSCRIPTION_SEED_SQL, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	stale := SubscriptionSnapshotRecord{
		ID: "snap-activate-1", SubscriptionID: "sub-1", ContentHash: "hash-a",
		FetchedAt: now, NodeCount: 1, CreatedAt: now,
	}
	if err := storage.CommitParsedSubscriptionSnapshot(ctx, stale, []NodeImportRecord{{
		ID: "node-activate-1", Fingerprint: "fp-activate-1", DisplayName: "kept",
		Protocol: "http", CanonicalConfigEncrypted: []byte{0x00}, SourceName: "src",
	}}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	// A second snapshot row that stays non-active, as a failed/duplicate fetch
	// would leave behind; it must not keep its links.
	if _, err := storage.db.ExecContext(ctx, `
INSERT INTO subscription_snapshots(id, subscription_id, content_hash, fetched_at, node_count, status, created_at)
VALUES ('snap-activate-2', 'sub-1', 'hash-b', ?, 1, 'candidate', ?)
	`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed candidate snapshot: %v", err)
	}
	if _, err := storage.db.ExecContext(ctx, `
INSERT INTO subscription_nodes(subscription_id, snapshot_id, node_id, source_name)
VALUES ('sub-1', 'snap-activate-2', 'node-activate-1', 'src')
	`); err != nil {
		t.Fatalf("seed candidate link: %v", err)
	}

	if err := storage.ActivateParsedSubscriptionSnapshot(ctx, stale, []NodeImportRecord{{
		ID: "node-activate-1", Fingerprint: "fp-activate-1", DisplayName: "kept",
		Protocol: "http", CanonicalConfigEncrypted: []byte{0x00}, SourceName: "src",
	}}, "{}"); err != nil {
		t.Fatalf("ActivateParsedSubscriptionSnapshot() error = %v", err)
	}

	var inactive int
	if err := storage.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM subscription_nodes sn
JOIN subscription_snapshots ss ON ss.id = sn.snapshot_id
WHERE ss.status <> 'active'
	`).Scan(&inactive); err != nil {
		t.Fatalf("count inactive links: %v", err)
	}
	if inactive != 0 {
		t.Fatalf("%d links remain on inactive snapshots, want 0", inactive)
	}
}
