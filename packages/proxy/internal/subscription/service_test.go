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

func TestSubscriptionLifecycleEncryptsSourceConfiguration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "hx-pxy.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(bytes.Repeat([]byte{0x23}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	service, err := NewService(database, box)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	secretURL := "https://subscription.example.invalid/list?token=secret-token-value"
	secretHeader := "Bearer private-header-value"
	created, err := service.Create(ctx, CreateRequest{
		Name:       "primary-airport",
		SourceType: SourceRemote,
		SourceConfig: SourceConfig{
			URL:       secretURL,
			Headers:   map[string]string{"Authorization": secretHeader},
			UserAgent: "HX-Pxy-Test",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 1 || !created.Enabled || !created.SourceConfigured {
		t.Fatalf("created subscription = %#v", created)
	}

	configured, err := service.SourceConfig(ctx, created.ID)
	if err != nil {
		t.Fatalf("SourceConfig() error = %v", err)
	}
	if configured.URL != secretURL || configured.Headers["Authorization"] != secretHeader {
		t.Fatalf("decrypted source config = %#v", configured)
	}

	assertDatabaseDoesNotContain(t, root, []byte("secret-token-value"))
	assertDatabaseDoesNotContain(t, root, []byte("private-header-value"))

	updated, err := service.Update(ctx, created.ID, UpdateRequest{
		Version:    created.Version,
		Name:       "primary-airport-renamed",
		SourceType: SourceInline,
		SourceConfig: &SourceConfig{
			Inline: "vless://redacted-node",
		},
		Enabled:                false,
		RefreshIntervalSeconds: 7200,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Version != 2 || updated.Enabled || updated.Name != "primary-airport-renamed" {
		t.Fatalf("updated subscription = %#v", updated)
	}
	kept, err := service.Update(ctx, created.ID, UpdateRequest{
		Version:                updated.Version,
		Name:                   "metadata-only-update",
		SourceType:             SourceInline,
		Enabled:                true,
		RefreshIntervalSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("metadata-only Update() error = %v", err)
	}
	keptConfig, err := service.SourceConfig(ctx, created.ID)
	if err != nil || keptConfig.Inline != "vless://redacted-node" || kept.SourceType != SourceInline {
		t.Fatalf("metadata-only source = %#v, subscription = %#v, error = %v", keptConfig, kept, err)
	}
	if _, err := service.Update(ctx, created.ID, UpdateRequest{
		Version:                1,
		Name:                   "stale-update",
		SourceType:             SourceInline,
		SourceConfig:           &SourceConfig{Inline: "vmess://stale"},
		Enabled:                true,
		RefreshIntervalSeconds: 3600,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update() error = %v, want ErrConflict", err)
	}

	items, err := service.List(ctx, 100, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("List() = %#v", items)
	}
	if err := service.Delete(ctx, created.ID, kept.Version); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestSubscriptionRejectsDuplicateNameAndInvalidSources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(bytes.Repeat([]byte{0x45}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	service, err := NewService(database, box)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	request := CreateRequest{
		Name:         "duplicate",
		SourceType:   SourceRemote,
		SourceConfig: SourceConfig{URL: "https://example.invalid/subscription"},
	}
	if _, err := service.Create(ctx, request); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Create() error = %v, want ErrConflict", err)
	}

	invalidRequests := []CreateRequest{
		{Name: "bad-scheme", SourceType: SourceRemote, SourceConfig: SourceConfig{URL: "ftp://example.invalid/sub"}},
		{Name: "bad-header", SourceType: SourceRemote, SourceConfig: SourceConfig{URL: "https://example.invalid", Headers: map[string]string{"X-Test": "bad\r\nvalue"}}},
		{Name: "relative-file", SourceType: SourceFile, SourceConfig: SourceConfig{FilePath: "relative/path"}},
		{Name: "empty-inline", SourceType: SourceInline, SourceConfig: SourceConfig{}},
	}
	for _, invalid := range invalidRequests {
		if _, err := service.Create(ctx, invalid); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%s) error = %v, want ErrInvalid", invalid.Name, err)
		}
	}
}

func assertDatabaseDoesNotContain(t *testing.T, root string, plaintext []byte) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		if bytes.Contains(content, plaintext) {
			t.Fatalf("database artifact %q contains secret plaintext %q", entry.Name(), plaintext)
		}
	}
}

// newClassTestService builds a service over a real store so the classification
// round-trips through the database rather than a stub.
func newClassTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "hx-pxy.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	box, err := secret.New(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	service, err := NewService(database, box)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, database
}

// TestCreateHonoursAnExplicitSourceClass proves an operator's classification is
// what reaches the row that drives the probe policy — the whole feature fails
// silently if this echo is wrong.
func TestCreateHonoursAnExplicitSourceClass(t *testing.T) {
	t.Parallel()
	service, database := newClassTestService(t)

	for _, class := range SupportedSourceClasses() {
		created, err := service.Create(context.Background(), CreateRequest{
			Name:         "source-" + class,
			SourceType:   SourceInline,
			SourceClass:  class,
			SourceConfig: SourceConfig{Inline: "vless://node-" + class},
		})
		if err != nil {
			t.Fatalf("Create(%s) error = %v", class, err)
		}
		if created.SourceClass != class {
			t.Errorf("Create(%s).SourceClass = %q, want %q", class, created.SourceClass, class)
		}
		record, err := database.GetSubscription(context.Background(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if record.SourceClass != class {
			t.Errorf("stored source_class for %s = %q, want %q", class, record.SourceClass, class)
		}
	}
}

// TestCreateDefaultsToThePromotionSafeClass documents the deliberate default.
// The management UI does not send source_class, and the safe direction is the
// one that cannot retire an irreplaceable exit by accident.
func TestCreateDefaultsToThePromotionSafeClass(t *testing.T) {
	t.Parallel()
	service, _ := newClassTestService(t)

	created, err := service.Create(context.Background(), CreateRequest{
		Name:         "unclassified",
		SourceType:   SourceInline,
		SourceConfig: SourceConfig{Inline: "vless://node"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.SourceClass != string(store.SourceClassSubscription) {
		t.Fatalf("default source_class = %q, want %q", created.SourceClass, store.SourceClassSubscription)
	}
}

// TestCreateRejectsAnUnknownSourceClass keeps the vocabulary closed: a typo
// must not be silently stored and then read back as the default policy.
func TestCreateRejectsAnUnknownSourceClass(t *testing.T) {
	t.Parallel()
	service, _ := newClassTestService(t)

	if _, err := service.Create(context.Background(), CreateRequest{
		Name:         "typo",
		SourceType:   SourceInline,
		SourceClass:  "residental",
		SourceConfig: SourceConfig{Inline: "vless://node"},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
}

// TestUpdateReclassifiesWithoutRestatingTheSource is the repair path: an
// operator learns a source is less replaceable than assumed and fixes the
// classification alone, without touching the source configuration.
func TestUpdateReclassifiesWithoutRestatingTheSource(t *testing.T) {
	t.Parallel()
	service, _ := newClassTestService(t)

	created, err := service.Create(context.Background(), CreateRequest{
		Name:         "misjudged",
		SourceType:   SourceInline,
		SourceClass:  string(store.SourceClassPool),
		SourceConfig: SourceConfig{Inline: "vless://only-exit"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := service.Update(context.Background(), created.ID, UpdateRequest{
		Version:                created.Version,
		Name:                   created.Name,
		SourceType:             created.SourceType,
		SourceClass:            string(store.SourceClassResidential),
		Enabled:                true,
		RefreshIntervalSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.SourceClass != string(store.SourceClassResidential) {
		t.Fatalf("reclassified source_class = %q, want residential", updated.SourceClass)
	}
	// The source configuration must survive a classification-only change.
	config, err := service.SourceConfig(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if config.Inline != "vless://only-exit" {
		t.Fatalf("reclassification altered the source config: %+v", config)
	}
}

// TestUpdateWithoutAClassKeepsTheStoredOne prevents a metadata-only update from
// silently downgrading a source to the default policy.
func TestUpdateWithoutAClassKeepsTheStoredOne(t *testing.T) {
	t.Parallel()
	service, _ := newClassTestService(t)

	created, err := service.Create(context.Background(), CreateRequest{
		Name:         "residential-kept",
		SourceType:   SourceInline,
		SourceClass:  string(store.SourceClassResidential),
		SourceConfig: SourceConfig{Inline: "vless://only-exit"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	renamed, err := service.Update(context.Background(), created.ID, UpdateRequest{
		Version:                created.Version,
		Name:                   "renamed-only",
		SourceType:             created.SourceType,
		Enabled:                true,
		RefreshIntervalSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if renamed.SourceClass != string(store.SourceClassResidential) {
		t.Fatalf("metadata-only update changed source_class to %q, want residential", renamed.SourceClass)
	}
}

func TestSupportedSourceClassesMatchesTheStore(t *testing.T) {
	t.Parallel()
	got := SupportedSourceClasses()
	want := store.SupportedSourceClasses()
	if len(got) != len(want) {
		t.Fatalf("SupportedSourceClasses() = %v, want %v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("SupportedSourceClasses() = %v, want %v", got, want)
		}
	}
}
