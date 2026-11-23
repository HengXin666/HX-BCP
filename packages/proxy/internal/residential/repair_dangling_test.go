package residential

import (
	"context"
	"strings"
	"testing"
)

// TestCreateChannelRepairsDanglingSessionSlots verifies that a stale session
// reference from an existing channel is cleared before a new channel publishes
// its configuration, so the stale reference cannot fail the whole apply.
func TestCreateChannelRepairsDanglingSessionSlots(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)

	first, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "broken-channel", ProviderID: provider.ID, Mode: ModeSticky,
		SessionCount: 1, Preallocate: true, PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	brokenRecord, err := harness.store.GetResidentialChannel(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := harness.store.GetResidentialClientSession(ctx, first.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if previous.NodeFingerprint == "" {
		t.Fatal("eager declared session should hold an allocation")
	}
	realFingerprint := previous.NodeFingerprint
	previous.NodeFingerprint = strings.Repeat("f", 64)
	if err := harness.store.RestoreResidentialClientSessionState(ctx, previous); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DeleteResidentialSessionNode(ctx, first.ID, realFingerprint); err != nil {
		t.Fatal(err)
	}

	// Creating a second channel must succeed even though the first channel
	// still holds a dangling reference at the moment the create starts.
	second, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "fresh-channel", ProviderID: provider.ID, Mode: ModeSticky,
		SessionCount: 1, Preallocate: true, PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel with a dangling session elsewhere: %v", err)
	}
	repaired, err := harness.store.GetResidentialClientSession(ctx, first.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if repaired.NodeFingerprint != "" {
		t.Fatalf("dangling session fingerprint = %q, want cleared", repaired.NodeFingerprint)
	}
	if brokenRecord.Mode != ModeSticky || second.ID == first.ID {
		t.Fatalf("unexpected second channel %q", second.ID)
	}
}

// TestUpdateChannelRepairsDanglingSessionSlots verifies the same repair runs
// before a channel edit publishes.
func TestUpdateChannelRepairsDanglingSessionSlots(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "broken-channel", ProviderID: provider.ID, Mode: ModeSticky,
		SessionCount: 1, Preallocate: true, PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	realFingerprint := previous.NodeFingerprint
	previous.NodeFingerprint = strings.Repeat("e", 64)
	if err := harness.store.RestoreResidentialClientSessionState(ctx, previous); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.DeleteResidentialSessionNode(ctx, channel.ID, realFingerprint); err != nil {
		t.Fatal(err)
	}

	region := "de"
	updated, err := harness.service.UpdateChannel(ctx, channel.ID, UpdateChannelRequest{
		Version: channel.Version,
		Name:    channel.Name,
		Region:  region,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateChannel with a dangling session: %v", err)
	}
	if updated.Region != region {
		t.Fatalf("updated region = %q, want %q", updated.Region, region)
	}
	repaired, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if repaired.NodeFingerprint != "" {
		t.Fatalf("dangling session fingerprint = %q, want cleared", repaired.NodeFingerprint)
	}
}
