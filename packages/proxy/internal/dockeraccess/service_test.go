package dockeraccess

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

type fakeRepo struct {
	entries   []store.DockerAccessEntryRecord
	createErr error
}

func (f *fakeRepo) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (f *fakeRepo) ListDockerAccessEntries(context.Context) ([]store.DockerAccessEntryRecord, error) {
	return append([]store.DockerAccessEntryRecord(nil), f.entries...), nil
}
func (f *fakeRepo) CreateDockerAccessEntry(_ context.Context, record store.DockerAccessEntryRecord) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.entries = append(f.entries, record)
	return nil
}
func (f *fakeRepo) DeleteDockerAccessEntryFor(_ context.Context, entryID string) error {
	kept := f.entries[:0]
	for _, entry := range f.entries {
		if entry.EntryListenerID != entryID {
			kept = append(kept, entry)
		}
	}
	f.entries = kept
	return nil
}

func (f *fakeRepo) DeleteDockerAccessEntry(_ context.Context, id string) error {
	kept := f.entries[:0]
	for _, entry := range f.entries {
		if entry.ListenerID != id {
			kept = append(kept, entry)
		}
	}
	f.entries = kept
	return nil
}

type fakeListenerService struct {
	items   map[string]listener.Listener
	created []listener.CreateRequest
	deleted []string
}

func (f *fakeListenerService) List(context.Context) ([]listener.Listener, error) {
	out := make([]listener.Listener, 0, len(f.items))
	for _, item := range f.items {
		out = append(out, item)
	}
	return out, nil
}
func (f *fakeListenerService) Get(_ context.Context, id string) (listener.Listener, error) {
	item, ok := f.items[id]
	if !ok {
		return listener.Listener{}, listener.ErrNotFound
	}
	return item, nil
}
func (f *fakeListenerService) Create(_ context.Context, request listener.CreateRequest) (listener.Listener, error) {
	f.created = append(f.created, request)
	item := listener.Listener{
		ID: "companion", Name: request.Name, Kind: request.Kind, BindAddress: request.BindAddress, Port: request.Port,
		ProxyGroupID: request.ProxyGroupID, NodeID: request.NodeID, AuthConfigured: request.Auth != nil,
		SharePath: "/sub/companion", SharedInbound: request.SharedInbound, Enabled: true, Version: 1,
	}
	f.items[item.ID] = item
	return item, nil
}
func (f *fakeListenerService) Delete(_ context.Context, id string, _ int) error {
	f.deleted = append(f.deleted, id)
	delete(f.items, id)
	return nil
}

type fakeSettingsReader struct{ settings systemsettings.Settings }

func (f fakeSettingsReader) Get(context.Context) (systemsettings.Settings, error) {
	return f.settings, nil
}

type fakeConverger struct{ calls int }

func (f *fakeConverger) ConvergeSharedInbound(context.Context) error { f.calls++; return nil }

func newTestService(t *testing.T, mode systemsettings.SharedInboundMode, bind string, extra ...Option) (*Service, *fakeRepo, *fakeListenerService, *fakeConverger) {
	t.Helper()
	source := listener.Listener{ID: "svc", Name: "svc", Kind: "vless", BindAddress: "127.0.0.1", Port: 7891, AuthConfigured: true, SharePath: "/sub/svc", Enabled: true, Version: 2, ProxyGroupID: "group", NodeID: "node-1"}
	listeners := &fakeListenerService{items: map[string]listener.Listener{"svc": source}}
	settings := systemsettings.Default()
	settings.SharedInbound.Mode = mode
	settings.SharedInbound.MixedBindAddress = bind
	settings.SharedInbound.MixedPort = 7890
	settings.SharedInbound.WSPort = 7891
	repo := &fakeRepo{}
	converger := &fakeConverger{}
	options := append([]Option{
		WithBridges(func() ([]Bridge, error) { return []Bridge{docker0}, nil }),
		WithLAN(func() []string { return nil }),
		WithPortProbe(func(host string, port int) bool { return port != 17890 }),
	}, extra...)
	service, err := NewService(repo, listeners, fakeSettingsReader{settings}, converger, options...)
	if err != nil {
		t.Fatal(err)
	}
	return service, repo, listeners, converger
}

