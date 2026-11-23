package sharedinbound

import (
	"context"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type fakeListeners struct {
	items   []listener.Listener
	updates []listener.UpdateRequest
	applies int
}

func (f *fakeListeners) List(context.Context) ([]listener.Listener, error) { return f.items, nil }

func (f *fakeListeners) Update(_ context.Context, id string, request listener.UpdateRequest) (listener.Listener, error) {
	f.updates = append(f.updates, request)
	for index := range f.items {
		if f.items[index].ID == id {
			updated := f.items[index]
			updated.BindAddress = request.BindAddress
			updated.Port = request.Port
			updated.SharedInbound = request.SharedInbound
			f.items[index] = updated
			return updated, nil
		}
	}
	return listener.Listener{}, nil
}

func (f *fakeListeners) EnsureSharedInbounds(context.Context, listener.SharedInboundSpec, []store.ProxyGroupRecord, []listener.SharedInboundMember) ([]listener.Listener, error) {
	f.applies++
	return nil, nil
}

type fakeRepository struct {
	channels []store.ResidentialChannelRecord
	entries  []store.DockerAccessEntryRecord
}

func (f fakeRepository) ListDockerAccessEntries(context.Context) ([]store.DockerAccessEntryRecord, error) {
	return f.entries, nil
}

func (f fakeRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return f.channels, nil
}

type fakeSettings struct {
	spec    listener.SharedInboundSpec
	enabled bool
}

func (f fakeSettings) SharedInboundSpec(context.Context) (listener.SharedInboundSpec, bool, error) {
	return f.spec, f.enabled, nil
}

func sharedSpec() listener.SharedInboundSpec {
	return listener.SharedInboundSpec{
		Enabled:          true,
		IncludeWebSocket: true,
		MixedBindAddress: "0.0.0.0",
		MixedPort:        7890,
		WSPort:           7891,
	}
}

func TestReconcileMovesExistingServicesOntoSharedInbound(t *testing.T) {
	listeners := &fakeListeners{items: []listener.Listener{
		{ID: "listener-1", Name: "service-a", Kind: "mixed", BindAddress: "0.0.0.0", Port: 18080, AuthConfigured: true, Version: 1, Enabled: true},
		{ID: "listener-2", Name: "service-b", Kind: "socks", BindAddress: "127.0.0.1", Port: 18081, AuthConfigured: true, Version: 1, Enabled: true},
		{ID: "listener-3", Name: "service-vless", Kind: "vless", BindAddress: "127.0.0.1", Port: 18082, AuthConfigured: true, Version: 1, Enabled: true},
	}}
	service, err := NewService(fakeRepository{}, listeners, fakeSettings{spec: sharedSpec(), enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Migrated != 3 {
		t.Fatalf("migrated = %d, want 3", result.Migrated)
	}
	if listeners.applies != 1 {
		t.Fatalf("aggregate publishes = %d, want 1", listeners.applies)
	}
	for _, update := range listeners.updates {
		switch update.Kind {
		case "mixed", "socks":
			if update.SharedInbound != listener.SharedInboundStandardOwner {
				t.Fatalf("%s owner = %q", update.Kind, update.SharedInbound)
			}
			if update.BindAddress != "0.0.0.0" || update.Port != 7890 {
				t.Fatalf("%s endpoint = %s:%d, want 0.0.0.0:7890", update.Kind, update.BindAddress, update.Port)
			}
		case "vless":
			if update.SharedInbound != listener.SharedInboundWebSocketOwner {
				t.Fatalf("vless owner = %q", update.SharedInbound)
			}
			if update.BindAddress != "127.0.0.1" || update.Port != 7891 {
				t.Fatalf("vless endpoint = %s:%d, want 127.0.0.1:7891", update.BindAddress, update.Port)
			}
			if update.Transport.WSPath != listener.SharedInboundRoutePath() {
				t.Fatalf("vless ws path = %q, want %q", update.Transport.WSPath, listener.SharedInboundRoutePath())
			}
		}
	}
	// Idempotent: a second run must not touch anything.
	before := len(listeners.updates)
	if _, err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(listeners.updates) != before {
		t.Fatalf("second reconcile changed %d listeners", len(listeners.updates)-before)
	}
}

func TestReconcileLeavesServicesWithoutCredentialsOnTheirPort(t *testing.T) {
	listeners := &fakeListeners{items: []listener.Listener{
		{ID: "listener-1", Name: "anonymous", Kind: "mixed", BindAddress: "0.0.0.0", Port: 18080, AuthConfigured: false, Version: 1, Enabled: true},
	}}
	service, err := NewService(fakeRepository{}, listeners, fakeSettings{spec: sharedSpec(), enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Migrated != 0 || len(result.SkippedWithoutCredentials) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(listeners.updates) != 0 {
		t.Fatalf("an unauthenticated service was migrated: %+v", listeners.updates)
	}
}

func TestReconcileSkipsResidentialManagedListeners(t *testing.T) {
	listeners := &fakeListeners{items: []listener.Listener{
		{ID: "channel-listener", Name: "住宅渠道 入口", Kind: "vless", BindAddress: "127.0.0.1", Port: 19001, AuthConfigured: true, Version: 1, Enabled: true},
	}}
	repository := fakeRepository{channels: []store.ResidentialChannelRecord{{ID: "channel-1", ListenerID: "channel-listener"}}}
	service, err := NewService(repository, listeners, fakeSettings{spec: sharedSpec(), enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Migrated != 0 || len(listeners.updates) != 0 {
		t.Fatalf("a residential listener was migrated: %+v / %+v", result, listeners.updates)
	}
}

func TestReconcileIsANoOpWhenSharedInboundIsDisabled(t *testing.T) {
	listeners := &fakeListeners{items: []listener.Listener{
		{ID: "listener-1", Name: "service-a", Kind: "mixed", BindAddress: "0.0.0.0", Port: 18080, AuthConfigured: true, Version: 1, Enabled: true},
	}}
	service, err := NewService(fakeRepository{}, listeners, fakeSettings{spec: sharedSpec(), enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Migrated != 0 || len(listeners.updates) != 0 {
		t.Fatalf("per-service mode was modified: %+v", result)
	}
}

func TestReconcileLeavesDockerAccessEntriesOnTheirBridge(t *testing.T) {
	listeners := &fakeListeners{items: []listener.Listener{
		{ID: "companion", Name: "svc · Docker", Kind: "mixed", BindAddress: "172.17.0.1", Port: 17890, AuthConfigured: true, Version: 1, Enabled: true},
	}}
	repository := fakeRepository{entries: []store.DockerAccessEntryRecord{{ListenerID: "svc", EntryListenerID: "companion"}}}
	service, err := NewService(repository, listeners, fakeSettings{spec: sharedSpec(), enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Migrated != 0 || len(listeners.updates) != 0 {
		t.Fatalf("docker entry was migrated: %+v", listeners.updates)
	}
}
