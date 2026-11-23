package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedClassedSource inserts a subscription with the given class and returns its
// id, so a node's policy can be derived the same way production derives it.
func seedClassedSource(t *testing.T, ctx context.Context, database *Store, id string, class SourceClass) {
	t.Helper()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	if _, err := database.CreateSubscription(ctx, SubscriptionRecord{
		ID:                     id,
		Name:                   id,
		SourceType:             "inline",
		SourceClass:            string(class),
		SourceConfigEncrypted:  []byte("x"),
		Enabled:                true,
		RefreshIntervalSeconds: 3600,
		Version:                1,
		CreatedAt:              now,
		UpdatedAt:              now,
	}); err != nil {
		t.Fatalf("create source %s: %v", id, err)
	}
	if _, err := database.db.ExecContext(ctx, `
INSERT INTO subscription_snapshots(id, subscription_id, content_hash, fetched_at, node_count, status, created_at)
VALUES (?, ?, ?, ?, 1, 'active', ?)
`, "snap-"+id, id, "hash-"+id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("create snapshot for %s: %v", id, err)
	}
	// The node row must exist before subscription_nodes can reference it:
	// foreign keys are enforced on this connection.
	if _, err := database.db.ExecContext(ctx, `
INSERT INTO nodes(id, fingerprint, display_name, protocol, canonical_config_encrypted,
                  lifecycle_state, first_seen_at, last_seen_at, version)
VALUES (?, ?, 'n', 'http', X'00', 'candidate', ?, ?, 1)
`, id+"-node", "fp-"+id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("create node for %s: %v", id, err)
	}
	if _, err := database.db.ExecContext(ctx, `
INSERT INTO subscription_nodes(subscription_id, snapshot_id, node_id, source_name)
VALUES (?, ?, ?, 'src')
`, id, "snap-"+id, id+"-node"); err != nil {
		t.Fatalf("link node for %s: %v", id, err)
	}
}

func failureAt(minute int) NodeQualityResult {
	return NodeQualityResult{
		NodeID:    "",
		CheckedAt: time.Date(2026, 9, 26, 12, minute, 0, 0, time.UTC),
		Success:   false,
		TestURL:   "http://example.invalid/generate_204",
	}
}

// TestResidentialSourceIsNeverQuarantinedOrPaused is the direct regression test
// for the reported incident: a single-member source whose only exit failed
// repeatedly was quarantined and then re-probed until the counter passed 249.
// Under the residential policy it must stay probeable and must never be
// retired, no matter how many times it fails.
func TestResidentialSourceIsNeverQuarantinedOrPaused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedClassedSource(t, ctx, database, "resi", SourceClassResidential)

	nodeID := "resi-node"
	for attempt := 1; attempt <= 300; attempt++ {
		result := failureAt(attempt)
		result.NodeID = nodeID
		record, err := database.RecordNodeQualityResult(ctx, result)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if record.LifecycleState == "quarantined" {
			t.Fatalf("attempt %d quarantined a residential node: %+v", attempt, record)
		}
		if record.LifecycleState == "retired" {
			t.Fatalf("attempt %d RETIRED a residential node — the only exit is gone: %+v", attempt, record)
		}
		if record.ProbePaused {
			t.Fatalf("attempt %d paused probing a residential node: %+v", attempt, record)
		}
	}
	// It must still be considered due: probing a dead residential node is the
	// only way to notice it recovered.
	due, err := database.DueNodeIDs(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0] != nodeID {
		t.Fatalf("due = %v, want the residential node to remain probeable", due)
	}
}

// TestPoolSourceStopsProbingOnceSpent pins the other half: a disposable pool
// member must be dropped rather than re-probed forever.
func TestPoolSourceStopsProbingOnceSpent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedClassedSource(t, ctx, database, "poolsrc", SourceClassPool)

	policy := PolicyFor(SourceClassPool)
	nodeID := "poolsrc-node"
	var quarantinedAt, retiredAt int
	for attempt := 1; attempt <= policy.RetireAfter; attempt++ {
		result := failureAt(attempt)
		result.NodeID = nodeID
		record, err := database.RecordNodeQualityResult(ctx, result)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if record.LifecycleState == "quarantined" && quarantinedAt == 0 {
			quarantinedAt = attempt
		}
		if record.LifecycleState == "retired" {
			retiredAt = attempt
		}
	}
	if quarantinedAt != policy.QuarantineAfter {
		t.Fatalf("pool quarantined at %d, want %d", quarantinedAt, policy.QuarantineAfter)
	}
	if retiredAt != policy.RetireAfter {
		t.Fatalf("pool retired at %d, want %d", retiredAt, policy.RetireAfter)
	}

	// A spent pool member must leave the due set entirely: this is what stops
	// the unbounded re-probing.
	due, err := database.DueNodeIDs(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("due = %v, want a retired pool member to be dropped", due)
	}
}

