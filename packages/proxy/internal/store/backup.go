package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	sqlite "modernc.org/sqlite"
)

type onlineBackupConnection interface {
	NewBackup(destinationURI string) (*sqlite.Backup, error)
}

func (s *Store) BackupTo(ctx context.Context, destination string) error {
	if destination == "" {
		return errors.New("backup destination is required")
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup destination: %w", err)
	}
	if absoluteDestination == s.path {
		return errors.New("backup destination must differ from the active database")
	}
	if _, err := os.Lstat(absoluteDestination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", absoluteDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absoluteDestination), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	s.backupMu.Lock()
	defer s.backupMu.Unlock()

	temporary, err := os.CreateTemp(filepath.Dir(absoluteDestination), ".sqlite-backup-*.db")
	if err != nil {
		return fmt.Errorf("reserve backup path: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close reserved backup path: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("prepare backup path: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()

	connection, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite backup connection: %w", err)
	}
	defer connection.Close()

	if err := connection.Raw(func(driverConnection any) error {
		source, ok := driverConnection.(onlineBackupConnection)
		if !ok {
			return fmt.Errorf("sqlite driver does not expose online backup API")
		}
		backup, err := source.NewBackup(temporaryPath)
		if err != nil {
			return fmt.Errorf("start sqlite online backup: %w", err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()

		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(256)
			if err != nil {
				return fmt.Errorf("copy sqlite backup pages: %w", err)
			}
			if !more {
				break
			}
		}
		if err := backup.Finish(); err != nil {
			return fmt.Errorf("finish sqlite online backup: %w", err)
		}
		finished = true
		return nil
	}); err != nil {
		return err
	}

	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("secure sqlite backup: %w", err)
	}
	if err := verifyBackup(ctx, temporaryPath); err != nil {
		return err
	}
	if err := syncFile(temporaryPath); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, absoluteDestination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("backup destination already exists: %s", absoluteDestination)
		}
		return fmt.Errorf("publish sqlite backup: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(absoluteDestination)
		return fmt.Errorf("remove temporary backup link: %w", err)
	}
	if err := syncDirectory(filepath.Dir(absoluteDestination)); err != nil {
		_ = os.Remove(absoluteDestination)
		return err
	}
	published = true
	return nil
}

func verifyBackup(ctx context.Context, databasePath string) error {
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return fmt.Errorf("open sqlite backup for verification: %w", err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()

	var integrity string
	if err := database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("verify sqlite backup integrity: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("sqlite backup integrity check returned %q", integrity)
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&schemaVersion); err != nil {
		return fmt.Errorf("verify sqlite backup schema: %w", err)
	}
	if schemaVersion != migrations[len(migrations)-1].version {
		return fmt.Errorf("sqlite backup schema version is %d, want %d", schemaVersion, migrations[len(migrations)-1].version)
	}
	return nil
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file for sync: %w", err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
