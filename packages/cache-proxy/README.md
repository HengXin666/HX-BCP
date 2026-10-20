# cache-proxy

省流缓存代理: 只缓存静态资源, 不终结 TLS, 动态请求原样透传。

**零终结 TLS 的静态缓存路由代理 + 抓包观测面。**
缓存放在**浏览器层** (CDP Fetch), 因此命中不产生网络流量, 也**不产生任何 TLS 握手** ——
指纹与直连逐项相同。

## 为什么不是 MITM

实测 (2026-09-18, 同机多路对测, 每条路 n=10):

| | TLS 扩展集合 | 与真身相同 |
|---|---|---|
| 直连真身 | **17 个** | — |
| **本工具** | **17 个** | ✅ 逐项相同 |
| MITM 缓存代理 | 16 个 (缺 GREASE `51764`) | ❌ |

7 个 curl_cffi impersonate (chrome / chrome146 / chrome150 / chrome136 / chrome142 / chrome145 / edge101)
**全部只有 16 个扩展** —— MITM 不是"版本选错", 是**结构性不可能**。
完整读数与复现: `workspaces/browser-auto/knowledge/hx-capture-proxy/AB-THREE-ARM-2026-09-17.md`。

## 实测收益 (github.com 首页, 同一环境两轮导航)

| 轮次 | 请求数 | 命中 | 填入 | 走网络字节 |
|---|---|---|---|---|
| 第一轮 | 164 | 0 | 152 | 4.65 MB |
| 第二轮 (增量) | 51 | 40 | +107 | **2.18 MB** |

第二轮仍走 2.18MB 网络: 因为 `github.com` 的文档本身是 `no-store` (每次必拉),
且第二轮含首轮未出现的懒加载资源。**HTML 属固有成本, 静态资源才是可省的部分。**

## 快速开始

```bash
uv sync --extra dev --extra api --extra mcp

# 起环境 + 挂引擎 + 跑完自动清理 (最常用)
uv run hxcp launch --port 9333 --duration 30 --verbose

# 带上抓包记录与取证档 (完整请求体): 分析/协议化回放用这个
uv run hxcp launch --port 9333 --duration 30 --verbose --record-flows --forensic

# 挂到已有环境 (接管正在跑的浏览器)
uv run hxcp run --endpoint http://127.0.0.1:9333

# 只看这些 host (白名单; 空 = 全部放行)
uv run hxcp launch --allow-host github.githubassets.com,cdn.example.com

# 缓存库
uv run hxcp cache stats
uv run hxcp cache list --limit 20
uv run hxcp cache lookup --url https://cdn.test/app.js
uv run hxcp cache export-seed --out ./cache_seed   # 新环境冷启动即命中
```

### HTTP API

```bash
uv run python -m hxcp.api --cache-dir ~/loli_data/hxcp --port 8898 --token TOKEN
# GET  /            -> 抓包流前端 (单文件原生 JS, 见下节)
# GET  /health /stats /entries?q=&limit= /lookup?url= /body?url=
# GET  /flows?host=&status=&q=&kind=&days=&limit=&offset=
# GET  /flow/body?sha256=&offset=&limit=   -> 取证档完整体 (分段读)
# POST /purge /engine/stop
```

### MCP

```bash
uv run python -m hxcp.mcp_server    # stdio
# 工具: cache_stats / cache_search / cache_lookup / cache_body / purge_expired
```

## 运行态 (跨进程观测)

引擎与 API **不在同一进程** (引擎挂在浏览器旁, API 是长驻 HTTP 服务)。因此引擎把运行态
写进缓存目录的 `runtime.json` (原子替换), API / MCP 从磁盘读 —— 不依赖同进程。

```bash
curl -s localhost:8898/health   # {"engine":true,"engine_state":"running","schema_version":1}
curl -s localhost:8898/stats    # {"engine":{...计数...},"store":{...}}
```

`engine_state` 取值: `running` / `stopped` / `stale` (落盘超过 120s 未更新) / `absent`。
**为什么必须落盘**: 实测症状是 `/stats` 的 `engine` 恒为 `null` —— 面板上看到的永远是空观测面,
而服务其实在正常缓存。

## 已注册到 HX-Webx

```
注册名  hxcp        端口 8898      健康检查 /health
面板    http://localhost:36000
前端    http://localhost:36000/proxy/hxcp/        <- 抓包流视图
直达    http://localhost:36000/proxy/hxcp/health
```

带 `workdir` + `startCommand`, 可在面板直接启动/停止/看日志:

```bash
curl -s -X POST localhost:36000/api/services/hxcp/start -H 'Content-Type: application/json' -d '{}'
curl -s "localhost:36000/api/services/hxcp/logs?tail=100"
```

