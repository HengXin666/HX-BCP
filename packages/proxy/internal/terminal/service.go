// Package terminal implements the v2 in-browser terminal: PTY sessions on
// the local server bridged to the web UI over WebSocket. It never implements
// a terminal protocol itself — the PTY comes from creack/pty and rendering
// from xterm.js in the frontend.
//
// Safety model (docs/V1_CORE.md 9.3):
//   - enabled by default, with an emergency environment kill switch;
//   - administrator authentication is required unconditionally;
//   - no idle disconnect and no absolute lifetime cap per session;
//   - bounded concurrent sessions;
//   - every session start and end is audit-logged with cause and duration.
package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrDisabled     = errors.New("terminal is disabled")
	ErrSessionLimit = errors.New("too many concurrent terminal sessions")
)

const (
	defaultMaxLifetime   = 0 // 0 means no absolute lifetime cap
	defaultMaxSessions   = 2
	defaultShellSizeCols = 120
	defaultShellSizeRows = 32
)

var (
	// cwdPersistInterval throttles per-actor persistence of the reported shell
	// directory. The client reports on every tracked change (bounded by its own
	// throttle); this server-side floor keeps SQLite writes to at most one per
	// actor per interval while still surviving reconnects and restarts.
	// Declared as a var (not const) so tests can shrink it.
	cwdPersistInterval = 5 * time.Second
)

// CwdStore persists the last known shell directory per administrator so a
// reconnect or a later login resumes in the previous directory. The
// control-plane store implements it with the system_metadata key-value table;
// the store is optional — without one the terminal simply falls back to the
// shell's HOME on every session.
type CwdStore interface {
	GetMetadata(ctx context.Context, key string) (string, error)
	SetMetadata(ctx context.Context, key, value string) error
}

type Config struct {
	// Enabled gates the whole feature. The control-plane default is enabled;
	// callers may explicitly disable it for emergency lockdown.
	Enabled bool
	// Shell overrides the login shell; empty uses $SHELL then /bin/bash.
	Shell string
	// IdleTimeout closes a session with no input/output activity. Zero disables
	// idle disconnection, which is the production default for weak networks.
	IdleTimeout time.Duration
	// MaxLifetime is the absolute per-session cap. Zero (the default for
	// v2) disables the cap entirely; configure a positive value to re-enable
	// a hard ceiling.
	MaxLifetime time.Duration
	// MaxSessions bounds concurrently open sessions.
	MaxSessions int
	// PrivilegedSocket is a local Unix socket served by the optional root PTY
	// helper. When set, terminal sessions are created by that helper so the
	// administrator can use su/sudo without running the control plane as root.
	PrivilegedSocket string
	// PersistHistory keeps shell command history across sessions (bash
	// ~/.bash_history, zsh ~/.zsh_history) instead of pointing HISTFILE at
	// /dev/null. Enabled by default; disable for a no-history lockdown.
	PersistHistory bool
	// CwdStore optionally persists the last shell directory per administrator
	// (nil disables directory resume).
	CwdStore CwdStore
	// RuntimeDirectory is where the generated shell-integration startup files
	// live. When empty, shell integration is skipped and the panel relies on
	// the kernel-reported directory alone.
	RuntimeDirectory string
	// UpdaterPath enables the fixed-command privileged update request. The
	// helper validates this root-owned executable before scheduling it.
	UpdaterPath string
}

// Session is the PTY-like surface shared by local and helper-backed shells.
// The control plane owns lifecycle and authentication; implementations only
// provide terminal I/O and window management.
type Session interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	TerminalMode() (Mode, error)
	Resize(columns, rows int) error
	Close(cause string)
}

type Service struct {
	config Config
	logger *slog.Logger

	mutex    sync.Mutex
	sessions map[string]Session

	// cwd state: per-actor last reported directory and the last time it was
	// persisted, used to resume sessions in the previous directory without
	// writing SQLite on every keystroke.
	cwdMutex  sync.Mutex
	lastCwd   map[string]string
	lastWrite map[string]time.Time

	// host samples the local machine + monitored processes at a low cadence.
	host           *hostCollector
	dataplanePIDs  func() map[int]string
	metricsTargets map[int]string
}

