# cache-proxy

多浏览器共用的静态缓存代理 + 抓包观测面。两种入口, 一份缓存:

| 入口 | 谁能用 | HTTPS | 命中时 |
|---|---|---|---|
| **代理档** `hxcp proxy` | 任何能配代理的浏览器 / 程序 / Agent (HTTP 或 SOCKS5) | 默认隧道, 按 host 显式解密 | 本地应答, 不建上游连接 |
| **CDP 档** `hxcp run` | Chromium 系 (要 CDP) | 浏览器原生握手, 永不解密 | 不出网, 也没有握手 |

所有入口写同一个缓存库, 一个浏览器拉过的 js/css/字体/图片, 另一个浏览器直接命中。
每条 flow 带 `client` (哪个浏览器) 与 `tls` (隧道 / 解密 / 明文 / 原生)。

## 代理档 (所有浏览器)

```bash
uv sync --extra api --extra proxy
uv run hxcp --cache-dir ~/loli_data/hxcp/_shared proxy --port 8899
# 每个浏览器一个入口, 各自的出口代理, 共用缓存:
uv run hxcp --cache-dir ~/loli_data/hxcp/_shared proxy --port 8899 \
    --client envA=8901,http://user:pw@egress-a:3128 --client envB=8902,http://egress-b:3128 \
    --socks-port 0
uv run python -m hxcp.api --cache-dir ~/loli_data/hxcp/_shared --port 8898   # 控制台 http://127.0.0.1:8898/
```

浏览器侧只要把代理指向入口 (`--proxy-server=http://127.0.0.1:8901`, Firefox 网络设置, 系统代理, PAC 均可)。
控制台 **接入** 页给出每种客户端的现成配置, 同样的内容 `GET /access` / MCP `proxy_access` 也能拿到。

**HTTPS 怎么处理 (指纹)**:

- 默认 **隧道**: 代理只看 SNI, 字节原样转发, 浏览器自己与站点握手, JA3/JA4 与直连相同。代价是看不到内容, 缓存不了。
- **解密**: 在控制台 主机与 TLS 页 (或 `POST /rules/host {host, mode}` / MCP `set_tls_mode`) 把纯静态 CDN 切过去,
  这些 host 才会被缓存。代价: 上游看到的是代理的握手 (OpenSSL), 浏览器要信任本地 CA (`GET /ca.crt`)。
- **永不解密**: 登录 / API / 风控相关域名放这里, 即使开了全部解密也走隧道。
- 规则在 `<cache-dir>/proxy-rules.json`, 代理约 1 秒内热加载, 只影响新连接。

**共享缓存的安全线** (policy.py): `Set-Cookie` / `no-store` / `private` / 敏感路径不入库;
`Vary` 含 cookie / authorization / user-agent / sec-ch-ua 的响应不入库 (换一个浏览器身份会拿错);
带 `Range` 或 `Authorization` 的请求不读也不写缓存。

