package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndPersistsMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "state", "hx-pxy.db")
	storage, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	status, err := storage.Status(ctx)
	if err != nil {
		storage.Close()
		t.Fatalf("Status() error = %v", err)
	}
	if status.SchemaVersion != migrations[len(migrations)-1].version {
		t.Errorf("schema version = %d", status.SchemaVersion)
	}
	if status.JournalMode != "wal" {
		t.Errorf("journal mode = %q, want wal", status.JournalMode)
	}
	if status.Integrity != "ok" {
		t.Errorf("integrity = %q, want ok", status.Integrity)
	}

	if err := storage.SetMetadata(ctx, "application_version", "test-v1"); err != nil {
		storage.Close()
		t.Fatalf("SetMetadata() error = %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("database permissions = %o, want 600", info.Mode().Perm())
	}

	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer reopened.Close()
	value, err := reopened.GetMetadata(ctx, "application_version")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if value != "test-v1" {
		t.Errorf("metadata value = %q, want test-v1", value)
	}
}

func TestOpenMigratesOlderSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "legacy-v1.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	transaction, err := legacy.BeginTx(ctx, nil)
	if err != nil {
		legacy.Close()
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    applied_at TEXT NOT NULL
) STRICT;
`); err != nil {
		transaction.Rollback()
		legacy.Close()
		t.Fatalf("create legacy migration table: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, migrations[0].sql); err != nil {
		transaction.Rollback()
		legacy.Close()
		t.Fatalf("apply legacy migration: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO schema_migrations(version, name, applied_at)
VALUES (1, 'initial_control_plane_schema', '2026-07-25T00:00:00Z');
INSERT INTO system_metadata(key, value, updated_at)
VALUES ('client_subscription_token', 'legacy-global-token', '2026-07-25T00:00:00Z')
`); err != nil {
		transaction.Rollback()
		legacy.Close()
		t.Fatalf("record legacy migration: %v", err)
	}
	if err := transaction.Commit(); err != nil {
		legacy.Close()
		t.Fatalf("Commit() error = %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("legacy Close() error = %v", err)
	}

	upgraded, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(upgrade) error = %v", err)
	}
	defer upgraded.Close()
	version, err := upgraded.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != migrations[len(migrations)-1].version {
		t.Fatalf("schema version = %d, want %d", version, migrations[len(migrations)-1].version)
	}
	if _, err := upgraded.GetMetadata(ctx, "client_subscription_token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy unified subscription token still exists: %v", err)
	}
	if _, err := upgraded.db.ExecContext(ctx, `
INSERT INTO subscriptions(
    id, name, source_type, source_config_encrypted, enabled,
    refresh_interval_seconds, version, created_at, updated_at, next_refresh_at
) VALUES ('migration-check', 'migration-check', 'inline', X'01', 1, 3600, 1,
          '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z')
`); err != nil {
		t.Fatalf("write using upgraded columns: %v", err)
	}
}

