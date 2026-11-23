package logretention

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// testLogger 只保留 Error 级别: 清理日志不是被断言的输出。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// writeFile 造一个带指定 mtime 的文件 (含父目录)。
func writeFile(t *testing.T, path string, age time.Duration, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	modTime := time.Now().Add(-age)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// writeDir 造一个只含一个文件的实例目录, 目录与文件的 mtime 都设为 age。
func writeDir(t *testing.T, path string, age time.Duration) {
	t.Helper()
	writeFile(t, filepath.Join(path, MarkerName), age, 4)
	modTime := time.Now().Add(-age)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s should have been deleted, stat error = %v", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s should still exist: %v", path, err)
	}
}

// TestPruneFilesDeletesOnlyExpiredLogs 是这条策略的核心断言: 过期删, 未过期留,
// 并且同一目录下的非日志文件 (数据库/配置/密钥形状) 一律不删。
func TestPruneFilesDeletesOnlyExpiredLogs(t *testing.T) {
	root := t.TempDir()
	expired := filepath.Join(root, "mihomo.log")
	fresh := filepath.Join(root, "runtime", "mihomo.log")
	expiredRotated := filepath.Join(root, "mihomo.log.1")
	freshRotated := filepath.Join(root, "mihomo.log.2")
	writeFile(t, expired, 40*24*time.Hour, 16)
	writeFile(t, fresh, 2*time.Hour, 32)
	writeFile(t, expiredRotated, 31*24*time.Hour, 8)
	writeFile(t, freshRotated, 29*24*time.Hour, 8)
	mustKeep := []string{
		filepath.Join(root, "hx-pxy.db"),
		filepath.Join(root, "master.key"),
		filepath.Join(root, "runtime", "active.yaml"),
		filepath.Join(root, "runtime", "cache.db"),
		filepath.Join(root, "artifacts", "bundle.zip"),
		filepath.Join(root, "notes.txt"),
	}
	for _, path := range mustKeep {
		writeFile(t, path, 90*24*time.Hour, 8)
	}

	report, err := Prune(context.Background(), Policy{Root: root, Mode: ModeFiles, Days: 30}, nil, 0)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	mustNotExist(t, expired)
	mustNotExist(t, expiredRotated)
	mustExist(t, fresh)
	mustExist(t, freshRotated)
	for _, path := range mustKeep {
		mustExist(t, path)
	}
	if report.Deleted != 2 {
		t.Fatalf("deleted = %d (%v), want 2", report.Deleted, report.DeletedPaths())
	}
	if report.Freed != 24 {
		t.Fatalf("freed = %d, want 24", report.Freed)
	}
	if report.Failed != 0 {
		t.Fatalf("failed = %d, want 0", report.Failed)
	}
}

// TestPruneFilesRespectsDaysBoundary 钉住判据是 mtime 而不是别的东西:
// 29 天留, 31 天删。
func TestPruneFilesRespectsDaysBoundary(t *testing.T) {
	root := t.TempDir()
	fresh := filepath.Join(root, "mihomo.log")
	expired := filepath.Join(root, "runtime", "mihomo.log")
	writeFile(t, fresh, 29*24*time.Hour, 4)
	writeFile(t, expired, 31*24*time.Hour, 4)

	if _, err := Prune(context.Background(), Policy{Root: root, Days: 30}, nil, 0); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	mustExist(t, fresh)
	mustNotExist(t, expired)
}

// TestPruneFilesSkipsSymlinks 断言符号链接既不跟随也不删除:
// 链接本身留下, 它的目标 (目录外) 更不能被删。
func TestPruneFilesSkipsSymlinks(t *testing.T) {
	outside := t.TempDir()
	target := filepath.Join(outside, "real.log")
	writeFile(t, target, 90*24*time.Hour, 16)

	root := t.TempDir()
	link := filepath.Join(root, "mihomo.log")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	report, err := Prune(context.Background(), Policy{Root: root, Days: 30}, nil, 0)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if report.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0 (symlinks are never followed)", report.Deleted)
	}
	mustExist(t, link)
	mustExist(t, target)
}

