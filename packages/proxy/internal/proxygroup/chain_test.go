package proxygroup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// newChainService returns a service over a real store so the dialer rules are
// exercised through persistence rather than a hand-rolled stub.
func newChainService(t *testing.T) (*Service, *testReconciler) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	reconciler := &testReconciler{}
	service, err := NewService(database, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	return service, reconciler
}

func createDirectGroup(t *testing.T, service *Service, name string) Group {
	t.Helper()
	created, err := service.Create(context.Background(), CreateRequest{
		Name:       name,
		Strategy:   "manual",
		SourceSpec: SourceSpec{IncludeDirect: true},
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return created
}

// A chain is a reference like a membership, so it must be validated with the
// same rigour: the target has to exist and be enabled.
func TestCreateRejectsMissingAndSelfDialer(t *testing.T) {
	service, _ := newChainService(t)
	ctx := context.Background()

	_, err := service.Create(ctx, CreateRequest{
		Name:             "chained",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{IncludeDirect: true},
		DialerProxyGroup: "group-does-not-exist",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing dialer error = %v, want ErrInvalid", err)
	}

	// A group cannot dial through itself. Note this is caught before the id even
	// exists, because the created id is generated up front.
	_, err = service.Create(ctx, CreateRequest{
		Name:             "self",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{IncludeDirect: true},
		DialerProxyGroup: "some-id",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown dialer error = %v, want ErrInvalid", err)
	}
}

// DIRECT is a builtin and cannot carry a dialer, so that member would genuinely
// bypass the chain. Accepting it silently would leak traffic around the path the
// operator asked for.
func TestCreateRejectsChainCombinedWithDirect(t *testing.T) {
	service, _ := newChainService(t)
	upstream := createDirectGroup(t, service, "upstream")

	_, err := service.Create(context.Background(), CreateRequest{
		Name:             "leaky",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{IncludeDirect: true},
		DialerProxyGroup: upstream.ID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("include_direct + dialer error = %v, want ErrInvalid", err)
	}
}

// Deleting a group another one dials through would silently turn that chain into
// a direct connection, so the delete must be refused.
func TestDeleteRefusesGroupUsedAsDialer(t *testing.T) {
	service, _ := newChainService(t)
	ctx := context.Background()
	upstream := createDirectGroup(t, service, "upstream")

	chained, err := service.Create(ctx, CreateRequest{
		Name:             "chained",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{GroupIDs: []string{upstream.ID}},
		DialerProxyGroup: upstream.ID,
	})
	if err != nil {
		t.Fatalf("create chained group: %v", err)
	}
	if chained.DialerProxyGroupID != upstream.ID {
		t.Fatalf("dialer = %q, want %q", chained.DialerProxyGroupID, upstream.ID)
	}

	err = service.Delete(ctx, upstream.ID, upstream.Version)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete() error = %v, want ErrConflict for a group in use as a dialer", err)
	}
}

// The dialer edge closes a loop with a member edge. Mihomo accepts a cyclic
// dialer at load time, so the write-time check is the only thing that can refuse
// it -- and it must fire for a group whose only other edge is the dialer itself.
func TestUpdateRejectsDialerCycle(t *testing.T) {
	service, _ := newChainService(t)
	ctx := context.Background()
	first := createDirectGroup(t, service, "first")

	second, err := service.Create(ctx, CreateRequest{
		Name:             "second",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{GroupIDs: []string{first.ID}},
		DialerProxyGroup: first.ID,
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	// Close the loop: first now references second and dials through it.
	_, err = service.Update(ctx, first.ID, UpdateRequest{
		Version:          first.Version,
		Name:             first.Name,
		Strategy:         "manual",
		SourceSpec:       SourceSpec{GroupIDs: []string{second.ID}},
		Enabled:          true,
		DialerProxyGroup: second.ID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Update() error = %v, want ErrInvalid for a dialer cycle", err)
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("Update() error = %v, want it to name the cycle", err)
	}
}

// The compiler names a chained group's derived proxies "<node>|via|<group id>".
// The separator must stay impossible in an operator name, or a group could be
// named to collide with a derived proxy -- which Mihomo rejects as a duplicate
// name, failing the whole document.
func TestCreateRejectsReservedSeparatorInName(t *testing.T) {
	service, _ := newChainService(t)
	_, err := service.Create(context.Background(), CreateRequest{
		Name:       "hx-node-aaaaaaaaaaaaaaaa|via|group-1",
		Strategy:   "manual",
		SourceSpec: SourceSpec{IncludeDirect: true},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid for a reserved separator", err)
	}
}

// A chain of arbitrary depth must validate at every hop, and the stored value
// must survive a read so the compiler can act on it.
func TestChainValidatesAndPersistsAtDepth(t *testing.T) {
	service, _ := newChainService(t)
	ctx := context.Background()
	hop1 := createDirectGroup(t, service, "hop1")

	hop2, err := service.Create(ctx, CreateRequest{
		Name:             "hop2",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{GroupIDs: []string{hop1.ID}},
		DialerProxyGroup: hop1.ID,
	})
	if err != nil {
		t.Fatalf("create hop2: %v", err)
	}
	hop3, err := service.Create(ctx, CreateRequest{
		Name:             "hop3",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{GroupIDs: []string{hop2.ID}},
		DialerProxyGroup: hop2.ID,
	})
	if err != nil {
		t.Fatalf("create hop3: %v", err)
	}

	fetched, err := service.Get(ctx, hop3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.DialerProxyGroupID != hop2.ID {
		t.Fatalf("persisted dialer = %q, want %q", fetched.DialerProxyGroupID, hop2.ID)
	}
}

// A disabled dialer cannot carry traffic, so pointing a chain at one must fail
// rather than compile into a chain with nothing in the middle.
func TestRejectsDisabledDialerTarget(t *testing.T) {
	service, _ := newChainService(t)
	ctx := context.Background()
	disabled := false
	upstream, err := service.Create(ctx, CreateRequest{
		Name:       "disabled-upstream",
		Strategy:   "manual",
		SourceSpec: SourceSpec{IncludeDirect: true},
		Enabled:    &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Create(ctx, CreateRequest{
		Name:             "chained",
		Strategy:         "manual",
		SourceSpec:       SourceSpec{IncludeDirect: true},
		DialerProxyGroup: upstream.ID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid for a disabled dialer target", err)
	}
}

// The update path must both set and clear the chain: NULLIF on the column turns
// an empty value back into SQL NULL, and a mismatch there would leave a stale
// dialer silently in place.
func TestUpdateSetsAndClearsDialer(t *testing.T) {
	service, _ := newChainService(t)
	ctx := context.Background()
	upstream := createDirectGroup(t, service, "upstream")
	target := createDirectGroup(t, service, "target")

	// A chained group must not also offer DIRECT, so this spec sources nothing
	// directly; AllowEmpty keeps it valid without inventing members.
	updated, err := service.Update(ctx, target.ID, UpdateRequest{
		Version:          target.Version,
		Name:             target.Name,
		Strategy:         "manual",
		SourceSpec:       SourceSpec{AllowEmpty: true},
		Enabled:          true,
		DialerProxyGroup: upstream.ID,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.DialerProxyGroupID != upstream.ID {
		t.Fatalf("dialer after update = %q, want %q", updated.DialerProxyGroupID, upstream.ID)
	}

	cleared, err := service.Update(ctx, target.ID, UpdateRequest{
		Version:    updated.Version,
		Name:       updated.Name,
		Strategy:   "manual",
		SourceSpec: SourceSpec{AllowEmpty: true},
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("clearing Update() error = %v", err)
	}
	if cleared.DialerProxyGroupID != "" {
		t.Fatalf("dialer after clear = %q, want empty", cleared.DialerProxyGroupID)
	}
	// Read it back so the cleared value is proven gone in storage, not just absent
	// from the value the update happened to return.
	fetched, err := service.Get(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.DialerProxyGroupID != "" {
		t.Fatalf("persisted dialer after clear = %q, want empty", fetched.DialerProxyGroupID)
	}
}
