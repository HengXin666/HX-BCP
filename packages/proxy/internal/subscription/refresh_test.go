package subscription

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestRefreshCreatesSnapshotDeduplicatesAndPreservesLastSuccessOnFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(bytes.Repeat([]byte{0x39}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	service, err := NewService(
		database,
		box,
		WithRefresh(NewDefaultSourceLoader(), filepath.Join(root, "snapshots")),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	created, err := service.Create(ctx, CreateRequest{
		Name:         "inline-refresh",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "vless://one\nvmess://two\n"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	first, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatalf("Refresh(first) error = %v", err)
	}
	if !first.Changed || first.SnapshotID == "" || first.DetectedFormat != "uri-list" || first.EstimatedNodes != 2 {
		t.Fatalf("first refresh = %#v", first)
	}
	snapshot, err := database.GetSubscriptionSnapshot(ctx, first.SnapshotID)
	if err != nil {
		t.Fatalf("GetSubscriptionSnapshot() error = %v", err)
	}
	if snapshot.Status != "active" || snapshot.ContentHash != first.ContentHash {
		t.Fatalf("stored snapshot = %#v", snapshot)
	}
	info, err := os.Stat(snapshot.ArtifactPath)
	if err != nil {
		t.Fatalf("Stat(snapshot) error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot permissions = %o, want 600", info.Mode().Perm())
	}
	content, err := os.ReadFile(snapshot.ArtifactPath)
	if err != nil {
		t.Fatalf("ReadFile(snapshot) error = %v", err)
	}
	if string(content) != "vless://one\nvmess://two\n" {
		t.Fatalf("snapshot content = %q", content)
	}

	second, err := service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatalf("Refresh(second) error = %v", err)
	}
	if second.Changed || second.SnapshotID != first.SnapshotID {
		t.Fatalf("second refresh = %#v, first=%#v", second, first)
	}

	updated, err := service.Update(ctx, created.ID, UpdateRequest{
		Version:                created.Version,
		Name:                   created.Name,
		SourceType:             SourceFile,
		SourceConfig:           &SourceConfig{FilePath: filepath.Join(root, "missing-subscription.txt")},
		Enabled:                true,
		RefreshIntervalSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Update(file source) error = %v", err)
	}
	if _, err := service.Refresh(ctx, created.ID); err == nil {
		t.Fatal("Refresh(missing file) error = nil")
	}
	afterFailure, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get(after failure) error = %v", err)
	}
	if afterFailure.LastSuccessSnapshotID != first.SnapshotID {
		t.Fatalf("last success snapshot changed: %q", afterFailure.LastSuccessSnapshotID)
	}
	if afterFailure.ConsecutiveFailures != 1 || afterFailure.Version != updated.Version {
		t.Fatalf("subscription after failure = %#v", afterFailure)
	}
}

func TestRefreshRejectsDisabledSubscription(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(bytes.Repeat([]byte{0x53}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	service, err := NewService(database, box, WithRefresh(NewDefaultSourceLoader(), filepath.Join(root, "snapshots")))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	disabled := false
	created, err := service.Create(ctx, CreateRequest{
		Name:         "disabled",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "vless://one"},
		Enabled:      &disabled,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.Refresh(ctx, created.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Refresh(disabled) error = %v, want ErrInvalid", err)
	}
}
