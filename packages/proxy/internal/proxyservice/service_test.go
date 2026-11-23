package proxyservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

type fakeGroups struct {
	created     proxygroup.Group
	createCalls int
	deleted     string
	deleteCalls int
	stored      proxygroup.Group
	// getErr fails every lookup, so a test can exercise "the store itself is
	// down" as opposed to "the group does not exist".
	getErr      error
	updateCalls []proxygroup.UpdateRequest
}

func (groups *fakeGroups) Create(context.Context, proxygroup.CreateRequest) (proxygroup.Group, error) {
	groups.createCalls++
	groups.created = proxygroup.Group{ID: "group-1", Version: 1}
	return groups.created, nil
}

func (groups *fakeGroups) Get(_ context.Context, id string) (proxygroup.Group, error) {
	if groups.getErr != nil {
		return proxygroup.Group{}, groups.getErr
	}
	// A stored group stands in for the one real group this fake knows. Asking
	// for anything else is a lookup miss, which is what makes "reuse a group
	// that does not exist" testable at all.
	if groups.stored.ID == "" {
		groups.stored = proxygroup.Group{ID: id, Version: 1, Name: "original", Enabled: true}
	}
	if id != groups.stored.ID {
		return proxygroup.Group{}, fmt.Errorf("%w: %s", proxygroup.ErrNotFound, id)
	}
	return groups.stored, nil
}

func (groups *fakeGroups) Update(_ context.Context, _ string, request proxygroup.UpdateRequest) (proxygroup.Group, error) {
	groups.updateCalls = append(groups.updateCalls, request)
	return proxygroup.Group{
		ID:               "group-1",
		Name:             request.Name,
		Strategy:         request.Strategy,
		SourceSpec:       request.SourceSpec,
		Enabled:          request.Enabled,
		EmptyBehavior:    request.EmptyBehavior,
		FallbackTargetID: request.FallbackTarget,
		Version:          request.Version + 1,
	}, nil
}

func (groups *fakeGroups) Delete(_ context.Context, id string, _ int) error {
	groups.deleted = id
	groups.deleteCalls++
	return nil
}

type fakeListeners struct {
	// err fails the write paths (Create, Update) so a test can exercise the
	// composite rollback. It deliberately does not fail Get: the ownership
	// check runs first, and a read error there would mask the write failure
	// the test is about.
	err error
	// getErr fails the ownership lookup on its own.
	getErr error
	// owner is the group every stored listener belongs to. Update refuses a
	// listener owned by anyone else, so a test that updates a service has to
	// state who owns its entry point.
	owner string
	// pin is the node the stored listener is pinned to, if any. The composite
	// update has to read it to know whether the caller is keeping the pin.
	pin string
	// updates records every listener update so a test can assert the pin that
	// was actually carried to the listener service.
	updates []listener.UpdateRequest
	// creates records every listener create, so a test can assert that a
	// rejected request created nothing.
	creates []listener.CreateRequest
	deleted string
	syncErr error
}

func (listeners *fakeListeners) Delete(_ context.Context, id string, _ int) error {
	listeners.deleted = id
	return nil
}

func (listeners *fakeListeners) Create(_ context.Context, request listener.CreateRequest) (listener.Listener, error) {
	if listeners.err != nil {
		return listener.Listener{}, listeners.err
	}
	listeners.creates = append(listeners.creates, request)
	return listener.Listener{ID: "listener-1", ProxyGroupID: request.ProxyGroupID}, nil
}

func (listeners *fakeListeners) Get(_ context.Context, id string) (listener.Listener, error) {
	if listeners.getErr != nil {
		return listener.Listener{}, listeners.getErr
	}
	if id != "listener-1" {
		return listener.Listener{}, fmt.Errorf("%w: %s", listener.ErrNotFound, id)
	}
	return listener.Listener{ID: id, ProxyGroupID: listeners.owner, NodeID: listeners.pin}, nil
}

func (listeners *fakeListeners) EnsureSharedInbounds(context.Context, listener.SharedInboundSpec, []store.ProxyGroupRecord, []listener.SharedInboundMember) ([]listener.Listener, error) {
	return nil, listeners.syncErr
}

func (listeners *fakeListeners) Update(_ context.Context, _ string, request listener.UpdateRequest) (listener.Listener, error) {
	if listeners.err != nil {
		return listener.Listener{}, listeners.err
	}
	listeners.updates = append(listeners.updates, request)
	return listener.Listener{ID: "listener-1", ProxyGroupID: request.ProxyGroupID, NodeID: request.NodeID, Name: request.Name}, nil
}

