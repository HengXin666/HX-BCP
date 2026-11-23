package residential

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

// declaredChannel provisions a sticky channel with N declared nodes so lease
// and guard tests operate on real persisted sessions.
func declaredChannel(t *testing.T, harness *testHarness, count int) (channelID, controlToken string) {
	t.Helper()
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "residential-lease",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		SessionCount:   count,
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	return channel.ID, strings.TrimPrefix(channel.ControlPath, "/ctl/")
}

func TestClaimGrantsExclusiveLeaseAndBlocksOtherHolders(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	channelID, controlToken := declaredChannel(t, harness, 1)

	first, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 300,
	})
	if err != nil {
		t.Fatalf("first claim error = %v", err)
	}
	if first.Lease == nil || first.Lease.LeaseID == "" || first.Lease.Holder != "service-a" ||
		first.Lease.ExpiresAt == nil {
		t.Fatalf("first claim did not grant a lease: %+v", first.Lease)
	}
	if first.Lease.ExpiresAt.Sub(time.Now().UTC()) < 250*time.Second {
		t.Fatalf("lease expiry too short: %v", first.Lease.ExpiresAt)
	}

	_, err = harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-b", TTLSeconds: 300,
	})
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second holder claim error = %v, want ErrLeaseHeld", err)
	}

	// Same holder may refresh and receives a fresh lease capability token.
	refreshed, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 120,
	})
	if err != nil {
		t.Fatalf("same holder refresh error = %v", err)
	}
	if refreshed.Lease == nil || refreshed.Lease.LeaseID == "" ||
		refreshed.Lease.LeaseID == first.Lease.LeaseID {
		t.Fatal("same holder refresh did not rotate the lease capability")
	}
	_ = channelID
}

func TestClaimAfterLeaseExpirySucceeds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	harness := newHarness(t, WithClock(func() time.Time { return now }))
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)

	first, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	second, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-b", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatalf("claim after expiry error = %v, want success", err)
	}
	if second.Lease == nil || second.Lease.Holder != "service-b" {
		t.Fatal("expired lease was not re-granted to the new holder")
	}
	_ = first
}

func TestClaimValidatesHolderAndTTL(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)

	if _, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "", TTLSeconds: 300,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty holder error = %v, want ErrInvalid", err)
	}
	if _, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "bad holder with spaces", TTLSeconds: 300,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid holder error = %v, want ErrInvalid", err)
	}
	if _, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 10,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short ttl error = %v, want ErrInvalid", err)
	}
	if _, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 100000,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long ttl error = %v, want ErrInvalid", err)
	}
}

func TestHeartbeatExtendsLeaseAndRejectsWrongID(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)

	claimed, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wrong lease id cannot extend another holder's lease.
	if _, err := harness.service.HeartbeatDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseHeartbeatRequest{
		LeaseID: "wrong-lease-id", TTLSeconds: 60,
	}); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("wrong lease heartbeat error = %v, want ErrLeaseHeld", err)
	}

	// Correct lease id extends the lease.
	before := claimed.Lease.ExpiresAt
	extended, err := harness.service.HeartbeatDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseHeartbeatRequest{
		LeaseID: claimed.Lease.LeaseID, TTLSeconds: 60,
	})
	if err != nil {
		t.Fatalf("heartbeat error = %v", err)
	}
	if extended.Lease == nil || extended.Lease.LeaseID != claimed.Lease.LeaseID {
		t.Fatal("heartbeat lost the lease identity")
	}
	if before == nil || extended.Lease.ExpiresAt == nil || !extended.Lease.ExpiresAt.After(*before) {
		t.Fatal("heartbeat did not extend the lease expiry")
	}
}

func TestHeartbeatOnUnleasedNodeFails(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)
	if _, err := harness.service.HeartbeatDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseHeartbeatRequest{
		LeaseID: "ghost-lease", TTLSeconds: 60,
	}); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("unleased heartbeat error = %v, want ErrLeaseExpired", err)
	}
}

func TestReleaseClearsLeaseAndIsIdempotent(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)

	claimed, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	released, err := harness.service.ReleaseDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseReleaseRequest{
		LeaseID: claimed.Lease.LeaseID,
	})
	if err != nil {
		t.Fatalf("release error = %v", err)
	}
	if released.Lease != nil {
		t.Fatal("release left the lease attached")
	}

	// Releasing again (or with a stale lease id) is an idempotent success and
	// never clears a newer lease.
	if _, err := harness.service.ReleaseDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseReleaseRequest{
		LeaseID: claimed.Lease.LeaseID,
	}); err != nil {
		t.Fatalf("second release error = %v, want idempotent success", err)
	}
}

func TestRotateRequiresLeaseWhenNodeIsHeld(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)

	claimed, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A non-holder cannot rotate a leased node, even without a body.
	if _, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{}); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("unguarded rotation of held node error = %v, want ErrLeaseHeld", err)
	}
	if _, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{
		LeaseID: "wrong-lease",
	}); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("wrong lease rotation error = %v, want ErrLeaseHeld", err)
	}

	// The holder rotates with its lease and the allocation version advances.
	channelRecord, err := harness.service.controlChannel(ctx, controlToken)
	if err != nil {
		t.Fatal(err)
	}
	before, err := harness.store.GetResidentialClientSession(ctx, channelRecord.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{
		LeaseID: claimed.Lease.LeaseID,
	}); err != nil {
		t.Fatalf("holder rotation error = %v", err)
	}
	after, err := harness.store.GetResidentialClientSession(ctx, before.ChannelID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if before.AllocVersion >= after.AllocVersion {
		t.Fatalf("allocation version did not advance: before=%d after=%d", before.AllocVersion, after.AllocVersion)
	}
	if before.NodeFingerprint == after.NodeFingerprint {
		t.Fatal("rotation did not change the residential allocation")
	}
}