func NewService(config Config, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if config.IdleTimeout < 0 {
		return nil, errors.New("terminal idle timeout cannot be negative")
	}
	if config.MaxLifetime < 0 {
		config.MaxLifetime = 0 // 0 disables the absolute cap
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = defaultMaxSessions
	}
	return &Service{
		config:         config,
		logger:         logger,
		sessions:       make(map[string]Session),
		lastCwd:        make(map[string]string),
		lastWrite:      make(map[string]time.Time),
		host:           newHostCollector(),
		metricsTargets: map[int]string{os.Getpid(): "hx-pxyd"},
	}, nil
}

func (s *Service) Enabled() bool { return s.config.Enabled }

// SetDataPlanePIDResolver lets the caller report the active Mihomo PID(s) so
// the terminal UI can show data-plane CPU/RAM alongside the control plane.
func (s *Service) SetDataPlanePIDResolver(resolver func() map[int]string) {
	s.dataplanePIDs = resolver
}

// HostSnapshot returns one resource sample. dataplane PIDs are resolved fresh
// on every call so a restarted data plane is picked up without wiring changes.
func (s *Service) HostSnapshot() HostSnapshot {
	targets := map[int]string{}
	for k, v := range s.metricsTargets {
		targets[k] = v
	}
	if s.dataplanePIDs != nil {
		for pid, name := range s.dataplanePIDs() {
			if pid > 0 {
				targets[pid] = name
			}
		}
	}
	return s.host.Snapshot(targets, 0)
}

func (s *Service) TriggerUpdate(ctx context.Context) error {
	if strings.TrimSpace(s.config.PrivilegedSocket) == "" || strings.TrimSpace(s.config.UpdaterPath) == "" {
		return errors.New("automatic update is unavailable outside a production systemd installation")
	}
	return requestRemoteUpdate(ctx, s.config.PrivilegedSocket)
}

// ExecResult is the outcome of one remote command execution.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// Execute runs one non-interactive command through the privileged helper and
// returns its captured output. It is only available in a production systemd
// installation where the root helper socket is configured (20260823: the API
// remote-management path — lets an authenticated operator drive the VPS
// without a PTY session).
func (s *Service) Execute(ctx context.Context, command string, timeout time.Duration) (ExecResult, error) {
	if strings.TrimSpace(s.config.PrivilegedSocket) == "" {
		return ExecResult{}, errors.New("remote exec is unavailable outside a production systemd installation")
	}
	return requestRemoteExec(ctx, s.config.PrivilegedSocket, command, timeout)
}

// Status is the API view of the terminal feature.
type Status struct {
	Enabled        bool `json:"enabled"`
	ActiveSessions int  `json:"active_sessions"`
	MaxSessions    int  `json:"max_sessions"`
	IdleTimeoutSec int  `json:"idle_timeout_seconds"`
	MaxLifetimeSec int  `json:"max_lifetime_seconds"`
	Privileged     bool `json:"privileged"`
}

func (s *Service) Status() Status {
	s.mutex.Lock()
	active := len(s.sessions)
	s.mutex.Unlock()
	return Status{
		Enabled:        s.config.Enabled,
		ActiveSessions: active,
		MaxSessions:    s.config.MaxSessions,
		IdleTimeoutSec: int(s.config.IdleTimeout / time.Second),
		MaxLifetimeSec: int(s.config.MaxLifetime / time.Second),
		Privileged:     strings.TrimSpace(s.config.PrivilegedSocket) != "",
	}
}

