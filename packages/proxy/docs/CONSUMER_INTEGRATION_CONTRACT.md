# 程序内接入契约（Nodes API / 配置中心）

> 2026-09-14 用户决策（见 [Agent Note](../../.agents/notes/implemented/feature/2026-09-14-consumer-nodes-api.md)）：
> HX-Pxy 是**配置中心**，不是转发中继，也不是消费者的探测调度器。
> 它只回答"我有哪些节点、怎么从这里拿到它们的连接方式"；探测、选点、轮询节奏**全部由消费者自己负责**。

## 0. 这条契约约束谁

写这段代码的人约束的是这个文件；读这段代码的 Agent 也信这个文件。双方共用一串常量：
**错误码、JSON 字段名、路径词元**。改这些词元 = 改契约，必须同一次改动里改三处：

1. 生成响应的 Go 代码（`internal/api/consumer.go` + `internal/listener` 的导出器）；
2. 本文件；
3. `.agents/skills/hx-consumer-api/SKILL.md`。

三者不一致 = 契约破裂，按 bug 处理。

---

## 1. 消费者是谁

| 消费者 | 形态 | 它要什么 |
| --- | --- | --- |
| 人类客户端 | Clash Verge / Mihomo / sing-box / v2rayN 等 | 一个能直接导入的订阅 URL |
| 程序内接入 | 注册机、爬虫、浏览器自动化、多租户服务 | **一份 JSON 节点清单** + 稳定的字段名 |
| AI Agent | 按本仓库 skill 写代码的助手 | 冻结的字段与错误码，不许自己发明 |

第三种消费者必须能**只靠 skill 目录下的文件**完成接入，不需要读 Go 源码。这是 skill 的验收标准，不是文档的小节。

---

## 2. 提供什么，不提供什么

**提供**：节点清单（一个 JSON）、每个节点的协议中立的连接方式、拿这些节点的凭据、四个渲染格式的订阅 URL。

**不提供、也不计划提供**：

- 出口探测、延迟测速、出口 IP 体检、"哪个节点能用"的判断 —— **消费者自己跑**；
- 轮询节奏、选点策略、重试退避 —— 消费者自己定；
- 在服务端为消费者落地本地端口 —— 控制面不得进入数据转发路径（`AGENTS.md` §1）。

原因不是能力不足，是职责边界：控制面无法知道消费者要访问哪个目标站、能接受多少延迟、愿意烧多少流量。把探测做进控制面只会得到一个对所有消费者都不最优、却必须长期兼容的接口。

### 2.1 唯一的例外：住宅节点的服务器端轮换

住宅渠道的 `/ctl/` 仍然是服务器端权威，因为住宅出口的轮换**只有服务器能做**：供应商凭据在服务端，出口 IP 映射在服务端，多服务并发时的互斥窗口也只能在服务端保证。

但它的语义也是"你问，我换"，不是"我替你轮询"：

- `POST /ctl/<control-token>/nodes/<index>/next` 由消费者主动调用；控制面不会自己触发；
- 调用频率由消费者的租约模型约束（`lease_id` + `expected_alloc_version` CAS + 每窗口 2 秒最小间隔）；
- 控制面不为任何消费者建立后台轮换任务。

普通节点没有任何等价接口 —— 普通节点不需要"换出口"，它就在那里，能不能用由消费者探测决定。

---

## 3. 认证与寻址

契约与 `/sub/` 完全一致：**token 即唯一凭据**，不走管理员 Session。token 放在路径里（与 `/sub/`、`/ctl/`、`/provision/` 同一族），因此**已有的订阅 URL 里的人，已经握着这份清单的 URL**。

```text
GET /nodes/<share-token>
```

