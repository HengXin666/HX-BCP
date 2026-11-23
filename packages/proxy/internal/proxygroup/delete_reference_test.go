package proxygroup

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// A group is reachable from two directions: another group can name it as a
// member or a dialer (referencedBy), and a listener can point at it as the
// egress of a published service. Only the first was checked, so deleting a
// group that a listener still used reached the database and was refused there
// by the RESTRICT foreign key — a correct refusal reported as an unmapped
// SQLite error, which the API turned into a generic 500.
//
// The concrete damage this prevented (measured 2026-10-01): a pinned listener's
// proxy_group_id is a shared schema anchor, so the front end's "delete this
// service" flow found the operator's main subscription group on the listener
// and tried to delete it. Nothing in this service objected; the foreign key
// did. This test pins the service-level objection so the next change cannot
// reintroduce the dependency on a database constraint for a domain rule.
func TestDeleteRefusesGroupStillUsedByAListener(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service, err := NewService(database, &testReconciler{})
	if err != nil {
		t.Fatal(err)
	}

	group, err := service.Create(ctx, CreateRequest{
		Name:       "shared-anchor",
		Strategy:   "manual",
		SourceSpec: SourceSpec{IncludeDirect: true},
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	record := store.ListenerRecord{
		ID:                 "listener-pinned",
		Name:               "svc-pinned",
		Kind:               "mixed",
		BindAddress:        "127.0.0.1",
		Port:               7890,
		ProxyGroupID:       group.ID,
		TransportJSON:      "{}",
		PublicEndpointJSON: "{}",
		AuthMode:           "none",
		ShareToken:         "share-pinned",
		Enabled:            true,
		Version:            1,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
	if _, err := database.CreateListener(ctx, record); err != nil {
		t.Fatalf("create listener: %v", err)
	}

	err = service.Delete(ctx, group.ID, group.Version)
	if !errors.Is(err, ErrReferenced) {
		t.Fatalf("Delete() = %v, want ErrReferenced so the API can answer 409 proxy_group_in_use", err)
	}
	// ErrReferenced must not be reported as ErrConflict: the two have different
	// remedies, and the front end distinguishes them by code rather than by
	// reading a message.
	if errors.Is(err, ErrConflict) {
		t.Fatalf("Delete() = %v, must not also match ErrConflict or the API maps it to the wrong code", err)
	}

	// Negative control: once the listener stops using the group, the same call
	// must succeed. Without this the test would also pass if Delete simply
	// refused every group that had ever been referenced.
	//
	// Releasing a group happens by removing the listener, not by blanking
	// proxy_group_id: the column is NOT NULL and its foreign key rejects an
	// empty string as a real (nonexistent) reference — the same "empty string is
	// not 'no reference'" trap this repository documented for listeners.node_id.
	if err := database.DeleteListener(ctx, record.ID, 1); err != nil {
		t.Fatalf("delete listener: %v", err)
	}
	if err := service.Delete(ctx, group.ID, group.Version); err != nil {
		t.Fatalf("Delete() after the listener is gone = %v, want success", err)
	}
}
