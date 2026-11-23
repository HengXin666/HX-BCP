// Package logretention 按时间清理产品自己写下的日志文件与陈旧运行时实例目录。
//
// 决策与取舍见
// .agents/notes/implemented/feature/2026-10-14-log-retention-by-mtime.md。
//
// 职责边界 (刻意收窄): 只做文件系统删除, 不解析日志、不落库、不认识数据库或配置格式。
// 它靠"显式给的根目录 + 文件模式 + 活跃路径白名单"工作, 因此既能清 mihomo 的
// 字节轮转日志 (mihomo.log / mihomo.log.N), 也能清 HOME 下按运行目录摘要命名、
// 越积越多的空闲实例目录, 而不会碰到 active.yaml / 数据库 / 快照 / master.key。
package logretention

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MaxDays 是保留天数允许的上限, 与 systemsettings 的校验保持一致:
// 十年以上的日志没有保留价值, 却会让一次清理遍历过深。
const MaxDays = 3650

// MaxFileBytes 限制单个被删除文件的大小。日志轮转文件远小于此值; 超过它的
// "*.log" 更可能是别人放错位置的数据, 交给人看比直接删掉安全。
const MaxFileBytes = 64 << 20

// DefaultMaxEntries 限制一次 Prune 处理的文件系统条目数, 避免一次清理把
// 巨大目录整棵走完。到达上限时 Report.Truncated 为 true, 下一次 tick 重新扫描；长期截断需要收窄根目录。
const DefaultMaxEntries = 2000

// DefaultMihomoLogPattern 匹配受管 Mihomo 的字节轮转日志: mihomo.log 与
// mihomo.log.1/mihomo.log.2... (见 internal/dataplane/mihomo/logwriter.go 的 rotate)。
const DefaultMihomoLogPattern = "mihomo.log*"

// Mode 决定一个根目录下被判定过期的条目如何删除。
type Mode string

const (
	// ModeFiles 只删根目录下 (递归时含子目录下) 匹配 Patterns 且过期的普通文件。
	// 目录结构、active.yaml、数据库、快照、artifacts 一律不碰。
	ModeFiles Mode = "files"
	// ModeInstanceDirectories 把根目录下每个直接子目录当成一个实例目录, 过期的整体删除。
	// 子目录名必须匹配 InstanceNamePattern (产品自己的哈希命名), 其它子目录一律不动。
	ModeInstanceDirectories Mode = "instance-directories"
)

// Policy 描述"哪个根目录、删什么、多久算过期"。
type Policy struct {
	// Root 是清理根目录; 不存在时跳过 (不算错误)。
	Root string
	// Mode 见 Mode 的说明; 空值等同 ModeFiles。
	Mode Mode
	// Patterns 是 ModeFiles 下按 basename 匹配的 filepath.Match 模式。
	// 空值等同 []string{DefaultMihomoLogPattern}。
	Patterns []string
	// Days 是保留天数: mtime 早于 now-Days 视为过期。<=0 表示禁用 (整个 Policy 跳过)。
	Days int
	// RequireMarker 在 ModeInstanceDirectories 下要求子目录含 MarkerName。
	RequireMarker bool
}

// Result 记录一个被判定 (或试图) 删除的条目。
type Result struct {
	Path    string
	Kind    string // "file" | "instance-directory"
	Size    int64
	Age     time.Duration
	Deleted bool
	Reason  string
	Err     error
}

// Report 汇总一次 Prune。
type Report struct {
	Results   []Result
	Deleted   int
	Failed    int
	Freed     int64
	Visited   int
	Truncated bool
}

// DeletedPaths 返回本次真正删除的路径, 顺序与遍历一致。
func (report Report) DeletedPaths() []string {
	paths := make([]string, 0, len(report.Results))
	for _, result := range report.Results {
		if result.Deleted {
			paths = append(paths, result.Path)
		}
	}
	return paths
}