- token 与 `/sub/<share-token>` 是同一个（同一 Listener 或同一住宅渠道）；
- 路径段就是 token：`/nodes/<share-token>`。**不要**写成 `/api/v1/` 下的路径 —— 那是会话认证的管理域，放公共 token 资源会与管理员节点列表撞路径（`net/http` 的 `ServeMux` 对重复注册直接 panic，守护进程起不来）；
- 未启用/未知 token 一律 **404**，不区分"凭证错"与"资源不存在"（前端对象的存在性不可探测）；
- 响应 `Cache-Control: no-store`，与订阅导出一致；
- 请求日志按 `/nodes/[redacted]` 记录，token 不落日志（与 `/sub/[redacted]` 同一条脱敏规则）。

---

## 4. 响应

`Content-Type: application/json; charset=utf-8`。

```json
{
  "name": "香港专线",
  "share_path": "/sub/8f3a...c1",
  "subscription_urls": {
    "clash": "/sub/8f3a...c1?format=clash",
    "v2rayn": "/sub/8f3a...c1?format=v2rayn",
    "sing-box": "/sub/8f3a...c1?format=sing-box",
    "uri": "/sub/8f3a...c1?format=uri"
  },
  "nodes": [
    {
      "name": "香港专线-01",
      "protocol": "mixed",
      "host": "proxy.example.com",
      "port": 7890,
      "auth": { "username": "svc-3f9c", "password": "…" },
      "transport": "tcp",
      "tls": true,
      "browser_compatible": true,
      "uri": "http://svc-3f9c:…@proxy.example.com:7890#香港专线-01"
    },
    {
      "name": "香港专线-02",
      "protocol": "vless",
      "host": "proxy.example.com",
      "port": 443,
      "auth": { "password": "…" },
      "transport": "ws",
      "ws_path": "/__hx-proxy__/shared",
      "tls": true,
      "server_name": "proxy.example.com",
      "browser_compatible": false,
      "uri": "vless://…@proxy.example.com:443?security=tls&type=ws&…"
    }
  ]
}
```

### 4.1 字段契约

| 字段 | 类型 | 稳定性 | 语义 |
| --- | --- | --- | --- |
| `name` | string | 稳定 | 订阅/渠道显示名。同一 token 下**不随时间变化**；轮换出口不改它 |
| `share_path` | string | 稳定 | `/sub/<token>`，四格式共同前缀 |
| `subscription_urls` | object | 稳定 | 四个键**总是存在**：`clash` / `v2rayn` / `sing-box` / `uri`。相对路径，host 由消费者按自己请求的 Host 补全 |
| `nodes[]` | array | 稳定 | 稳定排序：先按导出分组，组内按声明顺序。**同一输入两次请求顺序一致** |
| `nodes[].name` | string | 稳定 | 节点显示名。住宅渠道轮换出口后**不变** |
| `nodes[].protocol` | string | 稳定 | 取值为下表的封闭集合。**未知值必须被消费者拒绝**，不许猜测 |
| `nodes[].host` / `.port` | string / int | 稳定 | 消费者实际要连的地址；绝不会是只环回可用的内部端口——那种情况下整个响应是 404 而不是泄漏一个连不通的地址 |
| `nodes[].auth` | object \| null | 稳定 | `username` / `password`，按协议二选一或都给。**只有这里与 `/sub/` 会返回明文凭据** |
| `nodes[].transport` | string | 稳定 | `tcp` / `ws` |
| `nodes[].ws_path` | string | 稳定 | 仅 `transport=ws` 时出现，已规范化为 `/__hx-proxy__/` 前缀 |
| `nodes[].tls` | bool | 稳定 | 是否 TLS |
| `nodes[].server_name` | string | 稳定 | 仅 `tls=true` 时出现，SNI / Host |
| `nodes[].browser_compatible` | bool | 稳定 | 能否被浏览器直接当作代理使用。`ws` 传输恒为 false —— 浏览器不能直接吃 WS 代理 |
| `nodes[].uri` | string | 稳定 | 该节点的分享 URI，便于把单个节点塞进已有配置 |

`protocol` 的封闭集合：

```text
mixed | http | socks | vless | vmess | trojan
```

**这份清单是契约，不是当前实现快照。** 增删值 = 破坏性变更，走 §6 的流程。

