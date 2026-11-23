package subscription

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestParsedRefreshPersistsDeduplicatedNodesAndRetiresMissingNodes(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	service, err := NewService(
		database,
		box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	created, err := service.Create(ctx, CreateRequest{
		Name:       "parsed",
		SourceType: SourceInline,
		SourceConfig: SourceConfig{
			Inline: "vless://id@example.com:443?security=tls#first\n" +
				"vless://id@example.com:443?security=tls#renamed\n" +
				"unknown://bad.example.com:1234#unsupported",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.EstimatedNodes != 1 {
		t.Fatalf("EstimatedNodes = %d, want 1", result.EstimatedNodes)
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].LifecycleState != "candidate" || nodes[0].SourceCount != 1 {
		t.Fatalf("unexpected nodes after first refresh: %+v", nodes)
	}
	firstNodeID := nodes[0].ID

	updated, err := service.Update(ctx, created.ID, UpdateRequest{
		Version:                created.Version,
		Name:                   created.Name,
		SourceType:             SourceInline,
		SourceConfig:           &SourceConfig{Inline: "trojan://password@new.example.com:443#new"},
		Enabled:                true,
		RefreshIntervalSeconds: created.RefreshIntervalSeconds,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Version <= created.Version {
		t.Fatalf("updated version = %d, original = %d", updated.Version, created.Version)
	}
	if _, err := service.Refresh(ctx, created.ID); err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}
	nodes, err = database.ListNodes(ctx, store.NodeFilter{Limit: 100})
	if err != nil {
		t.Fatalf("second ListNodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("node count = %d, want 2", len(nodes))
	}
	states := map[string]string{}
	for _, item := range nodes {
		states[item.ID] = item.LifecycleState
	}
	if states[firstNodeID] != "retired" {
		t.Fatalf("old node state = %q, want retired", states[firstNodeID])
	}
}

func TestRefreshExpandsHTTPMihomoProvider(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("User-Agent") != "HX-Provider-Test/1" {
			t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
		}
		_, _ = response.Write([]byte("payload:\n  - {name: remote-provider, type: anytls, server: anytls.example.com, port: 443, password: secret}\n"))
	}))
	defer provider.Close()

	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, _ := secret.New(make([]byte, 32))
	service, err := NewService(database, box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateRequest{
		Name: "provider", SourceType: SourceInline,
		SourceConfig: SourceConfig{AllowPrivate: true, Inline: "proxy-providers:\n  remote:\n    type: http\n    url: " + provider.URL + "\n    header:\n      User-Agent: [HX-Provider-Test/1]\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.EstimatedNodes != 1 || result.DetectedFormat != "clash-yaml+providers" {
		t.Fatalf("unexpected refresh result: %+v", result)
	}
}

func TestRefreshExpandsRelativeFileMihomoProvider(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	rootPath := filepath.Join(directory, "root.yaml")
	providerPath := filepath.Join(directory, "provider.yaml")
	if err := os.WriteFile(rootPath, []byte("proxy-providers:\n  local:\n    type: file\n    path: provider.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(providerPath, []byte("payload:\n  - {name: local-provider, type: snell, server: snell.example.com, port: 443, psk: secret}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, _ := secret.New(make([]byte, 32))
	service, err := NewService(database, box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateRequest{Name: "file-provider", SourceType: SourceFile, SourceConfig: SourceConfig{FilePath: rootPath}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.EstimatedNodes != 1 {
		t.Fatalf("unexpected refresh result: %+v", result)
	}
}

func TestDeletingSubscriptionRetiresOrphanedNodes(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		database,
		box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateRequest{
		Name:         "delete-source",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "trojan://password@delete.example.com:443#delete"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, created.ID, created.Version); err != nil {
		t.Fatal(err)
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].LifecycleState != "retired" || nodes[0].SourceCount != 0 {
		t.Fatalf("orphaned node was not retired: %+v", nodes)
	}
}

func TestParsedRefreshFailureKeepsPreviousNodeSnapshot(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		database,
		box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateRequest{
		Name:         "rollback",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "vless://id@stable.example.com:443#stable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(ctx, created.ID, UpdateRequest{
		Version:                created.Version,
		Name:                   created.Name,
		SourceType:             SourceInline,
		SourceConfig:           &SourceConfig{Inline: "unsupported://broken.example.com:1234#broken"},
		Enabled:                true,
		RefreshIntervalSeconds: created.RefreshIntervalSeconds,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, updated.ID); err == nil {
		t.Fatal("Refresh() succeeded for an unsupported-only subscription")
	}
	current, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.LastSuccessSnapshotID != first.SnapshotID {
		t.Fatalf("last success snapshot = %q, want %q", current.LastSuccessSnapshotID, first.SnapshotID)
	}
	if current.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive failures = %d, want 1", current.ConsecutiveFailures)
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].LifecycleState != "candidate" || nodes[0].SourceCount != 1 {
		t.Fatalf("previous node snapshot was not preserved: %+v", nodes)
	}
}

func TestParsedNodeSecretsAreNotStoredAsPlaintext(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "state.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		database,
		box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatal(err)
	}
	const marker = "node-secret-6ec8f73a"
	created, err := service.Create(ctx, CreateRequest{
		Name:         "encrypted-node",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "trojan://" + marker + "@secret.example.com:443#encrypted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{databasePath, databasePath + "-wal"} {
		content, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", candidate, err)
		}
		if bytes.Contains(content, []byte(marker)) {
			t.Fatalf("plaintext node credential found in %s", candidate)
		}
	}
}

func TestParsedRefreshRebuildsNodesWhenRawSnapshotIsReused(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	legacyService, err := NewService(
		database,
		box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := legacyService.Create(ctx, CreateRequest{
		Name:         "legacy",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "vless://id@example.com:443#node"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyService.Refresh(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	parsedService, err := NewService(
		database,
		box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := parsedService.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatalf("parsed Refresh() error = %v", err)
	}
	if result.Changed {
		t.Fatal("reused snapshot should not be reported as changed")
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
}
