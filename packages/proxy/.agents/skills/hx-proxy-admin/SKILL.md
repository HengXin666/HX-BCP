---
name: hx-proxy-admin
description: 通过控制面自己的能力目录（GET /api/v1/capabilities）驱动 HX-ProxyGroup 管理 API：不改代码、不读 Go 源码就能知道每个端点的字段名、封闭枚举与合法示例，并用一次 POST /api/v1/quickstart 从零跑到「可用代理」。Use when 需要「用程序或 AI 配置 HX-ProxyGroup」——自动化建订阅/建组/建 Listener、批量发号、把控制面接进 CI 或其他 Agent；或遇到 HX 管理 API 字段名记不准、枚举值被 422 拒绝、不确定某个能力到底支持不支持、要判断 quickstart 与逐步调用的取舍、AI 改配置总要先翻 Go 源码等问题时。
---

# 用能力目录驱动 HX-ProxyGroup 管理 API

控制面把自己的接口面**当作数据发布**在 `GET /api/v1/capabilities`：端点、
字段名、封闭枚举、默认值、合法示例、错误码、以及产品边界。
**先读它，再写请求** —— 这样你不需要读 Go 源码，也不会靠猜字段名和枚举值。

> 词元（端点、字段名、枚举值）以**能力目录本身**为准。目录里的枚举是从
> 服务端校验器直接读出来的，所以它和你正在跑的那个版本永远一致。
> 若你手上的副本与目录冲突，**以目录为准**。

## 1. 两个前提

| 项 | 说明 |
| --- | --- |
| 基址 | 控制面地址，例如 `https://proxy.example.com` |
| 凭证 | API Key，走 `Authorization: Bearer <key>` 或 `X-API-Key: <key>`；在设置页创建 |

管理 API 在 `/api/v1/` 下，**全部需要凭证**。API Key 不是 Cookie，
所以**不需要 CSRF token** —— 适合脚本和 Agent。

## 2. 一条命令拿到全部词汇表

`scripts/hx-catalog.py` 读目录后本地校验，**不发坏请求**：

```bash
export HX_BASE_URL=https://proxy.example.com
export HX_API_KEY=hxk_...

# 所有封闭枚举（字段能取哪些值）
python3 scripts/hx-catalog.py enums

# 所有管理端点
python3 scripts/hx-catalog.py endpoints

# 某个端点的完整字段表 + 示例
python3 scripts/hx-catalog.py show proxy_service.create

# 先本地校验，不发出去
python3 scripts/hx-catalog.py check quickstart.create \
  --body '{"name":"hk-pool","subscription_url":"https://example.com/sub"}'

# 校验通过再发
python3 scripts/hx-catalog.py call subscription.create --body '{...}'
```

`check` 会抓两类最常见的 422：**字段名拼错**（服务端拒绝未知字段，不忽略）
和**枚举值不在封闭集合内**。它抓不到语义错误（引用不存在的 id、范围越界），
那些仍然由服务端判定。

## 3. 一次调用跑到可用代理

`POST /api/v1/quickstart` 把「注册订阅 → 刷新 → 建组 → 建 Listener」合成一步：

```bash
python3 scripts/hx-catalog.py quickstart --body '{
  "name": "hk-pool",
  "subscription_url": "https://example.com/sub?token=x",
  "strategy": "url-test"
}'
```

### 手上是代理列表而不是订阅 URL：用 `subscription_inline`

「几百上千条 HTTP/SOCKS 代理」的清单通常**没有 URL**（供应商导出、检测器结果、爬来的资源）。
这时不要先去搭一个服务器托管它 —— 把清单本身放进请求：

```bash
python3 scripts/hx-catalog.py quickstart --body "$(python3 - <<'PY'
import json
lines = [l.strip() for l in open('proxies.txt') if l.strip()]
print(json.dumps({'name': 'proxy-pool', 'subscription_inline': '\n'.join(lines) + '\n', 'strategy': 'url-test'}))
PY
)"
```

