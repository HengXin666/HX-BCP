package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const busyTimeout = 5 * time.Second

type Store struct {
	db       *sql.DB
	path     string
	backupMu sync.Mutex
}

type Status struct {
	Path          string `json:"path"`
	SchemaVersion int    `json:"schema_version"`
	JournalMode   string `json:"journal_mode"`
	Integrity     string `json:"integrity"`
}

func Open(ctx context.Context, databasePath string) (*Store, error) {
	if databasePath == "" {
		return nil, errors.New("database path is required")
	}
	absolutePath, err := filepath.Abs(databasePath)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	database, err := sql.Open("sqlite", absolutePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	database.SetConnMaxLifetime(0)

	store := &Store{db: database, path: absolutePath}
	if err := store.initialize(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := os.Chmod(absolutePath, 0o600); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("secure sqlite database: %w", err)
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("configure sqlite busy timeout: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA synchronous = NORMAL"); err != nil {
		return fmt.Errorf("configure sqlite synchronous mode: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA temp_store = MEMORY"); err != nil {
		return fmt.Errorf("configure sqlite temporary storage: %w", err)
	}
	var journalMode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("enable sqlite WAL: %w", err)
	}
	if journalMode != "wal" {
		return fmt.Errorf("sqlite journal mode is %q, want wal", journalMode)
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	var journalMode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return Status{}, fmt.Errorf("read sqlite journal mode: %w", err)
	}
	var integrity string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return Status{}, fmt.Errorf("check sqlite integrity: %w", err)
	}
	return Status{
		Path:          s.path,
		SchemaVersion: version,
		JournalMode:   journalMode,
		Integrity:     integrity,
	}, nil
}

func (s *Store) SetMetadata(ctx context.Context, key, value string) error {
	if key == "" {
		return errors.New("metadata key is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_metadata(key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set metadata %q: %w", key, err)
	}
	return nil
}

func (s *Store) GetMetadata(ctx context.Context, key string) (string, error) {
	var value string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM system_metadata WHERE key = ?", key).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get metadata %q: %w", key, err)
	}
	return value, nil
}

var ErrNotFound = errors.New("store record not found")