func TestRotateRejectsStaleAllocVersion(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)

	list, err := harness.service.ControlNodesByToken(ctx, controlToken)
	if err != nil {
		t.Fatal(err)
	}
	staleVersion := list.Nodes[0].AllocVersion

	if _, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{
		ExpectedAllocVersion: &staleVersion,
	}); err != nil {
		t.Fatalf("fresh rotation error = %v", err)
	}
	if _, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{
		ExpectedAllocVersion: &staleVersion,
	}); !errors.Is(err, ErrAllocVersionChanged) {
		t.Fatalf("stale rotation error = %v, want ErrAllocVersionChanged", err)
	}
}

func TestRouteSwitchGuardedByLeaseAndVersion(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 1)

	claimed, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A non-holder cannot re-route a leased node.
	if _, err := harness.service.SwitchDeclaredSessionRouteByControlToken(ctx, controlToken, 1, "direct", RotateOptions{}); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("unguarded route error = %v, want ErrLeaseHeld", err)
	}
	// The holder can.
	switched, err := harness.service.SwitchDeclaredSessionRouteByControlToken(ctx, controlToken, 1, "direct", RotateOptions{
		LeaseID: claimed.Lease.LeaseID,
	})
	if err != nil {
		t.Fatalf("holder route switch error = %v", err)
	}
	if switched.RouteMode != "direct" {
		t.Fatalf("route mode = %q, want direct", switched.RouteMode)
	}

	// A stale version guard rejects a route change after the allocation moved.
	if _, err := harness.service.SwitchDeclaredSessionRouteByControlToken(ctx, controlToken, 1, "residential", RotateOptions{
		LeaseID: claimed.Lease.LeaseID,
	}); err != nil {
		t.Fatal(err)
	}
	versionAfter := 0
	if _, err := harness.service.SwitchDeclaredSessionRouteByControlToken(ctx, controlToken, 1, "residential", RotateOptions{
		LeaseID:              claimed.Lease.LeaseID,
		ExpectedAllocVersion: &versionAfter,
	}); !errors.Is(err, ErrAllocVersionChanged) {
		t.Fatalf("stale route error = %v, want ErrAllocVersionChanged", err)
	}
}

func TestControlNodeListExposesVersionAndLeaseWithoutCapability(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	_, controlToken := declaredChannel(t, harness, 2)

	list, err := harness.service.ControlNodesByToken(ctx, controlToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Nodes) != 2 {
		t.Fatalf("node count = %d, want 2", len(list.Nodes))
	}
	for _, node := range list.Nodes {
		if node.AllocVersion < 0 {
			t.Fatalf("node %d has negative alloc_version", node.Index)
		}
		if node.Lease != nil {
			t.Fatal("node pool list rendered a lease before any claim")
		}
	}

	claimed, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err = harness.service.ControlNodesByToken(ctx, controlToken)
	if err != nil {
		t.Fatal(err)
	}
	first := list.Nodes[0]
	if first.Lease == nil || first.Lease.Holder != "service-a" || first.Lease.LeaseID != "" {
		t.Fatalf("leased node list must show holder without the capability token: %+v", first.Lease)
	}
	_ = claimed
}

func TestIdleReleaseSkipsLeasedNodes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	harness := newHarness(t, WithClock(func() time.Time { return now }))
	ctx := context.Background()
	channelID, controlToken := declaredChannel(t, harness, 2)

	claimed, err := harness.service.ClaimDeclaredNodeByControlToken(ctx, controlToken, 1, LeaseRequest{
		Holder: "service-a", TTLSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Give the leased node a real allocation so both nodes start with one.
	if _, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{
		LeaseID: claimed.Lease.LeaseID,
	}); err != nil {
		t.Fatal(err)
	}
	// Allocate the unleased second node so it becomes a release candidate.
	if _, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 2, RotateOptions{}); err != nil {
		t.Fatal(err)
	}
	channelRecord, err := harness.store.GetResidentialChannel(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	channelRecord.IdleReleaseSeconds = 60
	// ReleaseIdleDeclaredSessions reads the persisted value, so update it.
	if _, err := harness.store.UpdateResidentialChannel(ctx, channelRecord, channelRecord.Version); err != nil {
		t.Fatal(err)
	}

	now = now.Add(120 * time.Second)
	released, err := harness.service.ReleaseIdleDeclaredSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if released != 1 {
		t.Fatalf("idle release count = %d, want 1 (only the unleased node)", released)
	}

	// The leased node keeps its allocation.
	session, err := harness.store.GetResidentialClientSession(ctx, channelID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if session.NodeFingerprint == "" {
		t.Fatal("leased node was released while its lease was active")
	}
	// The unleased node was released.
	other, err := harness.store.GetResidentialClientSession(ctx, channelID, "s02")
	if err != nil {
		t.Fatal(err)
	}
	if other.NodeFingerprint != "" {
		t.Fatal("unleased idle node kept its allocation")
	}
}
