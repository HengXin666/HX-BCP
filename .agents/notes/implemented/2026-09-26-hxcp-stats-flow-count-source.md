# Agent Note: /stats 的 flow 段必须在引擎停时回落到磁盘, 不能读 runtime.json 快照

Status: implemented

- 影响: `toolbox/hx-capture-proxy/src/hxcp/api.py` (`/stats` 的 flow 段来源),
  `src/hxcp/web/index.html` (状态栏与空态文案)

## Problem

前端做好之后第一次拿真实数据打开, 状态栏显示 **"记录 0"**, 而库里有 50418 条。

排查 (直接读 API 响应):

```text
$ curl -s localhost:43580/stats | jq .flows.records
null              # 字段根本不存在, 不是 0
$ 扫文件统计
files=4  records=50418
```

根因在 `/stats` 的取数顺序:

```python
fl = ((eng._flow_stats() if eng else None)
      or (rt or {}).get("flows")
      or flows_mod.flow_stats(store.root / "flows"))
```

API 常驻进程里 `eng` 恒为 None, 于是走 `runtime.json` 的 `flows` 段。
而 `runtime.json` 是**引擎进程**写的: 引擎一停它的计数就永远冻在最后一次写入,
**既过期又不代表磁盘现状** —— 更糟的是当它来自更早的版本时, 里面压根没有 `records` 字段,
前端拿到的就是 `undefined`, 渲染成"记录 0"。

对比 `/health`: 它已经用 `read_runtime()` 的 `state` 做了新鲜度判定 (超过 120s 视为 `stale`),
但 `/stats` 只用 `(rt or {}).get("flows")` 取数, **没有跟着判新鲜度**。

## Decision

**flow 段的三级来源按"谁此刻是权威"排序, 且引擎不在跑时回落到扫磁盘。**

```python
rec = eng._flow_stats() if eng else None            # 同进程引擎: 未落盘的也已计数, 最准
if not rec and (rt or {}).get("state") == "running":  # 跨进程且在跑: runtime.json 新鲜
    rec = (rt or {}).get("flows")
if not rec:                                          # 否则: 扫磁盘, 库里有多少就是多少
    rec = flows_mod.flow_stats(store.root / "flows")
```

关键在第三级**不能省**: `runtime.json` 是死进程的遗物, 拿它当"库中记录数"是把快照当现状。
判据只有一条: 这个数是不是"此刻磁盘上真实存在的记录数"。是, 才配叫"库中 N 条"。

前端同步区分两个标签 (`records` = 库中真实记录 / `written` = 本次会话写入), 不再混成一个数。

## Alternatives considered

- **什么都不做, 前端容忍字段缺失** (最强理由: 后端的确返回了引擎在跑时的正确计数, 只有引擎停时不准;
  而"引擎停着"时抓包本来就没在产生新数据, 计数不准的实际危害有限, 前端显示 "-" 即可)。
  否决: 引擎停着恰恰是**回看历史**的常态 —— 逆向时是"先抓一轮, 停下来慢慢看"。
  此时面板上唯一的规模指标显示 0, 会直接让人以为抓包从来没工作过, 而这正是用户最初的怀疑。
  容忍缺失等于把最该准的场景做错。
- **让引擎在退出时把最终计数写成"历史累计"字段, API 读它** (最强理由: 读一个数比 glob 一次目录便宜,
  且不依赖文件系统遍历)。
  否决: 引擎可能被强杀 (kill -9 / OOM / 容器重启), 那时就没有"退出时写入"这一步,
  计数照样停留在上次节流发布的值。把权威交给一个**只在正常路径上更新的缓存**,
  就是把"不正常时更该准"的信息做丢了。扫磁盘是所有路径下都成立的那个。
- **给 `runtime.json` 加"已停止"标记并据此判断新鲜度** (最强理由: 复用现有的 120s 阈值,
  改动最小, 与 `/health` 判定逻辑一致)。
  否决: `/health` 的新鲜度判据是为"引擎是否在跑"服务的, 与"库里有多少条记录"是两个问题。
  即使标记新鲜, 引擎在跑时 `runtime.json` 的计数也是**节流发布**的 (≥1s 一次), 天然滞后;
  而记录数的真相永远在磁盘上。用运行态的新鲜度去推断数据规模, 是把两个判据混成一个。

## Consequences

- 引擎不在跑时 `/stats` 会 glob 一次 flows 目录并逐行统计 —— 真实数据 (4 文件 / 118 MB /
  50418 行) 实测 0.38s, 与前端"自动刷新 5s"的节奏相容。
- 引擎在跑时行为不变 (仍读引擎计数器, 最便宜也最新鲜)。
- 前端状态栏改为分别展示库中记录与本次写入, 空态也据此区分"没记录"与"被时间窗挡掉"。
- 同类风险已在取证池上规避: `/stats` 的 `flows.forensic` 段**始终现算**, 不读 `runtime.json`。
  取证池的规模更是必须现算 —— 它会被轮转改变, 与"谁在跑"无关。

## Testing

```bash
cd toolbox/hx-capture-proxy
uv run --no-sync python tests/e2e_web.py    # /stats 带 flow 与 forensic 段的判据
```

实测 (真实 cache-dir, 引擎未运行):

```text
修复前: /stats -> flows.records = null  (前端渲染为 "记录 0")
修复后: /stats -> flows.records = 50418  files = 4  last = 2026-09-22 00:43:20
```

浏览器实测 (经 `localhost:36000/proxy/hxcp/`) 状态栏:

```text
库中 50418 条 · 取证池 0B/1024.00M (0 体) · 缓存 733 条
```

结构门禁: `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`。