package listener

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// newSharedInboundService wires a listener service over a real database so the
// membership convergence runs against the same constraints production uses.
func newSharedInboundService(t *testing.T) (*Service, *store.Store, *testReconciler) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &testReconciler{}
	service, err := NewService(database, box, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := database.CreateProxyGroup(ctx, store.ProxyGroupRecord{
		ID:             "group-a",
		Name:           "group-a",
		Strategy:       "manual",
		SourceSpecJSON: "{}",
		Enabled:        true,
		EmptyBehavior:  "fail-closed",
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}
	return service, database, reconciler
}

func sharedSpec() SharedInboundSpec {
	return SharedInboundSpec{
		Enabled:          true,
		IncludeWebSocket: true,
		MixedBindAddress: "127.0.0.1",
		MixedPort:        7890,
		WSPort:           7891,
	}
}

func createSharedMember(t *testing.T, service *Service, name, kind string) Listener {
	t.Helper()
	// The username is per member: two members of one family may not share it,
	// which is what the compiler and the service now both enforce.
	auth := &Auth{Username: "u-" + name, Password: "hx-pass"}
	if kind == "vless" || kind == "vmess" {
		auth.Password = "11111111-1111-1111-1111-111111111111"
	}
	created, err := service.Create(context.Background(), CreateRequest{
		Name:          name,
		Kind:          kind,
		BindAddress:   "127.0.0.1",
		Port:          19999,
		ProxyGroupID:  "group-a",
		Auth:          auth,
		SharedInbound: SharedInboundStandardOwner,
		Transport:     Transport{Type: "ws", WSPath: SharedInboundRoutePath()},
	})
	if err != nil {
		t.Fatalf("create shared member %q: %v", name, err)
	}
	return created
}

func TestEnsureSharedInboundsCreatesOneAggregatePerFamily(t *testing.T) {
	service, _, reconciler := newSharedInboundService(t)
	createSharedMember(t, service, "service-a", "mixed")
	createSharedMember(t, service, "service-b", "mixed")

	aggregates, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("aggregates = %d, want one mixed aggregate", len(aggregates))
	}
	aggregate := aggregates[0]
	if aggregate.SharedInbound != SharedInboundStandardOwner || aggregate.Kind != "mixed" {
		t.Fatalf("aggregate = %+v", aggregate)
	}
	if aggregate.BindAddress != "127.0.0.1" || aggregate.Port != 7890 {
		t.Fatalf("aggregate endpoint = %s:%d, want 127.0.0.1:7890", aggregate.BindAddress, aggregate.Port)
	}
	if reconciler.calls == 0 {
		t.Fatal("creating an aggregate did not trigger a data-plane apply")
	}

	// Idempotent: a second convergence must not add rows or touch the port.
	before := len(aggregates)
	again, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != before {
		t.Fatalf("second convergence changed the aggregate count: %d -> %d", before, len(again))
	}
}

func TestEnsureSharedInboundsRepairsAggregateEndpoint(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	createSharedMember(t, service, "service-a", "mixed")
	if _, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil); err != nil {
		t.Fatal(err)
	}
	moved := sharedSpec()
	moved.MixedPort = 7999
	if _, err := service.EnsureSharedInbounds(context.Background(), moved, nil, nil); err != nil {
		t.Fatal(err)
	}
	aggregates, err := service.SharedInboundAggregates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 || aggregates[0].Port != 7999 {
		t.Fatalf("aggregate port was not repaired: %+v", aggregates)
	}
}

