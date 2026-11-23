package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// ptySession is one local PTY-backed shell. Lifecycle ownership lives in the
// trackedSession wrapper so local and helper-backed sessions behave alike.
type ptySession struct {
	pty     *os.File
	command *exec.Cmd

	mutex  sync.Mutex
	closed bool
}

// Mode describes the kernel-managed PTY input mode. Clients may only predict
// local echo while both flags are true; password prompts and full-screen
// programs disable at least Echo or Canonical.
type Mode struct {
	Echo      bool
	Canonical bool
}

// trackedSession adds lifecycle bookkeeping shared by all terminal backends.
// Its methods are intentionally small so lifecycle accounting is identical
// when a remote helper is selected.
type trackedSession struct {
	Session
	id         string
	startedAt  time.Time
	lastActive time.Time

	mutex   sync.Mutex
	closed  bool
	onClose func(*trackedSession, string)
}

func (s *trackedSession) Read(buffer []byte) (int, error) {
	count, err := s.Session.Read(buffer)
	if count > 0 {
		s.touch()
	}
	return count, err
}

func (s *trackedSession) Write(data []byte) (int, error) {
	s.touch()
	return s.Session.Write(data)
}

func (s *trackedSession) Close(cause string) {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.closed = true
	onClose := s.onClose
	s.mutex.Unlock()

	s.Session.Close(cause)
	if onClose != nil {
		onClose(s, cause)
	}
}

// CwdReporter is implemented by PTY sessions that can report the shell's real
// working directory from the kernel. The web UI follows it, so no probe command
// is ever typed into the user's session and the shell history stays clean.
type CwdReporter interface {
	Cwd() (string, error)
}

// Cwd reads the shell process's working directory from /proc. It is a
// directory the *shell* changed into, so it is correct for bash, zsh, fish and
// plain sh alike, and for subshells whose parent stayed put.
func (s *ptySession) Cwd() (string, error) {
	if s.command == nil || s.command.Process == nil {
		return "", errors.New("shell process is not running")
	}
	return processCwd(s.command.Process.Pid, s.childPid())
}

// childPid returns the foreground process group leader when the shell has
// spawned a child (an interactive program, a pipeline). The shell's own cwd is
// still authoritative, so this is only used to keep the read cheap.
func (s *ptySession) childPid() int {
	if s.pty == nil {
		return 0
	}
	pid, err := unix.IoctlGetInt(int(s.pty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return 0
	}
	return pid
}

// processCwd resolves /proc/<pid>/cwd. Errors are returned so callers can fall
// back to the last known directory instead of guessing.
func processCwd(pid, foregroundGroup int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid shell process id")
	}
	target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err == nil {
		if resolved := usableCwd(target); resolved != "" {
			return resolved, nil
		}
	}
	// The shell process may have exited while a foreground child still holds
	// the terminal; fall back to that child's directory before giving up.
	if foregroundGroup > 0 && foregroundGroup != pid {
		childTarget, childErr := os.Readlink("/proc/" + strconv.Itoa(foregroundGroup) + "/cwd")
		if childErr != nil {
			return "", childErr
		}
		if resolved := usableCwd(childTarget); resolved != "" {
			return resolved, nil
		}
	}
	if err != nil {
		return "", err
	}
	return "", errors.New("shell process has no usable working directory")
}

func usableCwd(target string) string {
	target = strings.TrimSpace(target)
	if target == "" || !filepath.IsAbs(target) || strings.ContainsRune(target, 0) {
		return ""
	}
	return target
}

// Cwd forwards to the wrapped session so lifecycle bookkeeping never hides the
// kernel-reported directory.
func (s *trackedSession) Cwd() (string, error) {
	if reporter, ok := s.Session.(CwdReporter); ok {
		return reporter.Cwd()
	}
	return "", errors.New("session does not report its working directory")
}

