package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/subscription"
)

type DueSubscriptionRepository interface {
	ClaimDueSubscriptions(context.Context, time.Time, time.Time, int) ([]string, error)
}

type SubscriptionRefresher interface {
	Refresh(context.Context, string) (subscription.RefreshResult, error)
}

type SubscriptionConfig struct {
	Workers       int
	BatchSize     int
	PollInterval  time.Duration
	LeaseDuration time.Duration
}

type SubscriptionScheduler struct {
	repository DueSubscriptionRepository
	refresher  SubscriptionRefresher
	logger     *slog.Logger
	config     SubscriptionConfig
	now        func() time.Time
}

func NewSubscriptionScheduler(
	repository DueSubscriptionRepository,
	refresher SubscriptionRefresher,
	logger *slog.Logger,
	config SubscriptionConfig,
) (*SubscriptionScheduler, error) {
	if repository == nil {
		return nil, errors.New("due subscription repository is required")
	}
	if refresher == nil {
		return nil, errors.New("subscription refresher is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.Workers == 0 {
		config.Workers = 4
	}
	if config.BatchSize == 0 {
		config.BatchSize = 100
	}
	if config.PollInterval == 0 {
		config.PollInterval = 30 * time.Second
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 5 * time.Minute
	}
	if config.Workers < 1 || config.Workers > 64 {
		return nil, errors.New("subscription scheduler workers must be between 1 and 64")
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, errors.New("subscription scheduler batch size must be between 1 and 1000")
	}
	if config.PollInterval < time.Second {
		return nil, errors.New("subscription scheduler poll interval must be at least one second")
	}
	if config.LeaseDuration < config.PollInterval {
		return nil, errors.New("subscription scheduler lease must not be shorter than the poll interval")
	}
	return &SubscriptionScheduler{
		repository: repository,
		refresher:  refresher,
		logger:     logger,
		config:     config,
		now:        time.Now,
	}, nil
}

func (scheduler *SubscriptionScheduler) Run(ctx context.Context) error {
	jobs := make(chan string, scheduler.config.BatchSize)
	var workers sync.WaitGroup
	for workerID := 0; workerID < scheduler.config.Workers; workerID++ {
		workers.Add(1)
		go func(workerID int) {
			defer workers.Done()
			scheduler.runWorker(ctx, workerID, jobs)
		}(workerID)
	}
	defer func() {
		close(jobs)
		workers.Wait()
	}()

	if err := scheduler.claimAndEnqueue(ctx, jobs); err != nil && !errors.Is(err, context.Canceled) {
		scheduler.logger.ErrorContext(ctx, "initial subscription refresh claim failed", "error", err)
	}
	ticker := time.NewTicker(scheduler.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := scheduler.claimAndEnqueue(ctx, jobs); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				scheduler.logger.ErrorContext(ctx, "subscription refresh claim failed", "error", err)
			}
		}
	}
}

func (scheduler *SubscriptionScheduler) RunOnce(ctx context.Context) error {
	now := scheduler.now().UTC()
	ids, err := scheduler.repository.ClaimDueSubscriptions(
		ctx,
		now,
		now.Add(scheduler.config.LeaseDuration),
		scheduler.config.BatchSize,
	)
	if err != nil {
		return err
	}
	jobs := make(chan string, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)

	var workers sync.WaitGroup
	workerCount := scheduler.config.Workers
	if workerCount > len(ids) {
		workerCount = len(ids)
	}
	for workerID := 0; workerID < workerCount; workerID++ {
		workers.Add(1)
		go func(workerID int) {
			defer workers.Done()
			scheduler.runWorker(ctx, workerID, jobs)
		}(workerID)
	}
	workers.Wait()
	return ctx.Err()
}

func (scheduler *SubscriptionScheduler) claimAndEnqueue(ctx context.Context, jobs chan<- string) error {
	now := scheduler.now().UTC()
	ids, err := scheduler.repository.ClaimDueSubscriptions(
		ctx,
		now,
		now.Add(scheduler.config.LeaseDuration),
		scheduler.config.BatchSize,
	)
	if err != nil {
		return err
	}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case jobs <- id:
		}
	}
	return nil
}

func (scheduler *SubscriptionScheduler) runWorker(ctx context.Context, workerID int, jobs <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case id, open := <-jobs:
			if !open {
				return
			}
			startedAt := time.Now()
			result, err := scheduler.refresher.Refresh(ctx, id)
			if err != nil {
				scheduler.logger.WarnContext(
					ctx,
					"scheduled subscription refresh failed",
					"subscription_id", id,
					"worker", workerID,
					"duration_ms", time.Since(startedAt).Milliseconds(),
					"error", err,
				)
				continue
			}
			scheduler.logger.InfoContext(
				ctx,
				"scheduled subscription refresh completed",
				"subscription_id", id,
				"snapshot_id", result.SnapshotID,
				"changed", result.Changed,
				"estimated_nodes", result.EstimatedNodes,
				"worker", workerID,
				"duration_ms", time.Since(startedAt).Milliseconds(),
			)
		}
	}
}

func (config SubscriptionConfig) String() string {
	return fmt.Sprintf(
		"workers=%d batch=%d poll=%s lease=%s",
		config.Workers,
		config.BatchSize,
		config.PollInterval,
		config.LeaseDuration,
	)
}