## CDP 档 (Chromium, 命中零握手)

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
# GET  /            -> 控制台 (总览 / 抓包 / 缓存 / 主机与 TLS / 接入)
# GET  /access /clients /hosts /runtimes /rules /ca.crt /proxy.pac /flows.har
# PUT  /rules   POST /rules/host {host, mode}   DELETE /entry?url=
# GET  /health /stats /entries?q=&limit= /lookup?url= /body?url=
# GET  /flows?host=&status=&q=&kind=&client=&via=&since=&days=&limit=&offset=
# GET  /flow/body?sha256=&offset=&limit=   -> 取证档完整体 (分段读)
# POST /purge /engine/stop
```

### MCP

```bash
uv run python -m hxcp.mcp_server    # stdio
# 工具: cache_stats / cache_search / cache_lookup / cache_body / purge_expired
#       flow_search / flow_stats / flow_payload / proxy_access / host_summary / set_tls_mode
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
uv run python tests/e2e_proxy.py      # 真 mitmproxy: 两个客户端共用缓存, flow 按客户端打标
```

端到端跑完会**自动清理** profile 与进程 (本仓库历史上堆过几十个残留 profile)。

## 模块

| 文件 | 职责 |
|---|---|
| `store.py` | 内容寻址缓存 (SQLite 索引 + blob), 带 `SCHEMA_VERSION`, 版本不符即重建 |
| `policy.py` | 该缓存什么 (静态类型直缓存 / JSON 需正向头 / Set-Cookie 永不缓存 / 敏感路径拒绝) |
| `flows.py` / `flows_query.py` | 请求级 flow 记录 (JSONL 按天切分, 非阻塞写, 多进程安全) / 查询统计清理 |
| `proxy.py` / `proxy_addon.py` | 代理档: mitmproxy 入口编排 / 缓存与 flow 钩子 (隧道或解密按 host 决定) |
| `proxy_rules.py` | 哪些 host 解密, `proxy-rules.json` 热加载 |
| `runtime.py` | 多实例运行态 (`runtime/<instance>.json`) 与汇总 |
| `access.py` / `api_access.py` | 接入方式, PAC, HAR 导出 / 对应的 HTTP 路由 |
| `cdp.py` | CDP 会话原语 (单 reader + 重入安全 + 超时不停) |
| `engine.py` | 拦截决策 + 被动捕获 + 流量计量 |
| `launcher.py` | 起停环境 (精确匹配停止 + 用后即清) |
| `web/` | 控制台 (原生 JS, 由 `api.py` 直接托管, 无构建链) |
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

## 前端 (控制台)

`api.py` 直接托管 `src/hxcp/web/` (index.html + core.js / views.js / flows.js + style.css), **根路径 `/` 就是它**。
所有请求都是相对路径, 经 HX-Webx `/proxy/hxcp/` 子路径打开同样可用。

- **总览**: 本轮已省流量, 字节节省率, 命中率, 累计已省, 最省流量的站点, 判定分布, 接入的客户端;
- **抓包**: 按 host / 状态 / 关键字 / 客户端 / 类型 / 入口 / 时间过滤 (条件写进地址栏, 可分享), 实时尾随,
  时序密度条, 详情 (概览 / 请求头 / 响应头 / 请求体 / 原始 JSON), 复制为 curl / fetch, 一键导出 HAR;
- **缓存**: 条目搜索与排序, 正文预览, 单条删除, 清理过期;
- **主机与 TLS**: 每个 host 的连接数, 客户端, 缓存规模, 已省字节, 一键切换 隧道 / 解密 / 永不解密;
- **接入**: 入口列表, Chrome / Firefox / Playwright / Puppeteer / curl / 环境变量 / PAC / 系统代理配置片段, CA 下载与信任步骤, Agent 的 API 与 MCP。

**技术选型仍是原生 JS, 不引 npm 构建链**: 理由同前 (hxcp 是 uv 项目, 交互只有过滤 / 选中 / 切换)。
拆成多个文件只是为了遵守单文件 500 行。

## 未做

- keylog (`--ssl-key-log-file`) 解包与 h2 帧解析 —— launcher 已支持传该 flag,
  但**没有消费 keylog 的代码**。补上它就能在不 MITM 的前提下解出明文 (唯一同时满足
  "指纹不破"与"能看报文"的路径);
- flow 的**派生索引** (现在靠扫 JSONL, 单文件几万行是毫秒级; 量大到秒级时再建,
  且必须可全量重建 —— 见 flows.py 的纪律);
- 种子缓存的自动化刷新 (需手动 `export-seed`);
- 取证档**只覆盖请求体** (post_data)。响应体不进池 —— 缓存的 blob 已按内容寻址存着,
  但被 policy 拒缓存的那类响应 (Set-Cookie / `no-store`) 目前只能看 8K 摘要。
- 浏览器包启动非 Chromium 内核时, 自动把浏览器代理指向 hxcp 入口并把原上游挪成该入口的 `--client` 上游 (现在需手动配);
- 隧道连接的字节计量 (mitmproxy 对忽略的连接不出 flow, 现在只记连接与 ALPN);
- SOCKS5 入口不能叠上游出口 (mitmproxy 的限制), 需要上游时用 HTTP 入口。
