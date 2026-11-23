package residential

import (
	"context"
	"testing"
)

// TestUpdateChannelDisableAlignsGroupAndListener verifies that disabling a
// channel through an edit also takes its managed proxy group and entry
// listener out of the data plane, and that re-enabling restores both.
func TestUpdateChannelDisableAlignsGroupAndListener(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "toggle-channel", ProviderID: provider.ID, Mode: ModeSticky,
		SessionCount: 1, PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := harness.service.UpdateChannel(ctx, channel.ID, UpdateChannelRequest{
		Version: channel.Version, Name: channel.Name, Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpdateChannel(disable) error = %v", err)
	}
	group, err := harness.store.GetProxyGroup(ctx, disabled.ProxyGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.Enabled {
		t.Fatal("proxy group must be disabled with the channel")
	}
	listenerRecord, err := harness.store.GetListener(ctx, disabled.ListenerID)
	if err != nil {
		t.Fatal(err)
	}
	if listenerRecord.Enabled {
		t.Fatal("managed listener must be disabled with the channel")
	}

	reEnabled, err := harness.service.UpdateChannel(ctx, disabled.ID, UpdateChannelRequest{
		Version: disabled.Version, Name: disabled.Name, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateChannel(enable) error = %v", err)
	}
	group, err = harness.store.GetProxyGroup(ctx, reEnabled.ProxyGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if !group.Enabled {
		t.Fatal("proxy group must be restored when the channel is re-enabled")
	}
	listenerRecord, err = harness.store.GetListener(ctx, reEnabled.ListenerID)
	if err != nil {
		t.Fatal(err)
	}
	if !listenerRecord.Enabled {
		t.Fatal("managed listener must be restored when the channel is re-enabled")
	}
}

// TestUpdateChannelResizesDeclaredSessions verifies that editing the node
// count grows and shrinks the declared session set while preserving surviving
// ordinals.
func TestUpdateChannelResizesDeclaredSessions(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "resize-channel", ProviderID: provider.ID, Mode: ModeSticky,
		SessionCount: 2, PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if channel.SessionCount != 2 || len(channel.Sessions) != 2 {
		t.Fatalf("created channel = %d sessions", channel.SessionCount)
	}
	firstFingerprint, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	grown, err := harness.service.UpdateChannel(ctx, channel.ID, UpdateChannelRequest{
		Version: channel.Version, Name: channel.Name, Enabled: true,
		SessionCount: intPtr(3),
	})
	if err != nil {
		t.Fatalf("UpdateChannel(grow) error = %v", err)
	}
	if grown.SessionCount != 3 {
		t.Fatalf("grown session count = %d", grown.SessionCount)
	}
	after, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if after.NodeFingerprint != firstFingerprint.NodeFingerprint {
		t.Fatal("surviving ordinal lost its allocation during grow")
	}
	if _, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s03"); err != nil {
		t.Fatalf("new ordinal s03 missing: %v", err)
	}

	shrunk, err := harness.service.UpdateChannel(ctx, grown.ID, UpdateChannelRequest{
		Version: grown.Version, Name: grown.Name, Enabled: true,
		SessionCount: intPtr(1),
	})
	if err != nil {
		t.Fatalf("UpdateChannel(shrink) error = %v", err)
	}
	if shrunk.SessionCount != 1 {
		t.Fatalf("shrunk session count = %d", shrunk.SessionCount)
	}
	if _, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s02"); err == nil {
		t.Fatal("shrunk ordinal s02 should have been released")
	}
	kept, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if kept.NodeFingerprint != firstFingerprint.NodeFingerprint {
		t.Fatal("surviving ordinal lost its allocation during shrink")
	}
}

func intPtr(value int) *int {
	return &value
}
