package scheduler

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/subscription"
)

func TestSubscriptionSchedulerRunOnceUsesBoundedWorkers(t *testing.T) {
	t.Parallel()

	repository := &fakeDueRepository{ids: []string{"a", "b", "c", "d"}}
	refresher := &fakeRefresher{}
	scheduler, err := NewSubscriptionScheduler(
		repository,
		refresher,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		SubscriptionConfig{
			Workers:       2,
			BatchSize:     10,
			PollInterval:  time.Second,
			LeaseDuration: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("NewSubscriptionScheduler() error = %v", err)
	}
	fixedNow := time.Date(2026, time.July, 25, 16, 0, 0, 0, time.UTC)
	scheduler.now = func() time.Time { return fixedNow }

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !repository.now.Equal(fixedNow) || !repository.leaseUntil.Equal(fixedNow.Add(time.Minute)) {
		t.Fatalf("claim times: now=%s lease=%s", repository.now, repository.leaseUntil)
	}
	if repository.limit != 10 {
		t.Fatalf("claim limit = %d, want 10", repository.limit)
	}

	refresher.mu.Lock()
	refreshed := append([]string(nil), refresher.ids...)
	maximumActive := refresher.maximumActive
	refresher.mu.Unlock()
	slices.Sort(refreshed)
	if !slices.Equal(refreshed, []string{"a", "b", "c", "d"}) {
		t.Fatalf("refreshed ids = %#v", refreshed)
	}
	if maximumActive < 1 || maximumActive > 2 {
		t.Fatalf("maximum active refreshes = %d, want 1..2", maximumActive)
	}
}

func TestSubscriptionSchedulerValidatesBounds(t *testing.T) {
	t.Parallel()

	repository := &fakeDueRepository{}
	refresher := &fakeRefresher{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	invalid := []SubscriptionConfig{
		{Workers: -1, BatchSize: 1, PollInterval: time.Second, LeaseDuration: time.Second},
		{Workers: 1, BatchSize: 1001, PollInterval: time.Second, LeaseDuration: time.Second},
		{Workers: 1, BatchSize: 1, PollInterval: time.Millisecond, LeaseDuration: time.Second},
		{Workers: 1, BatchSize: 1, PollInterval: time.Minute, LeaseDuration: time.Second},
	}
	for _, config := range invalid {
		if _, err := NewSubscriptionScheduler(repository, refresher, logger, config); err == nil {
			t.Errorf("NewSubscriptionScheduler(%+v) error = nil", config)
		}
	}
}

type fakeDueRepository struct {
	ids        []string
	now        time.Time
	leaseUntil time.Time
	limit      int
}

func (repository *fakeDueRepository) ClaimDueSubscriptions(
	_ context.Context,
	now time.Time,
	leaseUntil time.Time,
	limit int,
) ([]string, error) {
	repository.now = now
	repository.leaseUntil = leaseUntil
	repository.limit = limit
	return append([]string(nil), repository.ids...), nil
}

type fakeRefresher struct {
	mu            sync.Mutex
	ids           []string
	active        int
	maximumActive int
}

func (refresher *fakeRefresher) Refresh(
	ctx context.Context,
	id string,
) (subscription.RefreshResult, error) {
	refresher.mu.Lock()
	refresher.active++
	if refresher.active > refresher.maximumActive {
		refresher.maximumActive = refresher.active
	}
	refresher.mu.Unlock()

	select {
	case <-ctx.Done():
		return subscription.RefreshResult{}, ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}

	refresher.mu.Lock()
	refresher.ids = append(refresher.ids, id)
	refresher.active--
	refresher.mu.Unlock()
	return subscription.RefreshResult{SubscriptionID: id, SnapshotID: "snapshot-" + id}, nil
}