// TestPruneFilesHonoursWhitelist 断言白名单路径 (活跃日志) 永不删除且不被进入。
func TestPruneFilesHonoursWhitelist(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "runtime", "mihomo.log")
	other := filepath.Join(root, "runtime", "mihomo.log.1")
	writeFile(t, active, 90*24*time.Hour, 16)
	writeFile(t, other, 90*24*time.Hour, 16)

	report, err := Prune(context.Background(), Policy{Root: root, Days: 30}, Whitelist(active), 0)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	mustExist(t, active)
	mustNotExist(t, other)
	if report.Deleted != 1 {
		t.Fatalf("deleted = %d (%v), want 1", report.Deleted, report.DeletedPaths())
	}
}

// TestPruneFilesDisabledWhenDaysZero 断言 0 表示禁用: 一个字节都不动。
func TestPruneFilesDisabledWhenDaysZero(t *testing.T) {
	root := t.TempDir()
	expired := filepath.Join(root, "mihomo.log")
	writeFile(t, expired, 900*24*time.Hour, 16)

	for _, days := range []int{0, -1} {
		report, err := Prune(context.Background(), Policy{Root: root, Days: days}, nil, 0)
		if err != nil {
			t.Fatalf("Prune(days=%d) error = %v", days, err)
		}
		if report.Deleted != 0 || len(report.Results) != 0 {
			t.Fatalf("days=%d deleted = %d, want 0", days, report.Deleted)
		}
		mustExist(t, expired)
	}
}

// TestPruneFilesMissingRootIsNotAnError 断言根目录不存在时安静跳过:
// 未安装的产品或已删除的目录不该让后台任务报错。
func TestPruneFilesMissingRootIsNotAnError(t *testing.T) {
	report, err := Prune(context.Background(), Policy{Root: filepath.Join(t.TempDir(), "absent"), Days: 30}, nil, 0)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if report.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0", report.Deleted)
	}
}

// TestPruneFilesRespectsEntryLimit 断言条目上限会截断这一轮 (并如实标记),
// 而不是把无界目录一次走完。
func TestPruneFilesRespectsEntryLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"mihomo.log.1", "mihomo.log.2", "mihomo.log.3"} {
		writeFile(t, filepath.Join(root, name), 90*24*time.Hour, 4)
	}

	report, err := Prune(context.Background(), Policy{Root: root, Days: 30}, nil, 2)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if !report.Truncated {
		t.Fatal("truncated = false, want true at the entry limit")
	}
	if report.Visited != 2 {
		t.Fatalf("visited = %d, want 2", report.Visited)
	}
	if report.Deleted > 2 {
		t.Fatalf("deleted = %d, want at most the entry limit", report.Deleted)
	}
}

// TestPruneFilesHonoursCanceledContext 断言取消路径存在: 取消后不再删除。
func TestPruneFilesHonoursCanceledContext(t *testing.T) {
	root := t.TempDir()
	expired := filepath.Join(root, "mihomo.log")
	writeFile(t, expired, 90*24*time.Hour, 4)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := Prune(ctx, Policy{Root: root, Days: 30}, nil, 0)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if report.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0 after cancel", report.Deleted)
	}
	mustExist(t, expired)
}

// TestPruneInstanceDirectoriesDeletesOnlyStaleManagedOnes 是本条策略的核心断言:
// 过期且含 mihomo.sock 的哈希实例目录整体删除; 新鲜的、没有产品特征的、非哈希命名的、
// 以及白名单里的活跃目录都留下。
func TestPruneInstanceDirectoriesDeletesOnlyStaleManagedOnes(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "01682cfc45cc4c88")
	active := filepath.Join(root, "e9f7df3e967e0c91")
	fresh := filepath.Join(root, "13056356b20e4dcd")
	unmanaged := filepath.Join(root, "aaaaaaaaaaaaaaaa") // 哈希形状但没有 mihomo.sock
	other := filepath.Join(root, "not-an-instance")
	notOldEnough := filepath.Join(root, "bbbbbbbbbbbbbbbb")

	writeDir(t, stale, 40*24*time.Hour)
	writeDir(t, active, 40*24*time.Hour)
	writeDir(t, fresh, time.Hour)
	if err := os.MkdirAll(unmanaged, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", unmanaged, err)
	}
	modTime := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(unmanaged, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", unmanaged, err)
	}
	writeDir(t, other, 40*24*time.Hour)
	writeDir(t, notOldEnough, time.Hour)

	report, err := Prune(context.Background(), Policy{
		Root:          root,
		Mode:          ModeInstanceDirectories,
		Days:          30,
		RequireMarker: true,
	}, Whitelist(active), 0)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	mustNotExist(t, stale)
	mustExist(t, active)
	mustExist(t, fresh)
	mustExist(t, unmanaged)
	mustExist(t, other)
	mustExist(t, notOldEnough)
	if report.Deleted != 1 {
		t.Fatalf("deleted = %d (%v), want 1", report.Deleted, report.DeletedPaths())
	}
}

