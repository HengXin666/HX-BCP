package subscription

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// This is the end-to-end claim of the proxy-list feature, asserted against a
// real SQLite store, the real loader and the real parser — not against stubs:
// a flat proxy list pasted as an Inline source must become deduplicated nodes
// that the rest of the control plane can select, probe and export.
//
// See .agents/notes/implemented/feature/2026-09-26-proxy-list-pool-subscription-source.md

func newProxyListService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "state.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
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
	return service, database, databasePath
}

func TestInlineProxyListRefreshImportsEveryEndpoint(t *testing.T) {
	ctx := context.Background()
	service, database, _ := newProxyListService(t)

	created, err := service.Create(ctx, CreateRequest{
		Name:         "flat-pool",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "1.2.3.4:8080\n5.6.7.8:3128\n9.9.9.9:1080\n"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.EstimatedNodes != 3 {
		t.Fatalf("EstimatedNodes = %d, want 3", result.EstimatedNodes)
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("stored nodes = %d, want 3", len(nodes))
	}
	// A bare "host:port" carries no protocol, so the documented default applies.
	for _, node := range nodes {
		if node.Protocol != "http" {
			t.Fatalf("node %s protocol = %q, want http", node.DisplayName, node.Protocol)
		}
		if node.LifecycleState != "candidate" {
			t.Fatalf("node %s lifecycle = %q, want candidate", node.DisplayName, node.LifecycleState)
		}
	}
}

// The original defective shape, asserted on the real pipeline: an annotated
// vendor export used to import zero nodes.
func TestInlineAnnotatedShareURIListRefreshImportsEveryEndpoint(t *testing.T) {
	ctx := context.Background()
	service, database, _ := newProxyListService(t)

	created, err := service.Create(ctx, CreateRequest{
		Name:       "annotated-pool",
		SourceType: SourceInline,
		SourceConfig: SourceConfig{Inline: "socks5://184.178.172.13:4145#住宅-风险81% | AS22773 - Cox Communications Inc.\n" +
			"OK|http|http://user:pass@65.111.9.15:3129|65.111.9.15\n"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.EstimatedNodes != 2 {
		t.Fatalf("EstimatedNodes = %d, want 2 (was 0 before the list reader)", result.EstimatedNodes)
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	protocols := map[string]int{}
	for _, node := range nodes {
		protocols[node.Protocol]++
	}
	if protocols["socks5"] != 1 || protocols["http"] != 1 {
		t.Fatalf("protocol histogram = %v, want one socks5 and one http", protocols)
	}
}

func TestInlineProxyListRefreshDeduplicatesRepeatedEndpoints(t *testing.T) {
	ctx := context.Background()
	service, database, _ := newProxyListService(t)

	created, err := service.Create(ctx, CreateRequest{
		Name:         "dedup-pool",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "1.2.3.4:8080\n1.2.3.4:8080\n1.2.3.4:8080\n"},
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
	if len(nodes) != 1 {
		t.Fatalf("stored nodes = %d, want 1", len(nodes))
	}
}

// A list that contains nothing usable must fail the refresh instead of silently
// publishing an empty pool — the failure mode that makes a "working" service
// accept connections and route nothing.
func TestInlineProxyListRefreshRejectsADocumentWithNoEndpoints(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newProxyListService(t)

	created, err := service.Create(ctx, CreateRequest{
		Name:         "prose-pool",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "this document has no endpoints in it at all\n"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.Refresh(ctx, created.ID); err == nil {
		t.Fatal("Refresh() accepted a document with no endpoints")
	}
}

// The list document is source material, so it is stored encrypted exactly like
// a pasted subscription: an endpoint's credentials must not sit in plaintext.
func TestInlineProxyListCredentialsAreNotStoredAsPlaintext(t *testing.T) {
	ctx := context.Background()
	service, _, databasePath := newProxyListService(t)

	const marker = "list-secret-6ec8f73a"
	created, err := service.Create(ctx, CreateRequest{
		Name:         "credential-pool",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "1.2.3.4:8080:list-user:" + marker + "\n"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.Refresh(ctx, created.ID); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	// Asserted the way the sibling secrecy test does it: over the database files
	// themselves, because that is what "stored encrypted" actually claims.
	for _, candidate := range []string{databasePath, databasePath + "-wal"} {
		content, readErr := os.ReadFile(candidate)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			t.Fatalf("read %s: %v", candidate, readErr)
		}
		if bytes.Contains(content, []byte(marker)) {
			t.Fatalf("plaintext endpoint credential found in %s", candidate)
		}
	}
}
