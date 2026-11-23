package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type AlertEvaluator interface {
	Evaluate(context.Context) error
}

type AlertConfig struct {
	// Interval between evaluations. Defaults to 60 seconds.
	Interval time.Duration
}

// AlertScheduler runs the alert evaluator on a fixed interval with a single
// goroutine. Evaluation errors are logged and never stop the loop.
type AlertScheduler struct {
	evaluator AlertEvaluator
	logger    *slog.Logger
	interval  time.Duration
}

func NewAlertScheduler(evaluator AlertEvaluator, logger *slog.Logger, config AlertConfig) (*AlertScheduler, error) {
	if evaluator == nil {
		return nil, errors.New("alert evaluator is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	interval := config.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &AlertScheduler{evaluator: evaluator, logger: logger, interval: interval}, nil
}

func (s *AlertScheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			evaluateCtx, cancel := context.WithTimeout(ctx, s.interval)
			if err := s.evaluator.Evaluate(evaluateCtx); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("alert evaluation failed", "error", err)
			}
			cancel()
		}
	}
}
