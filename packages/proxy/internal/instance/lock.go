package instance

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var ErrAlreadyRunning = errors.New("control plane already running")

type Lock struct {
	file *os.File
}

func Acquire(dataDirectory string) (*Lock, error) {
	path := filepath.Join(dataDirectory, "control-plane.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open control-plane lock %q: %w", path, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure control-plane lock %q: %w", path, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		owner := lockOwner(file)
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			if owner != "" {
				return nil, fmt.Errorf("%w for data directory %q (pid %s)", ErrAlreadyRunning, dataDirectory, owner)
			}
			return nil, fmt.Errorf("%w for data directory %q", ErrAlreadyRunning, dataDirectory)
		}
		return nil, fmt.Errorf("lock control-plane file %q: %w", path, err)
	}

	releaseOnError := func(cause error) (*Lock, error) {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, cause
	}
	if err := file.Truncate(0); err != nil {
		return releaseOnError(fmt.Errorf("truncate control-plane lock %q: %w", path, err))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return releaseOnError(fmt.Errorf("seek control-plane lock %q: %w", path, err))
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		return releaseOnError(fmt.Errorf("write control-plane lock %q: %w", path, err))
	}
	if err := file.Sync(); err != nil {
		return releaseOnError(fmt.Errorf("sync control-plane lock %q: %w", path, err))
	}
	return &Lock{file: file}, nil
}

func (lock *Lock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	return errors.Join(
		unix.Flock(int(file.Fd()), unix.LOCK_UN),
		file.Close(),
	)
}

func lockOwner(file *os.File) string {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	content, err := io.ReadAll(io.LimitReader(file, 32))
	if err != nil {
		return ""
	}
	owner := strings.TrimSpace(string(content))
	if _, err := strconv.Atoi(owner); err != nil {
		return ""
	}
	return owner
}
