package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ptyCollector drains a PTY in the background so a test can assert on output
// without ever blocking. Reads are blocking by design, so the collector owns
// the read side while the test drives the shell.
type ptyCollector struct {
	mutex  sync.Mutex
	buffer strings.Builder
	done   chan struct{}
}

func newPtyCollector(session *ptySession) *ptyCollector {
	collector := &ptyCollector{done: make(chan struct{})}
	go func() {
		defer close(collector.done)
		chunk := make([]byte, 4096)
		for {
			count, err := session.Read(chunk)
			if count > 0 {
				collector.mutex.Lock()
				collector.buffer.Write(chunk[:count])
				collector.mutex.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return collector
}

func (c *ptyCollector) text() string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.buffer.String()
}

func (c *ptyCollector) waitFor(substring string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(c.text(), substring) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return strings.Contains(c.text(), substring)
}

// TestShellIntegrationReportsOsc7WithoutPollutingHistory is the regression test
// for the bug this work fixes: the panel used to type "pwd" into the user's
// shell, so every reconnect and every unrecognized command left a line in the
// history the user never wrote.
//
// It starts a real interactive bash through the production startShell path,
// navigates the way the file panel does, and asserts that the history contains
// only what the user typed while OSC 7 still reported the directory.
func TestShellIntegrationReportsOsc7WithoutPollutingHistory(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	home := t.TempDir()
	historyFile := filepath.Join(home, ".bash_history")
	// A minimal rc keeps the test hermetic; the generated fragment must source
	// it and still install the OSC 7 hook.
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export PS1='$ '\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	startDir := t.TempDir()
	environment := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm-256color"}

	ptyFile, command, err := startShell(bash, environment, true, startDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session := newPTYSession(ptyFile, command)
	defer session.Close("test finished")
	collector := newPtyCollector(session)

	if err := session.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if !collector.waitFor("\x1b]7;", 5*time.Second) {
		t.Fatalf("shell integration did not emit OSC 7; output = %q", collector.text())
	}
	if started := collector.text(); !strings.Contains(started, startDir) {
		t.Fatalf("OSC 7 did not report the start directory %q; output = %q", startDir, started)
	}
	if strings.Contains(collector.text(), "pwd") {
		t.Fatalf("a pwd probe leaked into the session; output = %q", collector.text())
	}

	// Simulate file-panel navigation: the leading space keeps the command out
	// of the history file (HISTCONTROL=ignorespace) while the shell still runs
	// it, so the panel works without ever being visible in the user's history.
	target := t.TempDir()
	if _, err := session.Write([]byte(" cd " + target + "\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cwd, cwdErr := session.Cwd(); cwdErr == nil && cwd == target {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cwd, cwdErr := session.Cwd(); cwdErr != nil || cwd != target {
		t.Fatalf("kernel cwd = %q (%v), want the panel target %q", cwd, cwdErr, target)
	}

	// A command the user actually types must still reach the history.
	if _, err := session.Write([]byte("echo user-typed-command\n")); err != nil {
		t.Fatal(err)
	}
	if !collector.waitFor("user-typed-command", 3*time.Second) {
		t.Fatalf("typed command did not run; output = %q", collector.text())
	}
	if _, err := session.Write([]byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-collector.done:
	case <-time.After(3 * time.Second):
	}

	history, err := os.ReadFile(historyFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	text := string(history)
	if strings.Contains(text, " cd ") || strings.Contains(text, "pwd") {
		t.Fatalf("panel navigation or a probe polluted the shell history: %q", text)
	}
	if !strings.Contains(text, "echo user-typed-command") {
		t.Fatalf("typed command missing from the history: %q", text)
	}
}

// TestShellIntegrationCanBeDisabled keeps the escape hatch working.
func TestShellIntegrationCanBeDisabled(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	home := t.TempDir()
	environment := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "HX_PXY_SHELL_INTEGRATION=0"}
	ptyFile, command, err := startShell(bash, environment, false, home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session := newPTYSession(ptyFile, command)
	defer session.Close("test finished")
	collector := newPtyCollector(session)
	if _, err := session.Write([]byte("echo marker-$((1+1))\n")); err != nil {
		t.Fatal(err)
	}
	if !collector.waitFor("marker-2", 3*time.Second) {
		t.Fatalf("shell did not start; output = %q", collector.text())
	}
	if strings.Contains(collector.text(), "\x1b]7;") {
		t.Fatalf("shell integration was disabled but OSC 7 was still emitted: %q", collector.text())
	}
}
