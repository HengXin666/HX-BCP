package transfer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/artifact"
	"github.com/HengXin666/hx-pxy/internal/bundle"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestBackupContainsConsistentDatabaseAndExportDoesNot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(root, "data", "active.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	if err := database.SetMetadata(ctx, "generation", "archived"); err != nil {
		t.Fatalf("SetMetadata() error = %v", err)
	}

	statePath := filepath.Join(root, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"portable":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile(state) error = %v", err)
	}
	catalog, err := artifact.NewCatalog(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	service, err := NewService(
		catalog,
		[]bundle.Source{{
			Name:     "state",
			Path:     statePath,
			Scope:    bundle.ScopeBackup | bundle.ScopeExport,
			Required: true,
		}},
		database,
		filepath.Join(root, "staging"),
		"test",
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	backupRecord, err := service.Create(ctx, artifact.KindBackup, bundle.CreateOptions{Description: "database backup"})
	if err != nil {
		t.Fatalf("Create(backup) error = %v", err)
	}
	verification, err := service.Verify(ctx, backupRecord.ID)
	if err != nil {
		t.Fatalf("Verify(backup) error = %v", err)
	}
	if !verification.Valid || verification.FilesChecked != 2 {
		t.Fatalf("backup verification = %#v", verification)
	}

	backupEntries := extractRegularEntries(t, service, backupRecord.ID, filepath.Join(root, "extracted-backup"))
	if _, exists := backupEntries["payload/database/hx-pxy.db"]; !exists {
		t.Fatalf("backup entries = %#v, database snapshot missing", backupEntries)
	}
	if _, exists := backupEntries["payload/state/state.json"]; !exists {
		t.Fatalf("backup entries = %#v, portable state missing", backupEntries)
	}

	extractedDatabase, err := store.Open(ctx, backupEntries["payload/database/hx-pxy.db"])
	if err != nil {
		t.Fatalf("open extracted database: %v", err)
	}
	value, err := extractedDatabase.GetMetadata(ctx, "generation")
	closeErr := extractedDatabase.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read extracted database: get=%v close=%v", err, closeErr)
	}
	if value != "archived" {
		t.Fatalf("extracted metadata = %q, want archived", value)
	}

	exportRecord, err := service.Create(ctx, artifact.KindExport, bundle.CreateOptions{Description: "portable export"})
	if err != nil {
		t.Fatalf("Create(export) error = %v", err)
	}
	exportEntries := extractRegularEntries(t, service, exportRecord.ID, filepath.Join(root, "extracted-export"))
	if _, exists := exportEntries["payload/database/hx-pxy.db"]; exists {
		t.Fatalf("portable export unexpectedly contains database: %#v", exportEntries)
	}
	if _, exists := exportEntries["payload/state/state.json"]; !exists {
		t.Fatalf("portable export entries = %#v", exportEntries)
	}
}

func extractRegularEntries(t *testing.T, service *Service, id, destination string) map[string]string {
	t.Helper()
	_, file, err := service.Open(id)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", id, err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gzipReader.Close()

	entries := make(map[string]string)
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if header.Name == "manifest.json" {
			continue
		}
		if filepath.IsAbs(header.Name) || filepath.Clean(header.Name) != header.Name {
			t.Fatalf("unsafe archive entry in test: %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("OpenFile(%q) error = %v", target, err)
		}
		written, copyErr := io.CopyN(output, tarReader, header.Size)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			t.Fatalf("extract %q: written=%d copy=%v close=%v", header.Name, written, copyErr, closeErr)
		}
		entries[header.Name] = target
	}
}
