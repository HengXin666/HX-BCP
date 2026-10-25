# Agent Note: HX-Capture-Proxy 落成三入口, 配对按 URL 不按 requestId

Status: implemented

## Problem

上一轮 (`2026-09-18-cache-at-browser-layer.md`) 只锁定了"缓存放浏览器层"这条决策与
一份原型原语 (`fp-audit/probes/intercept.py`)。要成为一个能用的组件, 还有三件没有答案的事:

1. **谁来做该缓存什么** —— 原型只说"能填充", 没说哪些响应可以填。填错会把 A 账号的
   会话化响应喂给 B 账号 (旧 cacheproxy 正因为 `Set-Cookie` 而明确禁止过这类缓存)。
2. **怎么让 AI 用得上** —— 用户明确要求 CLI / API / MCP / Skill 四件套。
3. **拿两组 CDP 事件怎么配对** —— 实测撞到的真 bug, 见下。

## Decision

组件落在 `workspaces/browser-auto/hx-capture-proxy/` (uv 管理, 包名 `hxcp`)，一份 core 三入口:

- **`store.py`** — 内容寻址缓存 (SQLite 索引 + `blobs/<sha256>`), 带 `SCHEMA_VERSION`。
  **版本不符即重建, 不做迁移**: 缓存是派生数据, 重建成本 = 一次回源, 而迁移逻辑是长期负担。
- **`policy.py`** — 入缓存判据。静态类型直缓存; JSON/HTML 只在显式正向 `Cache-Control`
  或 `immutable` 时缓存; **`Set-Cookie` / 敏感路径 (`/login` `/api` `/oauth` `/token` …)
  永不缓存**; 超限拒绝。
- **`cdp.py`** — 会话原语。单 reader; 事件回调内 `send()` 自动降级为只发不等 (重入安全);
  `recv` 超时**继续循环**而不是 return。
- **`engine.py`** — Fetch(Request 阶段) 命中即 `fulfillRequest`; Network(被动) 取响应体填缓存并计流量。
  **刻意不在 Response 阶段拦截**: 那样页面会等我们, 慢或错就挂。
- **`cli.py` / `api.py` / `mcp_server.py`** — 三入口, 全部只调用上面同一份 core。
  API 刻意**不提供"改请求"接口** —— 抓包回放要的是读, 而写会引入"API 层与服务层判据分叉"的风险。

**关键实现约束 (实测得出, 不是设计偏好)**:

- **配对必须按 URL, 不能按 requestId**。`Fetch.requestPaused` 的 id 形如 `"12345.67"`,
  `Network.*` 的形如 `"535604.237"` —— 两套命名空间。按 id 配对会永远取不到,
  且**静默失败**: 症状是 `filled` 与 `skipped` **同时为 0** (根本没进采集函数),
  与"进去了但判据拒绝" (会动 `skipped`) 完全不同。这条是本组件最大的坑。
- **开发依赖放 `[dependency-groups]` 而非 `optional-dependencies`**。uv 是精确同步,
  `uv sync --extra api` 会把未指定的 optional-extra **卸掉** —— 实测把 pytest 卸掉,
  `pytest` 报 "No module named pytest"。

## Alternatives considered

- **复用旧 `cacheproxy` 的 MITM 路线, 只把它包一层 API/MCP** (最强理由: 2320 行已跑通、
  有种子缓存、对**非浏览器客户端** (Burp/纯协议重放) 是唯一选择)。
  否决: 实测它的扩展集合只有 16 个, 与真身 17 个结构性不等; 包一层 API 不会改变这一点。
  它作为**独立的老档**保留, 不被本组件取代。
- **只在 fp-audit 里做原型, 不建正式组件** (最强理由: 原型已验证可行, 建组件是额外工程)。
  否决: 原型没有缓存策略 (会缓存会话化响应 = 真实的安全问题)、没有库、没有观测面,
  也没有 AI 入口 —— 而这些正是用户要的"一个功能做到 100%"。
- **用 Service Worker 代替 CDP Fetch** (最强理由: 纯 Web 层, 不依赖 CDP, 对用户透明)。
  否决: SW 只在 `https` 且**同域**生效, 覆盖不到跨域静态 CDN (github.githubassets.com);
  且 SW 需要注入页面可见对象。CDP Fetch 是进程外的, 覆盖全部且无注入痕迹。
- **在 Response 阶段拦截以直接改写响应体** (最强理由: 省掉一次"取 body 再入缓存"的往返,
  且能立刻把缓存回给当前这次请求)。
  否决: 它让页面等待我们 —— 我们的慢或错直接变成页面挂掉。Request 阶段命中 + Network 被动填充
  已经能拿到同样的省流效果, 且没有任何阻塞风险; 代价仅是"首次请求照常走网络",
  而首次请求本来就要走网络。

## Consequences

**买到的**: 一个可用的组件 —— 缓存收益与指纹 100% 同时成立, 且 AI 可直接查询。

实测 (真浏览器 + 真站点, github.com 首页, 同一环境两轮导航):

| 轮次 | 请求 | 命中 | 填入 | 走网络字节 |
|---|---|---|---|---|
| 第一轮 | 164 | 0 | 152 | 4.65 MB |
| 第二轮 (增量) | 51 | 40 | +107 | **2.18 MB** |

同一次运行里 `ja3n_hash` / `ja4_r` / `akamai_hash` 与无引擎基线**完全相同**。

**代价 (两半都要说)**:

- **第二轮仍走 2.18MB 网络**。因为 `github.com` 文档本身 `no-store` (每次必拉),
  且第二轮含首轮未出现的懒加载资源。**HTML 属固有成本**, 能省的只有静态资源。
  不要拿"命中 40 次"当收益数字 —— 命中次数不换算成字节就是无意义的。
- **需要 CDP 通道**。SDK 内部自建网络栈的客户端覆盖不到, 那类场景退回 MITM 档。
- **与浏览器生命周期耦合**: 浏览器重启后需重新 `Fetch.enable` 并重建会话。
- **抓包/回放的查询层没做**: 现在只落了"响应体缓存", **没有独立的 flow 存储**,
  所以按 host/状态码/正则检索历史请求还做不到 —— README 的"未做"一节如实记着。
- **keylog 未消费**: launcher 能传 `--ssl-key-log-file`, 但没有解析它的代码。
- 全量回归 (单测 + 三个 e2e) 约 2~4 分钟, 含起停真实 Chrome, 不适合作最短提交前钩子。

## Testing

```bash
cd workspaces/browser-auto/hx-capture-proxy
uv sync --extra api --extra mcp
uv run python -m pytest tests/ -q      # 18 passed (store 归一化/版本身份/去重; policy 拒缓存判据)
uv run python tests/e2e_real.py        # 真浏览器: 5 项断言全 PASS
uv run python tests/e2e_api.py         # 真 uvicorn + 真 HTTP: 11 项 PASS
uv run python tests/e2e_mcp.py         # 真 stdio MCP + 真客户端: 7 项 PASS
```

三个 e2e 均为**真端到端** (真 Chrome / 真 socket / 真 MCP 协议), 不是打桩 ——
依据: 本仓库铁律"测试须走真实端到端路径, 全绿不等于功能可用",
且本次正是一个只在 e2e 里才暴露的错误 (引擎字段名不一致) 推动了 e2e 的存在。

结构门禁: `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`。