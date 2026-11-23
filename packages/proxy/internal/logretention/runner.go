package logretention

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// DefaultInterval 是两次清理之间的间隔: 保留期以天计, 每天跑一次让"过期"最多晚一天生效。
const DefaultInterval = 24 * time.Hour

// Plan 是一次清理的完整输入: 清理哪些根目录, 以及哪些路径绝对不能被删。
type Plan struct {
	Policies  []Policy
	Whitelist map[string]struct{}
	// Days 是保留天数, <=0 表示本次 (以及后续) 不做任何删除。
	Days int
}

// Resolver 返回当前应当执行的清理计划。它每次 tick 都被调用一次, 因此设置页改了
// log_retention_days 后无需重启即可生效。
type Resolver func(ctx context.Context) (Plan, error)

// Runner 是控制面里的后台清理任务: 启动时立即跑一次, 之后每个 Interval 跑一次。
//
// 生命周期与其它后台任务一致: Run 随 ctx 取消返回, 不留下游离 goroutine
// (AGENTS.md §5)。它自身不创建任何文件, 只在 Prune 里做删除。
type Runner struct {
	resolve  Resolver
	logger   *slog.Logger
	interval time.Duration
}

// NewRunner 构造后台清理任务。interval <= 0 时用 DefaultInterval。
func NewRunner(resolve Resolver, logger *slog.Logger, interval time.Duration) (*Runner, error) {
	if resolve == nil {
		return nil, errors.New("log retention resolver is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Runner{resolve: resolve, logger: logger, interval: interval}, nil
}

// Run 阻塞直到 ctx 取消: 立即执行一次, 之后每 Interval 执行一次。返回时与取消时的错误
// (context.Canceled / DeadlineExceeded) 一律吞掉 —— 取消是正常退出路径。
func (runner *Runner) Run(ctx context.Context) error {
	runner.pruneOnce(ctx)
	ticker := time.NewTicker(runner.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			runner.pruneOnce(ctx)
		}
	}
}

// pruneOnce 读取一次计划并执行全部 Policy。任何错误都只记日志: 清理失败既不该拖垮
// 控制面, 也不该让下一个 tick 停下来。
func (runner *Runner) pruneOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	plan, err := runner.resolve(ctx)
	if err != nil {
		runner.logger.WarnContext(ctx, "log retention settings unavailable", "component", "logretention", "error", err)
		return
	}
	if plan.Days <= 0 {
		return
	}
	for _, policy := range plan.Policies {
		if policy.Days <= 0 {
			policy.Days = plan.Days
		}
		report, pruneErr := Prune(ctx, policy, plan.Whitelist, DefaultMaxEntries)
		if pruneErr != nil {
			if ctx.Err() != nil {
				return
			}
			runner.logger.WarnContext(ctx, "log retention pass failed", "component", "logretention", "root", policy.Root, "error", pruneErr)
			continue
		}
		report.Log(ctx, runner.logger, policy.Root)
		if report.Truncated {
			runner.logger.WarnContext(ctx, "log retention stopped at the entry limit; the next pass continues",
				"component", "logretention", "root", policy.Root, "limit", DefaultMaxEntries)
		}
	}
}
