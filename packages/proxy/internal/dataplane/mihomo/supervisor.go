package mihomo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// Supervisor 负责维护 Mihomo 数据面的可用性。
//
// Agent Note: .agents/notes/implemented/feature/2026-10-11-mihomo-data-plane-supervisor.md
//
// ## 为什么需要它
// Manager 只在被调用 (Apply/Status) 时才顺带检查进程是否还活着
// (refreshProcessLocked), 进程退出后 status.State 变成 "failed" 就**没人再拉起**。
// 实测后果: mihomo 退出后 7890 端口无人监听, 而控制面仍然健康,
// 于是"代理看起来在跑"但所有走代理的请求全部失败 —— 故障被静默吞掉。
//
// ## 判据 (可观测)
// 拉起的判据不是"进程句柄为空", 而是**监听端口真的在听**:
// 进程在但端口没起 (配置错误/端口占用) 同样是故障, 必须一并处理。
//
// ## 熔断
// 崩溃循环必须停下来 —— 连续 supervisorMaxRestarts 次拉起后仍不可用则放弃,
// 把原因写进 status.LastError 交给人看, 不做无界重试 (AGENTS.md §5)。

const (
	supervisorDefaultInterval = 15 * time.Second
	supervisorMaxRestarts     = 5
	supervisorRestartWindow   = 10 * time.Minute
)

// supervisor 是看护循环的运行态。所有者是 Manager, 生命周期随 ctx。
type supervisor struct {
	interval time.Duration
	restarts []time.Time
	cancel   context.CancelFunc
	supervisorDone
}

type supervisorDone struct {
	done chan struct{}
}

// StartSupervisor 启动数据面看护。重复调用是幂等的: 已在跑则直接返回。
//
// ctx 取消 (或 Manager.Close) 后循环退出, 不会泄漏 goroutine。
func (m *Manager) StartSupervisor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = supervisorDefaultInterval
	}
	m.mu.Lock()
	if m.sup != nil {
		m.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.sup = &supervisor{interval: interval, cancel: cancel}
	m.sup.done = make(chan struct{})
	m.mu.Unlock()

	go func() {
		defer close(m.sup.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				m.superviseOnce(runCtx)
			}
		}
	}()
	m.logger.Info("Mihomo supervisor started", "interval", interval.String())
}

// superviseOnce 检查一次并在需要时拉起。
func (m *Manager) superviseOnce(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.externalProcess {
		return // 外部/systemd 管的数据面不由本进程负责
	}
	// 从未成功启动过 => 没有可用配置, 拉起无意义; 交给 Apply 流程。
	if m.status.ActiveConfig == "" {
		return
	}
	m.refreshProcessLocked()
	if m.isRunningLocked() && m.listenersReadyLocked() {
		return
	}
	if m.trippedLocked() {
		return
	}
	config := m.status.ActiveConfig
	if _, err := os.Stat(config); err != nil {
		m.status.LastError = "supervisor: active config unreadable: " + err.Error()
		m.logger.Error("Supervisor cannot restart Mihomo", "config", config, "error", err)
		return
	}
	if m.process != nil {
		if err := m.stopLocked(ctx); err != nil {
			m.logger.Warn("Supervisor stop before restart failed", "error", err)
		}
	}
	m.logger.Warn("Mihomo data plane is down; restarting", "config", config)
	if err := m.startLocked(config); err != nil {
		m.status.LastError = "supervisor restart: " + err.Error()
		m.logger.Error("Supervisor failed to restart Mihomo", "error", err)
	} else {
		m.status.LastError = ""
		m.logger.Info("Mihomo restarted by supervisor", "pid", m.status.PID)
	}
	if m.sup != nil {
		m.sup.restarts = append(m.sup.restarts, time.Now())
	}
}

// listenersReadyLocked 判"端口真的在听" —— 进程在但没监听等同故障。
func (m *Manager) listenersReadyLocked() bool {
	ports := m.currentManagedPortsLocked()
	if len(ports) == 0 {
		// 还没编译出监听器 (首次 Apply 前): 不判故障, 避免误重启。
		return true
	}
	for port := range ports {
		if !portListening(port) {
			return false
		}
	}
	return true
}

// trippedLocked 熔断判据: 窗口内重启次数达上限则不再重试。
func (m *Manager) trippedLocked() bool {
	if m.sup == nil {
		return false
	}
	cutoff := time.Now().Add(-supervisorRestartWindow)
	kept := m.sup.restarts[:0]
	for _, t := range m.sup.restarts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	m.sup.restarts = kept
	if len(kept) >= supervisorMaxRestarts {
		m.status.State = "failed"
		m.status.LastError = fmt.Sprintf(
			"supervisor tripped: %d restarts within %s; refusing to restart further",
			len(kept), supervisorRestartWindow)
		m.logger.Error("Mihomo supervisor tripped; manual intervention required",
			"restarts", len(kept), "window", supervisorRestartWindow.String())
		return true
	}
	return false
}

// StopSupervisor 停止看护。Close 会调用它; 单独暴露便于测试与优雅退出。
func (m *Manager) StopSupervisor() {
	m.mu.Lock()
	sup := m.sup
	m.sup = nil
	m.mu.Unlock()
	if sup == nil {
		return
	}
	sup.cancel()
	select {
	case <-sup.done:
	case <-time.After(3 * time.Second):
	}
}

// portListening 判本机是否在监听该 TCP 端口。
//
// 用标准库拨号而不是读 /proc: 后者在容器/跨命名空间下会失真,
// 而"能不能连上"正是我们要保证的事 (与 externalSocketReadyLocked 同思路)。
func portListening(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

var errSupervisorNotRunning = errors.New("supervisor not running")