### 4.2 三种典型消费方式

```text
① 整段导入（最省事）
   把 subscription_urls.clash 交给本机 Mihomo / Clash Verge。

② 逐节点落地（程序内接入）
   自己起一个内核，遍历 nodes[] 生成配置。
   transport=ws 的节点由内核负责 WS 隧道 —— 控制面只给地址与凭据。

③ 直接拨号（HTTP/SOCKS）
   protocol 为 mixed / http / socks 时，host:port + auth 可以直接喂给
   Playwright --proxy-server、curl -x user:pass@host:port、HttpClient 的 Proxy。
   ws 节点不能走这条路。
```

---

## 5. 错误码

消费者**只依赖 HTTP 状态码**，不解析任何错误消息文本（`AGENTS.md` §4）。

| 状态 | 含义 | 消费者动作 |
| --- | --- | --- |
| 200 | 成功，body 是 §4 的 JSON | 正常处理 |
| 404 | token 未知/禁用/不属于任何可导出的服务 | 检查凭据；**不要**当成"稍后重试" |
| 405 | 方法不是 GET | 修客户端 |
| 500 | 控制面内部失败 | 退避后重试 |
| 500 | 控制面内部失败 | 退避后重试 |

这条契约里**没有**"稍后重试可能成功"的 404 —— 与 `/ctl/` 的 `409 lease_held` 不同，节点清单是纯读，读失败就是失败。

---

## 6. 契约不许变

"不会变化"是这份契约存在的前提。因此：

1. `nodes[]` 的字段**只增不改不删**。加字段是兼容变更；改字段名、改类型、改 protocol 枚举值都是破坏性变更。
2. 破坏性变更必须：新的路径版本（`/nodes/` → `/nodes/v2/`）、本文件更新、skill 更新、旧路径保留至少一个大版本。**同一次改动**完成。
3. 禁止在响应里加"内部使用、消费者别读"的字段 —— 只要是字段就会被读。
4. 每个字段都要有实现它的测试；没有测试的字段等于没承诺。

**skill 与契约的同步门禁**：skill 里写死的每个词元（路径、字段名、protocol 值、错误码）都必须能在本文件里找到，反之亦然。改动任一方的词元而不同时改另一方，视为契约破裂。可执行的检查见 §7。

---

## 7. 验证

```bash
# 契约测试：字段存在性、protocol 枚举、稳定排序、404 语义
go test ./internal/api/ ./internal/listener/ -run TestConsumer

# 两种 token（普通 Listener / 住宅渠道）都返回同一形状
go test ./internal/api/ -run TestConsumerNodesResidentialTokenUsesChannelExports

# skill 与契约的一致性（Node 直接跑 .ts；Node 20 以下用 npx tsx 同一脚本）
node scripts/verify-consumer-contract.ts
```

最后一条是把"本文 §6 的承诺"变成**可执行**的东西：它逐词元比对
`docs/CONSUMER_INTEGRATION_CONTRACT.md`、`internal/api/consumer.go` 的常量表和
`.agents/skills/hx-consumer-api/SKILL.md`。三方任一处漂移即失败，并在输出里指出
是哪一方、哪一个词元。没有它，"同步"就只是一句话。

---

## 8. 相关文档

- [OpenAPI：订阅服务消费者](openapi/subscription-consumer.openapi.yaml) — 本契约的机器可读版本；与住宅渠道控制面分属两份文档，`go test ./internal/api -run OpenAPI` 校验字段与 Go 类型一致
- [住宅代理客户端与自动化 API](RESIDENTIAL_SESSION_API.md) — `/ctl/` 的服务器端轮换契约
- [住宅代理并发集成标准](RESIDENTIAL_INTEGRATION_STANDARD.md) — 租约与版本护栏
- [Cloudflare / 雷池](CLOUDFLARE.md) — 公网拓扑与协议边界（WS 才走得通的那部分）
- [配置中心端点](PROVISION_CENTER_20260821.md) — `/provision/` 的同族设计：只发配置，不中继流量