// TestQuarantinedPoolMemberIsPausedBeforeRetirement covers the window between
// quarantine and retirement: the member is already useless there, so it must
// not be probed even though it is not yet retired.
func TestQuarantinedPoolMemberIsPausedBeforeRetirement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedClassedSource(t, ctx, database, "pool2", SourceClassPool)

	policy := PolicyFor(SourceClassPool)
	result := failureAt(1)
	result.NodeID = "pool2-node"
	// Drive it exactly to the quarantine threshold, one before retirement.
	for attempt := 1; attempt <= policy.QuarantineAfter; attempt++ {
		result.CheckedAt = time.Date(2026, 9, 26, 12, attempt, 0, 0, time.UTC)
		record, err := database.RecordNodeQualityResult(ctx, result)
		if err != nil {
			t.Fatal(err)
		}
		if attempt < policy.QuarantineAfter && record.ProbePaused {
			t.Fatalf("paused before quarantine at attempt %d", attempt)
		}
	}
	record, err := database.GetNode(ctx, "pool2-node")
	if err != nil {
		t.Fatal(err)
	}
	if record.LifecycleState != "quarantined" || !record.ProbePaused {
		t.Fatalf("quarantined pool member = %+v, want quarantined and paused", record)
	}
	due, err := database.DueNodeIDs(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("due = %v, want a paused pool member excluded", due)
	}
}

// TestFailureBackoffDelaysTheNextDueProbe proves the backoff is actually
// enforced through the due set rather than merely recorded — the previous
// implementation re-probed at the plain interval forever.
func TestFailureBackoffDelaysTheNextDueProbe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedClassedSource(t, ctx, database, "sub1", SourceClassSubscription)

	checkedAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	result := failureAt(0)
	result.NodeID = "sub1-node"
	result.CheckedAt = checkedAt
	record, err := database.RecordNodeQualityResult(ctx, result)
	if err != nil {
		t.Fatal(err)
	}
	if record.NextProbeAt == nil {
		t.Fatal("no backoff recorded after a failure")
	}
	wantNext := checkedAt.Add(PolicyFor(SourceClassSubscription).BackoffBase)
	if !record.NextProbeAt.Equal(wantNext) {
		t.Fatalf("NextProbeAt = %v, want %v", record.NextProbeAt, wantNext)
	}
	// Immediately after the failure the node is NOT due, even though the
	// steady-state interval would have elapsed.
	due, err := database.DueNodeIDs(ctx, checkedAt.Add(time.Second), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("due = %v, want the backoff to hold the node out", due)
	}
	// Once the backoff expires it becomes due again.
	due, err = database.DueNodeIDs(ctx, wantNext.Add(time.Second), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %v, want the node due after the backoff", due)
	}
}

