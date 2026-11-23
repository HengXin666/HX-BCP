package mihomo

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestExternalManagerReloadsWithoutOwningDataPlaneLifetime(t *testing.T) {
	manager, requests, closeServer := newExternalManager(t, nil)
	ctx := context.Background()
	if err := manager.Apply(ctx); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if requests.Load() != 1 || !manager.Status().Running {
		t.Fatalf("reloads = %d, status = %+v", requests.Load(), manager.Status())
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !manager.Status().Running {
		t.Fatal("closing the control-plane manager stopped the external data plane")
	}
	closeServer()
}

func TestExternalManagerRestoresPreviousConfigAfterReloadFailure(t *testing.T) {
	var requestNumber atomic.Int32
	manager, _, closeServer := newExternalManager(t, func(response http.ResponseWriter, _ *http.Request) {
		if requestNumber.Add(1) == 2 {
			http.Error(response, "injected reload failure", http.StatusInternalServerError)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
	defer closeServer()
	ctx := context.Background()
	if err := manager.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(manager.activePath)
	if err != nil {
		t.Fatal(err)
	}
	manager.compiler.setEgressInterface("lo")
	if err := manager.Apply(ctx); err == nil {
		t.Fatal("Apply() succeeded despite injected reload failure")
	}
	restored, err := os.ReadFile(manager.activePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(first) {
		t.Fatal("active config was not restored after reload failure")
	}
	if requestNumber.Load() != 3 {
		t.Fatalf("reload request count = %d, want failed apply plus rollback", requestNumber.Load())
	}
}

func newExternalManager(t *testing.T, handler http.HandlerFunc) (*Manager, *atomic.Int32, func()) {
	t.Helper()
	directory := t.TempDir()
	runtimeDirectory := filepath.Join(directory, "runtime")
	if err := os.MkdirAll(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo 'Mihomo test'; fi\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(context.Background(), filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	box, _ := secret.New(make([]byte, 32))
	compiler, err := NewCompiler(database, box)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(compiler, binary, filepath.Join(runtimeDirectory, "active.yaml"), slog.New(slog.NewTextHandler(io.Discard, nil)), WithExternalProcess(true))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", manager.controllerSocket)
	if err != nil {
		t.Fatal(err)
	}
	requestCount := &atomic.Int32{}
	if handler == nil {
		handler = func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) }
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		handler(response, request)
	}), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	closeServer := func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}
	return manager, requestCount, closeServer
}