func TestOpenMigrationClosesLegacyResidentialListenerBoundary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "legacy-residential.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	transaction, err := legacy.BeginTx(ctx, nil)
	if err != nil {
		legacy.Close()
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    applied_at TEXT NOT NULL
) STRICT;
`); err != nil {
		transaction.Rollback()
		legacy.Close()
		t.Fatalf("create migration table: %v", err)
	}
	// This test seeds the schema as it existed immediately before the
	// residential path-boundary migration, then verifies that migration's
	// normalization. Pin the boundary by version so adding later migrations
	// does not silently change what is being exercised.
	const residentialBoundaryVersion = 23
	for _, migration := range migrations {
		if migration.version >= residentialBoundaryVersion {
			break
		}
		if _, err := transaction.ExecContext(ctx, migration.sql); err != nil {
			transaction.Rollback()
			legacy.Close()
			t.Fatalf("apply migration %d: %v", migration.version, err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)
`, migration.version, migration.name, "2026-07-25T00:00:00Z"); err != nil {
			transaction.Rollback()
			legacy.Close()
			t.Fatalf("record migration %d: %v", migration.version, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO proxy_groups(
    id, name, source_spec_json, rule_pipeline_json, strategy,
    empty_behavior, enabled, version, created_at, updated_at
) VALUES ('legacy-res-group', 'legacy-res-group', '{}', '{}', 'manual',
          'fail-closed', 1, 1, '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z');
INSERT INTO listeners(
    id, name, listener_type, kind, bind_address, port, proxy_group_id,
    auth_mode, transport_json, public_endpoint_json, share_token,
    enabled, version, created_at, updated_at
) VALUES ('legacy-res-listener', 'legacy-res-listener', 'mixed', 'mixed',
          '0.0.0.0', 32825, 'legacy-res-group', 'none', '{}',
          '{"host":"proxy.example.com","port":32825,"tls":true}',
          'legacy-res-token', 1, 1, '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z');
INSERT INTO residential_providers(
    id, name, vendor, protocol, gateway_host, gateway_port,
    credentials_encrypted, username_template, rotation_mode,
    session_ttl_seconds, pool_size, default_region, enabled, version,
    created_at, updated_at
) VALUES ('legacy-res-provider', 'legacy-res-provider', 'custom', 'http',
          'gateway.example.com', 8000, X'01', '{user}', 'session-template',
          600, 1, 'US', 1, 1, '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z');
INSERT INTO residential_channels(
    id, name, provider_id, mode, proxy_group_id, listener_id,
    rotate_token, enabled, version, created_at, updated_at
) VALUES ('legacy-res-channel', 'legacy-res-channel', 'legacy-res-provider',
          'passthrough', 'legacy-res-group', 'legacy-res-listener',
          'legacy-res-rotate-token', 1, 1, '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z');
`); err != nil {
		transaction.Rollback()
		legacy.Close()
		t.Fatalf("insert legacy residential state: %v", err)
	}
	if err := transaction.Commit(); err != nil {
		legacy.Close()
		t.Fatalf("Commit() error = %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("legacy Close() error = %v", err)
	}

	upgraded, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(upgrade) error = %v", err)
	}
	defer upgraded.Close()
	var bindAddress string
	var publicPort int
	if err := upgraded.db.QueryRowContext(ctx, `
SELECT bind_address, json_extract(public_endpoint_json, '$.port')
FROM listeners WHERE id = 'legacy-res-listener'
`).Scan(&bindAddress, &publicPort); err != nil {
		t.Fatalf("read migrated residential listener: %v", err)
	}
	if bindAddress != "127.0.0.1" {
		t.Fatalf("migrated bind address = %q, want 127.0.0.1", bindAddress)
	}
	if publicPort != 443 {
		t.Fatalf("migrated public port = %d, want 443", publicPort)
	}
}

func TestGetMetadataNotFound(t *testing.T) {
	t.Parallel()

	storage, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer storage.Close()
	if _, err := storage.GetMetadata(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMetadata() error = %v, want ErrNotFound", err)
	}
}

func TestOnlineBackupIsConsistentAndIndependent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	storage, err := Open(ctx, filepath.Join(root, "active.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer storage.Close()

	if err := storage.SetMetadata(ctx, "generation", "before-backup"); err != nil {
		t.Fatalf("SetMetadata(before) error = %v", err)
	}
	backupPath := filepath.Join(root, "backups", "snapshot.db")
	if err := storage.BackupTo(ctx, backupPath); err != nil {
		t.Fatalf("BackupTo() error = %v", err)
	}
	if err := storage.SetMetadata(ctx, "generation", "after-backup"); err != nil {
		t.Fatalf("SetMetadata(after) error = %v", err)
	}

	backupInfo, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("Stat(backup) error = %v", err)
	}
	if backupInfo.Mode().Perm() != 0o600 {
		t.Errorf("backup permissions = %o, want 600", backupInfo.Mode().Perm())
	}

	backup, err := Open(ctx, backupPath)
	if err != nil {
		t.Fatalf("Open(backup) error = %v", err)
	}
	defer backup.Close()
	value, err := backup.GetMetadata(ctx, "generation")
	if err != nil {
		t.Fatalf("backup GetMetadata() error = %v", err)
	}
	if value != "before-backup" {
		t.Errorf("backup metadata = %q, want before-backup", value)
	}

	activeValue, err := storage.GetMetadata(ctx, "generation")
	if err != nil {
		t.Fatalf("active GetMetadata() error = %v", err)
	}
	if activeValue != "after-backup" {
		t.Errorf("active metadata = %q, want after-backup", activeValue)
	}
}

func TestOnlineBackupRefusesExistingDestination(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	storage, err := Open(ctx, filepath.Join(root, "active.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer storage.Close()

	destination := filepath.Join(root, "existing.db")
	if err := os.WriteFile(destination, []byte("do not overwrite"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := storage.BackupTo(ctx, destination); err == nil {
		t.Fatal("BackupTo() error = nil, want existing destination error")
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "do not overwrite" {
		t.Errorf("existing destination was modified: %q", content)
	}
}
