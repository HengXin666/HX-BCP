package proxygroup

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func newRefTestService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service, err := NewService(database, &testReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	return service, ctx
}

func TestGroupReferencesValidateAndRejectCycles(t *testing.T) {
	service, ctx := newRefTestService(t)

	leaf, err := service.Create(ctx, CreateRequest{
		Name:       "leaf",
		Strategy:   "manual",
		SourceSpec: SourceSpec{IncludeDirect: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	parent, err := service.Create(ctx, CreateRequest{
		Name:       "parent",
		Strategy:   "fallback",
		SourceSpec: SourceSpec{GroupIDs: []string{leaf.ID}},
	})
	if err != nil {
		t.Fatalf("Create() with group reference error = %v", err)
	}
	if len(parent.SourceSpec.GroupIDs) != 1 || parent.SourceSpec.GroupIDs[0] != leaf.ID {
		t.Fatalf("group reference not persisted: %+v", parent.SourceSpec)
	}

	_, err = service.Create(ctx, CreateRequest{
		Name:       "missing-ref",
		Strategy:   "manual",
		SourceSpec: SourceSpec{GroupIDs: []string{"group-does-not-exist"}},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing reference error = %v, want ErrInvalid", err)
	}

	// leaf -> parent would close the loop parent -> leaf.
	_, err = service.Update(ctx, leaf.ID, UpdateRequest{
		Version:    leaf.Version,
		Name:       leaf.Name,
		Strategy:   "manual",
		SourceSpec: SourceSpec{IncludeDirect: true, GroupIDs: []string{parent.ID}},
		Enabled:    true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("cycle error = %v, want ErrInvalid", err)
	}

	// A referenced group must not be deletable while the reference exists.
	if err := service.Delete(ctx, leaf.ID, leaf.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete() referenced group error = %v, want ErrConflict", err)
	}
	if err := service.Delete(ctx, parent.ID, parent.Version); err != nil {
		t.Fatalf("Delete() parent error = %v", err)
	}
	refreshedLeaf, err := service.Get(ctx, leaf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, leaf.ID, refreshedLeaf.Version); err != nil {
		t.Fatalf("Delete() leaf after parent removed error = %v", err)
	}
}
