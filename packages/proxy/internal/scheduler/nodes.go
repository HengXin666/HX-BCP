package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/HengXin666/hx-pxy/internal/node"
)

type NodeChecker interface {
	CheckDue(context.Context) ([]node.CheckResult, error)
}

// NodeConfig only carries the scheduler poll cadence. The effective check
// interval, batch size, test URL, and timeout live in the node service's
// administrator-editable quality settings and are re-read on every pass.
type NodeConfig struct {
	PollInterval time.Duration
}

type NodeScheduler struct {
	checker NodeChecker
	logger  *slog.Logger
	config  NodeConfig
}

func NewNodeScheduler(checker NodeChecker, logger *slog.Logger, config NodeConfig) (*NodeScheduler, error) {
	if checker == nil {
		return nil, errors.New("node checker is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Minute
	}
	if config.PollInterval < 10*time.Second {
		return nil, errors.New("node quality poll interval must be at least 10 seconds")
	}
	return &NodeScheduler{checker: checker, logger: logger, config: config}, nil
}

func (scheduler *NodeScheduler) Run(ctx context.Context) error {
	if err := scheduler.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		scheduler.logger.ErrorContext(ctx, "initial node quality pass failed", "error", err)
	}
	ticker := time.NewTicker(scheduler.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := scheduler.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				scheduler.logger.ErrorContext(ctx, "node quality pass failed", "error", err)
			}
		}
	}
}

func (scheduler *NodeScheduler) runOnce(ctx context.Context) error {
	results, err := scheduler.checker.CheckDue(ctx)
	if err != nil {
		return err
	}
	for _, result := range results {
		attributes := []any{
			"node_id", result.Node.ID,
			"state", result.Node.LifecycleState,
			"success", result.Success,
		}
		if result.LatencyMS != nil {
			attributes = append(attributes, "latency_ms", *result.LatencyMS)
		}
		if result.ErrorCode != "" {
			attributes = append(attributes, "error_code", result.ErrorCode)
		}
		scheduler.logger.InfoContext(ctx, "node quality check completed", attributes...)
	}
	return nil
}
