package listener

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestNormalizeWebSocketPathReservesEdgeNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "legacy path is prefixed", input: "/edge", want: WebSocketPathPrefix + "edge"},
		{name: "nested legacy path is prefixed", input: "/tenant/edge", want: WebSocketPathPrefix + "tenant/edge"},
		{name: "canonical path is stable", input: WebSocketPathPrefix + "edge", want: WebSocketPathPrefix + "edge"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeWebSocketPath(test.input)
			if err != nil {
				t.Fatalf("NormalizeWebSocketPath() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("NormalizeWebSocketPath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeWebSocketPathRejectsAmbiguousRoutes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"",
		"edge",
		"/__hx-proxy__",
		WebSocketPathPrefix,
		"/edge?format=ws",
		"/edge#fragment",
		"/edge\\other",
		"/edge//other",
		"/edge/../other",
		"/edge%2Fother",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := NormalizeWebSocketPath(input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("NormalizeWebSocketPath(%q) error = %v, want ErrInvalid", input, err)
			}
		})
	}
}

type testReconciler struct {
	calls int
}

func (reconciler *testReconciler) Apply(context.Context) error {
	reconciler.calls++
	return nil
}

func TestNonLoopbackListenerRequiresEncryptedAuthentication(t *testing.T) {
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
	now := time.Now().UTC()
	group, err := database.CreateProxyGroup(ctx, store.ProxyGroupRecord{
		ID:               "group-direct",
		Name:             "direct",
		Strategy:         "manual",
		SourceSpecJSON:   `{"node_ids":[],"include_direct":true}`,
		RulePipelineJSON: "{}",
		Enabled:          true,
		EmptyBehavior:    "fail-closed",
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &testReconciler{}
	service, err := NewService(database, box, reconciler)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Create(ctx, CreateRequest{
		Name:         "unsafe",
		Kind:         "mixed",
		BindAddress:  "0.0.0.0",
		Port:         18080,
		ProxyGroupID: group.ID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}

	const marker = "listener-secret-96cc5607"
	created, err := service.Create(ctx, CreateRequest{
		Name:         "authenticated",
		Kind:         "mixed",
		BindAddress:  "0.0.0.0",
		Port:         18080,
		ProxyGroupID: group.ID,
		Auth: &Auth{
			Username: "operator",
			Password: marker,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created.AuthConfigured || reconciler.calls != 1 {
		t.Fatalf("unexpected listener: %+v, reconcile calls = %d", created, reconciler.calls)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(marker)) || bytes.Contains(encoded, []byte("operator")) {
		t.Fatalf("public listener response contains credentials: %s", encoded)
	}
	for _, candidate := range []string{databasePath, databasePath + "-wal"} {
		content, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte(marker)) {
			t.Fatalf("plaintext listener password found in %s", candidate)
		}
	}
}

func TestLoopbackListenerCanRunWithoutAuthentication(t *testing.T) {
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
	_, err = database.CreateProxyGroup(ctx, store.ProxyGroupRecord{
		ID:               "group-loopback",
		Name:             "loopback",
		Strategy:         "manual",
		SourceSpecJSON:   `{"node_ids":[],"include_direct":true}`,
		RulePipelineJSON: "{}",
		Enabled:          true,
		EmptyBehavior:    "fail-closed",
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &testReconciler{}
	service, err := NewService(database, box, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateRequest{
		Name:         "local",
		Kind:         "http",
		BindAddress:  "127.0.0.1",
		Port:         18081,
		ProxyGroupID: "group-loopback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.AuthConfigured {
		t.Fatal("loopback listener unexpectedly requires authentication")
	}
}

type failingReconciler struct {
	err error
}

func (reconciler *failingReconciler) Apply(context.Context) error {
	return reconciler.err
}

func TestUpdateRestoresDatabaseRecordWhenApplyFails(t *testing.T) {
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
		ID:               "group-atomic",
		Name:             "atomic",
		Strategy:         "manual",
		SourceSpecJSON:   `{"node_ids":[],"include_direct":true}`,
		RulePipelineJSON: "{}",
		Enabled:          true,
		EmptyBehavior:    "fail-closed",
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatal(err)
	}
	reconciler := &failingReconciler{}
	service, err := NewService(database, box, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateRequest{
		Name:         "local",
		Kind:         "http",
		BindAddress:  "127.0.0.1",
		Port:         18082,
		ProxyGroupID: "group-atomic",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	reconciler.err = errors.New("mihomo apply failed")
	_, err = service.Update(ctx, created.ID, UpdateRequest{
		Version:      created.Version,
		Name:         "local",
		Kind:         "http",
		BindAddress:  "0.0.0.0",
		Port:         18082,
		ProxyGroupID: "group-atomic",
		Enabled:      true,
		Auth:         &Auth{Username: "operator", Password: "secret-9f0e"},
	})
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("Update() error = %v, want ErrApplyFailed", err)
	}

	// The database must be restored to the previous record, not left ahead of
	// the data plane: a retry with the same version must not hit a conflict.
	stored, err := database.GetListener(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BindAddress != "127.0.0.1" || stored.Port != 18082 {
		t.Fatalf("listener was not restored: bind = %s:%d, want 127.0.0.1:18082", stored.BindAddress, stored.Port)
	}
	if stored.Version <= created.Version {
		t.Fatalf("restored listener version = %d, want > %d", stored.Version, created.Version)
	}
}
