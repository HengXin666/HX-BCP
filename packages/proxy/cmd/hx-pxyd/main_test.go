package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestMigrateLegacyDatabasePathAdoptsOldFileAndSidecars(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "hx-pxy.db")
	legacyPath := filepath.Join(directory, legacyDatabaseName)
	for _, path := range []string{legacyPath, legacyPath + "-wal", legacyPath + "-shm"} {
		if err := os.WriteFile(path, []byte("legacy"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := migrateLegacyDatabasePath(databasePath, testLogger()); err != nil {
		t.Fatalf("migrateLegacyDatabasePath() error = %v", err)
	}

	// The data must survive under the new name, sidecars included: SQLite in WAL
	// mode keeps committed writes in -wal, so adopting only the main file would
	// silently drop them.
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
		if string(content) != "legacy" {
			t.Fatalf("%s content = %q, want %q", path, content, "legacy")
		}
	}
	for _, path := range []string{legacyPath, legacyPath + "-wal", legacyPath + "-shm"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy path %s should be gone, err = %v", path, err)
		}
	}
}

func TestMigrateLegacyDatabasePathKeepsExistingDatabase(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "hx-pxy.db")
	legacyPath := filepath.Join(directory, legacyDatabaseName)
	if err := os.WriteFile(databasePath, []byte("current"), 0o600); err != nil {
		t.Fatalf("write current database: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write legacy database: %v", err)
	}

	if err := migrateLegacyDatabasePath(databasePath, testLogger()); err != nil {
		t.Fatalf("migrateLegacyDatabasePath() error = %v", err)
	}

	// A configured database always wins; the migration must never overwrite it.
	content, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("read current database: %v", err)
	}
	if string(content) != "current" {
		t.Fatalf("current database content = %q, want %q", content, "current")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy database should be left untouched: %v", err)
	}
}

func TestMigrateLegacyDatabasePathWithoutLegacyFile(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "hx-pxy.db")

	// A fresh install has no legacy file at all; that is not an error.
	if err := migrateLegacyDatabasePath(databasePath, testLogger()); err != nil {
		t.Fatalf("migrateLegacyDatabasePath() error = %v", err)
	}
	if _, err := os.Stat(databasePath); !os.IsNotExist(err) {
		t.Fatalf("migration should not create the database, err = %v", err)
	}
}

func TestMigrateLegacyDatabasePathAdoptsMainFileWithoutSidecars(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "hx-pxy.db")
	legacyPath := filepath.Join(directory, legacyDatabaseName)
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write legacy database: %v", err)
	}

	// A cleanly shut down SQLite checkpoints and removes -wal/-shm; missing
	// sidecars must not block the rename.
	if err := migrateLegacyDatabasePath(databasePath, testLogger()); err != nil {
		t.Fatalf("migrateLegacyDatabasePath() error = %v", err)
	}
	content, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("read adopted database: %v", err)
	}
	if string(content) != "legacy" {
		t.Fatalf("adopted database content = %q, want %q", content, "legacy")
	}
}
