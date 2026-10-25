# Agent Note: 请求级 flow 记录与缓存判定解耦 (JSONL 独立存储)

Status: implemented

- 影响: `workspaces/browser-auto/hx-capture-proxy/src/hxcp/flows.py` (新增)、
  `engine.py` (`_note_request` / `_emit` 重写 / `stop()` drain)、`api.py` (`/flows` 端点、
  `/stats` 的 `flows` 段)、`cli.py` (`--record-flows` + 3 个 cache 子命令)、
  `mcp_server.py` (`flow_search` / `flow_stats`)

## Problem

`hx-capture-proxy` 的 `engine.py` 早就有一个 `on_flow` 回调, 每条请求都会触发,
但它的输出只到 `cli.py` 的 `print` —— 出了那个进程就什么都没有。
同时 `api.py` 里 `record_event()` / `STATE["events"]` / `MAX_EVENTS` 三样都定义了,
**却没有任何调用点**, 于是 `/stats` 里 `events` 恒为 0。

结果是一个很难察觉的状态: **抓包看着"实现了", 实则不可回溯** ——
要摸一个站点的接口路径, 只能盯着终端滚动的即时输出, 关掉就没了。
而"缩减层次、协议化"这条工作流的第一步正是**回看历史流量找关键路径**。

还有一个更早的误判需要纠正: `README.md` 的"未做"写着"没有独立的 flow 存储",
容易被读成"记录没实现"。实际缺的是**查询层**, 记录本身一直在跑 ——
所以本 note 锁定的决策不是"要不要记录", 而是**记录该存在哪里**。

## Decision

**flow 记录独立成模块, 存 JSONL 按天切分, 与缓存判定完全解耦。**

三个不可让步的点:

1. **不进 `store.py` 的 entries 表**。`store.py` 的纪律是 `SCHEMA_VERSION` 不符即
   **丢弃重建**, 那对缓存索引是对的 (派生数据, 重建成本 = 一次回源); 但 flow 是
   **一次性观测**, 回源拿不回来。塞进同一张表 = 拿"可重建"的纪律管"不可重建"的数据。
2. **记录与判定解耦**: 被 `policy.decide` 拒绝缓存的请求 (sensitive_path / set_cookie /
   `no_store`) 照样写 flow。拒绝的是"把这份响应喂给别的会话", 不是"不能看"。
   这条不是理论 —— 正是它让 `/api/*` 这类**最想看的流量**能被记录。
3. **写入非阻塞**: 独立线程 + 有界队列, 满则丢弃并计数。`passed` 分支就在 CDP reader
   回调里, 在那里直接写文件会把浏览器请求拖死 —— **观测面永远不能反压被观测的链路**。

一条完整 flow 由两个阶段拼成, 按 **URL** 配对 (不是 requestId):
请求头只在 `Fetch.requestPaused` 拿得到, 响应头只在 `Network.responseReceived` 拿得到,
而两个域的 requestId 是**两套命名空间** (`12345.67` vs `535604.237`, 见 ERROR_LESSONS E1)。

顺带修掉的三个静默缺陷:

- `_capture` 的 `host_filtered` 分支直接 `return`, **连 `_emit` 都没调** —— 被 host 规则
  排除的请求在观测面上完全不存在;
- `/stats` 的 `engine` 段读 `STATE["engine"]` (同进程变量), 而常驻形态下引擎在别的进程里;
  真值在 `runtime.json` (`read_runtime()` 就是为它写的, `/health` 用了, `/stats` 没用);
- `_emit` 不带响应头 —— 而 `Set-Cookie` 轮换与签名相关字段全在响应头里,
  没有它, 抓包对协议化的价值直接减半。

## Alternatives considered

- **给 `entries` 表加 flow 列 / 加一张 flow 表** (最强理由: 复用既有 SQLite 与索引,
  查询直接写 SQL, 天然支持按 host/状态码/正则过滤, 也是 README 原本暗示的方向)。
  否决: 必须动 `SCHEMA_VERSION`, 而该版本不符的语义是**丢弃重建** —— 生产库当前有
  604 条 / 42.6MB / 累计命中 6 万次的缓存, 会一次性清零。用"清空生产缓存"换一个
  查询便利, 代价与收益完全不成比例; 且 flow 的保留期逻辑上就该独立于缓存 TTL
  (缓存过期 = 该回源了, flow 过期 = 不需要这段历史了)。
