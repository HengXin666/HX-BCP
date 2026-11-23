package terminal

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// expectFrame reads one frame from connection with a bounded deadline.
func expectFrame(t *testing.T, connection net.Conn, want byte) []byte {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, payload, err := readFrame(connection)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if kind != want {
		t.Fatalf("frame kind = %d, want %d", kind, want)
	}
	return payload
}

// newTestScheduler returns an updateScheduler whose privileged gates and
// updater are replaced with test doubles. systemdActive reports whether the
// fixed-name update unit appears active; runError makes the updater fail.
func newTestScheduler(t *testing.T, systemdActive bool, runError error) *updateScheduler {
	t.Helper()
	scheduler := newUpdateScheduler("/usr/local/sbin/hx-pxy-install", discardLogger())
	scheduler.validatePath = func(string) bool { return true }
	scheduler.systemdActive = func(context.Context) bool { return systemdActive }
	scheduler.runUpdater = func(context.Context) error { return runError }
	return scheduler
}

// waitNotInFlight polls until the scheduler slot is released (the detached
// schedule goroutine finished) or the deadline expires.
func waitNotInFlight(t *testing.T, scheduler *updateScheduler) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		scheduler.mutex.Lock()
		inFlight := scheduler.inFlight
		scheduler.mutex.Unlock()
		if !inFlight {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("update scheduler slot was not released")
}

// TestUpdateSchedulerAcknowledgesBeforeScheduling reproduces the production
// bug: the old helper replied only after systemd-run returned, so a slow
// systemd made the control plane time out with
// "read .../terminal.sock: i/o timeout". The request must be acknowledged
// while the updater is still blocked.
func TestUpdateSchedulerAcknowledgesBeforeScheduling(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	scheduler := newTestScheduler(t, false, nil)
	scheduler.runUpdater = func(context.Context) error {
		started <- struct{}{}
		<-block
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, server := net.Pipe()
	serveDone := make(chan struct{})
	go func() {
		scheduler.serve(ctx, server)
		close(serveDone)
	}()

	// The handshake must complete while the updater is still blocked.
	if payload := expectFrame(t, client, frameReady); len(payload) != 0 {
		t.Fatalf("unexpected frameReady payload: %q", payload)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("updater was never started")
	}
	close(block)
	<-serveDone
}

// TestUpdateSchedulerRejectsConcurrentRequest verifies the single-flight
// guard: while one update is in flight a second request is refused with a
// clear error, and the slot is released once the first finishes.
func TestUpdateSchedulerRejectsConcurrentRequest(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	scheduler := newTestScheduler(t, false, nil)
	scheduler.runUpdater = func(context.Context) error {
		started <- struct{}{}
		<-block
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientOne, serverOne := net.Pipe()
	go scheduler.serve(ctx, serverOne)
	expectFrame(t, clientOne, frameReady)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first updater was never started")
	}

	// Second request while the first is in flight must be refused.
	clientTwo, serverTwo := net.Pipe()
	go scheduler.serve(ctx, serverTwo)
	if payload := expectFrame(t, clientTwo, frameError); !strings.Contains(string(payload), "already in progress") {
		t.Fatalf("duplicate refusal payload = %q", payload)
	}

	close(block)
	waitNotInFlight(t, scheduler)

	// After the first update finished, a new request is accepted again.
	clientThree, serverThree := net.Pipe()
	go scheduler.serve(ctx, serverThree)
	expectFrame(t, clientThree, frameReady)
}

// TestUpdateSchedulerRefusesWhenSystemdUpdateActive verifies the
// cross-restart guard: when systemd still reports the fixed-name update unit
// active (e.g. the helper was restarted mid-update and its in-memory flag
// reset), the request is refused without touching the updater.
func TestUpdateSchedulerRefusesWhenSystemdUpdateActive(t *testing.T) {
	called := false
	scheduler := newTestScheduler(t, true, nil)
	scheduler.runUpdater = func(context.Context) error {
		called = true
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, server := net.Pipe()
	go scheduler.serve(ctx, server)
	if payload := expectFrame(t, client, frameError); !strings.Contains(string(payload), "already in progress") {
		t.Fatalf("refusal payload = %q", payload)
	}
	if called {
		t.Fatal("updater must not run while systemd reports an active update")
	}
}

// TestUpdateSchedulerRefusesInvalidUpdater verifies the privileged gate with
// the real validation: an updater path that is not a root-owned regular file
// is refused before anything is scheduled.
func TestUpdateSchedulerRefusesInvalidUpdater(t *testing.T) {
	scheduler := newUpdateScheduler(filepath.Join(t.TempDir(), "missing-updater"), discardLogger())
	scheduler.systemdActive = func(context.Context) bool { return false }
	scheduler.runUpdater = func(context.Context) error {
		t.Fatal("updater must not run for an invalid updater path")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, server := net.Pipe()
	go scheduler.serve(ctx, server)
	if payload := expectFrame(t, client, frameError); !strings.Contains(string(payload), "automatic updater is unavailable") {
		t.Fatalf("refusal payload = %q", payload)
	}
}

// TestUpdateSchedulerLogsScheduleFailure verifies that a failed systemd-run
// is logged and does not leave the scheduler slot occupied.
func TestUpdateSchedulerLogsScheduleFailure(t *testing.T) {
	var buffer syncBuffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	scheduler := newUpdateScheduler("/usr/local/sbin/hx-pxy-install", logger)
	scheduler.validatePath = func(string) bool { return true }
	scheduler.systemdActive = func(context.Context) bool { return false }
	scheduler.runUpdater = func(context.Context) error { return errors.New("systemd-run failed") }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, server := net.Pipe()
	serveDone := make(chan struct{})
	go func() {
		scheduler.serve(ctx, server)
		close(serveDone)
	}()
	expectFrame(t, client, frameReady)
	<-serveDone
	waitNotInFlight(t, scheduler)

	if !strings.Contains(buffer.String(), "automatic update scheduling failed") {
		t.Fatalf("expected failure log, got: %q", buffer.String())
	}
}

// TestPrivilegedHelperRejectsInvalidUpdater runs the full helper and checks
// that a frameUpdate request against an invalid updater path is answered with
// a frameError instead of being scheduled.
func TestPrivilegedHelperRejectsInvalidUpdater(t *testing.T) {
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
			UpdaterPath: filepath.Join(root, "missing-updater"),
		}, discardLogger())
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

	connection, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial helper: %v", err)
	}
	defer connection.Close()
	if err := writeFrame(connection, frameUpdate, nil); err != nil {
		t.Fatalf("write update request: %v", err)
	}
	payload := expectFrame(t, connection, frameError)
	if !strings.Contains(string(payload), "automatic updater is unavailable") {
		t.Fatalf("refusal payload = %q", payload)
	}
}

type syncBuffer struct {
	mutex sync.Mutex
	data  strings.Builder
}

func (b *syncBuffer) Write(chunk []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.data.Write(chunk)
}

func (b *syncBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.data.String()
}
