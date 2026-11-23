package dockeraccess

import (
	"context"
	"errors"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

func TestDeleteSourceRejectsStaleVersionWithoutTouchingTheEntry(t *testing.T) {
	ctx := context.Background()
	service, repo, listeners, _ := newTestService(t, systemsettings.SharedInboundPerService, "0.0.0.0")
	preview, err := service.Assess(ctx, "svc", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, "svc", ApplyRequest{PlanID: preview.Plan.ID, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSource(ctx, "svc", 1); !errors.Is(err, listener.ErrConflict) {
		t.Fatalf("stale delete err = %v, want conflict", err)
	}
	if len(listeners.deleted) != 0 || len(repo.entries) != 1 {
		t.Fatalf("stale delete touched state: deleted=%v entries=%+v", listeners.deleted, repo.entries)
	}
	if err := service.DeleteSource(ctx, "svc", 2); err != nil {
		t.Fatal(err)
	}
	if len(listeners.deleted) != 2 || listeners.deleted[0] != "svc" || listeners.deleted[1] != "companion" {
		t.Fatalf("deleted = %v, want source first, then companion", listeners.deleted)
	}
	if len(repo.entries) != 0 {
		t.Fatalf("link left behind: %+v", repo.entries)
	}
}

func TestDeleteSourceWithoutEntryDeletesOnlyTheSource(t *testing.T) {
	ctx := context.Background()
	service, _, listeners, _ := newTestService(t, systemsettings.SharedInboundPerService, "0.0.0.0")
	if err := service.DeleteSource(ctx, "svc", 2); err != nil {
		t.Fatal(err)
	}
	if len(listeners.deleted) != 1 || listeners.deleted[0] != "svc" {
		t.Fatalf("deleted = %v", listeners.deleted)
	}
}