func TestEnsureSharedInboundsRemovesEmptyAggregate(t *testing.T) {
	service, database, _ := newSharedInboundService(t)
	member := createSharedMember(t, service, "service-a", "mixed")
	if _, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), member.ID, member.Version); err != nil {
		t.Fatal(err)
	}
	aggregates, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 0 {
		t.Fatalf("aggregates = %+v, want none after the last member left", aggregates)
	}
	// The port must really be free again.
	if _, err := database.GetListener(context.Background(), member.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("member still exists: %v", err)
	}
	recreated, err := service.Create(context.Background(), CreateRequest{
		Name: "replacement", Kind: "mixed", BindAddress: "127.0.0.1", Port: 7890,
		ProxyGroupID: "group-a", Auth: &Auth{Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatalf("port was not released: %v", err)
	}
	_ = recreated
}

// TestEnsureSharedInboundsCarriesEveryStandardProtocolAsOneMixedEntry is the
// regression test for the failure where an HTTP or SOCKS5 proxy service vanished
// from the data plane.
//
// The family was keyed by the member's own protocol, so an http member produced
// an aggregate row of kind "http" — and Mihomo has no aggregate HTTP listener, so
// the service was published nowhere while its row still claimed a shared entry
// point. Every standard protocol must converge on the single Mixed carrier.
func TestEnsureSharedInboundsCarriesEveryStandardProtocolAsOneMixedEntry(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	createSharedMember(t, service, "service-http", "http")
	createSharedMember(t, service, "service-socks", "socks")
	createSharedMember(t, service, "service-mixed", "mixed")

	aggregates, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("aggregates = %d, want exactly one Mixed entry point: %+v", len(aggregates), aggregates)
	}
	if aggregates[0].Kind != "mixed" {
		t.Fatalf("aggregate kind = %q, want %q", aggregates[0].Kind, "mixed")
	}
}

// TestEnsureSharedInboundsRetiresPerProtocolAggregates covers an install that
// already stored one aggregate row per protocol. The stale rows must be removed,
// not left behind holding a port the family no longer uses.
func TestEnsureSharedInboundsRetiresPerProtocolAggregates(t *testing.T) {
	service, database, _ := newSharedInboundService(t)
	createSharedMember(t, service, "service-http", "http")
	if _, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil); err != nil {
		t.Fatal(err)
	}
	records, err := database.ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := 0
	for _, record := range records {
		if IsSharedInboundAggregate(record) {
			before++
		}
	}
	if before != 1 {
		t.Fatalf("aggregate rows = %d, want 1", before)
	}

	// Simulate the older layout by relabelling the aggregate row as the
	// family's per-protocol variant, then adding a mixed member.
	stale := records[0]
	for _, record := range records {
		if IsSharedInboundAggregate(record) {
			stale = record
		}
	}
	stale.Kind = "http"
	stale.Name = sharedAggregateName(SharedInboundStandardOwner, "http")
	if _, err := database.UpdateListener(context.Background(), stale, stale.Version); err != nil {
		t.Fatal(err)
	}
	createSharedMember(t, service, "service-mixed", "mixed")

	aggregates, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 || aggregates[0].Kind != "mixed" {
		t.Fatalf("aggregates = %+v, want one Mixed entry point", aggregates)
	}
	after, err := database.ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	aggregateRows := 0
	for _, record := range after {
		if IsSharedInboundAggregate(record) {
			aggregateRows++
		}
	}
	if aggregateRows != 1 {
		t.Fatalf("aggregate rows after repair = %d, want 1", aggregateRows)
	}
}

