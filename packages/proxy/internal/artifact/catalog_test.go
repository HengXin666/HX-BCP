package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogLifecycle(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	catalog, err := NewCatalog(directory)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}

	createdAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	payload := []byte("hx-pxy-backup")
	record, err := catalog.Write(context.Background(), Record{
		ID:          "test-artifact-0001",
		Kind:        KindBackup,
		CreatedAt:   createdAt,
		ContentType: "application/octet-stream",
		Description: "test backup",
	}, ".bin", func(writer io.Writer) error {
		_, err := writer.Write(payload)
		return err
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	digest := sha256.Sum256(payload)
	if record.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("Write() SHA256 = %q", record.SHA256)
	}
	if record.Size != int64(len(payload)) {
		t.Fatalf("Write() size = %d, want %d", record.Size, len(payload))
	}
	if record.Filename != "backup-test-artifact-0001.bin" {
		t.Fatalf("Write() filename = %q", record.Filename)
	}
	info, err := os.Stat(filepath.Join(directory, record.Filename))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact permissions = %o, want 600", info.Mode().Perm())
	}

	records, err := catalog.List(KindBackup)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].ID != record.ID {
		t.Fatalf("List() = %#v", records)
	}

	openedRecord, file, err := catalog.Open(record.ID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	readPayload, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read artifact: read=%v close=%v", readErr, closeErr)
	}
	if openedRecord.ID != record.ID || string(readPayload) != string(payload) {
		t.Fatalf("Open() returned record=%#v payload=%q", openedRecord, readPayload)
	}

	if err := catalog.Delete(record.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := catalog.Get(record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestCatalogWriteHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	catalog, err := NewCatalog(t.TempDir())
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = catalog.Write(ctx, Record{Kind: KindExport}, ".bin", func(io.Writer) error {
		t.Fatal("writer should not be called")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Write() error = %v, want context.Canceled", err)
	}
}