// TestSuccessClearsBackoffAndPause proves a recovering node is not held out by
// stale failure state — otherwise a residential exit that came back would stay
// unusable.
func TestSuccessClearsBackoffAndPause(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedClassedSource(t, ctx, database, "sub2", SourceClassSubscription)

	nodeID := "sub2-node"
	policy := PolicyFor(SourceClassSubscription)
	for attempt := 1; attempt <= policy.QuarantineAfter; attempt++ {
		result := failureAt(attempt)
		result.NodeID = nodeID
		if _, err := database.RecordNodeQualityResult(ctx, result); err != nil {
			t.Fatal(err)
		}
	}
	before, err := database.GetNode(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if before.LifecycleState != "quarantined" || before.NextProbeAt == nil {
		t.Fatalf("setup: node = %+v, want quarantined with a backoff", before)
	}

	latency := 120
	recovered, err := database.RecordNodeQualityResult(ctx, NodeQualityResult{
		NodeID:    nodeID,
		CheckedAt: time.Date(2026, 9, 26, 13, 0, 0, 0, time.UTC),
		Success:   true,
		LatencyMS: &latency,
		TestURL:   "http://example.invalid/generate_204",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.LifecycleState != "healthy" || recovered.ConsecutiveProbeFailures != 0 {
		t.Fatalf("recovered = %+v, want healthy and reset", recovered)
	}
	if recovered.NextProbeAt != nil || recovered.ProbePaused {
		t.Fatalf("recovered node kept failure schedule: %+v", recovered)
	}
}

// TestReclassificationChangesTheNextFailureDecision proves the class is read
// live rather than snapshotted at node creation: an operator who reclassifies a
// source changes how its nodes are treated from then on.
func TestReclassificationChangesTheNextFailureDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedClassedSource(t, ctx, database, "reclass", SourceClassPool)

	nodeID := "reclass-node"
	// Fail once: a pool member is not yet quarantined at 1.
	result := failureAt(1)
	result.NodeID = nodeID
	if _, err := database.RecordNodeQualityResult(ctx, result); err != nil {
		t.Fatal(err)
	}
	// Reclassify to residential.
	if _, err := database.db.ExecContext(ctx,
		"UPDATE subscriptions SET source_class = 'residential' WHERE id = 'reclass'"); err != nil {
		t.Fatal(err)
	}
	// Now drive far past the old pool threshold: it must never quarantine.
	for attempt := 2; attempt <= 50; attempt++ {
		result = failureAt(attempt)
		result.NodeID = nodeID
		record, err := database.RecordNodeQualityResult(ctx, result)
		if err != nil {
			t.Fatal(err)
		}
		if record.LifecycleState == "quarantined" || record.LifecycleState == "retired" {
			t.Fatalf("attempt %d: reclassified node was still retired/quarantined: %+v", attempt, record)
		}
	}
}

// TestMigrationBackfillsSourceClassFromSnapshotSize pins the backfill rule on a
// fresh database: the classification is derived from how many members the
// source actually has, because that is what "can I replace this member" means.
func TestMigrationBackfillsSourceClassFromSnapshotSize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := "2026-09-26T12:00:00Z"
	cases := []struct {
		id        string
		nodeCount int
		want      string
	}{
		{"one", 1, "residential"},
		{"four", 4, "residential"},
		{"mid", 41, "subscription"},
		{"huge", 5000, "pool"},
		{"zero", 0, "subscription"},
	}
	for _, tc := range cases {
		if _, err := database.db.ExecContext(ctx, `
INSERT INTO subscriptions(id, name, source_type, source_config_encrypted, enabled,
    refresh_interval_seconds, version, created_at, updated_at, source_class)
VALUES (?, ?, 'inline', X'01', 1, 3600, 1, ?, ?, 'subscription')
`, tc.id, tc.id, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `
INSERT INTO subscription_snapshots(id, subscription_id, content_hash, fetched_at, node_count, status, created_at)
VALUES (?, ?, ?, ?, ?, 'active', ?)
`, "snap-"+tc.id, tc.id, "hash-"+tc.id, now, tc.nodeCount, now); err != nil {
			t.Fatal(err)
		}
	}

	// Replay just the backfill UPDATE from the real migration. The ALTER
	// TABLE statements already ran during Open(), and replaying them would fail
	// on a duplicate column — so the extraction below takes the UPDATE only,
	// which is also the part that decides each source's class.
	if _, err := database.db.ExecContext(ctx, migration39BackfillSQL()); err != nil {
		t.Fatalf("replay backfill: %v", err)
	}
	for _, tc := range cases {
		var got string
		if err := database.db.QueryRowContext(ctx,
			"SELECT source_class FROM subscriptions WHERE id = ?", tc.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("source %s (%d nodes) classified %q, want %q", tc.id, tc.nodeCount, got, tc.want)
		}
	}
}

// migration39BackfillSQL extracts the backfill UPDATE from the real migration,
// so this test cannot pass against a copy that drifted from what ships.
func migration39BackfillSQL() string {
	for _, m := range migrations {
		if m.version != 39 {
			continue
		}
		start := strings.Index(m.sql, "UPDATE subscriptions")
		if start < 0 {
			return ""
		}
		remainder := m.sql[start:]
		end := strings.Index(remainder, "ALTER TABLE nodes")
		if end < 0 {
			return remainder
		}
		return remainder[:end]
	}
	return ""
}