- **直接订阅 `Network.requestWillBeSent` 拿请求头** (最强理由: 一处拿全, 不需要两阶段拼接,
  也就不需要按 URL 配对与 `_pending_req` 的容量上限)。
  否决: 同一个请求**已经**从 `Fetch.requestPaused` 过了一遍, 那里本来就带 `request.headers`;
  再订阅一个域等于同一事实两处判据 —— 本仓栽过这个坑 (判据散落两处必然不同步)。
  仅为省一次拼接而新增一个事件域的订阅面, 不划算。
- **用 `on_flow` 回调, 由调用方自己落盘** (最强理由: 零改动, 引擎不需要知道文件系统)。
  否决: 那正是现在的状态, 而它的实际结果是**没人落盘** —— 回调接口给了能力却没给实现,
  等于把基础设施的责任推给每个调用方。三个入口 (CLI/API/MCP) 各写一遍必然不同步。
- **什么都不做** (最强理由: 缓存的收益已经拿到了, 抓包是额外功能, 当前注册链路不需要它)。
  否决: 本 skill 的工作流第一步就是"回看历史流量找关键路径", 没有落盘这一步直接断掉;
  且 `record_event` 那套死代码会持续误导下一个读者以为"已经有事件存储了"。

## Consequences

- 新增 `--record-flows` 开关, **默认关**。记录是观测行为, 不该在没人要日志时默默长文件。
- 落盘位置 `<cache-dir>/flows/flows-YYYY-MM-DD.jsonl`; 保留期独立 (`flow-purge --days N`),
  与 `purge` (缓存 TTL) 互不影响。
- 记录量并入 `runtime.json` 的 `flows` 段与 `/stats` 的 `flows` 段 ——
  "有没有在记"不必去文件系统翻。
- 字段有截断 (头值 2048 / body 8192 / 单行 65536), 超限降级为骨架 + `truncated: true`。
  **这是刻意的**: multipart 上传能把单条记录撑到几十 MB, 扫描成本随之崩塌。
- 无索引, 查询靠扫文件。单天几万行是毫秒级; 真到秒级时另建派生索引,
  且必须可全量重建 (本仓通用纪律)。
- 未覆盖: keylog 解密、h2 帧解析、请求/响应的二进制 body。要看 body 仍走 CDP 侧导出
  (`js-reverse-mcp` 的 `outputFile`) 或 `cache_body`。

## Testing

```bash
cd workspaces/browser-auto/hx-capture-proxy
uv run --no-sync python -m pytest tests/ -q            # 30 passed
uv run --no-sync python tests/e2e_api.py               # PASS
uv run --no-sync python tests/e2e_mcp.py               # PASS
uv run --no-sync python tests/e2e_runtime.py           # PASS (跨进程)
cd ../freebuff-rotator && uv run --no-sync python -m unittest rotator.test_hxcp   # 11 OK
```

真环境实证 (起真浏览器挂引擎, 独立 cache-dir):

```text
flow 记录: 4
  flow | skip:conditional_no_header | 200 | GET | https://example.com/      | req_h=6 resp_h=9
  flow | skip:conditional_no_header | 200 | GET | https://httpbin.org/json  | req_h=6 resp_h=6
  flow | skip:non_http_scheme       | 200 | GET | chrome-extension://...    | req_h=2 resp_h=7
  flow | skip:status:404            | 404 | GET | https://httpbin.org/favicon.ico | req_h=6 resp_h=6
```

**四条全是 skip** —— 这恰好是本决策的证据: 这些分支在改动前对观测面**完全不存在**
(`host_filtered` 连 `_emit` 都没调, 其余虽 emit 但无处落盘)。

契约回归: `/stats` 的 `engine` 段保持**扁平形态** (消费方 `rotator/hxcp.py` 的 `view()`
直接读 `engine.bytes_served`), flow 统计作为兄弟字段加入, 不动既有字段 ——
`rotator.test_hxcp` 11 项通过即该契约未被破坏。

结构门禁: `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`。