// startShell launches the login shell inside a new PTY. startDir is the
// optionally persisted last session directory; it wins over HOME when valid so
// a reconnect or re-login resumes where the previous session ended.
func startShell(shell string, environment []string, persistHistory bool, startDir, runtimeDirectory string) (*os.File, *exec.Cmd, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	// Shell integration is injected without editing the user's rc files: bash
	// gets a generated --rcfile that sources the original one, zsh gets a
	// ZDOTDIR shim. It only turns on OSC 7 reporting; no probe command is ever
	// typed on the user's behalf.
	family := shellFamily(shell)
	var scriptPath string
	var integrationEnv []string
	if !ShellIntegrationDisabled(environment) {
		scriptPath, integrationEnv = writeShellIntegration(runtimeDirectory, shell)
	}
	command := exec.Command(shell, integrationArgs(family, scriptPath, environment)...)
	command.Env = safeShellEnvironment(environment, shell, persistHistory, scriptPath, integrationEnv)
	command.Dir = resolveShellStartDir(startDir, environmentValue(environment, "HOME"))
	ptyFile, err := pty.Start(command)
	if err != nil {
		return nil, nil, fmt.Errorf("start shell pty: %w", err)
	}
	return ptyFile, command, nil
}

// resolveShellStartDir picks the working directory for a new shell. The
// persisted last-cwd wins when it is a usable absolute directory (the previous
// session's directory may legitimately have been removed since); otherwise the
// user's HOME is used. An empty result leaves the shell in the helper's own
// working directory, matching the pre-persistence behavior.
func resolveShellStartDir(startDir, home string) string {
	if usable := usableDirectory(startDir); usable != "" {
		return usable
	}
	return usableDirectory(home)
}

// usableDirectory returns the path when it is a valid absolute directory the
// shell may start in, otherwise "".
func usableDirectory(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || len(path) > 4096 {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ""
	}
	return path
}

func newPTYSession(ptyFile *os.File, command *exec.Cmd) *ptySession {
	return &ptySession{pty: ptyFile, command: command}
}

// safeShellEnvironment prevents application credentials or deployment
// controls from leaking into an interactive shell. It receives only
// conventional locale and identity values, regardless of the PTY backend.
func safeShellEnvironment(environment []string, shell string, persistHistory bool, integrationScript string, integrationEnv []string) []string {
	allowed := map[string]struct{}{
		"HOME": {}, "USER": {}, "LOGNAME": {}, "PATH": {}, "LANG": {}, "TZ": {},
	}
	values := make(map[string]string)
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, ok := allowed[name]; ok || strings.HasPrefix(name, "LC_") {
			values[name] = value
		}
	}
	values["TERM"] = "xterm-256color"
	values["COLORTERM"] = "truecolor"
	values["SHELL"] = shell
	// Hand the shell integration shim the original rc locations before the
	// generated file replaces the shell's startup path.
	if integrationScript != "" {
		switch shellFamily(shell) {
		case "bash":
			values["HX_ORIGINAL_BASHRC"] = bashOriginalRc(environment)
		case "zsh":
			if dir := environmentValue(environment, "ZDOTDIR"); dir != "" {
				values["HX_ORIGINAL_ZDOTDIR"] = dir
			} else if home := environmentValue(environment, "HOME"); home != "" {
				values["HX_ORIGINAL_ZDOTDIR"] = home
			}
		}
	}
	for _, entry := range integrationEnv {
		name, value, found := strings.Cut(entry, "=")
		if found && name != "" {
			values[name] = value
		}
	}
	if persistHistory {
		if strings.Contains(shell, "zsh") {
			if home := environmentValue(environment, "HOME"); home != "" {
				values["HISTFILE"] = filepath.Join(home, ".zsh_history")
				values["SAVEHIST"] = "10000"
				values["HISTSIZE"] = "2000"
			}
		}
		// Other shells (bash, sh) keep their built-in history defaults: bash
		// writes ~/.bash_history whenever HOME is set, so no HISTFILE override
		// is needed and the user's own rc settings still apply.
	} else {
		values["HISTFILE"] = "/dev/null"
	}
	result := make([]string, 0, len(values))
	for _, name := range []string{
		"HOME", "USER", "LOGNAME", "PATH", "LANG", "TZ", "TERM", "COLORTERM", "SHELL",
		"HISTFILE", "SAVEHIST", "HISTSIZE", "HISTCONTROL", "HIST_IGNORE_SPACE", "ZDOTDIR",
		"HX_ORIGINAL_BASHRC", "HX_ORIGINAL_ZDOTDIR",
	} {
		if value, ok := values[name]; ok {
			result = append(result, name+"="+value)
			delete(values, name)
		}
	}
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	return result
}

