package residential

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRotateClientSessionAllocatesFreshNodeAndKeepsClientCredentials(t *testing.T) {
	t.Parallel()
	router := &recordingSessionRouter{}
	harness := newHarness(t, WithSessionRouter(router))
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "client-rotate", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	channelRecord, _ := harness.store.GetResidentialChannel(ctx, channel.ID)
	created, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := harness.store.GetResidentialClientSession(ctx, channel.ID, created.SessionID)
	rotated, err := harness.service.RotateClientSessionByToken(ctx, channelRecord.RotateToken, created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := harness.store.GetResidentialClientSession(ctx, channel.ID, created.SessionID)
	if before.NodeFingerprint == after.NodeFingerprint {
		t.Fatal("manual rotation reused the previous node")
	}
	if rotated.ProxyUsername != created.ProxyUsername || after.RotateCount != 1 {
		t.Fatalf("rotated session = %+v", rotated)
	}
	if len(router.closed) != 1 || router.closed[0] != created.ProxyUsername {
		t.Fatalf("closed users = %v", router.closed)
	}
}

func TestRotateClientSessionIsRateLimitedPerLogicalSession(t *testing.T) {
	t.Parallel()
	harness := newHarness(t, WithRotateInterval(time.Minute))
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "client-rate-limit", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	channelRecord, _ := harness.store.GetResidentialChannel(ctx, channel.ID)
	created, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.RotateClientSessionByToken(ctx, channelRecord.RotateToken, created.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.RotateClientSessionByToken(ctx, channelRecord.RotateToken, created.SessionID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second rotation error = %v, want ErrRateLimited", err)
	}
}

func TestRotateClientSessionRepairsMissingPoolSlot(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "repair-missing-slot", ProviderID: provider.ID, Mode: ModeSticky,
		SessionCount: 1, Preallocate: true, PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	channelRecord, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	oldFingerprint := previous.NodeFingerprint
	previous.NodeFingerprint = strings.Repeat("f", 64)
	if err := harness.store.RestoreResidentialClientSessionState(ctx, previous); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DeleteResidentialSessionNode(ctx, channel.ID, oldFingerprint); err != nil {
		t.Fatal(err)
	}

	rotated, err := harness.service.RotateClientSessionByToken(
		ctx,
		channelRecord.RotateToken,
		"s01",
	)
	if err != nil {
		t.Fatalf("RotateClientSessionByToken() error = %v", err)
	}
	current, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if current.NodeFingerprint == "" || current.NodeFingerprint == previous.NodeFingerprint {
		t.Fatalf("repaired session fingerprint = %q", current.NodeFingerprint)
	}
	if rotated.AllocatedAt == nil || current.RotateCount != previous.RotateCount+1 {
		t.Fatalf("rotated session = %+v, record = %+v", rotated, current)
	}
}

func TestRotateClientSessionKeepsNodeReferencedByAnotherSession(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "shared-pool-slot", ProviderID: provider.ID, Mode: ModeSticky,
		SessionCount: 2, Preallocate: true, PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	channelRecord, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	second, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s02")
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint := second.NodeFingerprint
	second.NodeFingerprint = first.NodeFingerprint
	if err := harness.store.RestoreResidentialClientSessionState(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DeleteResidentialSessionNode(ctx, channel.ID, secondFingerprint); err != nil {
		t.Fatal(err)
	}

	if _, err := harness.service.RotateClientSessionByToken(
		ctx,
		channelRecord.RotateToken,
		first.SessionID,
	); err != nil {
		t.Fatalf("RotateClientSessionByToken() error = %v", err)
	}
	nodes, err := harness.store.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundShared := false
	for _, node := range nodes {
		if node.Fingerprint == first.NodeFingerprint {
			foundShared = true
			break
		}
	}
	if !foundShared {
		t.Fatal("rotation deleted a pool node still referenced by s02")
	}
}