// TestPruneInstanceDirectoryAgeUsesNewestChild 断言目录判据是"目录自身 mtime 与直接子项
// mtime 中最新者": 目录本身很旧、但内容刚动过的实例目录不能被当陈旧删除。
func TestPruneInstanceDirectoryAgeUsesNewestChild(t *testing.T) {
	root := t.TempDir()
	instance := filepath.Join(root, "01682cfc45cc4c88")
	writeDir(t, instance, 40*24*time.Hour)
	fresh := filepath.Join(instance, "mihomo.log")
	writeFile(t, fresh, time.Hour, 8)
	if err := os.Chtimes(instance, time.Now().Add(-40*24*time.Hour), time.Now().Add(-40*24*time.Hour)); err != nil {
		t.Fatalf("chtimes %s: %v", instance, err)
	}

	report, err := Prune(context.Background(), Policy{
		Root: root, Mode: ModeInstanceDirectories, Days: 30, RequireMarker: true,
	}, nil, 0)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	mustExist(t, instance)
	if report.Deleted != 0 {
		t.Fatalf("deleted = %d (%v), want 0", report.Deleted, report.DeletedPaths())
	}
}

// TestPruneRejectsInvalidArguments 断言配置错误是显式错误, 不是静默不动或乱动。
func TestPruneRejectsInvalidArguments(t *testing.T) {
	if _, err := Prune(context.Background(), Policy{Root: "  ", Days: 30}, nil, 0); err == nil {
		t.Fatal("empty root should be rejected")
	}
	if _, err := Prune(context.Background(), Policy{Root: t.TempDir(), Mode: Mode("nonsense"), Days: 30}, nil, 0); err == nil {
		t.Fatal("unknown mode should be rejected")
	}
	if _, err := Prune(context.Background(), Policy{Root: t.TempDir(), Patterns: []string{"["}, Days: 30}, nil, 0); err == nil {
		t.Fatal("invalid pattern should be rejected")
	}
}

// TestPruneClampsOversizedDays 断言超过上限的保留天数被夹到 MaxDays, 不会溢出或走飞。
func TestPruneClampsOversizedDays(t *testing.T) {
	root := t.TempDir()
	ancient := filepath.Join(root, "mihomo.log")
	writeFile(t, ancient, 100*24*time.Hour, 4)

	if _, err := Prune(context.Background(), Policy{Root: root, Days: MaxDays * 2}, nil, 0); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	mustExist(t, ancient)
}

// TestInstanceDirectoryMatchesManagerAlgorithm 用固定的 sha256 结果钉住算法:
// 换算法 = 白名单保护错目录 (删掉正在用的实例目录)。
func TestInstanceDirectoryMatchesManagerAlgorithm(t *testing.T) {
	home := t.TempDir()
	got := InstanceDirectory(home, "/var/lib/hx-pxy/runtime")
	// sha256("/var/lib/hx-pxy/runtime")[:16] —— 与 internal/dataplane/mihomo 的
	// shortControllerSocketPath 用同一个算法, 换算法就是保护错目录。
	want := filepath.Join(home, ".hx-pxy-runtime", "e9f7df3e967e0c91")
	if got != want {
		t.Fatalf("InstanceDirectory() = %q, want %q", got, want)
	}
	if InstanceDirectory("", "/var/lib/hx-pxy/runtime") != "" {
		t.Fatal("empty HOME must not produce an instance directory")
	}
	if InstanceRoot("") != "" {
		t.Fatal("empty HOME must not produce an instance root")
	}
}

