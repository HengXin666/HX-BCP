# Agent Note: 取证档 — 完整体落 payload 池, JSONL 只留 sha256 引用

Status: implemented

- 影响: `toolbox/hx-capture-proxy/src/hxcp/flows.py` (`ForensicStore` / `forensic_path` /
  `open_forensic` / `FlowLog.forensic`)、`engine.py` (`forensic=` 等三个参数)、
  `cli.py` (`--forensic` / `--forensic-max-bytes` / `--forensic-free-mb`)、
  `api.py` (`GET /flow/body`, `/stats` 的 `flows.forensic` 段)、`mcp_server.py` (`flow_payload`)

## Problem

`flows.py` 的记录里, 请求体被截断在前 8K (`MAX_BODY = 8192`, 超出即 `...[truncated]`)。
那对"人/agent 扫流量找关键路径"是对的 —— 一条 multipart 上传能把单条记录撑到几十 MB,
`flow_search` 的逐行扫描成本随之崩塌, 而扫描是这个观测面唯一的主用法。

但**协议化回放要的是完整请求体**: 8K 截断的 POST body 回放不了, 缺的那半截正好带签名参数。
于是"抓包"在回放这条链路上名不副实。

实测 (生产 cache-dir, 2026-09-20/21 两个文件, 共 45700 条记录):

```text
带 post_data 的记录  1446 条 (全是 POST)
post_data 长度       min=138  p50=1194  p90=8206  max=8206
恰好 8206 (=8192+14) 309 条   <- 被截断的那批
骨架降级行           4 条
```

截断精确地落在**最大的那批**上, 而它们恰恰是回放最需要的 (体越大越可能是真载荷)。
同时可见: 带体的只占 3.2% —— 这是后面选方案的决定性数字。

## Decision

**完整体落到 flows 目录旁边的 payload 池, JSONL 记录里只加一个 sha256 引用。**

池: `<cache-dir>/flows/payloads/<sha256 前2位>/<sha256>.bin`, 内容寻址。
记录多出来一个字段 `body_ref: {sha256, size}`。开关: `--forensic` (需同时 `--record-flows`)。

四个不可让步的点:

1. **完整体不进 JSONL**。这是整个决策的核心: 摘要留在 flows, 全量在池里由引用指向。
   逐行扫描的成本因此与取证开关**完全无关** —— 实测开取证档后单条记录行长度 9205B,
   而完整体是 42000B。
2. **内容寻址**。同一份体重复发送 (轮询/重试) 只落一份; 按引用取回的字节与捕获时逐字节相同,
   这是"取证"一词的全部含义。幂等写入让写侧与读侧无需任何协调。
3. **三道硬上限**。池预算 (默认 1 GiB, 超出按 mtime 从旧到新轮转删除)、磁盘余量水位
   (默认 512 MiB, 低于就继续删)、单条上限 (16 MiB, 超出不进池)。取证档是"开启后一直在收流量"的,
   没有闸就是拿取证把机器写爆。
4. **写失败只吞不抛**。写入点在 CDP 回调链路上 (`FlowLog.write` ← `engine._emit`),
   磁盘满/权限错绝不能把被观测的请求拖死 —— 观测面永远不反压被观测的链路。

关掉开关时行为**逐字段不变**: 记录里没有 `body_ref`, `post_data` 照旧 8K 截断。

## Alternatives considered

- **什么都不做 / 承认 8K 摘要就是取证档的上限** (最强理由: 现有 8K 覆盖了 p50=1194 的六倍,
  绝大多数请求体本来就装得下; 而"回放"这条需求目前只有推断没有实证, 为一个未验证的场景
  引入一整套池、轮转、引用、校验、读回端点与测试, 是典型的过度设计)。
  否决: 截断恰好集中在最大的那批体上 (309 条全在 8206), 而体越大越可能带真载荷 ——
  8K 摘要丢掉的正是最有价值的那部分。且代价可控: 实测只有 3.2% 的记录带请求体,
  池按内容寻址去重后占用很小, 不构成负担。
- **直接把 `MAX_BODY` 调大 (比如 1 MiB)** (最强理由: 一行常量改动, 零新模块、零新开关、
  零新测试面; 所有既有读侧 (`/flows` / `flow_search` / `cli cache flows`) 立刻就能看到完整体)。
  否决: 它把成本摊到**每一条**记录上 —— 而只有 3.2% 带体。更关键的是它打击的正是本工具的主用法:
  `flow_stats` 与 `flow_search` 都要逐行 `json.loads` 全部记录, 单文件已到 43~65 MB,
  记录普遍变大后这两个高频操作会从毫秒级走向秒级。用少数记录换全表代价, 方向就是错的。
