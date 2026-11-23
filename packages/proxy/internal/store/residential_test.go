package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	storage, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { storage.Close() })
	return storage
}

func createTestProvider(t *testing.T, storage *Store, id, name string) ResidentialProviderRecord {
	t.Helper()
	now := time.Now().UTC()
	record, err := storage.CreateResidentialProvider(context.Background(), ResidentialProviderRecord{
		ID:                   id,
		Name:                 name,
		Vendor:               "bestproxy",
		Protocol:             "http",
		GatewayHost:          "gate.example.com",
		GatewayPort:          8000,
		CredentialsEncrypted: []byte{0x01, 0x02},
		UsernameTemplate:     "{user}-session-{session}",
		RotationMode:         "session-template",
		SessionTTLSeconds:    600,
		PoolSize:             4,
		Enabled:              true,
		Version:              1,
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	if err != nil {
		t.Fatalf("CreateResidentialProvider() error = %v", err)
	}
	return record
}

func TestResidentialProviderLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := openTestStore(t)

	created := createTestProvider(t, storage, "provider-1", "bestproxy-main")
	fetched, err := storage.GetResidentialProvider(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetResidentialProvider() error = %v", err)
	}
	if fetched.Name != "bestproxy-main" || fetched.GatewayPort != 8000 {
		t.Fatalf("unexpected provider %+v", fetched)
	}
	if string(fetched.CredentialsEncrypted) != string([]byte{0x01, 0x02}) {
		t.Fatalf("credentials were not persisted verbatim")
	}

	// A duplicate name must conflict rather than silently create a second row.
	duplicate := created
	duplicate.ID = "provider-2"
	if _, err := storage.CreateResidentialProvider(ctx, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name error = %v, want ErrConflict", err)
	}

	fetched.PoolSize = 8
	fetched.UpdatedAt = time.Now().UTC()
	updated, err := storage.UpdateResidentialProvider(ctx, fetched, fetched.Version)
	if err != nil {
		t.Fatalf("UpdateResidentialProvider() error = %v", err)
	}
	if updated.PoolSize != 8 || updated.Version != fetched.Version+1 {
		t.Fatalf("unexpected update result %+v", updated)
	}
	// A stale version must be rejected.
	if _, err := storage.UpdateResidentialProvider(ctx, fetched, fetched.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}

	if err := storage.DeleteResidentialProvider(ctx, updated.ID, updated.Version); err != nil {
		t.Fatalf("DeleteResidentialProvider() error = %v", err)
	}
	if _, err := storage.GetResidentialProvider(ctx, updated.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete error = %v, want ErrNotFound", err)
	}
}

func TestResidentialChannelRotationState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := openTestStore(t)
	now := time.Now().UTC()

	provider := createTestProvider(t, storage, "provider-1", "bestproxy-main")
	group, err := storage.CreateProxyGroup(ctx, ProxyGroupRecord{
		ID:               "group-1",
		Name:             "residential-sticky",
		Strategy:         "manual",
		SourceSpecJSON:   `{"node_ids":[],"include_direct":false}`,
		RulePipelineJSON: "{}",
		Enabled:          true,
		EmptyBehavior:    "fail-closed",
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatalf("CreateProxyGroup() error = %v", err)
	}
	listenerRecord, err := storage.CreateListener(ctx, ListenerRecord{
		ID:                  "listener-1",
		Name:                "residential-entry",
		Kind:                "mixed",
		BindAddress:         "127.0.0.1",
		Port:                29080,
		ProxyGroupID:        group.ID,
		AuthMode:            "none",
		TransportJSON:       "{}",
		PublicEndpointJSON:  "{}",
		ShareToken:          "share-token-1",
		AuthConfigEncrypted: nil,
		Enabled:             true,
		Version:             1,
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CreateListener() error = %v", err)
	}

	channel, err := storage.CreateResidentialChannel(ctx, ResidentialChannelRecord{
		ID:           "channel-1",
		Name:         "sticky-us",
		ProviderID:   provider.ID,
		Mode:         "sticky",
		ProxyGroupID: group.ID,
		ListenerID:   listenerRecord.ID,
		Region:       "us",
		RotateToken:  "rotate-token-1",
		PoolCreatedAt: func() *time.Time {
			value := now.Add(-time.Minute)
			return &value
		}(),
		Enabled:   true,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateResidentialChannel() error = %v", err)
	}
	if channel.LastRotatedAt != nil {
		t.Fatalf("a new channel must not carry a rotation timestamp")
	}
	if channel.PoolCreatedAt == nil {
		t.Fatal("pool creation timestamp was not persisted")
	}
	refreshedAt := now.Add(time.Minute)
	if err := storage.SetResidentialChannelPoolCreatedAt(ctx, channel.ID, refreshedAt); err != nil {
		t.Fatalf("SetResidentialChannelPoolCreatedAt() error = %v", err)
	}
	updatedPoolState, err := storage.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatalf("GetResidentialChannel(after pool timestamp) error = %v", err)
	}
	if updatedPoolState.PoolCreatedAt == nil || !updatedPoolState.PoolCreatedAt.Equal(refreshedAt) {
		t.Fatalf("pool creation timestamp = %v, want %v", updatedPoolState.PoolCreatedAt, refreshedAt)
	}

	byToken, err := storage.GetResidentialChannelByRotateToken(ctx, "rotate-token-1")
	if err != nil {
		t.Fatalf("GetResidentialChannelByRotateToken() error = %v", err)
	}
	if byToken.ID != channel.ID {
		t.Fatalf("token lookup returned %q, want %q", byToken.ID, channel.ID)
	}
	if _, err := storage.GetResidentialChannelByRotateToken(ctx, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token error = %v, want ErrNotFound", err)
	}

	// Rotation is runtime state: it records progress without consuming the
	// optimistic-concurrency version used by configuration edits.
	rotated, err := storage.SetResidentialChannelRotation(ctx, channel.ID, 3, "203.0.113.7", time.Now().UTC())
	if err != nil {
		t.Fatalf("SetResidentialChannelRotation() error = %v", err)
	}
	if rotated.ActiveSessionIndex != 3 || rotated.LastExitIP != "203.0.113.7" {
		t.Fatalf("unexpected rotation state %+v", rotated)
	}
	if rotated.RotateCount != 1 || rotated.LastRotatedAt == nil {
		t.Fatalf("rotation counters were not updated: %+v", rotated)
	}
	if rotated.Version != channel.Version {
		t.Fatalf("rotation changed the config version to %d", rotated.Version)
	}

	retokened, err := storage.RotateResidentialChannelToken(ctx, channel.ID, "rotate-token-2")
	if err != nil {
		t.Fatalf("RotateResidentialChannelToken() error = %v", err)
	}
	if retokened.RotateToken != "rotate-token-2" {
		t.Fatalf("token was not replaced: %+v", retokened)
	}
	if _, err := storage.GetResidentialChannelByRotateToken(ctx, "rotate-token-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token still resolves, error = %v", err)
	}

	// A provider that still has channels must not be deletable.
	if err := storage.DeleteResidentialProvider(ctx, provider.ID, provider.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete referenced provider error = %v, want ErrConflict", err)
	}
}

func TestResidentialSessionPoolReplaceAndCandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := openTestStore(t)
	now := time.Now().UTC()

	sessions := []ResidentialSessionNode{
		{ID: "node-r1", Fingerprint: "fp-1", DisplayName: "us-session-1", Protocol: "http", CanonicalConfigEncrypted: []byte{0x11}},
		{ID: "node-r2", Fingerprint: "fp-2", DisplayName: "us-session-2", Protocol: "http", CanonicalConfigEncrypted: []byte{0x12}},
	}
	ids, err := storage.ReplaceResidentialSessionPool(ctx, "channel-1", sessions, now)
	if err != nil {
		t.Fatalf("ReplaceResidentialSessionPool() error = %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("pool returned %d ids, want 2", len(ids))
	}

	pooled, err := storage.ListResidentialSessionNodes(ctx, "channel-1")
	if err != nil {
		t.Fatalf("ListResidentialSessionNodes() error = %v", err)
	}
	if len(pooled) != 2 {
		t.Fatalf("pool contains %d nodes, want 2", len(pooled))
	}

	// Residential nodes have no active snapshot, so they must still surface as
	// group candidates and must not carry a subscription id.
	candidates, err := storage.ListGroupNodeCandidates(ctx)
	if err != nil {
		t.Fatalf("ListGroupNodeCandidates() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}
	for _, candidate := range candidates {
		if len(candidate.SubscriptionIDs) != 0 {
			t.Fatalf("residential candidate %q reported subscriptions %v", candidate.ID, candidate.SubscriptionIDs)
		}
	}

	// Replacing the pool retires sessions that are no longer present and keeps
	// the identity of sessions whose fingerprint is unchanged.
	refreshed := []ResidentialSessionNode{
		{ID: "node-r1", Fingerprint: "fp-1", DisplayName: "us-session-1", Protocol: "http", CanonicalConfigEncrypted: []byte{0x11}},
		{ID: "node-r3", Fingerprint: "fp-3", DisplayName: "us-session-3", Protocol: "http", CanonicalConfigEncrypted: []byte{0x13}},
	}
	if _, err := storage.ReplaceResidentialSessionPool(ctx, "channel-1", refreshed, now); err != nil {
		t.Fatalf("ReplaceResidentialSessionPool(refresh) error = %v", err)
	}
	pooled, err = storage.ListResidentialSessionNodes(ctx, "channel-1")
	if err != nil {
		t.Fatalf("ListResidentialSessionNodes(after refresh) error = %v", err)
	}
	if len(pooled) != 2 {
		t.Fatalf("refreshed pool contains %d nodes, want 2", len(pooled))
	}
	names := map[string]bool{}
	for _, node := range pooled {
		names[node.DisplayName] = true
	}
	if !names["us-session-1"] || !names["us-session-3"] || names["us-session-2"] {
		t.Fatalf("unexpected refreshed pool contents: %v", names)
	}

	if err := storage.DeleteResidentialSessionPool(ctx, "channel-1"); err != nil {
		t.Fatalf("DeleteResidentialSessionPool() error = %v", err)
	}
	pooled, err = storage.ListResidentialSessionNodes(ctx, "channel-1")
	if err != nil {
		t.Fatalf("ListResidentialSessionNodes(after delete) error = %v", err)
	}
	if len(pooled) != 0 {
		t.Fatalf("pool still contains %d nodes after delete", len(pooled))
	}
}

// TestSubscriptionRefreshNeverRetiresResidentialNodes is the regression test
// for the 2026-10-15 gh-pool incident.
//
// A residential node is materialized by the residential channel allocation
// path and is never linked into subscription_nodes. Before the origin guard,
// every subscription refresh ran a global retire sweep that retired it: the
// channel's proxy group was republished with zero members, Mihomo compiled the
// group as REJECT, and every consumer of that group failed with
// "组 <group> 内无可选节点" until the next rotation allocated a fresh node.
//
// The test drives the real CommitParsedSubscriptionSnapshot path and asserts
// the residential node survives while a genuinely orphaned subscription node
// is still retired.
func TestSubscriptionRefreshNeverRetiresResidentialNodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := openTestStore(t)
	now := time.Now().UTC()

	if _, err := storage.db.ExecContext(ctx, `
INSERT INTO nodes(
    id, fingerprint, display_name, protocol, canonical_config_encrypted,
    lifecycle_state, first_seen_at, last_seen_at, retired_at, version, origin, origin_ref
) VALUES ('node-residential-live', 'fp-resi-live', 'linkup session s01', 'http', X'00',
          'healthy', ?, ?, NULL, 1, 'residential', 'channel-1')
`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed residential node: %v", err)
	}
	// An orphaned subscription node that the sweep is SUPPOSED to retire: it is
	// not present in any active snapshot.
	if _, err := storage.db.ExecContext(ctx, `
INSERT INTO nodes(
    id, fingerprint, display_name, protocol, canonical_config_encrypted,
    lifecycle_state, first_seen_at, last_seen_at, retired_at, version, origin
) VALUES ('node-sub-orphan', 'fp-sub-orphan', 'orphan', 'http', X'00',
          'healthy', ?, ?, NULL, 1, 'subscription')
`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed orphan node: %v", err)
	}

	if _, err := storage.db.ExecContext(ctx, SUBSCRIPTION_SEED_SQL, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	record := SubscriptionSnapshotRecord{
		ID:             "snap-refresh-1",
		SubscriptionID: "sub-1",
		ContentHash:    "hash-1",
		FetchedAt:      now,
		NodeCount:      1,
		CreatedAt:      now,
	}
	imports := []NodeImportRecord{{
		ID:                       "node-sub-fresh",
		Fingerprint:              "fp-sub-fresh",
		DisplayName:              "fresh",
		Protocol:                 "http",
		CanonicalConfigEncrypted: []byte{0x00},
		SourceName:               "src",
	}}
	if err := storage.CommitParsedSubscriptionSnapshot(ctx, record, imports); err != nil {
		t.Fatalf("CommitParsedSubscriptionSnapshot() error = %v", err)
	}

	residential, err := storage.GetNode(ctx, "node-residential-live")
	if err != nil {
		t.Fatalf("GetNode(residential) error = %v", err)
	}
	if residential.LifecycleState == "retired" {
		t.Fatalf("subscription refresh retired the residential node: %+v", residential)
	}
	orphan, err := storage.GetNode(ctx, "node-sub-orphan")
	if err != nil {
		t.Fatalf("GetNode(orphan) error = %v", err)
	}
	if orphan.LifecycleState != "retired" {
		t.Fatalf("orphaned subscription node lifecycle = %q, want retired", orphan.LifecycleState)
	}
}

const SUBSCRIPTION_SEED_SQL = "INSERT INTO subscriptions(id, name, source_type, source_class," +
	" source_config_encrypted, enabled, refresh_interval_seconds, refresh_cron," +
	" consecutive_failures, version, created_at, updated_at)" +
	" VALUES ('sub-1', 'sub one', 'remote', 'subscription', X'00', 1, 3600, '', 0, 1, ?, ?)"