// Prune 删除 root 下过期 (按 mtime) 的日志文件或陈旧实例目录。
//
// 判定口径 (选择及其理由):
//   - 文件: 用自己的 mtime。
//   - 实例目录: 取"目录自身 mtime 与直接子项 mtime 中最新者"作为年龄。目录 mtime 是
//     自己或子项被改动的时间; 两者取最新, 只是因为删除目录时会连内容一起删 —— 用一个
//     比内容更新的时间做判据, 只会让删除更保守。
//
// 白名单里的路径 (调用方必须传入当前活跃的运行时目录/日志文件) 永不删除, 且不进入其子树。
// "正在使用的活跃目录"因此不依赖 mtime 猜测。
//
// 符号链接: 一律不跟随、不删除 —— 遍历里直接跳过, 删除前再 lstat 复核一次
// (TOCTOU: 判定与删除之间路径可能被换成链接)。
//
// 所有删除错误都记进 Report 而不中断: 一个删不掉的条目不该让剩下几百个过期文件留在盘上。
func Prune(ctx context.Context, policy Policy, whitelist map[string]struct{}, maxEntries int) (Report, error) {
	report := Report{}
	if policy.Days <= 0 {
		return report, nil
	}
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	days := policy.Days
	if days > MaxDays {
		days = MaxDays
	}
	root := strings.TrimSpace(policy.Root)
	if root == "" {
		return report, errors.New("log retention root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return report, fmt.Errorf("resolve log retention root %s: %w", root, err)
	}
	mode := policy.Mode
	if mode == "" {
		mode = ModeFiles
	}
	if mode != ModeFiles && mode != ModeInstanceDirectories {
		return report, fmt.Errorf("unknown log retention mode %q", policy.Mode)
	}
	patterns := policy.Patterns
	if mode == ModeFiles && len(patterns) == 0 {
		patterns = []string{DefaultMihomoLogPattern}
	}
	for _, pattern := range patterns {
		if _, err := filepath.Match(pattern, "probe"); err != nil {
			return report, fmt.Errorf("invalid log retention pattern %q: %w", pattern, err)
		}
	}
	if _, err := os.Lstat(absolute); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return report, nil
		}
		return report, fmt.Errorf("inspect log retention root %s: %w", absolute, err)
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)

	walkErr := filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkError error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if path != absolute {
			if report.Visited >= maxEntries {
				report.Truncated = true
				return filepath.SkipAll
			}
			report.Visited++
		}
		if walkError != nil {
			// 根目录本身不可读是配置错误, 必须冒泡; 子项读不到只是少清一个条目。
			if path == absolute {
				return walkError
			}
			report.Results = append(report.Results, Result{Path: path, Reason: "unreadable", Err: walkError})
			report.Failed++
			return nil
		}
		if filepath.Clean(path) == filepath.Clean(absolute) {
			return nil
		}
		if isWhitelisted(whitelist, path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		switch {
		case mode == ModeInstanceDirectories:
			if !entry.IsDir() {
				return nil
			}
			// 实例目录不再深入: 整体删除或整体保留。
			if !matchesInstanceName(entry.Name()) {
				return filepath.SkipDir
			}
			if policy.RequireMarker && !hasMarker(path) {
				return filepath.SkipDir
			}
			modTime, size, ageErr := directoryAge(path, entry)
			if ageErr != nil {
				report.Results = append(report.Results, Result{Path: path, Kind: "instance-directory", Reason: "unreadable", Err: ageErr})
				report.Failed++
				return filepath.SkipDir
			}
			if modTime.After(cutoff) {
				return filepath.SkipDir
			}
			// A different running instance may have an old, idle socket. Mtime
			// alone is never proof that its Controller directory is abandoned.
			marker := filepath.Join(path, MarkerName)
			if info, err := os.Lstat(marker); err == nil && info.Mode()&os.ModeSocket != 0 {
				connection, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "unix", marker)
				if err == nil {
					_ = connection.Close()
					return filepath.SkipDir
				}
				if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, syscall.ENOENT) {
					return filepath.SkipDir
				}
			}
			report.deleteEntry(path, "instance-directory", size, modTime)
			return filepath.SkipDir
		case entry.IsDir():
			// ModeFiles 只删文件: 目录本身既不删也不跟随, 只继续向下找日志。
			return nil
		default:
			if !matchesAnyPattern(patterns, entry.Name()) {
				return nil
			}
			info, statErr := entry.Info()
			if statErr != nil {
				report.Results = append(report.Results, Result{Path: path, Kind: "file", Reason: "unreadable", Err: statErr})
				report.Failed++
				return nil
			}
			if info.Size() > MaxFileBytes {
				report.Results = append(report.Results, Result{Path: path, Kind: "file", Size: info.Size(), Reason: "oversized"})
				return nil
			}
			if info.ModTime().After(cutoff) {
				return nil
			}
			report.deleteEntry(path, "file", info.Size(), info.ModTime())
			return nil
		}
	})
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) {
		return report, fmt.Errorf("walk log retention root %s: %w", absolute, walkErr)
	}
	return report, nil
}

