package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteWALRecoversCommittedStateAfterProcessKill(t *testing.T) {
	if os.Getenv("HX_SQLITE_CRASH_HELPER") == "1" {
		runSQLiteCrashHelper()
		return
	}
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "recovery.db")
	readyPath := filepath.Join(directory, "transaction-ready")
	command := exec.Command(os.Args[0], "-test.run=TestSQLiteWALRecoversCommittedStateAfterProcessKill")
	command.Env = append(os.Environ(), "HX_SQLITE_CRASH_HELPER=1", "HX_SQLITE_CRASH_DB="+databasePath, "HX_SQLITE_CRASH_READY="+readyPath)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			t.Fatal("crash helper did not reach the uncommitted transaction")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	recovered, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() after kill error = %v", err)
	}
	defer recovered.Close()
	value, err := recovered.GetMetadata(context.Background(), "generation")
	if err != nil || value != "committed" {
		t.Fatalf("recovered generation = %q, error = %v", value, err)
	}
	status, err := recovered.Status(context.Background())
	if err != nil || status.JournalMode != "wal" || status.Integrity != "ok" {
		t.Fatalf("recovered status = %+v, error = %v", status, err)
	}
}

func runSQLiteCrashHelper() {
	ctx := context.Background()
	storage, err := Open(ctx, os.Getenv("HX_SQLITE_CRASH_DB"))
	if err != nil {
		os.Exit(2)
	}
	if err := storage.SetMetadata(ctx, "generation", "committed"); err != nil {
		os.Exit(3)
	}
	transaction, err := storage.db.BeginTx(ctx, nil)
	if err != nil {
		os.Exit(4)
	}
	if _, err := transaction.ExecContext(ctx, "UPDATE system_metadata SET value = 'uncommitted' WHERE key = 'generation'"); err != nil {
		os.Exit(5)
	}
	if err := os.WriteFile(os.Getenv("HX_SQLITE_CRASH_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(6)
	}
	time.Sleep(24 * time.Hour)
}