- **把完整体写进同一个 JSONL 行 (摘要字段照留)** (最强理由: 一条记录自包含, 不需要引用、
  不需要池、不需要处理"引用在但体被轮转删了"这种状态; 复制一条记录就复制了全部证据)。
  否决: 这正是 `MAX_LINE = 65536` 那道闸要挡的东西。行被撑大后逐行解析变慢,
  且会把 JSONL 从"可 grep 的观测面"变成"混着二进制的准归档"。
  附带一个硬缺陷: 一条 10 MB 上传会让**那一行**超限, 触发 `truncated` 骨架降级, 连请求头一起丢。
- **用 SQLite 另开一张 payload 表 (BLOB)** (最强理由: 天然事务与索引, 可 SQL 查询、可统计,
  不必自己写轮转; 与既有的 `store.py` 是同一套技术栈, 团队已熟)。
  否决: `store.py` 的纪律是 `SCHEMA_VERSION` 不符**丢弃重建** —— 那是为可重建的缓存索引定的。
  取证体是**不可重建**的一次性观测, 套用那条纪律等于"改一次 schema 清空全部证据"。
  且取证池要按大小轮转 (LRU), 这不是 SQLite 擅长的形状, 写起来比 glob+unlink 长得多。
  第三方读侧 (纯 grep / 直接拷走一个文件) 也更喜欢裸文件。

## Consequences

- 新增三个旗标, **默认关**; 只有 `--record-flows` 与 `--forensic` 同时给, 池才启用。
  单给 `--forensic` 时池标记为 `enabled=False` 且每条记录的 `body_ref.dropped` 写明
  `store_disabled` —— 开了开关却没生效必须留痕, 否则看的人会以为拿到了完整体。
- 取不到完整体时**引用字段仍在**, 并带 `dropped` 原因 (`too_large` / `write_failed` /
  `store_disabled`)。记录形态始终只有 `body_ref` 一种, 不另造第二形状。
- 读侧入口: `GET /flow/body?sha256=&offset=&limit=` (分段返回, base64 + 文本双份)、
  MCP `flow_payload`。引用格式在 `ForensicStore.get` 里用 `^[0-9a-f]{64}$` 校验 ——
  它直接当路径片段用, 不校验就是一条路径穿越。
- 池的保留期与 flow 记录**同步** (`POST /purge` 的 `flows_keep_days` 会一并清池):
  记录删了体就成了孤儿, 留着白占磁盘且让人误以为还能取回。
- 池统计并入 `/stats` 的 `flows.forensic` 段。引擎不在跑时该段**现算** (glob 一次目录),
  不读 `runtime.json` —— 后者的计数是死进程留下的快照, 同类缺陷的定论见
  `implemented/bug-fix/2026-09-26-hxcp-stats-flow-count-source.md`。
- 未覆盖: 响应体不进池 (缓存的 blob 已按内容寻址存着, 但被 policy 拒缓存的那类响应目前只有 8K 摘要);
  头值仍按 2048 截断 (取证档只放行请求体)。

## Testing

```bash
cd toolbox/hx-capture-proxy
uv run --no-sync python -m pytest tests/ -q          # 47 passed (新增 17 条取证单测)
uv run --no-sync python tests/e2e_forensic.py        # 真浏览器发真 POST, 逐字节比对
```

真环境实证 (`e2e_forensic.py`, 真 Chrome + 真 HTTP 端点 + 42000B 请求体):

```text
记录里 post_data 仍是 8K 摘要 (观测面形态不变)  8206
body_ref 指向完整体  ref.size=42000 实际=42000
按引用取回的字节与发送的 body 逐字节相同  取回 42000 字节
JSONL 单条记录未被完整体撑大 (< 32KB)  行长度=9205
完整体确实不在 JSONL 里
API 跨进程按引用取回完整体 (引擎写的池)  取回 42000 字节
关闭取证档后记录里没有 body_ref / post_data 仍被截断
```

池的上限各有独立单测: `test_pool_budget_rotates_oldest_first` (超预算从旧到新删,
最新必留)、`test_disk_free_watermark_evicts_even_under_budget` (池没到预算但盘要满也删)、
`test_oversize_is_dropped_not_written`、`test_reference_is_validated_against_traversal`。

结构门禁: `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`。