`subscription_inline` 与 `subscription_url`、`subscription_ids` **互斥**，三选一。它建出来的是
一条**普通 Inline 订阅**（同样的加密存储、同样的解析/去重/刷新/导出路径），所以后续任何订阅
操作都能用。清单的行格式（裸 `host:port`、`host:port:user:pass`、带 scheme 的 URI、带导出标注
的 URI）见 `docs/SUBSCRIPTIONS.md` §2 Inline。

请求体上限与该端点能接受的清单规模一致（5 MiB），不是默认的 64 KiB。

> 想清楚了只是要一个**统一入口**（一个地址走完整个池子），而不是自己控制出口？
> 那属于消费侧视角，看 `hx-consumer-api` 的「代理池」一节 —— 那里说的是拿到 URL 之后怎么用。

输出里你会拿到下一步真正需要的东西：

```text
group:    group-...
listener: listener-...
share:    /sub/<share-token>      ← 交给消费者订阅
nodes:    /nodes/<share-token>    ← 交给程序内接入（见 hx-consumer-api）
auth:     svc-... / ...           ← 已生成的凭据
```

**刷新发生在建组之前**，这是刻意的：订阅没刷新时节点还不存在，此时建组会选中空集合，
发布出一个「能连上但什么都不转发」的服务。手动分步调用时，你必须自己保证这个顺序。

不传 `auth` 时代码会**生成**凭据。共享入口模式按用户名路由成员，
没有凭据的成员会被拒绝 —— 所以「一次调用」必须替你把凭据补上。

### 失败时不留半成品

任何一步失败，这次调用**自己创建的东西会被删掉**（订阅、组、Listener）。
所以报错后可以放心重试，不会积累重复的订阅或孤儿 Listener。
错误信息会指明失败的步骤。

## 4. 什么时候用 quickstart，什么时候分步

| 场景 | 用什么 |
| --- | --- |
| 从零到可用代理 | `quickstart` —— 一步到位 |
| 手上有代理清单但没有 URL | `quickstart` 的 `subscription_inline`（清单直接进请求体） |
| 订阅已经存在且已刷新 | `POST /api/v1/proxy-services`（组 + Listener） |
| 要复用多个订阅 / 组合多个组 | 分步：`subscriptions` → `refresh` → `proxy-groups` → `listeners` |
| 只想让**某一个服务**固定走**某一个节点** | `POST /api/v1/listeners` 带 `node_id`（**不需要建组**） |
| 链式（把上游组当出口），按组 | `proxy-groups` 的 `dialer_proxy_group_id` —— 任意组，任意深度 |
| 链式，按住宅渠道 | 住宅供应商端点的 `upstream_proxy_group_id` |

分步调用时的**顺序不能变**：先 `refresh`，确认有节点，再建组。
`GET /api/v1/nodes` 用来确认刷新真的产出了成员。

## 5. 控制面支持什么、不支持什么

目录的 `not_supported` 段落是**产品边界**，不是能力缺失。关键三条：

- **指名单个节点用 `node_id`，不要为它建一个只有单个成员的组**。
  `POST /api/v1/listeners` 与 `PUT /api/v1/listeners/<id>` 都接受 `node_id`（控制面的节点 ID，取自 `GET /api/v1/nodes`）。
  这是让「一个服务从一个节点出网」的**直接**写法：服务端的 `IN-USER` 规则会以该节点的 Mihomo 代理名为目标。
  它与 `proxy_group_id` 的关系是：**二选一，`node_id` 优先**；只给 `node_id` 时行上的组会被自动填成家族的锚组（该锚组不承载这个服务的流量）。
  两者的区别只在语义：`proxy_group_id` 是「这个服务由那个组来选点」，`node_id` 是「这个服务就走那个节点」。
  用后者时，被指向的节点消失/停用会让整份配置编译失败并**指名那条 Listener**，不会静默退化成别的出口。
  反例（别再这么干）：为一个节点建一个 `manual` 组、里面只放它一个 —— 那正是「组当句柄用」，
  实测本机一度积出 57 个单成员组却只指向 28 个不同节点。
  **服务端按解析后的成员数拒绝**：只要 `source_spec` 当前会解析出 1 个成员，建组/更新就返回
  422 `validation_failed`，消息里给出 `node_id` 这条正确用法。算数的包括：一个 `node_id`、
  当前快照只有一个节点的订阅、筛选项（`name_keywords` / `regions` / `states` / `max_latency_ms`）
  或 `limit=1` 之后只剩一个、以及 `allow_empty` 配一个节点。**没有任何 JSON 字段可以绕过**；
  `allow_empty` 只放行 0 个成员（休眠态），不放行 1 个。不要试图拆成两次调用：
  把两节点组改成一节点同样被拒。只有 `include_direct` 单独成组（服务器直连锚组）和
  单个 `group_ids` 引用（组合策略）是例外；它们不是节点句柄。
  `quickstart` 的 `node_ids` 也必须 ≥ 2；`subscription_url` / `subscription_inline` 刷新后只剩
  1 个节点也会在创建组之前被拒并回滚订阅。
  见 `.agents/notes/implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md`
  与 `.agents/notes/implemented/architecture/2026-10-18-a-group-size-is-its-members.md`。
