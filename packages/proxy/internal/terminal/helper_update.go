package terminal

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// updateScheduleTimeout bounds how long a busy systemd may take to accept
	// the transient update unit. systemd-run itself returns quickly when
	// systemd is idle; the bound exists so a stuck systemd can never hold the
	// scheduler slot (and its goroutine) forever.
	updateScheduleTimeout = 30 * time.Second
	// updateActiveCheckTimeout bounds the in-flight systemd state check done
	// before accepting a new update request.
	updateActiveCheckTimeout = 2 * time.Second
)

// updateScheduler serializes privileged automatic-update requests and
// acknowledges them before scheduling.
//
// The previous implementation ran systemd-run synchronously inside the
// request connection and replied only after it returned. systemd-run can take
// longer than the control plane's fixed handshake timeout whenever systemd is
// busy, which made the UI report "read .../terminal.sock: i/o timeout" and
// falsely claim the update failed even though it then ran to completion. The
// scheduler instead replies immediately and runs systemd-run on a detached
// goroutine, and refuses a second request while one update is in flight: the
// transient unit name is fixed, so two concurrent runs would collide inside
// systemd-run (the second one fails with "unit already exists/active").
type updateScheduler struct {
	mutex    sync.Mutex
	inFlight bool

	updater string
	logger  *slog.Logger

	// validatePath reports whether the updater executable is acceptable: a
	// root-owned, group/other-non-writable regular file.
	validatePath func(string) bool
	// systemdActive reports whether the fixed-name update unit is currently
	// active in systemd. It guards against a duplicate request after this
	// helper restarted mid-update, when the in-memory inFlight flag has reset.
	systemdActive func(context.Context) bool
	// runUpdater schedules the installer through systemd-run. Replaced in
	// tests.
	runUpdater func(context.Context) error
}

func newUpdateScheduler(updaterPath string, logger *slog.Logger) *updateScheduler {
	updater := filepath.Clean(strings.TrimSpace(updaterPath))
	return &updateScheduler{
		updater:       updater,
		logger:        logger,
		validatePath:  validUpdaterPath,
		systemdActive: updateUnitActive,
		runUpdater: func(ctx context.Context) error {
			return scheduleSystemdUpdate(ctx, updater)
		},
	}
}

// serve handles one privileged update request on connection. The connection
// is closed here; scheduling continues on a detached goroutine and the caller
// must not touch the connection afterwards.
func (s *updateScheduler) serve(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	if !s.validatePath(s.updater) {
		_ = writeFrame(connection, frameError, []byte("automatic updater is unavailable"))
		return
	}
	if s.systemdActive(ctx) || !s.tryBegin() {
		_ = writeFrame(connection, frameError, []byte("an automatic update is already in progress"))
		return
	}
	// Acknowledge before scheduling: systemd-run may block on a busy systemd,
	// and the control plane's request handshake must not wait for it.
	if err := writeFrame(connection, frameReady, nil); err != nil {
		s.end()
		return
	}
	go s.schedule(ctx)
}

// schedule runs the updater on a detached goroutine and releases the
// single-flight slot when done. The update itself is a systemd unit, not a
// child of this helper, so it keeps running even if the helper exits or
// restarts mid-update; cancelling the context here only aborts the
// systemd-run client, never the scheduled unit.
func (s *updateScheduler) schedule(ctx context.Context) {
	defer s.end()
	if err := s.runUpdater(ctx); err != nil {
		s.logger.Error("automatic update scheduling failed",
			"audit", "system_update",
			"error", err,
		)
		return
	}
	s.logger.Info("automatic update scheduled",
		"audit", "system_update",
	)
}

func (s *updateScheduler) tryBegin() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.inFlight {
		return false
	}
	s.inFlight = true
	return true
}

func (s *updateScheduler) end() {
	s.mutex.Lock()
	s.inFlight = false
	s.mutex.Unlock()
}

// validUpdaterPath reports whether path is an acceptable fixed updater: an
// absolute path to a regular file owned by root with no group/other write
// bits. Every other shape is refused so the privileged helper never executes
// a browser-influenced command.
func validUpdaterPath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	stat, ownedByRoot := info.Sys().(*syscall.Stat_t)
	return info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0 && ownedByRoot && stat.Uid == 0
}

// updateUnitActive reports whether the fixed-name transient update unit is
// running. The unit is short-lived (--collect), so "active" means a previous
// update is still in progress; a duplicate request would collide inside
// systemd-run and is refused up front with a clear error.
func updateUnitActive(ctx context.Context) bool {
	checkContext, cancel := context.WithTimeout(ctx, updateActiveCheckTimeout)
	defer cancel()
	command := exec.CommandContext(checkContext, "systemctl", "is-active", "--quiet", "hx-pxy-update.service")
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	return command.Run() == nil
}

// scheduleSystemdUpdate starts the installer through a transient systemd unit.
// The unit runs asynchronously under systemd; this function returns once
// systemd has accepted the start job, which can take a few seconds when
// systemd is busy.
func scheduleSystemdUpdate(ctx context.Context, updater string) error {
	runContext, cancel := context.WithTimeout(ctx, updateScheduleTimeout)
	defer cancel()
	command := exec.CommandContext(
		runContext,
		"systemd-run",
		"--unit=hx-pxy-update",
		"--collect",
		"--property=Type=exec",
		updater,
		"upgrade",
	)
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	output, err := command.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return fmt.Errorf("schedule automatic update: %w: %s", err, detail)
		}
		return fmt.Errorf("schedule automatic update: %w", err)
	}
	return nil
}
