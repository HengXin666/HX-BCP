package proxyservice

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// Tests for reusing an existing proxy group as a service's egress anchor. See
// .agents/notes/implemented/feature/2026-10-14-reuse-existing-group-for-an-entry.md.

// reuseRequest is the smallest reuse payload: a listener pointed at an existing
// group and nothing else. The credentials are required because the default
// shared inbound selects its members by username.
func reuseRequest(groupID string) CreateRequest {
	return CreateRequest{Listener: ListenerCreateRequest{
		Name:         "entry",
		Kind:         "mixed",
		Port:         7890,
		Auth:         &listener.Auth{Username: "hx-user", Password: "hx-pass"},
		ProxyGroupID: groupID,
	}}
}

// TestCreateReusesAnExistingGroupWithoutCreatingOne is the residential entry
// point: listener.proxy_group_id names the group a channel already built, so
// the service must not build a second one for the same exits. Before the
// change the create surface offered no way to say "use that group", so the
// operator's only options were a duplicate group or a channel they did not want.
func TestCreateReusesAnExistingGroupWithoutCreatingOne(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{
		ID: "group-residential", Version: 3, Name: "residential-linkup", Enabled: true,
	}}
	listeners := &fakeListeners{}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), reuseRequest("group-residential"))
	if err != nil {
		t.Fatal(err)
	}
	if groups.createCalls != 0 {
		t.Fatalf("group creates = %d, want 0 (the request named an existing group)", groups.createCalls)
	}
	if created.Group.ID != "group-residential" {
		t.Fatalf("returned group = %q, want the reused %q", created.Group.ID, "group-residential")
	}
	if len(listeners.creates) != 1 || listeners.creates[0].ProxyGroupID != "group-residential" {
		t.Fatalf("listener creates = %+v, want one bound to group-residential", listeners.creates)
	}
}

// TestCreateRejectsUnknownReuseGroup keeps a stale id from becoming an internal
// error: the caller named a group that is not there, which is its own mistake.
func TestCreateRejectsUnknownReuseGroup(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-other", Version: 1, Enabled: true}}
	listeners := &fakeListeners{}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), reuseRequest("group-missing"))
	if !errors.Is(err, proxygroup.ErrInvalid) {
		t.Fatalf("Create() error = %v, want %v", err, proxygroup.ErrInvalid)
	}
	if errors.Is(err, ErrCreateFailed) {
		t.Fatalf("Create() error = %v, must not be reported as a generic create failure", err)
	}
	if groups.createCalls != 0 || len(listeners.creates) != 0 || groups.deleteCalls != 0 {
		t.Fatalf("a rejected reuse wrote something: creates=%d listener_creates=%d deletes=%d",
			groups.createCalls, len(listeners.creates), groups.deleteCalls)
	}
}

// TestCreateRejectsDisabledReuseGroup pins the boundary the operator cannot see
// from the form: a disabled channel's group is disabled too, so its exits are
// not in the data plane and an entry point onto it would route nowhere.
func TestCreateRejectsDisabledReuseGroup(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{
		ID: "group-residential", Version: 2, Name: "residential-linkup", Enabled: false,
	}}
	listeners := &fakeListeners{}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), reuseRequest("group-residential"))
	if !errors.Is(err, proxygroup.ErrInvalid) {
		t.Fatalf("Create() error = %v, want %v", err, proxygroup.ErrInvalid)
	}
	if groups.createCalls != 0 || len(listeners.creates) != 0 {
		t.Fatalf("a rejected reuse wrote something: creates=%d listener_creates=%d",
			groups.createCalls, len(listeners.creates))
	}
}