func TestCreateBindsListenerToGroup(t *testing.T) {
	service, err := NewService(&fakeGroups{}, &fakeListeners{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), CreateRequest{Listener: ListenerCreateRequest{
		Port: 7890,
		Auth: &listener.Auth{Username: "hx-user", Password: "hx-pass"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Listener.ProxyGroupID != created.Group.ID {
		t.Fatalf("listener group = %q, want %q", created.Listener.ProxyGroupID, created.Group.ID)
	}
}

// TestCreateRejectsSharedMemberWithoutCredentials pins the shared-inbound rule
// that a member must be selectable by username: without credentials Mihomo
// would accept every connection on the aggregate port.
func TestCreateRejectsSharedMemberWithoutCredentials(t *testing.T) {
	service, err := NewService(&fakeGroups{}, &fakeListeners{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(context.Background(), CreateRequest{Listener: ListenerCreateRequest{Port: 7890}}); err == nil {
		t.Fatal("Create() error = nil, want a credential requirement")
	}
}

// TestCreateUsesDedicatedPortWhenSharedInboundDisabled keeps the historical
// per-port behaviour working after an upgrade.
func TestCreateUsesDedicatedPortWhenSharedInboundDisabled(t *testing.T) {
	settings := &fakeSettings{mode: systemsettings.SharedInboundPerService}
	service, err := NewService(&fakeGroups{}, &fakeListeners{}, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(context.Background(), CreateRequest{Listener: ListenerCreateRequest{
		Kind: "mixed", BindAddress: "127.0.0.1", Port: 7890,
	}}); err != nil {
		t.Fatal(err)
	}
}

type fakeSettings struct {
	mode systemsettings.SharedInboundMode
}

func (settings *fakeSettings) Get(context.Context) (systemsettings.Settings, error) {
	current := systemsettings.Default()
	current.SharedInbound.Mode = settings.mode
	return current, nil
}

func TestCreateRemovesGroupWhenListenerFails(t *testing.T) {
	groups := &fakeGroups{}
	service, err := NewService(groups, &fakeListeners{err: errors.New("port conflict")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), CreateRequest{})
	if !errors.Is(err, ErrCreateFailed) || groups.deleted != "group-1" {
		t.Fatalf("Create() error = %v, deleted = %q", err, groups.deleted)
	}
}

func TestUpdateBindsUpdatedListenerToGroup(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Name: "original"}}
	service, err := NewService(groups, &fakeListeners{owner: "group-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(context.Background(), UpdateRequest{
		GroupID:         "group-1",
		GroupVersion:    1,
		Name:            "renamed",
		ListenerID:      "listener-1",
		ListenerVersion: 1,
		Listener:        ListenerUpdateRequest{Name: "listener-renamed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Group.Name != "renamed" {
		t.Fatalf("group name = %q, want %q", updated.Group.Name, "renamed")
	}
	if updated.Listener.ProxyGroupID != "group-1" {
		t.Fatalf("listener group = %q, want %q", updated.Listener.ProxyGroupID, "group-1")
	}
	if updated.Listener.Name != "listener-renamed" {
		t.Fatalf("listener name = %q, want %q", updated.Listener.Name, "listener-renamed")
	}
}

// A composite update that does not mention the pin must carry the stored one
// through. Measured 2026-10-01: this call dropped the field, so renaming a
// pinned service silently re-pointed its egress at the anchor group's members.
func TestUpdateKeepsStoredNodePinWhenCallerOmitsIt(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Name: "original"}}
	listeners := &fakeListeners{owner: "group-1", pin: "node-a"}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(context.Background(), UpdateRequest{
		GroupID:         "group-1",
		GroupVersion:    1,
		Name:            "renamed",
		ListenerID:      "listener-1",
		ListenerVersion: 1,
		Listener:        ListenerUpdateRequest{Name: "listener-renamed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners.updates) != 1 {
		t.Fatalf("listener updates = %d, want 1", len(listeners.updates))
	}
	if got := listeners.updates[0].NodeID; got != "node-a" {
		t.Fatalf("carried pin = %q, want %q (stored pin dropped by a rename)", got, "node-a")
	}
	if updated.Listener.NodeID != "node-a" {
		t.Fatalf("returned pin = %q, want %q", updated.Listener.NodeID, "node-a")
	}
}

// Moving the pin is a real edit and must be honoured.
func TestUpdateMovesNodePinWhenCallerNamesOne(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Name: "original"}}
	listeners := &fakeListeners{owner: "group-1", pin: "node-a"}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), UpdateRequest{
		GroupID:         "group-1",
		GroupVersion:    1,
		Name:            "renamed",
		ListenerID:      "listener-1",
		ListenerVersion: 1,
		Listener:        ListenerUpdateRequest{Kind: "mixed", Name: "listener-renamed", NodeID: "node-b"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := listeners.updates[0].NodeID; got != "node-b" {
		t.Fatalf("moved pin = %q, want %q", got, "node-b")
	}
}

// Releasing the pin is the one case an omitted NodeID cannot express, so it has
// its own flag; the empty string is a meaningful value there.
func TestUpdateClearsNodePinOnlyWhenAsked(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Name: "original"}}
	listeners := &fakeListeners{owner: "group-1", pin: "node-a"}
	service, err := NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), UpdateRequest{
		GroupID:         "group-1",
		GroupVersion:    1,
		Name:            "renamed",
		ListenerID:      "listener-1",
		ListenerVersion: 1,
		Listener:        ListenerUpdateRequest{Kind: "mixed", Name: "listener-renamed", ClearNodePin: true},
	}); err != nil {
		t.Fatal(err)
	}
	if got := listeners.updates[0].NodeID; got != "" {
		t.Fatalf("cleared pin = %q, want empty", got)
	}
}

func TestUpdateRollsBackGroupWhenListenerFails(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Name: "original"}}
	service, err := NewService(groups, &fakeListeners{owner: "group-1", err: errors.New("port conflict")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(context.Background(), UpdateRequest{
		GroupID:         "group-1",
		GroupVersion:    1,
		Name:            "renamed",
		ListenerID:      "listener-1",
		ListenerVersion: 1,
		Listener:        ListenerUpdateRequest{Name: "listener-renamed"},
	})
	if err == nil {
		t.Fatal("Update() error = nil, want listener error")
	}
	if len(groups.updateCalls) != 2 {
		t.Fatalf("group update calls = %d, want 2", len(groups.updateCalls))
	}
	rollback := groups.updateCalls[1]
	if rollback.Name != "original" {
		t.Fatalf("rollback name = %q, want %q", rollback.Name, "original")
	}
	firstVersion := groups.updateCalls[0].Version
	if rollback.Version != firstVersion+1 {
		t.Fatalf("rollback version = %d, want %d (version after first group update)", rollback.Version, firstVersion+1)
	}
	if !strings.Contains(err.Error(), "port conflict") {
		t.Fatalf("Update() error = %v, want listener error", err)
	}
}

// TestUpdateRejectsListenerOwnedByAnotherGroup pins the boundary that a
// composite save may only touch its own entry point. The HTTP handler used to
// take the listener id from the body and the group id from the body, so naming
// someone else's listener moved it: the source service was left pointing at a
// listener that now routed to a different group.
func TestUpdateRejectsListenerOwnedByAnotherGroup(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Name: "original"}}
	service, err := NewService(groups, &fakeListeners{owner: "group-2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(context.Background(), UpdateRequest{
		GroupID:         "group-1",
		GroupVersion:    1,
		Name:            "renamed",
		ListenerID:      "listener-1",
		ListenerVersion: 1,
		Listener:        ListenerUpdateRequest{Name: "listener-renamed"},
	})
	if !errors.Is(err, listener.ErrConflict) {
		t.Fatalf("Update() error = %v, want %v", err, listener.ErrConflict)
	}
	// Nothing may be written: a rejected update that still bumped the group
	// version would make the caller's retry fail on a stale version.
	if len(groups.updateCalls) != 0 {
		t.Fatalf("group updates on rejection = %d, want 0", len(groups.updateCalls))
	}
}

// TestUpdateRejectsUnknownListener keeps a stale listener id from being
// reported as an internal error.
func TestUpdateRejectsUnknownListener(t *testing.T) {
	groups := &fakeGroups{stored: proxygroup.Group{ID: "group-1", Version: 1, Name: "original"}}
	service, err := NewService(groups, &fakeListeners{owner: "group-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(context.Background(), UpdateRequest{
		GroupID:         "group-1",
		GroupVersion:    1,
		ListenerID:      "listener-missing",
		ListenerVersion: 1,
	})
	if !errors.Is(err, listener.ErrNotFound) {
		t.Fatalf("Update() error = %v, want %v", err, listener.ErrNotFound)
	}
	if len(groups.updateCalls) != 0 {
		t.Fatalf("group updates on rejection = %d, want 0", len(groups.updateCalls))
	}
}
