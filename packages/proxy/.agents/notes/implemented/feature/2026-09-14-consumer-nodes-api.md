# Agent Note: 对外程序内接入用一份冻结的节点清单契约, 由 skill + 门禁跟代码同步

Status: implemented

## Problem

HX-ProxyGroup 对外的程序化接口只有两条路: `/sub/<token>` 给人用的订阅文本, 和 `/ctl/<token>` 给住宅自动化用的节点池。
前者是渲染结果, 不是数据 —— 消费者想"拿订阅里某个节点去拨号", 只能自己解析 Clash YAML 或 base64 URI, 于是每个消费者
各写一份解析器, 各踩一次"哪个字段是 host、哪个是 sni、ws path 带不带前缀"的坑。

后者只管住宅渠道的换出口, 不回答"经过这台控制面的普通代理服务有哪些"。

结果就是 gh-registrar 这类程序**完全绕开本仓库**, 自己实现了一整条订阅解析 + 自建 mihomo + 选点的链路
(`HX-Jungle/workspaces/browser-auto/gh-registrar`)。控制面明明已经有这些节点, 却没有任何一个"只读、稳定、
字段冻结"的入口能把它们交出去。

不做这件事的代价不是"少一个功能", 而是**每接一个新程序就要重新对一次口径**: 字段名对不对、路径前缀对不对、
哪个协议能直接给浏览器用。口径靠口头约定, 就一定会漂移。

## Decision

新增一条**只读**的公开契约 `GET /nodes/<share-token>`, 返回该 Listener / 住宅渠道的完整节点清单 JSON。
它与 `/sub/` 共用同一个 share token、同一套导出解析逻辑, 因此**不会出现两套真相**。

契约本身写在 [docs/CONSUMER_INTEGRATION_CONTRACT.md](../../../../docs/CONSUMER_INTEGRATION_CONTRACT.md), 字段、协议枚举、
错误码全部冻结; 变更只能走"新路径版本", 不允许改字段。

**职责边界是这条契约的核心**: 控制面只回答"我有哪些节点、怎么连它们"。它不探测、不测速、不选点、不轮询, 也不为消费者
落地端口。住宅的 `/ctl/next` 保持服务器端权威, 但仍然是"消费者主动问、服务器应答", 控制面不为任何消费者建后台轮换任务。

### Wire contract

响应形如:

```json
{
  "name": "香港专线",
  "share_path": "/sub/<share-token>",
  "subscription_urls": {"clash": "...?format=clash", "v2rayn": "...", "sing-box": "...", "uri": "..."},
  "nodes": [{
    "name": "香港专线-01", "protocol": "mixed",
    "host": "proxy.example.com", "port": 7890,
    "auth": {"username": "svc-3f9c", "password": "..."},
    "transport": "tcp", "tls": true,
    "browser_compatible": true,
    "uri": "http://svc-3f9c:...@proxy.example.com:7890#香港专线-01"
  }]
}
```

`protocol` 是封闭集合 `mixed | http | socks | vless | vmess | trojan`; `transport` 是 `tcp | ws`。

`browser_compatible` **只由 transport 决定**: `ws` 传输恒为 false(浏览器不能直接吃 WS 代理)。TLS 端**不**使其为 false ——
Chromium 接受 `https://host:port` 形式的 `--proxy-server` 并能据此完成请求, 已端到端实测。把 TLS 也算作不兼容会无缘无故
地把一批可用节点挡在浏览器自动化之外。

未知 token、禁用、以及"整体不可导出"一律 **404**, 不区分"凭证错"与"资源不存在", 让资源存在性不可探测。

**路径落在 `/nodes/` 而不是 `/api/v1/`**: `/api/v1/` 是会话认证的管理域, 公共 token 资源放进去会与管理员的
`GET /api/v1/nodes` 撞同一个 pattern, 而 `net/http` 的 `ServeMux` 对重复注册是**启动即 panic** ——
两个路由各自的测试都只注册自己那一个服务, 所以全绿也拦不住守护进程起不来。token 放路径里同时让
"已经有订阅 URL 的人"天然握着清单 URL。

### Single source of truth: skill 与契约同源

用户要求的"约束 skill 更新和程序同步", 不是靠文档里写一句"记得同步"就能成立的。它被拆成三层:

1. **导出器只有一份**: `/sub/` 与 `/nodes/` 都读 `internal/listener` 的同一个导出结构(`ShareExport` / `ShareNode`),
   不新增第二套订阅/节点解析。协议能力边界变了两个端点一起变。
2. **skill 里词元是写死的**: `.agents/skills/hx-consumer-api/SKILL.md` 直接写路径、字段名、protocol 值与错误码,
   并且**引用的就是契约文档**, 不复制一套自己的措辞。
3. **门禁是可执行的**: `scripts/verify-consumer-contract.ts` 逐词元比对三处 —— 契约文档、Go 常量表
   (`internal/api/consumer.go`)、skill 文件。任一方漂移即失败并指出是哪一方、哪个词元。

第 3 条是这条决策能被长期信任的唯一理由。没有它, 前两条只是愿望。

## Alternatives considered

- **只加文档, 不动代码, 让消费者继续解析 `/sub/`** — 这是最省事的路, 而且"字段已经都在 URI 里"的说法部分成立。
  它输在 URI 是渲染产物: 每个格式(`clash` / `v2rayn` / `sing-box`)对同一节点的表达不同, 消费者为了稳定性反而要
  实现多个解析器; 且 `/sub/` 的响应体是给人导入的, 没有任何机制阻止它在小版本里变得更花哨。冻结契约需要的
  "字段名 + 枚举 + 错误码"这三样, 在渲染结果里根本无处安放。