// Open starts a new shell session. ctx cancellation (the WebSocket closing)
// terminates the session. actor and remote are audit metadata only.
func (s *Service) Open(ctx context.Context, actor, remote string) (Session, error) {
	if !s.config.Enabled {
		return nil, ErrDisabled
	}
	s.mutex.Lock()
	if len(s.sessions) >= s.config.MaxSessions {
		s.mutex.Unlock()
		return nil, ErrSessionLimit
	}
	// Reserve the slot before the fork so parallel opens cannot exceed the cap.
	id := newSessionID()
	s.sessions[id] = nil
	s.mutex.Unlock()

	release := func() {
		s.mutex.Lock()
		delete(s.sessions, id)
		s.mutex.Unlock()
	}
	now := time.Now()
	startDir := s.resumeCwd(ctx, actor)
	var base Session
	var shellName string
	var err error
	if socketPath := strings.TrimSpace(s.config.PrivilegedSocket); socketPath != "" {
		base, err = openRemoteSession(ctx, socketPath, startDir)
		shellName = "root PTY helper"
	} else {
		var ptyFile *os.File
		var command *exec.Cmd
		ptyFile, command, err = startShell(s.config.Shell, os.Environ(), s.config.PersistHistory, startDir, s.config.RuntimeDirectory)
		if err == nil {
			base = newPTYSession(ptyFile, command)
			shellName = command.Path
		}
	}
	if err != nil {
		release()
		return nil, err
	}
	session := &trackedSession{
		Session:    base,
		id:         id,
		startedAt:  now,
		lastActive: now,
	}
	session.onClose = func(closed *trackedSession, cause string) {
		release()
		s.logger.Info("terminal session closed",
			"audit", "terminal",
			"session_id", closed.id,
			"actor", actor,
			"remote", remote,
			"cause", cause,
			"duration_ms", time.Since(closed.startedAt).Milliseconds(),
		)
	}
	_ = session.Resize(defaultShellSizeCols, defaultShellSizeRows)

	s.mutex.Lock()
	s.sessions[id] = session
	s.mutex.Unlock()

	s.logger.Info("terminal session opened",
		"audit", "terminal",
		"session_id", id,
		"actor", actor,
		"remote", remote,
		"shell", shellName,
		"privileged", strings.TrimSpace(s.config.PrivilegedSocket) != "",
		"resume_dir", startDir,
	)
	go session.watch(ctx, s.config.IdleTimeout, s.config.MaxLifetime)
	return session, nil
}

func cwdKey(actor string) string { return "terminal_cwd:" + actor }

// validReportedCwd reports whether a client-provided directory is safe to
// remember as a resume hint. The directory does not need to exist yet — the
// shell start fallback handles a vanished directory — but it must be a bounded
// absolute path with no NUL or line breaks.
func validReportedCwd(cwd string) bool {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || !filepath.IsAbs(cwd) || strings.ContainsRune(cwd, 0) || strings.ContainsAny(cwd, "\r\n") || len(cwd) > 4096 {
		return false
	}
	return true
}

// ReportCwd records the shell directory reported by the frontend for an
// administrator. The value is cached in memory immediately (so a concurrent
// session and a same-process reconnect see it) and persisted to the optional
// store at most once per cwdPersistInterval per actor. Persistence failures
// are logged but never fail the terminal: a missed write only costs the next
// resume.
func (s *Service) ReportCwd(ctx context.Context, actor, cwd string) {
	actor = strings.TrimSpace(actor)
	if actor == "" || !validReportedCwd(cwd) {
		return
	}
	cwd = filepath.Clean(cwd)
	s.cwdMutex.Lock()
	changed := s.lastCwd[actor] != cwd
	s.lastCwd[actor] = cwd
	last := s.lastWrite[actor]
	shouldWrite := s.config.CwdStore != nil && changed && (last.IsZero() || time.Since(last) >= cwdPersistInterval)
	s.cwdMutex.Unlock()
	if !shouldWrite {
		return
	}
	if err := s.config.CwdStore.SetMetadata(ctx, cwdKey(actor), cwd); err != nil {
		s.logger.Warn("terminal resume directory persist failed", "actor", actor, "error", err)
		return
	}
	s.cwdMutex.Lock()
	s.lastWrite[actor] = time.Now()
	s.cwdMutex.Unlock()
}

// resumeCwd returns the last reported directory for an actor, preferring the
// in-memory value (fresh reports, concurrent sessions) and falling back to the
// optional store after a service restart. The returned value is only a hint:
// startShell validates it again and falls back to HOME when it is unusable.
func (s *Service) resumeCwd(ctx context.Context, actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return ""
	}
	s.cwdMutex.Lock()
	cached := s.lastCwd[actor]
	s.cwdMutex.Unlock()
	if cached != "" {
		return cached
	}
	if s.config.CwdStore == nil {
		return ""
	}
	value, err := s.config.CwdStore.GetMetadata(ctx, cwdKey(actor))
	if err != nil {
		return ""
	}
	if !validReportedCwd(value) {
		return ""
	}
	return filepath.Clean(value)
}

// Shutdown closes every open session (service stop).
func (s *Service) Shutdown() {
	s.mutex.Lock()
	open := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		if session != nil {
			open = append(open, session)
		}
	}
	s.mutex.Unlock()
	for _, session := range open {
		session.Close("service shutdown")
	}
}

func newSessionID() string {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("term-%d", time.Now().UnixNano())
	}
	return "term-" + hex.EncodeToString(buffer[:])
}
