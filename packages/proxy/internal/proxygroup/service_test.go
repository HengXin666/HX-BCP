package proxygroup

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func intPointer(value int) *int {
	return &value
}

func TestResolveNodeIDsCombinesSubscriptionsAndSelectsFastestRegion(t *testing.T) {
	spec := SourceSpec{
		SubscriptionIDs: []string{"subscription-a", "subscription-b", "subscription-c"},
		Regions:         []string{"jp"},
		States:          []string{"healthy"},
		SortBy:          "latency",
		Limit:           2,
	}
	candidates := []store.GroupNodeCandidate{
		{NodeConfigRecord: store.NodeConfigRecord{ID: "slow", DisplayName: "JP Tokyo 02", LifecycleState: "healthy"}, LastLatencyMS: intPointer(90), SubscriptionIDs: []string{"subscription-a"}},
		{NodeConfigRecord: store.NodeConfigRecord{ID: "fast", DisplayName: "日本 东京 01", LifecycleState: "healthy"}, LastLatencyMS: intPointer(30), SubscriptionIDs: []string{"subscription-b"}},
		{NodeConfigRecord: store.NodeConfigRecord{ID: "middle", DisplayName: "Japan Osaka", LifecycleState: "healthy"}, LastLatencyMS: intPointer(55), SubscriptionIDs: []string{"subscription-c"}},
		{NodeConfigRecord: store.NodeConfigRecord{ID: "other", DisplayName: "US Los Angeles", LifecycleState: "healthy"}, LastLatencyMS: intPointer(10), SubscriptionIDs: []string{"subscription-a"}},
	}
	resolved := ResolveNodeIDs(spec, candidates)
	if len(resolved) != 2 || resolved[0] != "fast" || resolved[1] != "middle" {
		t.Fatalf("ResolveNodeIDs() = %#v", resolved)
	}
}

type testReconciler struct {
	calls int
	err   error
}

func (reconciler *testReconciler) Apply(context.Context) error {
	reconciler.calls++
	return reconciler.err
}

func TestProxyGroupRequiresNodeOrDirectAndReconcilesMutations(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	reconciler := &testReconciler{}
	service, err := NewService(database, reconciler)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Create(ctx, CreateRequest{
		Name:       "invalid",
		Strategy:   "manual",
		SourceSpec: SourceSpec{},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}

	created, err := service.Create(ctx, CreateRequest{
		Name:     "local-direct",
		Strategy: "manual",
		SourceSpec: SourceSpec{
			IncludeDirect: true,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if reconciler.calls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", reconciler.calls)
	}
	if created.Version != 1 || !created.SourceSpec.IncludeDirect {
		t.Fatalf("unexpected created group: %+v", created)
	}

	_, err = service.Create(ctx, CreateRequest{
		Name:     "local-direct",
		Strategy: "manual",
		SourceSpec: SourceSpec{
			IncludeDirect: true,
		},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Create() error = %v, want ErrConflict", err)
	}

	if err := service.Delete(ctx, created.ID, created.Version); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if reconciler.calls != 2 {
		t.Fatalf("reconcile calls = %d, want 2", reconciler.calls)
	}
}

func TestProxyGroupReturnsSavedResourceWhenApplyFails(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	reconciler := &testReconciler{err: errors.New("apply failed")}
	service, err := NewService(database, reconciler)
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.Create(ctx, CreateRequest{
		Name:     "saved-before-apply",
		Strategy: "manual",
		SourceSpec: SourceSpec{
			IncludeDirect: true,
		},
	})
	if err == nil || created.ID == "" {
		t.Fatalf("Create() = (%+v, %v), want saved resource and apply error", created, err)
	}
	persisted, getErr := service.Get(ctx, created.ID)
	if getErr != nil || persisted.Name != created.Name {
		t.Fatalf("Get() = (%+v, %v)", persisted, getErr)
	}
}