// integrationArgs picks the shell flags that source the generated fragrment
// while preserving the user's own interactive configuration.
func integrationArgs(family, scriptPath string, environment []string) []string {
	if scriptPath == "" {
		return nil
	}
	switch family {
	case "bash":
		// --rcfile replaces ~/.bashrc, so the generated file sources the
		// original first (HX_ORIGINAL_BASHRC). Without a readable original the
		// shell still starts with the OSC 7 hook installed.
		return []string{"--rcfile", scriptPath}
	default:
		return nil
	}
}

// bashOriginalRc resolves the rc file an interactive bash would have read, so
// the generated shim can source it before installing the OSC 7 hook.
func bashOriginalRc(environment []string) string {
	if home := environmentValue(environment, "HOME"); home != "" {
		candidate := filepath.Join(home, ".bashrc")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	if info, err := os.Stat("/etc/bash.bashrc"); err == nil && info.Mode().IsRegular() {
		return "/etc/bash.bashrc"
	}
	return ""
}

func environmentValue(environment []string, target string) string {
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found && name == target {
			return value
		}
	}
	return ""
}

// Read streams PTY output; it blocks like a file read and returns an error
// after Close.
func (s *ptySession) Read(buffer []byte) (int, error) {
	return s.pty.Read(buffer)
}

// Write feeds user keystrokes into the shell.
func (s *ptySession) Write(data []byte) (int, error) {
	return s.pty.Write(data)
}

// TerminalMode reads the current line discipline flags directly from the PTY.
func (s *ptySession) TerminalMode() (Mode, error) {
	settings, err := unix.IoctlGetTermios(int(s.pty.Fd()), unix.TCGETS)
	if err != nil {
		return Mode{}, fmt.Errorf("read terminal mode: %w", err)
	}
	return Mode{
		Echo:      settings.Lflag&unix.ECHO != 0,
		Canonical: settings.Lflag&unix.ICANON != 0,
	}, nil
}

// Resize adjusts the PTY window.
func (s *ptySession) Resize(columns, rows int) error {
	if columns < 1 || columns > 1000 || rows < 1 || rows > 1000 {
		return errors.New("terminal size out of range")
	}
	return pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(columns), Rows: uint16(rows)})
}

// Close terminates the shell and the PTY. It is idempotent; the first cause
// wins and is reported to the audit hook. The shell is asked to exit
// gracefully first so bash/zsh can persist their history file, then killed
// after a short grace period so teardown stays bounded.  Interactive bash
// ignores SIGTERM, so SIGHUP is used instead: bash writes history on
// SIGHUP then exits cleanly.
func (s *ptySession) Close(_ string) {
	const waitGrace = 1200 * time.Millisecond
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.closed = true
	s.mutex.Unlock()

	_ = s.pty.Close()
	if s.command.Process == nil {
		return
	}
	_ = s.command.Process.Signal(unix.SIGHUP)
	done := make(chan struct{})
	go func() { _ = s.command.Wait(); close(done) }()
	select {
	case <-done:
		// Normal exit — shell saved history.
	case <-time.After(waitGrace):
		_ = s.command.Process.Kill()
		<-done
	}
}

func (s *trackedSession) touch() {
	s.mutex.Lock()
	s.lastActive = time.Now()
	s.mutex.Unlock()
}

func (s *trackedSession) idleSince() time.Time {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.lastActive
}

// watch enforces the optional idle timeout and the absolute lifetime cap with a
// single goroutine per session that exits when the session closes.
func (s *trackedSession) watch(ctx context.Context, idleTimeout, maxLifetime time.Duration) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline := s.startedAt.Add(maxLifetime)
	for {
		select {
		case <-ctx.Done():
			s.Close("connection closed")
			return
		case <-ticker.C:
			s.mutex.Lock()
			closed := s.closed
			lastActive := s.lastActive
			s.mutex.Unlock()
			if closed {
				return
			}
			now := time.Now()
			if idleTimeout > 0 && now.Sub(lastActive) >= idleTimeout {
				s.Close("idle timeout")
				return
			}
			if maxLifetime > 0 && now.After(deadline) {
				s.Close("maximum session lifetime reached")
				return
			}
		}
	}
}