⚠️ 经 `/proxy/hxcp/` 访问时**要带根路径** (代理只剥注册名前缀): `/proxy/hxcp/health`, 不是 `/proxy/hxcp`。

## 三条硬纪律 (每条都有实测代价)

1. **配对必须按 URL, 不能按 requestId**。`Fetch.requestPaused` 的 id 形如 `"12345.67"`,
   `Network.*` 的形如 `"535604.237"` —— 两套命名空间。用错会**静默失败**,
   症状是 `filled` 与 `skipped` **同时为 0** (即根本没进采集函数), 极易被误读成"判据太严"。
2. **拦截流与请求流必须同一条 CDP 会话**; 事件回调里调 `send()` 必须降级为只发不等
   (否则 reader 等自己 = 死锁); reader 的 `recv` 超时**绝不退出循环** (静默退出 = 整条链路假死)。
3. **报收益必须给两轮网络字节对照**。命中次数不换算成字节, 是无意义的数字。

## 测试

```bash
uv run python -m pytest tests/ -q     # 单元 (store/policy)
uv run python tests/e2e_real.py       # 真浏览器 + 真站点 (起停真实 Chrome)
uv run python tests/e2e_api.py        # 真 uvicorn + 真 HTTP
uv run python tests/e2e_mcp.py        # 真 stdio MCP + 真客户端协议
uv run python tests/e2e_web.py        # 真 uvicorn 托管的页面 + 数据端点读写
uv run python tests/e2e_forensic.py   # 真浏览器发真 POST -> 取证档 -> 按引用逐字节取回
```

端到端跑完会**自动清理** profile 与进程 (本仓库历史上堆过几十个残留 profile)。

## 模块

| 文件 | 职责 |
|---|---|
| `store.py` | 内容寻址缓存 (SQLite 索引 + blob), 带 `SCHEMA_VERSION`, 版本不符即重建 |
| `policy.py` | 该缓存什么 (静态类型直缓存 / JSON 需正向头 / Set-Cookie 永不缓存 / 敏感路径拒绝) |
| `flows.py` | 请求级 flow 记录 (JSONL 按天切分, 非阻塞写) + 查询/统计/清理 |
| `cdp.py` | CDP 会话原语 (单 reader + 重入安全 + 超时不停) |
| `engine.py` | 拦截决策 + 被动捕获 + 流量计量 |
| `launcher.py` | 起停环境 (精确匹配停止 + 用后即清) |
| `web/index.html` | 抓包流前端 (单文件原生 JS, 由 `api.py` 直接托管, 无构建链) |
| `cli.py` / `api.py` / `mcp_server.py` | 三个入口, 共用上面同一份 core |

## 抓包 (请求级 flow)

缓存与记录是**两件事**, 代码里也分成两个模块 —— 判据见 `flows.py` 的模块注释。

| | 缓存 (`store.py`) | flow (`flows.py`) |
|---|---|---|
| 内容 | 能命中的静态资源 | **全部**请求的请求头/响应头/时序/判定 |
| 被 policy 拒绝的 `/api`、`/login` | 不记 | **照记** |
| 存储 | SQLite + 内容寻址 blob | 按天切分 JSONL |
| 可重建 | 是 (SCHEMA_VERSION 不符即重建) | **否** (一次性观测, 拿不回来) |

**为什么分开**: `store.py` 的纪律是版本不符即**丢弃重建**, 那对缓存索引是对的;
但 flow 是观测, 塞进同一张表就会跟着缓存被清掉。

开启与查询:

```bash
uv run hxcp launch --port 9333 --duration 30 --verbose --record-flows
uv run hxcp cache flows --host api.example.com --status 200 --q sign --limit 20
uv run hxcp cache flow-stats
uv run hxcp cache flow-purge --days 7
```

HTTP: `GET /flows?host=&status=&q=&kind=&limit=&days=&offset=` (与 `/entries` 互补 ——
`/entries` 是缓存索引只含命中成功的静态资源, `/flows` 是全量观测)。
MCP: `flow_search` / `flow_stats` / `flow_payload` (取证档完整体)。

记录是**非阻塞**的 (独立线程 + 有界队列): 队列满丢记录并计数, 绝不反压被观测的链路。

## 取证档 (完整请求体)

flow 里的 `post_data` 是 **8K 摘要** —— 那是刻意的: 一条 multipart 上传能把单条记录撑到几十 MB,
`flow_search` 的逐行扫描成本随之崩塌。但**协议化回放要的是完整体**, 摘要回放不了。

于是取证档把两件事分开: **摘要留在 flows, 全量存到旁边的 payload 池, 记录里只留一个 sha256 引用。**