// TestCreateRejectsReuseThatAlsoDescribesAGroup rests on the rule that hiding a
// field and not sending it are the same thing. Accepting name/source_spec here
// while ignoring them would let a caller believe it created the group it
// described - the silent divergence the create form was already fixed for.
func TestCreateRejectsReuseThatAlsoDescribesAGroup(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		request CreateRequest
		field   string
	}{
		{name: "name", request: func() CreateRequest {
			request := reuseRequest("group-residential")
			request.Name = "second entry"
			return request
		}(), field: "name"},
		{name: "source_spec", request: func() CreateRequest {
			request := reuseRequest("group-residential")
			request.SourceSpec = proxygroup.SourceSpec{NodeIDs: []string{"node-a"}}
			return request
		}(), field: "source_spec"},
		{name: "enabled", request: func() CreateRequest {
			request := reuseRequest("group-residential")
			enabled := true
			request.Enabled = &enabled
			return request
		}(), field: "enabled"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			groups := &fakeGroups{stored: proxygroup.Group{
				ID: "group-residential", Version: 1, Name: "residential-linkup", Enabled: true,
			}}
			listeners := &fakeListeners{}
			service, err := NewService(groups, listeners, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Create(context.Background(), testCase.request)
			if !errors.Is(err, proxygroup.ErrInvalid) {
				t.Fatalf("Create() error = %v, want %v", err, proxygroup.ErrInvalid)
			}
			// The message must name the offending field, or the caller has to
			// guess which of six fields the server refused.
			if !strings.Contains(err.Error(), testCase.field) {
				t.Fatalf("Create() error = %v, want it to name %q", err, testCase.field)
			}
			if groups.createCalls != 0 || len(listeners.creates) != 0 {
				t.Fatalf("a rejected reuse wrote something: creates=%d listener_creates=%d",
					groups.createCalls, len(listeners.creates))
			}
		})
	}
}

// TestCreateDoesNotDeleteReusedGroupWhenListenerFails guards the cleanup path of
// the ordinary create: it deletes the group it just built. On reuse that group
// belongs to a residential channel, so deleting it would tear down an existing
// channel's egress because a new entry point could not be published.
func TestCreateDoesNotDeleteReusedGroupWhenListenerFails(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{
		ID: "group-residential", Version: 3, Name: "residential-linkup", Enabled: true,
	}}
	service, err := NewService(groups, &fakeListeners{err: errors.New("port conflict")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), reuseRequest("group-residential"))
	if !errors.Is(err, ErrCreateFailed) {
		t.Fatalf("Create() error = %v, want %v", err, ErrCreateFailed)
	}
	if groups.deleteCalls != 0 || groups.deleted != "" {
		t.Fatalf("deletes = %d (last %q), want 0: the reused group is not ours", groups.deleteCalls, groups.deleted)
	}
}

// TestCreateReportsReuseLookupFailureAsAnInternalError is the negative control
// for the two tests above: a storage failure must not be dressed up as the
// caller's validation error, or a broken database answers 422 forever.
func TestCreateReportsReuseLookupFailureAsAnInternalError(t *testing.T) {
	groups := &fakeGroups{getErr: errors.New("database is unavailable")}
	service, err := NewService(groups, &fakeListeners{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), reuseRequest("group-residential"))
	if err == nil {
		t.Fatal("Create() error = nil, want the lookup failure")
	}
	if errors.Is(err, proxygroup.ErrInvalid) {
		t.Fatalf("Create() error = %v, must not be reported as a validation error", err)
	}
}

func TestReuseRemovesNewEntryWhenSharedPublishFails(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-residential", Enabled: true}}
	listeners := &fakeListeners{syncErr: errors.New("publish failed")}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), reuseRequest("group-residential"))
	if !errors.Is(err, ErrCreateFailed) || listeners.deleted != "listener-1" || groups.deleteCalls != 0 {
		t.Fatalf("err=%v deleted entry=%s deleted groups=%d", err, listeners.deleted, groups.deleteCalls)
	}
}

// TestCreateRejectsOneNodeGroupBeforeWritingAnything pins the composite
// boundary with the real group service: proxy_service.create builds a group, so
// the group rule has to stop it here — with nothing written and nothing
// published. A service that exits through one node is a listener pin, not a
// group, and the error must say so.
func TestCreateRejectsOneNodeGroupBeforeWritingAnything(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "proxy-service-single-node.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	nodeID, err := database.UpsertResidentialSessionNode(ctx, "channel-1", store.ResidentialSessionNode{
		ID: "node-a", Fingerprint: "fp-node-a", DisplayName: "node a",
		Protocol: "http", CanonicalConfigEncrypted: []byte{0x01},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	groups, err := proxygroup.NewService(database, noopApply{})
	if err != nil {
		t.Fatal(err)
	}
	listeners := &fakeListeners{}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(ctx, CreateRequest{
		Name: "solo", Strategy: "manual",
		SourceSpec: proxygroup.SourceSpec{NodeIDs: []string{nodeID}},
		Listener: ListenerCreateRequest{
			Name: "solo", Kind: "mixed", Port: 7890,
			Auth: &listener.Auth{Username: "hx-user", Password: "hx-pass"},
		},
	})
	if !errors.Is(err, ErrCreateFailed) {
		t.Fatalf("Create() error = %v, want %v", err, ErrCreateFailed)
	}
	if !strings.Contains(err.Error(), "listener.node_id") {
		t.Fatalf("Create() error = %v, want the remedy (listener.node_id)", err)
	}
	if len(listeners.creates) != 0 {
		t.Fatalf("a rejected create published %d listener(s)", len(listeners.creates))
	}
	records, err := database.ListProxyGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("a rejected create left %d group(s) behind", len(records))
	}
}

