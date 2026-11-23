package logretention

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// InstanceNamePattern 是产品的短运行时实例目录名形状: 运行目录 sha256 的前 8 字节十六进制
// (见 internal/dataplane/mihomo/manager.go 的 shortControllerSocketPath)。写成 16 个字符类
// 而不是 {16}, 是因为 filepath.Match 不支持重复计数。
const InstanceNamePattern = "[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]"

// MarkerName 是实例目录必须含有的文件: 只有产品自己创建、并且确实被用作 socket 目录的
// 目录才符合这个特征。没有它, 一个恰好像哈希的目录不会被删 —— 宁可漏清, 不可误删。
const MarkerName = "mihomo.sock"

// DefaultPolicy 返回产品默认的清理范围:
//
//   - data 目录 (递归): mihomo.log 与 mihomo.log.N 轮转备份; 数据库 / master.key /
//     active.yaml / 快照 / artifacts 都不匹配, 因此不会被碰。
//   - HOME 下的短运行时实例目录: 每个哈希命名的实例目录整体删除, 当前活跃目录由
//     白名单排除。HOME 为空或该目录不存在时这一条自动跳过。
//
// 调用方负责填 Days 与白名单 (见 Runner 与 cmd/hx-pxyd/main.go)。
func DefaultPolicy(dataDirectory, homeDirectory string) []Policy {
	policies := make([]Policy, 0, 2)
	if directory := strings.TrimSpace(dataDirectory); directory != "" {
		policies = append(policies, Policy{
			Root:     directory,
			Mode:     ModeFiles,
			Patterns: []string{DefaultMihomoLogPattern},
		})
	}
	if directory := InstanceRoot(homeDirectory); directory != "" {
		policies = append(policies, Policy{
			Root:          directory,
			Mode:          ModeInstanceDirectories,
			RequireMarker: true,
		})
	}
	return policies
}

// InstanceRoot 返回产品的短运行时实例目录根: <home>/.hx-pxy-runtime。
// home 为空时返回空字符串 —— 猜一个 HOME 比不清更危险。
func InstanceRoot(homeDirectory string) string {
	home := strings.TrimSpace(homeDirectory)
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".hx-pxy-runtime")
}

// InstanceDirectory 复算某个运行目录对应的短实例目录路径。
//
// 它必须与 internal/dataplane/mihomo 的 shortControllerSocketPath 给出同一个路径:
// 那个包按运行目录 sha256 的前 8 字节建目录, 这里用同样的算法算出"当前活跃"的那一个,
// 交给白名单排除。算法不同 = 保护错对象 (删掉正在用的目录)。见
// .agents/notes/implemented/architecture/2026-10-14-controller-socket-path-must-match-the-data-plane.md。
func InstanceDirectory(homeDirectory, runtimeDirectory string) string {
	root := InstanceRoot(homeDirectory)
	trimmed := strings.TrimSpace(runtimeDirectory)
	if root == "" || trimmed == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(trimmed))
	return filepath.Join(root, hex.EncodeToString(digest[:8]))
}

// Whitelist 把一组路径变成 Prune 需要的白名单 (清理后的写法 + 绝对路径两种形式,
// 因为调用方给的可能是相对路径, 而 WalkDir 给的始终是绝对路径)。
func Whitelist(paths ...string) map[string]struct{} {
	whitelist := make(map[string]struct{}, len(paths)*2)
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		whitelist[filepath.Clean(trimmed)] = struct{}{}
		if absolute, err := filepath.Abs(trimmed); err == nil {
			whitelist[filepath.Clean(absolute)] = struct{}{}
		}
	}
	return whitelist
}