func TestApplyBridgeCompanionCarriesEgressAndIsRevocable(t *testing.T) {
	ctx := context.Background()
	service, repo, listeners, converger := newTestService(t, systemsettings.SharedInboundPerService, "0.0.0.0")
	preview, err := service.Assess(ctx, "svc", "")
	if err != nil {
		t.Fatal(err)
	}
	if preview.State != StateNeedsEntry || preview.Plan.Placement != PlacementBridge || preview.Plan.Port != 17891 {
		t.Fatalf("preview = %+v plan=%+v", preview, preview.Plan)
	}
	if _, err := service.Apply(ctx, "svc", ApplyRequest{PlanID: preview.Plan.ID}); !errors.Is(err, listener.ErrInvalid) {
		t.Fatalf("apply without confirm err = %v", err)
	}
	if _, err := service.Apply(ctx, "svc", ApplyRequest{PlanID: "stale", Confirm: true}); !errors.Is(err, listener.ErrConflict) {
		t.Fatalf("apply with stale plan err = %v", err)
	}
	result, err := service.Apply(ctx, "svc", ApplyRequest{PlanID: preview.Plan.ID, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateReady || !result.Entry.Companion || result.Entry.Host != "172.17.0.1" || result.Entry.Port != 17891 {
		t.Fatalf("result = %+v entry=%+v", result, result.Entry)
	}
	created := listeners.created[0]
	if created.BindAddress != "172.17.0.1" || created.NodeID != "node-1" || created.ProxyGroupID != "group" || created.SharedInbound != "" {
		t.Fatalf("created = %+v", created)
	}
	if created.Auth == nil || !strings.HasPrefix(created.Auth.Username, "docker-") || len(created.Auth.Password) < 20 {
		t.Fatalf("credentials = %+v", created.Auth)
	}
	if converger.calls != 0 || len(repo.entries) != 1 || repo.entries[0].BindTarget != "172.17.0.1" {
		t.Fatalf("converger=%d entries=%+v", converger.calls, repo.entries)
	}
	if err := service.Revoke(ctx, "svc"); err != nil {
		t.Fatal(err)
	}
	if len(listeners.deleted) != 1 || listeners.deleted[0] != "companion" {
		t.Fatalf("deleted = %v", listeners.deleted)
	}
	if links, _ := service.Links(ctx); len(links) != 0 {
		t.Fatalf("first revoke must clear the link, got %+v", links)
	}
	if err := service.Revoke(ctx, "svc"); err != nil {
		t.Fatalf("second revoke must be a no-op, got %v", err)
	}
}

func TestApplySharedCompanionJoinsCarrierWithoutNewPort(t *testing.T) {
	ctx := context.Background()
	service, repo, listeners, converger := newTestService(t, systemsettings.SharedInboundShared, "0.0.0.0")
	preview, err := service.Assess(ctx, "svc", "")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Plan.Placement != PlacementShared || preview.Plan.NewPort {
		t.Fatalf("plan = %+v", preview.Plan)
	}
	if _, err := service.Apply(ctx, "svc", ApplyRequest{PlanID: preview.Plan.ID, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if listeners.created[0].SharedInbound != listener.SharedInboundStandardOwner || converger.calls != 1 || repo.entries[0].BindTarget != PlacementShared {
		t.Fatalf("created=%+v converger=%d entries=%+v", listeners.created[0], converger.calls, repo.entries)
	}
}

func TestApplyRollsBackCompanionWhenLinkFails(t *testing.T) {
	ctx := context.Background()
	service, repo, listeners, _ := newTestService(t, systemsettings.SharedInboundPerService, "0.0.0.0")
	repo.createErr = store.ErrConflict
	preview, err := service.Assess(ctx, "svc", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, "svc", ApplyRequest{PlanID: preview.Plan.ID, Confirm: true}); !errors.Is(err, listener.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if _, exists := listeners.items["companion"]; exists {
		t.Fatal("companion listener must be removed when the link cannot be stored")
	}
}

// TestAssessReportsUnreadableLinkTableInsteadOfMissingBridge covers the
// 2026-10-17 report "明明就是有，凭什么说没有": the host had ten Docker bridges
// and the dialog still said none existed. The link table could not be read at
// all (sandbox without AF_NETLINK), and the assessment must say that instead
// of claiming the host has no bridge.
func TestAssessReportsUnreadableLinkTableInsteadOfMissingBridge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _, _ := newTestService(t, systemsettings.SharedInboundPerService, "0.0.0.0",
		WithBridges(func() ([]Bridge, error) {
			return nil, errors.New("route ip+net: netlinkrib: address family not supported by protocol")
		}),
	)
	got, err := service.Assess(ctx, "svc", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateBlocked || got.Reason != ReasonBridgeScanFailed {
		t.Fatalf("assessment = %+v", got)
	}
	if len(got.Bridges) != 0 || got.Plan != nil {
		t.Fatalf("bridges=%+v plan=%+v", got.Bridges, got.Plan)
	}
}