// noopApply satisfies the group service's reconciler; these tests assert on
// what was written, not on what was compiled.
type noopApply struct{}

func (noopApply) Apply(context.Context) error { return nil }

// TestCreatePinnedNodeBuildsNoGroup is the remedy the one-node rejection points
// at. "One service, one node" must be reachable in one call without building a
// group; otherwise the error tells the caller to do something this endpoint
// cannot do.
func TestCreatePinnedNodeBuildsNoGroup(t *testing.T) {
	groups := &fakeGroups{}
	listeners := &fakeListeners{}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{Listener: ListenerCreateRequest{
		Name: "solo", Kind: "mixed", Port: 7890,
		Auth:   &listener.Auth{Username: "hx-user", Password: "hx-pass"},
		NodeID: "node-a",
	}}
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatalf("Create() error = %v, want success", err)
	}
	if groups.createCalls != 0 {
		t.Fatalf("group creates = %d, want 0 (a pinned service has no group of its own)", groups.createCalls)
	}
	if len(listeners.creates) != 1 {
		t.Fatalf("listener creates = %d, want 1", len(listeners.creates))
	}
	// The listener service resolves the anchor group itself; the proxy service
	// must not name one, or a pinned service would be born owning a group.
	if got := listeners.creates[0].ProxyGroupID; got != "" {
		t.Fatalf("listener create carried proxy_group_id %q, want empty", got)
	}
	if got := listeners.creates[0].NodeID; got != "node-a" {
		t.Fatalf("listener create node_id = %q, want node-a", got)
	}
}

// TestCreatePinnedNodeRejectsGroupFields is the same rule reuse already obeys:
// a field that has no effect must not be accepted quietly. Here the caller would
// otherwise believe it built the group it described.
func TestCreatePinnedNodeRejectsGroupFields(t *testing.T) {
	groups := &fakeGroups{}
	listeners := &fakeListeners{}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Name: "described-but-ignored", Strategy: "url-test",
		SourceSpec: proxygroup.SourceSpec{SubscriptionIDs: []string{"sub-1"}},
		Listener: ListenerCreateRequest{
			Name: "solo", Kind: "mixed", Port: 7890,
			Auth:   &listener.Auth{Username: "hx-user", Password: "hx-pass"},
			NodeID: "node-a",
		},
	}
	_, err = service.Create(context.Background(), request)
	if !errors.Is(err, proxygroup.ErrInvalid) {
		t.Fatalf("Create() error = %v, want %v", err, proxygroup.ErrInvalid)
	}
	for _, field := range []string{"name", "strategy", "source_spec"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("Create() error = %v, want it to name %q", err, field)
		}
	}
	if groups.createCalls != 0 || len(listeners.creates) != 0 {
		t.Fatalf("a rejected create wrote something: group_creates=%d listener_creates=%d", groups.createCalls, len(listeners.creates))
	}
}

// TestCreatePinnedNodeRejectsReuseTogether keeps the two mutually exclusive
// shapes from sharing one request: one names the exits, the other names a group
// that already does.
func TestCreatePinnedNodeRejectsReuseTogether(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Enabled: true}}
	listeners := &fakeListeners{}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := reuseRequest("group-1")
	request.Listener.NodeID = "node-a"
	if _, err := service.Create(context.Background(), request); !errors.Is(err, proxygroup.ErrInvalid) {
		t.Fatalf("Create() error = %v, want %v", err, proxygroup.ErrInvalid)
	}
	if groups.createCalls != 0 || len(listeners.creates) != 0 {
		t.Fatalf("a rejected create wrote something: group_creates=%d listener_creates=%d", groups.createCalls, len(listeners.creates))
	}
}