- **复用 `/ctl/<control-token>/nodes`** — 它已经返回 `endpoints[]` 这类协议中立字段, 语义上最接近, 复用看起来理所当然。
  它输在权限模型: `/ctl/` token 可以消耗供应商配额、轮换出口、读取临时节点鉴权, 是高权限凭据; 而"我只想拿普通代理服务的
  节点列表"是完全低权限的诉求。把只读消费者赶去持高权限 token, 会让凭据分级失效 —— 拿订阅的代码不该握着能烧配额的东西。
  另外 `/ctl/` 的发现入口是渠道, 而普通 Listener 根本不是渠道。
- **把探测/选点也做进控制面(代理组自动选最快)** — 有真实诱惑力: 控制面确实有 Probe 体系和延迟数据, 看起来顺手就能选。
  它输在职责: 控制面不知道消费者要访问哪个目标站、能接受多少延迟、愿意烧多少流量。"最有用的节点"是目标相关的,
  服务端无法替消费者回答。真做进去, 得到的只会是一个对所有消费者都不最优、却必须永久兼容的接口。
  (它也不是本项目要做的事 —— 数据面选点本来就由 Mihomo 的 `url-test` / `fallback` 在消费者侧完成。)
- **什么都不做, 维持现状** — 现状并非不可用: gh-registrar 已经能工作, 只是它自己实现了一遍。放弃这条的代价是不对称的:
  每多一个消费者, 就多一份私有解析器; 口径漂移的修复成本会随着消费者数量线性分摊到每一个调用方, 而不是在控制面修一次。

## Consequences

正面: 消费者拿到的是字段冻结的数据, 不是渲染产物; 新增一个消费者不再需要重新对一次口径; 凭据分级保持有效 ——
只读消费者不必握着能烧供应商配额的高权限 token。

代价与风险:

- **契约冻结的反面是接口不能变**。任何一个字段设计失误都要靠"新增路径版本 + 旧路径并存"来修, 成本远高于普通接口。
  缓解: 只暴露消费者真正要的三样(地址、凭据、协议), 不暴露任何"以后可能有用"的元数据。
- **明文凭据多了一个出口**。`/nodes/` 和 `/sub/` 一样会返回甲方凭据, 因此它必须和 `/sub/` 受同等级别的保护:
  仅 HTTPS、不缓存、日志脱敏、token 可轮换。这一点与现有的 `/sub/` 是同一个安全等级, 不是新增风险, 但**没有任何理由让它更宽松**。
- **skill 是给外部 Agent 用的**, 一旦发布就会被复制出仓库。门禁能保证仓库内三方一致, 保证不了仓库外的副本 ——
  缓解: skill 里显式写明"本文件受 `scripts/verify-consumer-contract.ts` 约束, 以契约文档为准", 并把契约文档路径放在开头。
- **门禁本身可能被绕过**(改词元时连门禁一起改)。缓解: 门禁只做逐词元比对, 不做语义判断; 若有人同时改三处,
  那是一次显式的契约变更, 会落进 git diff 被审阅 —— 这正是想要的效果, 而不是把判断塞进脚本。

## Testing

- `GET /nodes/<普通 Listener token>` 与 `/nodes/<住宅渠道 token>` 返回**同一形状**的 JSON,
  字段与 [契约文档](../../../../docs/CONSUMER_INTEGRATION_CONTRACT.md) §4.1 逐条对应。
- 未知/禁用 token 返回 404; 非 GET 返回 405; 响应带 `Cache-Control: no-store`。
- 同一输入连续两次请求, `nodes[]` 顺序逐字节一致。
- 请求日志不包含 token(query string 不记录)。
- `node scripts/verify-consumer-contract.ts` 在契约文档、Go 常量表、skill 三方一致时退出 0;
  任一方改动词元后退出非 0, 并打印漂移的**词元名与来源文件**。
- `.agents/skills/hx-consumer-api/SKILL.md` 足以让一个没读过 Go 源码的 Agent 完成接入:
  包含端点、认证、响应示例、错误码表、以及 §4.2 的三种消费方式。

已跑通(2026-09-14, 无本地 Go 工具链, 测试在 `golang:1.25-alpine` 容器内执行):

```bash
# 契约形状、枚举封闭性、稳定排序、404/405 语义
go test ./internal/api/ ./internal/listener/ -run TestConsumer -count=1

# 受影响包的回归
go test ./internal/api/ ./internal/listener/ ./internal/residential/ ./internal/dataplane/... -count=1

# 契约文档 / 渲染器 / skill 三方词元一致
node scripts/verify-consumer-contract.ts
```

三条都通过。门禁的**非空转**也被验证过: 只把 skill 里的 `browser_compatible` 改成 `browserCompatible`,
门禁立刻以 `skill lacks "browser_compatible"` 退出 1; 还原后退出 0。

重复注册 panic 也有独立回归测试: `TestConsumerNodesCoexistsWithAdminNodeRoutes` 同时装配
`WithNodes` + `WithListeners`(与 `cmd/hx-proxygroupd` 一致)并调用 `Handler()`。把路径改回
`/api/v1/nodes` 后可复现:

```text
panic: pattern "/api/v1/nodes" conflicts with pattern "/api/v1/nodes":
        /api/v1/nodes matches the same requests as /api/v1/nodes
```

这正是当初漏掉的判据 —— `consumer_test.go` 只传 `WithListeners`, `nodes_test.go` 只传
`WithNodes`, 两条路由各自的测试都无法看见对方。