// deleteEntry 执行删除并回填结果。
//
// 删除前 lstat 复核类型: 判定与删除之间路径可能被换成符号链接, 直接 os.Remove 会删掉链接
// 本身 (不是目标), 但 os.RemoveAll 对目录就可能被诱导; 复核后只对真实类型的条目动手。
func (report *Report) deleteEntry(path, kind string, size int64, modTime time.Time) {
	result := Result{Path: path, Kind: kind, Size: size}
	if !modTime.IsZero() {
		result.Age = time.Since(modTime)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		result.Reason = "unreadable"
		result.Err = statErr
		report.Results = append(report.Results, result)
		report.Failed++
		return
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		result.Reason = "symlink"
		report.Results = append(report.Results, result)
		return
	}
	if info.IsDir() {
		result.Err = os.RemoveAll(path)
	} else {
		result.Err = os.Remove(path)
	}
	if result.Err != nil {
		result.Reason = "remove-failed"
		report.Results = append(report.Results, result)
		report.Failed++
		return
	}
	result.Deleted = true
	report.Deleted++
	report.Freed += size
	report.Results = append(report.Results, result)
}

// Log 把一次清理的可观测结果写进结构化日志: 有删除才 Info, 失败必 Warn。
func (report Report) Log(ctx context.Context, logger *slog.Logger, root string) {
	if logger == nil {
		return
	}
	if report.Deleted > 0 {
		logger.InfoContext(ctx, "log retention removed expired entries",
			"component", "logretention",
			"root", root,
			"deleted", report.Deleted,
			"freed_bytes", report.Freed,
			"truncated", report.Truncated,
		)
	}
	if report.Failed > 0 {
		failures := make([]Result, 0, report.Failed)
		for _, result := range report.Results {
			if result.Err != nil {
				failures = append(failures, result)
			}
		}
		logger.WarnContext(ctx, "log retention could not remove some entries",
			"component", "logretention",
			"root", root,
			"failed", report.Failed,
			"first_path", firstPath(failures),
			"error", firstError(failures),
		)
	}
}

// directoryAge 返回实例目录的年龄判据时间与总大小。
//
// 选择"目录自身 mtime 与直接子项 mtime 中最新者": 目录 mtime 只在自己或子项被改动时更新,
// 两者取最新 = 只有内容也一起变老才删, 比只信目录 mtime 保守。
func directoryAge(path string, entry fs.DirEntry) (time.Time, int64, error) {
	info, err := entry.Info()
	if err != nil {
		return time.Time{}, 0, err
	}
	newest := info.ModTime()
	var size int64
	entries, readErr := os.ReadDir(path)
	if readErr != nil {
		return newest, 0, nil
	}
	for _, child := range entries {
		childInfo, childErr := child.Info()
		if childErr != nil {
			continue
		}
		size += childInfo.Size()
		if childInfo.ModTime().After(newest) {
			newest = childInfo.ModTime()
		}
	}
	return newest, size, nil
}

// hasMarker 判断目录是否含产品实例特征文件 (mihomo.sock)。
func hasMarker(path string) bool {
	info, err := os.Lstat(filepath.Join(path, MarkerName))
	if err != nil {
		return false
	}
	return info.Mode()&fs.ModeSymlink == 0
}

// isWhitelisted 判断路径是否与白名单中的某条完全相等。
func isWhitelisted(whitelist map[string]struct{}, path string) bool {
	if len(whitelist) == 0 {
		return false
	}
	_, found := whitelist[filepath.Clean(path)]
	return found
}

// matchesInstanceName 判断目录名是否是产品的哈希实例名 (16 位小写十六进制)。
func matchesInstanceName(name string) bool {
	matched, err := filepath.Match(InstanceNamePattern, name)
	return err == nil && matched
}

// matchesAnyPattern 按 basename 逐个匹配 filepath.Match 模式。
func matchesAnyPattern(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return true
		}
	}
	return false
}

func firstPath(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	return results[0].Path
}

func firstError(results []Result) error {
	for _, result := range results {
		if result.Err != nil {
			return result.Err
		}
	}
	return nil
}
