package terminal

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// memoryCwdStore is an in-memory CwdStore that counts writes so tests can
// assert persistence and throttling.
type memoryCwdStore struct {
	mutex  sync.Mutex
	values map[string]string
	writes int
}

func (m *memoryCwdStore) GetMetadata(_ context.Context, key string) (string, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	value, ok := m.values[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (m *memoryCwdStore) SetMetadata(_ context.Context, key, value string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.values[key] = value
	m.writes++
	return nil
}

func (m *memoryCwdStore) writeCount() int {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.writes
}

func TestDisabledServiceRefusesSessions(t *testing.T) {
	service, err := NewService(Config{Enabled: false}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if service.Enabled() {
		t.Fatal("terminal must be disabled by default")
	}
	if _, err := service.Open(context.Background(), "admin", "127.0.0.1"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestDefaultLifecycleDisablesIdleTimeoutAndAbsoluteCap(t *testing.T) {
	service, err := NewService(Config{}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	if status.IdleTimeoutSec != 0 {
		t.Fatalf("idle timeout = %d, want disabled", status.IdleTimeoutSec)
	}
	if status.MaxLifetimeSec != 0 {
		t.Fatalf("max lifetime = %d, want unlimited (0)", status.MaxLifetimeSec)
	}
}

func TestSessionEchoAndClose(t *testing.T) {
	service, err := NewService(Config{Enabled: true, Shell: "/bin/sh"}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := service.Open(ctx, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer session.Close("test done")

	if _, err := session.Write([]byte("echo terminal-roundtrip-ok\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	var output strings.Builder
	buffer := make([]byte, 4096)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		count, readErr := session.Read(buffer)
		if count > 0 {
			output.WriteString(string(buffer[:count]))
			if strings.Contains(output.String(), "terminal-roundtrip-ok") {
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	if !strings.Contains(output.String(), "terminal-roundtrip-ok") {
		t.Fatalf("shell output missing echo, got: %q", output.String())
	}

	if status := service.Status(); status.ActiveSessions != 1 {
		t.Fatalf("expected 1 active session, got %d", status.ActiveSessions)
	}
	session.Close("test done")
	// Close is synchronous for bookkeeping; the audit hook released the slot.
	if status := service.Status(); status.ActiveSessions != 0 {
		t.Fatalf("expected 0 active sessions after close, got %d", status.ActiveSessions)
	}
	// Idempotent close.
	session.Close("again")
}

func TestSessionLimit(t *testing.T) {
	service, err := NewService(Config{Enabled: true, Shell: "/bin/sh", MaxSessions: 1}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := service.Open(ctx, "admin", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close("test done")
	if _, err := service.Open(ctx, "admin", "127.0.0.1"); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("expected ErrSessionLimit, got %v", err)
	}
	first.Close("test done")
	second, err := service.Open(ctx, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("slot must be released after close: %v", err)
	}
	second.Close("test done")
}

func TestResizeValidation(t *testing.T) {
	service, err := NewService(Config{Enabled: true, Shell: "/bin/sh"}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Open(context.Background(), "admin", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close("test done")
	if err := session.Resize(0, 10); err == nil {
		t.Fatal("zero columns must be rejected")
	}
	if err := session.Resize(5000, 10); err == nil {
		t.Fatal("oversized terminal must be rejected")
	}
	if err := session.Resize(80, 24); err != nil {
		t.Fatalf("valid resize failed: %v", err)
	}
}

func TestTerminalModeTracksEchoAndCanonicalState(t *testing.T) {
	service, err := NewService(Config{Enabled: true, Shell: "/bin/sh"}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Open(context.Background(), "admin", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close("test done")

	mode, err := session.TerminalMode()
	if err != nil {
		t.Fatal(err)
	}
	if !mode.Echo || !mode.Canonical {
		t.Fatalf("initial terminal mode = %+v, want echo + canonical", mode)
	}

	if _, err := session.Write([]byte("stty -echo\n")); err != nil {
		t.Fatal(err)
	}
	mode = waitTerminalMode(t, session, func(mode Mode) bool { return !mode.Echo })
	if mode.Echo {
		t.Fatalf("disabled terminal mode = %+v, want echo disabled", mode)
	}
}

func waitTerminalMode(t *testing.T, session Session, matches func(Mode) bool) Mode {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last Mode
	for time.Now().Before(deadline) {
		mode, err := session.TerminalMode()
		if err != nil {
			t.Fatal(err)
		}
		last = mode
		if matches(mode) {
			return mode
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal mode did not reach expected state; last = %+v", last)
	return Mode{}
}

func TestSafeShellEnvironmentDropsApplicationSecrets(t *testing.T) {
	environment := safeShellEnvironment([]string{
		"HOME=/srv/hx",
		"USER=hx-pxy",
		"PATH=/usr/bin:/bin",
		"LANG=C.UTF-8",
		"LC_TIME=C",
		"HX_PXY_MASTER_KEY=must-not-leak",
		"DATABASE_URL=sqlite-secret",
		"AUTHORIZATION=Bearer-secret",
	}, "/bin/sh", false, "", nil)
	joined := strings.Join(environment, "\n")
	for _, secret := range []string{"must-not-leak", "sqlite-secret", "Bearer-secret", "HX_PXY_MASTER_KEY", "DATABASE_URL", "AUTHORIZATION"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("safe environment leaks %q: %s", secret, joined)
		}
	}
	for _, expected := range []string{"HOME=/srv/hx", "PATH=/usr/bin:/bin", "LC_TIME=C", "TERM=xterm-256color", "HISTFILE=/dev/null"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("safe environment missing %q: %s", expected, joined)
		}
	}
}

func TestStartShellResumesRequestedDirectory(t *testing.T) {
	home := t.TempDir()
	startDir := t.TempDir()

	file, command, err := startShell("/bin/sh", []string{"HOME=" + home}, false, startDir, "")
	if err != nil {
		t.Fatal(err)
	}
	newPTYSession(file, command).Close("test done")
	if command.Dir != startDir {
		t.Fatalf("shell start dir = %q, want %q", command.Dir, startDir)
	}

	// A vanished or relative start dir must fall back to HOME, never be
	// passed through to the shell.
	for _, invalid := range []string{filepath.Join(startDir, "does-not-exist"), "relative/dir", "", "/tmp/\x00bad"} {
		file, command, err = startShell("/bin/sh", []string{"HOME=" + home}, false, invalid, "")
		if err != nil {
			t.Fatalf("startShell(%q): %v", invalid, err)
		}
		newPTYSession(file, command).Close("test done")
		if command.Dir != home {
			t.Fatalf("invalid start dir %q resolved to %q, want HOME %q", invalid, command.Dir, home)
		}
	}
}

func TestCwdReportPersistsAndResumesNextSession(t *testing.T) {
	store := &memoryCwdStore{values: map[string]string{}}
	service, err := NewService(Config{Enabled: true, Shell: "/bin/sh", CwdStore: store}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	startDir := t.TempDir()
	service.ReportCwd(context.Background(), "admin", startDir)

	// A fresh service (no in-memory cache) must pick the directory up from the
	// store: this is the re-login / control-plane restart path.
	resumed, err := NewService(Config{Enabled: true, Shell: "/bin/sh", CwdStore: store}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	session, err := resumed.Open(context.Background(), "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("open resumed session: %v", err)
	}
	defer session.Close("test done")
	if _, err := session.Write([]byte("pwd\n")); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	buffer := make([]byte, 4096)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(output.String(), startDir) {
		count, readErr := session.Read(buffer)
		if count > 0 {
			output.WriteString(string(buffer[:count]))
		}
		if readErr != nil {
			break
		}
	}
	if !strings.Contains(output.String(), startDir) {
		t.Fatalf("resumed shell did not start in %q; output: %q", startDir, output.String())
	}
}

func TestCwdReportThrottlesPersistence(t *testing.T) {
	previous := cwdPersistInterval
	cwdPersistInterval = 50 * time.Millisecond
	defer func() { cwdPersistInterval = previous }()

	store := &memoryCwdStore{values: map[string]string{}}
	service, err := NewService(Config{Enabled: true, CwdStore: store}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := t.TempDir()
	second := t.TempDir()
	third := t.TempDir()

	service.ReportCwd(ctx, "admin", first)
	if writes := store.writeCount(); writes != 1 {
		t.Fatalf("first report writes = %d, want 1", writes)
	}
	// A second report inside the throttle window must not persist again.
	service.ReportCwd(ctx, "admin", second)
	if writes := store.writeCount(); writes != 1 {
		t.Fatalf("throttled report writes = %d, want 1", writes)
	}
	// After the window, a changed directory persists.
	time.Sleep(80 * time.Millisecond)
	service.ReportCwd(ctx, "admin", third)
	if writes := store.writeCount(); writes != 2 {
		t.Fatalf("post-window report writes = %d, want 2", writes)
	}
	// A report of the same directory never rewrites even after the window.
	time.Sleep(80 * time.Millisecond)
	service.ReportCwd(ctx, "admin", third)
	if writes := store.writeCount(); writes != 2 {
		t.Fatalf("unchanged report writes = %d, want 2", writes)
	}

	// Invalid inputs never write.
	before := store.writeCount()
	service.ReportCwd(ctx, "admin", "relative/path")
	service.ReportCwd(ctx, "admin", "")
	service.ReportCwd(ctx, "admin", "/tmp/bad\x00path")
	service.ReportCwd(ctx, "", first)
	if writes := store.writeCount(); writes != before {
		t.Fatalf("invalid reports wrote %d times, want 0", writes-before)
	}
}

func TestCwdReportCachedForConcurrentSessions(t *testing.T) {
	store := &memoryCwdStore{values: map[string]string{}}
	service, err := NewService(Config{Enabled: true, CwdStore: store}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	startDir := t.TempDir()
	service.ReportCwd(context.Background(), "admin", startDir)
	// Same process: the in-memory cache serves the value without a store read.
	if got := service.resumeCwd(context.Background(), "admin"); got != startDir {
		t.Fatalf("resumeCwd = %q, want %q", got, startDir)
	}
}

func TestSafeShellEnvironmentPersistsHistory(t *testing.T) {
	base := []string{
		"HOME=/srv/hx",
		"USER=hx-pxy",
		"PATH=/usr/bin:/bin",
	}
	// Bash keeps its default ~/.bash_history when history is enabled.
	joined := strings.Join(safeShellEnvironment(base, "/bin/bash", true, "", nil), "\n")
	if strings.Contains(joined, "HISTFILE=/dev/null") {
		t.Fatalf("enabled history must not point HISTFILE at /dev/null: %s", joined)
	}
	// zsh needs an explicit HISTFILE plus SAVEHIST to persist history.
	joined = strings.Join(safeShellEnvironment(base, "/bin/zsh", true, "", nil), "\n")
	for _, expected := range []string{"HISTFILE=/srv/hx/.zsh_history", "SAVEHIST=10000", "HISTSIZE=2000"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("zsh history environment missing %q: %s", expected, joined)
		}
	}
	// The no-history lockdown is preserved when explicitly disabled.
	joined = strings.Join(safeShellEnvironment(base, "/bin/zsh", false, "", nil), "\n")
	if !strings.Contains(joined, "HISTFILE=/dev/null") {
		t.Fatalf("disabled history must keep HISTFILE=/dev/null: %s", joined)
	}
	if strings.Contains(joined, "SAVEHIST") {
		t.Fatalf("disabled history must not set zsh save variables: %s", joined)
	}
}
