package instance

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireRejectsConcurrentControlPlane(t *testing.T) {
	dataDirectory := t.TempDir()
	first, err := Acquire(dataDirectory)
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	second, err := Acquire(dataDirectory)
	if !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second Acquire() error = %v, want ErrAlreadyRunning", err)
	}
	if second != nil {
		_ = second.Close()
		t.Fatal("second Acquire() returned a lock")
	}
	if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("second Acquire() error = %q, want owner pid", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	restarted, err := Acquire(dataDirectory)
	if err != nil {
		t.Fatalf("Acquire() after Close() error = %v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("restarted Close() error = %v", err)
	}
}

func TestAcquireSecuresLockFile(t *testing.T) {
	dataDirectory := t.TempDir()
	lock, err := Acquire(dataDirectory)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer lock.Close()

	info, err := os.Stat(filepath.Join(dataDirectory, "control-plane.lock"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("lock permissions = %o, want 600", permissions)
	}
}
