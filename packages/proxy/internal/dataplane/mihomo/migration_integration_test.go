package mihomo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/sharedinbound"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// stubSharedSettings reports a fixed shared-inbound configuration.
type stubSharedSettings struct{ spec listener.SharedInboundSpec }

func (stub stubSharedSettings) SharedInboundSpec(context.Context) (listener.SharedInboundSpec, bool, error) {
	return stub.spec, stub.spec.Enabled, nil
}

type noopReconciler struct{}

func (noopReconciler) Apply(context.Context) error { return nil }

// TestSharedInboundMigrationCarriesAnHTTPServiceThroughToTheCompiledConfig is
// the integration test for the whole path an operator actually walks when they
// switch the shared inbound on: existing services are migrated onto the
// aggregate entry point with the real store and the real listener service, and
// the result is then compiled.
//
// It is the test that would have caught the reported failure. Each layer passed
// its own unit tests, yet an HTTP service ended up published nowhere: the
// migration moved the row onto the standard family, and the compiler carried
// only Mixed members. Only running the two together shows that.
func TestSharedInboundMigrationCarriesAnHTTPServiceThroughToTheCompiledConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := database.CreateProxyGroup(ctx, store.ProxyGroupRecord{
		ID: "group-outlook", Name: "group-outlook", Strategy: "manual",
		SourceSpecJSON: `{"include_direct":true}`, RulePipelineJSON: "{}",
		Enabled: true, EmptyBehavior: "direct", Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	listenerService, err := listener.NewService(database, box, noopReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	// The reported installation: an authenticated HTTP service on loopback.
	created, err := listenerService.Create(ctx, listener.CreateRequest{
		Name: "residential-outlook 入口", Kind: "http",
		BindAddress: "127.0.0.1", Port: 7890,
		ProxyGroupID: "group-outlook",
		Auth:         &listener.Auth{Username: "hx-user", Password: "hx-pass-9f0e"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.AuthConfigured {
		t.Fatal("the service was created without credentials, so the migration would skip it")
	}

	spec := listener.SharedInboundSpec{
		Enabled: true, IncludeWebSocket: true,
		MixedBindAddress: "0.0.0.0", MixedPort: 7890, WSPort: 7891,
	}
	migrator, err := sharedinbound.NewService(database, listenerService, stubSharedSettings{spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	result, err := migrator.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.Migrated != 1 || len(result.SkippedWithoutCredentials) != 0 {
		t.Fatalf("migration result = %+v, want the HTTP service migrated", result)
	}

	compiler, err := NewCompiler(database, box)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	document := decodeDocument(t, compiled.YAML)

	listeners := listenersOf(t, document)
	if len(listeners) != 1 {
		t.Fatalf("compiled listeners = %d, want one Mixed entry point: %#v", len(listeners), listeners)
	}
	carrier := listeners[0]
	if carrier["type"] != "mixed" {
		t.Fatalf("carrier type = %v, want mixed", carrier["type"])
	}
	if carrier["listen"] != "0.0.0.0" || carrier["port"] != 7890 {
		// 7890 is the family's configured Mixed port, and the service happened
		// to use it too; the point is that the entry point is the family's.
		t.Fatalf("carrier endpoint = %v:%v, want 0.0.0.0:7890", carrier["listen"], carrier["port"])
	}
	if _, ok := usersOf(t, carrier)["hx-user"]; !ok {
		t.Fatalf("the migrated service has no username on the entry point: %#v", carrier["users"])
	}
	expected := "AND,((IN-NAME," + SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed") + "),(IN-USER,hx-user)),group-outlook"
	if indexOfRule(rulesOf(t, document), expected) < 0 {
		t.Fatalf("missing member rule %q in %#v", expected, rulesOf(t, document))
	}
}

// TestSharedInboundMigrationLeavesAnUnauthenticatedServiceAlone pins the other
// half of the reported situation: a service with no credentials cannot be
// carried by a username-routed entry point, so it must stay on its own port and
// be reported rather than silently published as a member.
func TestSharedInboundMigrationLeavesAnUnauthenticatedServiceAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := database.CreateProxyGroup(ctx, store.ProxyGroupRecord{
		ID: "group-a", Name: "group-a", Strategy: "manual",
		SourceSpecJSON: `{"include_direct":true}`, RulePipelineJSON: "{}",
		Enabled: true, EmptyBehavior: "direct", Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	listenerService, err := listener.NewService(database, box, noopReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := listenerService.Create(ctx, listener.CreateRequest{
		Name: "anonymous 入口", Kind: "http",
		BindAddress: "127.0.0.1", Port: 7890, ProxyGroupID: "group-a",
	}); err != nil {
		t.Fatal(err)
	}
	migrator, err := sharedinbound.NewService(database, listenerService, stubSharedSettings{spec: listener.SharedInboundSpec{
		Enabled: true, IncludeWebSocket: true, MixedBindAddress: "0.0.0.0", MixedPort: 7890, WSPort: 7891,
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := migrator.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.Migrated != 0 || len(result.SkippedWithoutCredentials) != 1 {
		t.Fatalf("migration result = %+v, want the service skipped and reported", result)
	}
	records, err := database.ListListeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if listener.SharedInboundOwnerOf(record) != "" {
			t.Fatalf("an unauthenticated service was marked as a shared-inbound member: %+v", record)
		}
	}
}
