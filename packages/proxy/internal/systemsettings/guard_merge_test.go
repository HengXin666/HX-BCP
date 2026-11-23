package systemsettings

import (
	"context"
	"encoding/json"
	"testing"
)

// PUT /api/v1/settings replaces the whole document, so any client that does not
// know about a field — an older build, or a script that only wants to change
// the log level — sends a body without it. Decoding a missing key into a plain
// bool yields false, which silently disarmed the group-creation guard while the
// operator believed it was on. Measured live on 2026-10-01: saving settings
// without the key cleared it.
//
// The guard is a safety gate; nothing unrelated may reset it. An absent field
// therefore has to mean "unchanged", which needs three states and hence a
// pointer. This test pins that, including the round trip through real JSON —
// a struct-level test would miss the decode step where the distinction is lost.
func TestOmittedGuardFieldKeepsStoredValue(t *testing.T) {
	ctx := context.Background()
	repository := &memoryRepository{}
	service, err := NewService(repository, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Turn the guard on through the service, as the settings page does.
	on := Default()
	on.Guard.DenyAPIGroupCreate = boolPointer(true)
	if _, err := service.Update(ctx, on); err != nil {
		t.Fatalf("enable guard: %v", err)
	}

	// A client that predates the field serializes the rest of the document and
	// omits it entirely.
	stored, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "guard")
	stripped, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var incoming Settings
	if err := json.Unmarshal(stripped, &incoming); err != nil {
		t.Fatal(err)
	}
	if incoming.Guard.DenyAPIGroupCreate != nil {
		t.Fatal("a body without the key must decode to nil, not to a value; the merge below depends on it")
	}

	// The save must not change the guard.
	incoming.Performance.LogLevel = "info"
	if _, err := service.Update(ctx, incoming); err != nil {
		t.Fatalf("save without the guard key: %v", err)
	}
	after, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Guard.DenyAPIGroupCreate == nil || !*after.Guard.DenyAPIGroupCreate {
		t.Fatal("an unrelated save cleared the guard: absent must mean unchanged")
	}
	// Negative control: the rest of the document really was updated, so the
	// assertion above is not passing because the whole save was ignored.
	if after.Performance.LogLevel != "info" {
		t.Fatalf("log level = %q, want the save to have applied", after.Performance.LogLevel)
	}

	// And an explicit false still turns it off — "unchanged" must not make the
	// switch one-way.
	off := after
	off.Guard.DenyAPIGroupCreate = boolPointer(false)
	if _, err := service.Update(ctx, off); err != nil {
		t.Fatalf("disable guard: %v", err)
	}
	final, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if final.Guard.DenyAPIGroupCreate == nil || *final.Guard.DenyAPIGroupCreate {
		t.Fatal("an explicit false must still disable the guard")
	}
}