// TestDefaultPolicyCoversDataAndInstanceDirectory 断言默认范围恰好是 data 目录 (文件模式)
// 与 HOME 实例目录 (整目录模式), 且每条都能被赋予保留天数。
func TestDefaultPolicyCoversDataAndInstanceDirectory(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(t.TempDir(), "data")
	policies := DefaultPolicy(data, home)
	if len(policies) != 2 {
		t.Fatalf("policies = %d, want 2", len(policies))
	}
	if policies[0].Root != data || policies[0].Mode != ModeFiles {
		t.Fatalf("data policy = %+v", policies[0])
	}
	if policies[1].Root != InstanceRoot(home) || policies[1].Mode != ModeInstanceDirectories || !policies[1].RequireMarker {
		t.Fatalf("instance policy = %+v", policies[1])
	}
	if len(DefaultPolicy("", home)) != 1 || len(DefaultPolicy(data, "")) != 1 {
		t.Fatal("a missing data directory or HOME must drop exactly one policy")
	}
}

// TestRunnerPrunesImmediatelyAndStopsOnCancel 断言后台任务的两条硬性要求:
// 启动后立即清一次; 取消后返回 (不泄漏 goroutine), 且取消后不再删除。
func TestRunnerPrunesImmediatelyAndStopsOnCancel(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "mihomo.log")
	writeFile(t, stale, 90*24*time.Hour, 4)

	var calls atomic.Int64
	resolver := func(context.Context) (Plan, error) {
		calls.Add(1)
		return Plan{
			Days:     30,
			Policies: []Policy{{Root: root, Mode: ModeFiles}},
		}, nil
	}
	runner, err := NewRunner(resolver, testLogger(), time.Hour)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Lstat(stale); errors.Is(err, os.ErrNotExist) {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("the runner did not prune on start")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancel — goroutine leak")
	}
	if calls.Load() == 0 {
		t.Fatal("the resolver was never called")
	}

	// 取消之后再写一个过期文件, 确认没有任何后台 goroutine 还在清理。
	late := filepath.Join(root, "mihomo.log.9")
	writeFile(t, late, 90*24*time.Hour, 4)
	time.Sleep(50 * time.Millisecond)
	mustExist(t, late)
}

// TestRunnerSkipsWhenRetentionDisabled 断言保留天数 <=0 时一次删除都不做,
// 且设置读取失败不会让任务退出 (只告警)。
func TestRunnerSkipsWhenRetentionDisabled(t *testing.T) {
	root := t.TempDir()
	expired := filepath.Join(root, "mihomo.log")
	writeFile(t, expired, 400*24*time.Hour, 4)

	runner, err := NewRunner(
		func(context.Context) (Plan, error) { return Plan{Days: 0, Policies: []Policy{{Root: root}}}, nil },
		testLogger(),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.pruneOnce(context.Background())
	mustExist(t, expired)

	failing, err := NewRunner(
		func(context.Context) (Plan, error) { return Plan{}, errors.New("database unavailable") },
		testLogger(),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	failing.pruneOnce(context.Background())
	mustExist(t, expired)
}

// TestNewRunnerValidatesArguments 断言没有解析器时构造失败 (而不是起一个空转任务)。
func TestNewRunnerValidatesArguments(t *testing.T) {
	if _, err := NewRunner(nil, nil, time.Hour); err == nil {
		t.Fatal("a nil resolver should be rejected")
	}
	runner, err := NewRunner(func(context.Context) (Plan, error) { return Plan{}, nil }, nil, 0)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if runner.interval != DefaultInterval {
		t.Fatalf("interval = %s, want %s", runner.interval, DefaultInterval)
	}
}

func TestPruneBoundsTraversalOfUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		writeFile(t, filepath.Join(root, name), 90*24*time.Hour, 4)
	}
	report, err := Prune(context.Background(), Policy{Root: root, Days: 30}, nil, 2)
	if err != nil || report.Visited != 2 || !report.Truncated || report.Deleted != 0 {
		t.Fatalf("%+v %v", report, err)
	}
}
