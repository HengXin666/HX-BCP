package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/artifact"
)

func TestServiceCreatesBackupAndPortableExport(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	databasePath := filepath.Join(root, "hx-pxy.db")
	secretPath := filepath.Join(root, "active.yaml")
	writeTestFile(t, statePath, `{"listen":"127.0.0.1:19090"}`)
	writeTestFile(t, databasePath, "sqlite-placeholder")
	writeTestFile(t, secretPath, "password: should-not-leak")

	artifactDirectory := filepath.Join(root, "artifacts")
	catalog, err := artifact.NewCatalog(artifactDirectory)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	service, err := NewService(catalog, []Source{
		{Name: "state", Path: statePath, Scope: ScopeBackup | ScopeExport, Required: true},
		{Name: "database", Path: databasePath, Scope: ScopeBackup},
		{Name: "runtime", Path: secretPath, Scope: ScopeBackup, Sensitive: true},
	}, "test")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	backup, err := service.Create(context.Background(), artifact.KindBackup, CreateOptions{Description: "manual backup"})
	if err != nil {
		t.Fatalf("Create(backup) error = %v", err)
	}
	backupManifest := readManifest(t, service, backup.ID)
	assertManifestPaths(t, backupManifest, []string{
		"payload/database/hx-pxy.db",
		"payload/state/state.json",
	})
	if len(backupManifest.Skipped) != 1 || backupManifest.Skipped[0].Source != "runtime" {
		t.Fatalf("backup skipped entries = %#v", backupManifest.Skipped)
	}
	verification, err := service.Verify(context.Background(), backup.ID)
	if err != nil {
		t.Fatalf("Verify(backup) error = %v", err)
	}
	if !verification.Valid || verification.FilesChecked != 2 {
		t.Fatalf("Verify(backup) = %#v", verification)
	}

	exported, err := service.Create(context.Background(), artifact.KindExport, CreateOptions{Description: "portable export"})
	if err != nil {
		t.Fatalf("Create(export) error = %v", err)
	}
	exportManifest := readManifest(t, service, exported.ID)
	assertManifestPaths(t, exportManifest, []string{"payload/state/state.json"})
	if len(exportManifest.Skipped) != 0 {
		t.Fatalf("export skipped entries = %#v", exportManifest.Skipped)
	}

	if _, err := service.Create(context.Background(), artifact.KindBackup, CreateOptions{IncludeSecrets: true}); !errors.Is(err, ErrSecretBundleDisabled) {
		t.Fatalf("Create(include secrets) error = %v, want ErrSecretBundleDisabled", err)
	}
}

func TestServiceRejectsMissingRequiredSource(t *testing.T) {
	t.Parallel()

	catalog, err := artifact.NewCatalog(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	service, err := NewService(catalog, []Source{{
		Name:     "required-state",
		Path:     filepath.Join(t.TempDir(), "missing.json"),
		Scope:    ScopeBackup,
		Required: true,
	}}, "test")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Create(context.Background(), artifact.KindBackup, CreateOptions{}); err == nil {
		t.Fatal("Create() error = nil, want missing required source error")
	}
}

func TestServiceDetectsArtifactTampering(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	writeTestFile(t, statePath, "state")
	artifactDirectory := filepath.Join(root, "artifacts")
	catalog, err := artifact.NewCatalog(artifactDirectory)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	service, err := NewService(catalog, []Source{{Name: "state", Path: statePath, Scope: ScopeExport, Required: true}}, "test")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	record, err := service.Create(context.Background(), artifact.KindExport, CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	file, err := os.OpenFile(filepath.Join(artifactDirectory, record.Filename), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.Write([]byte("tampered")); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close tampered artifact: %v", err)
	}
	if _, err := service.Verify(context.Background(), record.ID); err == nil {
		t.Fatal("Verify() error = nil, want checksum error")
	}
}

func readManifest(t *testing.T, service *Service, id string) Manifest {
	t.Helper()
	_, file, err := service.Open(id)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			t.Fatal("manifest.json not found")
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}
		if header.Name != "manifest.json" {
			continue
		}
		var manifest Manifest
		if err := json.NewDecoder(tarReader).Decode(&manifest); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		return manifest
	}
}

func assertManifestPaths(t *testing.T, manifest Manifest, expected []string) {
	t.Helper()
	if len(manifest.Entries) != len(expected) {
		t.Fatalf("manifest entries = %#v, want %v", manifest.Entries, expected)
	}
	for index, entry := range manifest.Entries {
		if entry.Path != expected[index] {
			t.Fatalf("manifest entry %d path = %q, want %q", index, entry.Path, expected[index])
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
