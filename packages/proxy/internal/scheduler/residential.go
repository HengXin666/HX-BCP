package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// ResidentialRefresher is the residential service surface the scheduler needs.
type ResidentialRefresher interface {
	MaintainExpiredClientSessions(context.Context, int) (int, error)
}

// ResidentialConfig controls the session-pool maintenance pass.
type ResidentialConfig struct {
	// PollInterval is how often channels are inspected.
	PollInterval time.Duration
	// MaximumRefreshesPerPass bounds expiry actions and their data-plane applies.
	MaximumRefreshesPerPass int
}

// ResidentialScheduler keeps residential session pools healthy.
//
// It only refreshes pools that are empty or stale; ordinary exit-IP rotation is
// consumer-driven and takes the cheap selector path instead. Refreshes are
// bounded per pass and run sequentially, so this never turns into an unbounded
// burst of data plane applies.
type ResidentialScheduler struct {
	service ResidentialRefresher
	logger  *slog.Logger
	config  ResidentialConfig
}

func NewResidentialScheduler(
	service ResidentialRefresher,
	logger *slog.Logger,
	config ResidentialConfig,
) (*ResidentialScheduler, error) {
	if service == nil {
		return nil, errors.New("residential service is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Minute
	}
	if config.PollInterval < 30*time.Second {
		return nil, errors.New("residential poll interval must be at least 30 seconds")
	}
	if config.MaximumRefreshesPerPass == 0 {
		config.MaximumRefreshesPerPass = 4
	}
	if config.MaximumRefreshesPerPass < 1 || config.MaximumRefreshesPerPass > 64 {
		return nil, errors.New("residential refreshes per pass must be between 1 and 64")
	}
	return &ResidentialScheduler{service: service, logger: logger, config: config}, nil
}

func (scheduler *ResidentialScheduler) Run(ctx context.Context) error {
	if err := scheduler.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		scheduler.logger.ErrorContext(ctx, "initial residential session pool pass failed", "error", err)
	}
	ticker := time.NewTicker(scheduler.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := scheduler.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				scheduler.logger.ErrorContext(ctx, "residential session pool pass failed", "error", err)
			}
		}
	}
}

func (scheduler *ResidentialScheduler) runOnce(ctx context.Context) error {
	processed, err := scheduler.service.MaintainExpiredClientSessions(ctx, scheduler.config.MaximumRefreshesPerPass)
	if err != nil {
		return err
	}
	if processed > 0 {
		scheduler.logger.InfoContext(ctx, "residential client session expiry actions completed", "count", processed)
	}
	return nil
}
