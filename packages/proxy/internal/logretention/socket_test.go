package logretention

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIdleLiveInstanceIsNeverPruned(t *testing.T) {
	// Keep the Unix socket path short on all test platforms.
	root, err := os.MkdirTemp("", "hx-log-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	dir := filepath.Join(root, "0123456789abcdef")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, MarkerName)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	old := time.Now().Add(-90 * 24 * time.Hour)
	for _, path := range []string{socket, dir} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Prune(context.Background(), Policy{Root: root, Mode: ModeInstanceDirectories, Days: 30, RequireMarker: true}, nil, 0)
	if err != nil || report.Deleted != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	mustExist(t, socket)
}