```bash
uv run hxcp launch --port 9333 --duration 60 --record-flows --forensic
uv run hxcp launch ... --forensic --forensic-max-bytes $((2*1024*1024*1024)) --forensic-free-mb 2048

# 取回: 先用 flow 查到记录, 再按记录里的 body_ref.sha256 取完整体
curl -s "localhost:8898/flows?q=sign&limit=5" | python3 -m json.tool   # 看 body_ref
curl -s "localhost:8898/flow/body?sha256=SHA256&limit=131072"
```

| | 观测面 (默认) | 取证档 (`--forensic`) |
|---|---|---|
| 请求体在 JSONL 里 | 前 8K + `...[truncated]` | **照旧 8K 摘要** (扫描成本不变) |
| 完整体 | 不存 | `flows/payloads/<sha256 前2位>/<sha256>.bin` |
| 记录里的额外字段 | 无 | `body_ref: {sha256, size}`, 取不到时带 `dropped` 原因 |
| 需要的开关 | `--record-flows` | `--record-flows --forensic` |

**落盘结构**: `payloads/` 用**内容寻址** —— 同一份请求体重复发送 (轮询/重试) 只落一份,
回放时按引用取回的字节与捕获时逐字节相同。可与 flow 记录分处不同磁盘。

**三道上限** (池默认 1 GiB, 磁盘余量水位默认 512 MiB, 单条上限 16 MiB):
池超预算按 mtime 从旧到新轮转删除, 磁盘余量低于水位继续删, 单条超限则不进池并留 `dropped`。
写入失败也**只吞不抛** —— 它跑在 CDP 回调链路上, 磁盘满不能把被观测的请求拖死。

**为什么不是直接把 `MAX_BODY` 调大**: 那会让**每一条**记录都被撑大, `flow_search` / `flow-stats`
的扫描成本跟着涨; 而实测只有 1446/45700 条带请求体、其中 309 条被截断, 真正需要的完整体是少数。
为少数记录付全表的代价, 是拿观测面的性能换个别记录的完整。

## 前端 (抓包流)

`api.py` 直接托管单文件页面 `src/hxcp/web/index.html`, **根路径 `/` 就是它**。

- **请求列表**: 时间 (跨天带日期) / `+Δ` 距上一条的间隔 / 方法 / host+path / 状态 / 大小 / 判定;
- **点开看头与体**: 概览 / 请求头 / 响应头 / 请求体四个标签, `cookie`、`authorization`、`set-cookie` 标红;
  请求体被截断时给出提示, 并可直接**取回完整体** (走 `/flow/body`);
- **过滤**: host / 状态码 / 关键字 / kind / 时间范围, **条件写进 URL** (`?days=7&host=api.github.com`), 可分享可刷新;
- **时序**: 顶部密度条按时间分布, 4xx/5xx 标红, 两端标出时间范围;
- 自动刷新 5s 可选; 列表分页 300 条一页。

**技术选型: 单文件原生 JS, 不引 npm 构建链。** 理由:

- hxcp 是 Python + uv 项目, `api.py` 已在跑; 引入构建链会把"能跑起来"变成"先 install 再 build 再托管产物";
- 本仓既有前端约定 (rotator / gh-registrar) 是 Vite + React, 但那些都有独立进程与 node 工具链, hxcp 没有;
- 需要的交互只有"过滤 + 选中 + 展开", 原生 DOM 足够, 且没有构建产物与实际源码不一致的风险。

**经 HX-Webx 子路径访问可用**: `http://localhost:36000/proxy/hxcp/`。页面里的 `basePrefix()`
从 `location.pathname` 抽出 `/proxy/hxcp`, 所有 fetch 都带上它 (直连 8898 时前缀为空, 行为一致)。
这是本仓既有约定, 见 `.agents/notes/implemented/bug-fix/2026-09-18-rotator-webx-subpath.md`。

## 未做

- keylog (`--ssl-key-log-file`) 解包与 h2 帧解析 —— launcher 已支持传该 flag,
  但**没有消费 keylog 的代码**。补上它就能在不 MITM 的前提下解出明文 (唯一同时满足
  "指纹不破"与"能看报文"的路径);
- flow 的**派生索引** (现在靠扫 JSONL, 单文件几万行是毫秒级; 量大到秒级时再建,
  且必须可全量重建 —— 见 flows.py 的纪律);
- 种子缓存的自动化刷新 (需手动 `export-seed`);
- 取证档**只覆盖请求体** (post_data)。响应体不进池 —— 缓存的 blob 已按内容寻址存着,
  但被 policy 拒缓存的那类响应 (Set-Cookie / `no-store`) 目前只能看 8K 摘要。