- **代理组和节点是两种对象，id 不能互换**。两者的 id 都长得像不透明字符串，字段名只差一个
  复数 `s`，所以最容易犯的错是把 `GET /api/v1/proxy-groups` 拿到的 id 填进
  `source_spec.node_ids`。判据看前缀：`node-…` 是节点（`GET /api/v1/nodes`）、`group-…`
  是代理组（`GET /api/v1/proxy-groups`）、`sub-…` 是订阅、`listener-…` 是入口。
  `source_spec.node_ids` 只收节点 id、`group_ids` 只收组 id、`subscription_ids` 只收订阅 id；
  放错会被 422 点名「这是代理组 ID，应该放 group_ids」。**单数 `listener.node_id` = 这个服务
  走这个节点（不建组）；复数 `source_spec.node_ids` = 这个组在这些节点里挑（至少 2 个）。**
  机器可读版本在 `GET /api/v1/capabilities` 的 `object_model` 段。
- 控制面**不承载流量**：它是控制面，数据面是 Mihomo 受管进程。
  它不会进入数据转发路径。
- 控制面**不替你轮询/选点/排名**：哪些节点可用、延迟多少，取决于你要访问的目标，
  所以由消费者自己决定。控制面只发布节点。
- **链式代理有两个层级，都能用**：
  - 组级 `dialer_proxy_group_id`（在 `POST/PUT /api/v1/proxy-groups` 上）：任意组都可以把出口
    经由另一个组，**深度不限**。目标组必须存在且已启用，不能形成环；同时设
    `source_spec.include_direct` 会被拒绝，因为 DIRECT 会绕过链路。
  - 节点级 `upstream_proxy_group_id`（只在住宅供应商上）：把该渠道**自动生成**的每个节点
    都经指定组出网。
  - 一个节点只能有一条出口路径。若某节点已由住宅渠道烧入上游，再把它所在的组配上组级链路
    会**报错**而不是静默覆盖。

用户问「支持不支持 X」时，**先查目录的 `not_supported`**，不要从别的端点推断。

## 6. 别做的事

| 别做 | 为什么 |
| --- | --- |
| 猜字段名或枚举值 | 服务端拒绝未知字段，且枚举是封闭的；先跑 `show`/`enums` |
| 假设「WebSocket path 是认证」 | path 不是认证机制，凭据才是 |
| 把控制面塞进流量转发路径 | 违反产品边界，也会让控制面变成瓶颈 |
| 让控制面替你轮询节点 | 那是消费者的职责 |
| 依赖错误**消息文本**做逻辑 | 状态码和 `error.code` 才有稳定语义 |

## 7. 目录的稳定性

- 目录**只增不改不删**字段名与枚举值。加端点、加字段不会破坏你。
- 改名、改类型、删枚举值 = 破坏性变更，控制面会开新版本路径而不是改这个。
- 因此遇到**未知枚举值要报错并上报**，那说明你的副本过期了，不是让你猜的余地。
- 完整能力目录就是运行时的事实来源：`GET /api/v1/capabilities`。
