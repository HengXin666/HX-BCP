package terminal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivilegedHelperRoundTrip(t *testing.T) {
	root := t.TempDir()
	socketPath := filepath.Join(root, "terminal.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	helperErrors := make(chan error, 1)
	go func() {
		helperErrors <- RunHelper(ctx, HelperConfig{
			SocketPath:  socketPath,
			Shell:       "/bin/sh",
			MaxSessions: 1,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	waitForSocket(t, socketPath)

	service, err := NewService(Config{
		Enabled:          true,
		PrivilegedSocket: socketPath,
		MaxSessions:      1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Open(context.Background(), "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("open helper session: %v", err)
	}
	if status := service.Status(); !status.Privileged || status.ActiveSessions != 1 {
		t.Fatalf("unexpected privileged status: %+v", status)
	}
	defer session.Close("test done")

	if _, err := session.Write([]byte("printf 'helper-roundtrip-ok\\n'\n")); err != nil {
		t.Fatalf("write helper session: %v", err)
	}
	var output strings.Builder
	buffer := make([]byte, 1024)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		count, readErr := session.Read(buffer)
		if count > 0 {
			output.Write(buffer[:count])
			if strings.Contains(output.String(), "helper-roundtrip-ok") {
				break
			}
		}
		if readErr != nil {
			t.Fatalf("read helper session: %v", readErr)
		}
	}
	if !strings.Contains(output.String(), "helper-roundtrip-ok") {
		t.Fatalf("helper output missing roundtrip marker: %q", output.String())
	}

	session.Close("test done")
	if status := service.Status(); status.ActiveSessions != 0 {
		t.Fatalf("helper session slot was not released: %+v", status)
	}
	cancel()
	select {
	case err := <-helperErrors:
		if err != nil {
			t.Fatalf("stop helper: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not stop after context cancellation")
	}
}

// TestFileOperationsNotStarvedByPTYSessions reproduces the production bug
// behind "read unix .../terminal.sock: i/o timeout" in the file manager: file
// operations used to contend for the same MaxSessions semaphore as PTY
// sessions, so once every slot was held by an open shell the file panel
// starved until a session closed. File operations must be served while the
// session cap is fully occupied.
func TestFileOperationsNotStarvedByPTYSessions(t *testing.T) {
	root := t.TempDir()
	socketPath := filepath.Join(root, "terminal.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	helperErrors := make(chan error, 1)
	go func() {
		helperErrors <- RunHelper(ctx, HelperConfig{
			SocketPath:  socketPath,
			Shell:       "/bin/sh",
			MaxSessions: 1,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	waitForSocket(t, socketPath)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-helperErrors:
			if err != nil {
				t.Errorf("stop helper: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("helper did not stop after context cancellation")
		}
	})

	service, err := NewService(Config{
		Enabled:          true,
		PrivilegedSocket: socketPath,
		MaxSessions:      1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// Occupy the only PTY session slot with a live shell.
	session, err := service.Open(context.Background(), "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("open helper session: %v", err)
	}
	defer session.Close("test done")

	// A file operation must still complete promptly while the slot is taken.
	done := make(chan error, 1)
	go func() {
		entries, listErr := service.ListFiles(context.Background(), root)
		if listErr == nil {
			found := false
			for _, entry := range entries {
				if entry.Name == "terminal.sock" {
					found = true
				}
			}
			if !found {
				listErr = fmt.Errorf("file list missing expected entry: %+v", entries)
			}
		}
		done <- listErr
	}()
	select {
	case listErr := <-done:
		if listErr != nil {
			t.Fatalf("file list starved behind PTY session: %v", listErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("file list did not complete while a PTY session held the only slot")
	}
}

func TestAutomaticUpdateUsesDedicatedHelperFrame(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "update.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan byte, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		kind, _, readErr := readFrame(connection)
		if readErr != nil {
			return
		}
		received <- kind
		_ = writeFrame(connection, frameReady, nil)
	}()
	service, err := NewService(Config{
		PrivilegedSocket: socketPath,
		UpdaterPath:      "/usr/local/sbin/hx-pxy-install",
	}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.TriggerUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kind := <-received; kind != frameUpdate {
		t.Fatalf("helper request frame = %d, want %d", kind, frameUpdate)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal helper socket was not created: %s", path)
}
