package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestClaimDueSubscriptionsUsesLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	now := time.Date(2026, time.July, 25, 15, 30, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	createScheduledSubscription(t, database, "due-a", true, &past, now)
	createScheduledSubscription(t, database, "due-b", true, nil, now)
	createScheduledSubscription(t, database, "future", true, &future, now)
	createScheduledSubscription(t, database, "disabled", false, &past, now)

	leaseUntil := now.Add(5 * time.Minute)
	claimed, err := database.ClaimDueSubscriptions(ctx, now, leaseUntil, 100)
	if err != nil {
		t.Fatalf("ClaimDueSubscriptions() error = %v", err)
	}
	if len(claimed) != 2 || claimed[0] != "due-a" || claimed[1] != "due-b" {
		t.Fatalf("claimed subscriptions = %#v", claimed)
	}

	claimedAgain, err := database.ClaimDueSubscriptions(ctx, now.Add(time.Minute), now.Add(6*time.Minute), 100)
	if err != nil {
		t.Fatalf("second ClaimDueSubscriptions() error = %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("second claim = %#v, want none while lease is active", claimedAgain)
	}

	afterLease, err := database.ClaimDueSubscriptions(ctx, leaseUntil.Add(time.Second), leaseUntil.Add(6*time.Minute), 100)
	if err != nil {
		t.Fatalf("post-lease ClaimDueSubscriptions() error = %v", err)
	}
	if len(afterLease) != 2 {
		t.Fatalf("post-lease claim = %#v, want two recovered jobs", afterLease)
	}
}

func createScheduledSubscription(
	t *testing.T,
	database *Store,
	id string,
	enabled bool,
	nextRefreshAt *time.Time,
	now time.Time,
) {
	t.Helper()
	_, err := database.CreateSubscription(context.Background(), SubscriptionRecord{
		ID:                     id,
		Name:                   id,
		SourceType:             "inline",
		SourceConfigEncrypted:  []byte("encrypted-placeholder"),
		Enabled:                enabled,
		RefreshIntervalSeconds: 3600,
		NextRefreshAt:          nextRefreshAt,
		Version:                1,
		CreatedAt:              now,
		UpdatedAt:              now,
	})
	if err != nil {
		t.Fatalf("CreateSubscription(%s) error = %v", id, err)
	}
}