func TestEnsureSharedInboundsKeepsOneListenerPerWebSocketProtocol(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	for _, kind := range []string{"vless", "vmess", "trojan"} {
		created, err := service.Create(context.Background(), CreateRequest{
			Name: "ws-" + kind, Kind: kind, BindAddress: "127.0.0.1", Port: 19999,
			ProxyGroupID:  "group-a",
			Auth:          &Auth{Username: "hx-user", Password: "11111111-1111-1111-1111-111111111111"},
			SharedInbound: SharedInboundWebSocketOwner,
			Transport:     Transport{Type: "ws", WSPath: SharedInboundRoutePath()},
			// An advanced listener is only reachable through the edge relay, so
			// the service requires the public host it is published behind.
			PublicEndpoint: PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
		})
		if err != nil {
			t.Fatalf("create %s member: %v", kind, err)
		}
		_ = created
	}
	aggregates, err := service.EnsureSharedInbounds(context.Background(), sharedSpec(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 3 {
		t.Fatalf("aggregates = %d, want one listener per WebSocket protocol (they share a port, not a listener)", len(aggregates))
	}
}

// TestSharedInboundCarrierKind pins the family contract: the standard family is
// always carried by a Mixed listener, whatever protocol its members use.
func TestSharedInboundCarrierKind(t *testing.T) {
	for _, kind := range SharedInboundMemberKinds(SharedInboundStandardOwner) {
		if got := SharedInboundCarrierKind(SharedInboundStandardOwner, kind); got != "mixed" {
			t.Fatalf("standard carrier for %q = %q, want mixed", kind, got)
		}
		if !IsSharedInboundMemberKind(SharedInboundStandardOwner, kind) {
			t.Fatalf("%q is not recognised as a standard member kind", kind)
		}
	}
	for _, kind := range SharedInboundMemberKinds(SharedInboundWebSocketOwner) {
		if got := SharedInboundCarrierKind(SharedInboundWebSocketOwner, kind); got != kind {
			t.Fatalf("websocket carrier for %q = %q, want %q", kind, got, kind)
		}
	}
	if SharedInboundCarrierKind(SharedInboundStandardOwner, "vless") != "mixed" {
		t.Fatal("an advanced kind must not be reported as a WebSocket carrier of the standard family")
	}
	if IsSharedInboundMemberKind(SharedInboundStandardOwner, "vless") {
		t.Fatal("a VLESS listener is not a standard-family member")
	}
	if IsSharedInboundMemberKind(SharedInboundWebSocketOwner, "http") {
		t.Fatal("an HTTP listener is not a WebSocket-family member")
	}
}

// TestCreateRejectsDuplicateSharedInboundUsername pins the boundary that used
// to be discovered only by the compiler: two members of one family with the
// same username refuse the whole configuration, so the create must fail before
// the row is written and rolled back again.
func TestCreateRejectsDuplicateSharedInboundUsername(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	first := createSharedMember(t, service, "service-a", "mixed")

	_, err := service.Create(context.Background(), CreateRequest{
		Name: "service-b", Kind: "mixed", BindAddress: "127.0.0.1", Port: 7890,
		ProxyGroupID:  "group-a",
		Auth:          &Auth{Username: "u-service-a", Password: "hx-pass"},
		SharedInbound: SharedInboundStandardOwner,
	})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("Create() error = %v, want ErrUsernameTaken", err)
	}
	if !strings.Contains(err.Error(), first.Name) {
		t.Fatalf("error does not name the listener holding the username: %v", err)
	}
}

// TestSameUsernameAcrossFamiliesIsAllowed keeps the check keyed by the carrier
// that publishes the member, exactly like the compiler: the standard Mixed
// entry and each WebSocket protocol are separate listeners, so their usernames
// live in separate namespaces.
func TestSameUsernameAcrossFamiliesIsAllowed(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	createSharedMember(t, service, "service-a", "mixed")

	_, err := service.Create(context.Background(), CreateRequest{
		Name: "ws-vless", Kind: "vless", BindAddress: "127.0.0.1", Port: 19999,
		ProxyGroupID:  "group-a",
		Auth:          &Auth{Username: "u-service-a", Password: "11111111-1111-1111-1111-111111111111"},
		SharedInbound: SharedInboundWebSocketOwner,
		Transport:     Transport{Type: "ws", WSPath: SharedInboundRoutePath()},
		PublicEndpoint: PublicEndpoint{
			Host: "proxy.example.com", Port: 443, TLS: true,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want the same username to be reusable in another family", err)
	}
}

// TestUpdateKeepsItsOwnUsername guards the self-exclusion: re-saving a member
// without changing its credential must not collide with itself.
func TestUpdateKeepsItsOwnUsername(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	member := createSharedMember(t, service, "service-a", "mixed")

	updated, err := service.Update(context.Background(), member.ID, UpdateRequest{
		Version: member.Version, Name: member.Name, Kind: member.Kind,
		BindAddress: member.BindAddress, Port: member.Port,
		ProxyGroupID:  member.ProxyGroupID,
		SharedInbound: SharedInboundStandardOwner,
		Enabled:       true,
		Auth:          &Auth{Username: "u-service-a", Password: "hx-pass"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ID != member.ID {
		t.Fatalf("Update() returned %q, want %q", updated.ID, member.ID)
	}
}

// TestUpdateRejectsDuplicateSharedInboundUsername is the edit-side half of the
// same invariant: renaming a member onto another member's username must be
// refused before it reaches the data plane.
func TestUpdateRejectsDuplicateSharedInboundUsername(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	createSharedMember(t, service, "service-a", "mixed")
	second := createSharedMember(t, service, "service-b", "mixed")

	_, err := service.Update(context.Background(), second.ID, UpdateRequest{
		Version: second.Version, Name: second.Name, Kind: second.Kind,
		BindAddress: second.BindAddress, Port: second.Port,
		ProxyGroupID:  second.ProxyGroupID,
		SharedInbound: SharedInboundStandardOwner,
		Enabled:       true,
		Auth:          &Auth{Username: "u-service-a", Password: "hx-pass"},
	})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("Update() error = %v, want ErrUsernameTaken", err)
	}
}

func TestCreateRejectsUnknownSharedInboundOwner(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	_, err := service.Create(context.Background(), CreateRequest{
		Name: "bad", Kind: "mixed", BindAddress: "127.0.0.1", Port: 7890,
		ProxyGroupID: "group-a", Auth: &Auth{Username: "u", Password: "p"},
		SharedInbound: "not-a-family",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
}

// TestCreateJoinsTheStandardFamilyByDefault pins the behaviour change: a standard
// listener created without shared_inbound becomes a member of the single Mixed
// entry point instead of reserving a port of its own.
//
// The reported failure was exactly the opposite: 59 unmarked Mixed listeners, one
// bound port each, produced by every caller that did not know about shared
// inbounds. See .agents/notes/implemented/architecture/2026-09-26-shared-inbound-by-default.md.
func TestCreateJoinsTheStandardFamilyByDefault(t *testing.T) {
	service, database, _ := newSharedInboundService(t)
	service.SetSharedEndpointProvider(func() (SharedInboundSpec, bool) { return sharedSpec(), true })

	created, err := service.Create(context.Background(), CreateRequest{
		Name:         "default-mixed",
		Kind:         "mixed",
		BindAddress:  "127.0.0.1",
		Port:         17890,
		ProxyGroupID: "group-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.SharedInbound != SharedInboundStandardOwner {
		t.Fatalf("shared_inbound = %q, want %q", created.SharedInbound, SharedInboundStandardOwner)
	}
	// A member must not keep a port of its own: the family's carrier owns the
	// endpoint, and the member is selected by username.
	if created.Port != sharedSpec().MixedPort {
		t.Fatalf("member port = %d, want the family port %d", created.Port, sharedSpec().MixedPort)
	}
	if !created.AuthConfigured {
		t.Fatal("a member joined the username-routed family without a credential, so it cannot be compiled")
	}
	record, err := database.GetListener(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.AuthMode != "userpass" || len(record.AuthConfigEncrypted) == 0 {
		t.Fatalf("stored row = auth_mode %q / %d credential bytes, want a minted credential", record.AuthMode, len(record.AuthConfigEncrypted))
	}
}

// TestCreateKeepsADedicatedPortWhenTheSharedInboundIsOff guards the other side of
// the default: with the shared inbound switched off the family is not compiled,
// so membership would publish the listener nowhere at all.
func TestCreateKeepsADedicatedPortWhenTheSharedInboundIsOff(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	disabled := sharedSpec()
	disabled.Enabled = false
	disabled.IncludeWebSocket = false
	service.SetSharedEndpointProvider(func() (SharedInboundSpec, bool) { return disabled, false })

	created, err := service.Create(context.Background(), CreateRequest{
		Name:         "per-service-mixed",
		Kind:         "mixed",
		BindAddress:  "127.0.0.1",
		Port:         17891,
		ProxyGroupID: "group-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.SharedInbound != "" {
		t.Fatalf("shared_inbound = %q, want a dedicated listener while the shared inbound is off", created.SharedInbound)
	}
	if created.Port != 17891 {
		t.Fatalf("port = %d, want the requested 17891", created.Port)
	}
}

// TestCreateKeepsAdvancedKindsOnTheirOwnPort pins why WebSocket protocols are not
// folded into the family by default: the only advanced-kind listener this build
// creates is a residential channel entry point, which carries per-session IN-USER
// routes the aggregate listener cannot express.
func TestCreateKeepsAdvancedKindsOnTheirOwnPort(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	service.SetSharedEndpointProvider(func() (SharedInboundSpec, bool) { return sharedSpec(), true })

	created, err := service.Create(context.Background(), CreateRequest{
		Name:         "residential-entry",
		Kind:         "vless",
		BindAddress:  "127.0.0.1",
		Port:         32000,
		ProxyGroupID: "group-a",
		Auth:         &Auth{Username: "hx-bootstrap", Password: "11111111-1111-1111-1111-111111111111"},
		Transport:    Transport{Type: "ws", WSPath: "/__hx-proxy__/residential/channel-a"},
		PublicEndpoint: PublicEndpoint{
			Host: "proxy.example.com", Port: 443, TLS: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.SharedInbound != "" {
		t.Fatalf("shared_inbound = %q, want a dedicated entry point", created.SharedInbound)
	}
	if created.Port != 32000 {
		t.Fatalf("port = %d, want 32000", created.Port)
	}
}

// TestBackfillMemberCredentialsRepairsARowWrittenDirectly covers the migration
// path: the store cannot mint credentials (encryption needs the master key), so
// rows folded into the family there are repaired before the data plane compiles.
//
// Without the repair the compiler refuses the whole configuration - measured as
// 'listener "x" has no credentials but the shared inbound routes members by
// username' - which takes every proxy down rather than only the one member.
func TestBackfillMemberCredentialsRepairsARowWrittenDirectly(t *testing.T) {
	service, database, _ := newSharedInboundService(t)
	ctx := context.Background()
	service.SetSharedEndpointProvider(func() (SharedInboundSpec, bool) { return sharedSpec(), true })

	// Mimic the migration: a marked member with no credential at all.
	now := time.Now().UTC()
	if _, err := database.CreateListener(ctx, store.ListenerRecord{
		ID: "listener-migrated", Name: "migrated-member", Kind: "mixed",
		BindAddress: "127.0.0.1", Port: 17899, ProxyGroupID: "group-a",
		AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}",
		ShareToken: "0123456789abcdef0123456789abcdef", SharedInbound: SharedInboundStandardOwner,
		Enabled: true, Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	minted, err := service.BackfillMemberCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if minted != 1 {
		t.Fatalf("minted = %d, want 1", minted)
	}
	record, err := database.GetListener(ctx, "listener-migrated")
	if err != nil {
		t.Fatal(err)
	}
	if record.AuthMode != "userpass" || len(record.AuthConfigEncrypted) == 0 {
		t.Fatalf("row was not repaired: auth_mode=%q bytes=%d", record.AuthMode, len(record.AuthConfigEncrypted))
	}
	// Idempotent: a repaired row is left alone, so convergence does not rotate a
	// credential that a published subscription already handed out.
	again, err := service.BackfillMemberCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("second backfill minted %d credentials, want 0 (idempotent)", again)
	}
}

// TestUnknownSharedInboundOwnerIsStillRejected keeps the validation contract:
// only the two families (or empty) may reach the database CHECK constraint, even
// though an omitted marker now resolves to a family by default.
func TestUnknownSharedInboundOwnerIsStillRejected(t *testing.T) {
	service, _, _ := newSharedInboundService(t)
	service.SetSharedEndpointProvider(func() (SharedInboundSpec, bool) { return sharedSpec(), true })
	if _, err := service.Create(context.Background(), CreateRequest{
		Name: "bogus-owner", Kind: "mixed", BindAddress: "127.0.0.1", Port: 17898,
		ProxyGroupID: "group-a", SharedInbound: "aggregate",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}
