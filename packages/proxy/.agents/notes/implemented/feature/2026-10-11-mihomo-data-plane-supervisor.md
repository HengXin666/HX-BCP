# Agent Note: Mihomo 数据面由控制面自己看护 (进程退出自动拉起)

Status: implemented

## Problem

Manager 只在被调用 (Apply/Status) 时顺带检查 mihomo 进程是否还活着
(refreshProcessLocked), 进程退出后 status.State 变成 "failed" 就**没人再拉起**。

实测后果 (2026-10-11): mihomo 退出后 **7890 端口无人监听**, 而控制面 hx-pxyd
仍然健康 —— 表现为"代理看起来在跑", 但所有走 7890 的请求全部 000 失败。
故障被静默吞掉, 且没有任何组件负责恢复。

## Decision

新增 `internal/dataplane/mihomo/supervisor.go`: 控制面自己起一个看护 goroutine,
周期性检查数据面, 不可用就用**当前 active 配置**拉起。在 `cmd/hx-pxyd/main.go`
构造 Manager 后挂接 `StartSupervisor`, `Close` 里 `StopSupervisor`。

## Evidence (实机验证, 2026-10-11 13:48)

```
杀前  PID=41974  7890=1
kill -9 后       7890=0        <- 端口真的没了
等 42s 后 PID=42383 7890=1     <- 自动拉起, 端口恢复
```

日志 (`.tmp/run/logs/backend.log`):

```
13:41:04  Mihomo supervisor started      (interval 15s)
13:41:19  Mihomo restarted by supervisor
13:42:34  Mihomo restarted by supervisor
```

## 三条设计约束 (都踩过才定下来)

1. **判据是"端口真的在听", 不是"进程句柄非空"**
   进程在但端口没起 (配置错误/端口占用) 同样是故障 —— `listenersReadyLocked`
   逐个拨 active 监听端口。只判句柄会漏掉这类半死状态。

2. **没有 active config 时不拉起**
   `status.ActiveConfig == ""` 时直接返回: 首次 Apply 前没有可用配置,
   强行拉起只会起一个空壳。交给正常 Apply 流程。

3. **熔断: 10 分钟内 5 次放弃**
   崩溃循环必须停 —— 达上限后写 `status.LastError` 交给人看, 不做无界重试
   (AGENTS.md §5 禁止无界重试/无界 goroutine)。

## 踩过的坑 (别再犯)

- **`startLocked` 里不要顺手置 `Running=true`**: 我为了让日志显示 PID 而加了
  `m.status.Running = true`, 结果看护误以为已恢复、不再处理, 而 mihomo 实际
  **配置没加载完、端口根本没起** —— 7890 变 0。
  修法: 只记 `status.PID`, 运行状态一律以端口拨号为准。

## Consequences

- 数据面进程退出/端口消失后最长 15s 自动恢复, 不再需要人工发现与重启。
- 外部托管的 mihomo (`WithExternalProcess`) 不归本进程管, 看护直接跳过。
- 看护只复用 active 配置, **不会**自行改配置 —— 配置正确性仍由 Apply 负责。

## Alternatives considered

- **什么都不做 (靠 systemd Restart=)**: 最强理由是外部进程管理器本就该干这件事,
  且本仓也支持 external 模式。但本机 hx-pxy 由 `run.sh` 起的、非 systemd 单元,
  没有 Restart 兜底; 而且即便有 systemd, 它也不知道"要重新 Apply 哪份配置"
  (它只会重放同一条命令)。故仍需要控制面内建看护 —— 否决。
- **把检查塞进 Status() 被调用时**: 最省事, 但没人调 Status 就永远不检查,
  正是本次故障的成因 —— 否决。
- **每 1s 高频检查**: 恢复更快, 但 15s 已足够 (故障窗口可接受),
  且高频拨号会对数据面造成额外负担 —— 否决。
