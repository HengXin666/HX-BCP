# Agent Note: 服务的形态由来源类别派生, 不靠名字; 用不上的选项必须连内容一起消失

Status: implemented

## Problem

用户原话:

> 「给不同的节点就是不同类型的节点。就比如说住宅代理订阅或者说代理池，它有不同的样式用于区分」
> 「你就不应该把一些根本用不上的选项，把它放在那种类型节点里面去。某个节点，它就只有一些刻画的类型」

生产库实测 (2026-10-01, 4 个代理服务), 三行**渲染成同一句话**:

| 服务 | 实际内容 | 行上显示的 |
|---|---|---|
| 良心云 入口 | 41 节点付费订阅 | `MIXED` `共享入口` `10/10 可用` | 自动测速 | `1 个订阅 · Top 全部` |
| ioa-vm-mixed | **钉死 1 个节点** | `MIXED` `共享入口` `10/10 可用` | 自动测速 | `1 个订阅 · Top 全部` |
| proxyscrape-pool-mixed | 1305 成员一次性池 | `MIXED` `共享入口` `0/493 可用` | 自动测速 | `1 个订阅 · Top 全部` |

三个各自可实测的缺陷, 全部由"类型没有进入界面"派生:

**缺陷一: 不适用的选项不是无害的 —— 它能清空一个组。** 新建表单无条件下发 `最大延迟 2000 ms`。该类规则 (`internal/pipeline/rules.go` 的 `maxLatencyPredicate`) 对**没有延迟样本**的节点判 false (`"node has no latency sample yet"`)。用生产库副本跑真实的 `ResolveNodeIDs`:

```text
池组 source_spec (states=[healthy,degraded])   无过滤 -> 436 个成员
同一 spec + max_latency_ms=2000                -> 0 个成员
```

池成员**全部**没有延迟样本 (`last_latency_ms` 1305/1305 为 NULL)。所以那不是"一个用不上的选项", 是一键把服务做成空组的开关; 配合 `empty_behavior=fail-closed` 就是静默断流。

**缺陷二: 钉节点的服务按锚组解析。** `resolveServiceNodes` 用 `listener.proxy_group_id` 找组再解析成员, 而钉节点的 listener 的组只是共享 schema 锚组 (`egressAnchorGroup`)。实测: `ioa-vm-mixed` 钉在 `ioa-vm-socks5` 上, 界面却列出**良心云的 10 个节点**, 计数也是那 10 个。用户看到的成员表与这个服务的出口无关。

**缺陷三: 所有计数都在残缺集合上算。** `api.listNodes()` 写死 `limit=1000`, 而库里有 1349 个节点 —— 单页返回恰好 1000 条 (实测 737998 字节)。于是行上的 `N/M 可用`、健康统计、每订阅节点数全部只统计了库存的前 1000 条。池服务显示 `0/493 可用`, 真实成员是 1305。

另有第四条, 同一类: `PUT /api/v1/proxy-services` 从不下发 `listener.node_id`, 所以**给一个钉节点的服务改名, 会静默把它解钉**, 出口从那个节点换成锚组的成员。

## Decision

**一、形态由 `subscriptions.source_class` 派生, 不由名字派生; 派生逻辑只有一处。**

新增 `web/src/lib/service-shape.ts`: 一张 `shapeMeta` 表同时供给**行徽章、行摘要、创建表单可选项、编辑表单可选项**。任何一处单独再写一份判据, 就是界面开始讲述与探测策略不同的故事的起点 —— 本仓已经在"同一事实两处判据"上栽过 (`compiler.go` 的 `groupNodeIDs` 注释、`guard.go` 的路径表)。

形态与判据:

| 形态 | 判据 | 来源类别 |
|---|---|---|
| `pinned` | `listener.node_id` 非空 | (由该节点所属来源反推) |
| `direct` | 无订阅且无 node_ids 的 DIRECT 组 | — |
| `manual` | `source_spec.node_ids` 非空 | — |
| `pool` / `subscription` / `residential` | 组的订阅来源类别 | 同名 |
| `mixed` | 引用多类来源 | 多类 |

`pinned` 与 `manual` 刻意分开: 前者是 **listener 钉一个节点**(不选择, 规则直接指向节点名), 后者是**组成员是手工列出的一批节点**(仍按策略在其中选择)。把两者混为一谈正是"某个节点"这句话含糊的来源。

**二、每个形态声明自己**拥有**哪些旋钮, 表单据此隐藏, 并且不再下发。**

`knobs.maxLatency = false` 对池与混合来源成立, 依据是上面那次实测。**隐藏与不下发是同一件事**: 只隐藏仍然发送, 等于保留缺陷一。

**三、来源类别必须可被操作员显式选择, 并在订阅行上可见。**

后端一直要求/默许 `source_class`, 能力目录也把它写成"创建订阅时必须显式给出", 但**前端从不发送该字段**, 一律落到服务端默认 `subscription`。于是导出的用户既看不到池与住宅的差别, 也无法在界面上分类 —— 池会被按订阅策略处理 (白烧探测额度且被隔离的成员不会换掉)。现在: 订阅行显示类别徽章; 新建/编辑订阅表单提供三选一, 编辑时带出存库值 (否则一次缺省的保存会把住宅源降级)。

**四、`node_id` 的"省略"必须是**保留**而不是**清除**。**

`proxy-service.update` 增加两个字段: `listener.node_id` (移动 pin) 与 `listener.clear_node_pin` (显式解除)。**不能用空串表达"清除"**: 行本身已经把这两种意图分开 —— `listeners.node_id` 可空、写入走 `NULLIF(?, '')`、读取走 `COALESCE(node_id, '')` (见 `2026-09-26-listener-pins-a-node.md` 里 SQLite 复合外键那段实测)。服务端在省略时读取存库的 pin 并原样带走。

**五、列表 API 分页取全, 不靠单次大 limit。**

`listNodes` 与 `listSubscriptions` 改为按服务器上限 (分别为 1000 / 500, 实测) 循环翻页。订阅端点的上限是 500 而不是 1000 —— 写错会得到 400 而不是一页更少的数据, 所以这个数字必须来自实测而非推断。

**六、行上的计数改称"可路由", 分母是全部成员。**

旧的 `N/M 可用` 只数 healthy, 于是几百成员的池显示 `0/N`。现在分母是成员总数, 分子是 `healthy + degraded + candidate` (隔离与退役的不参与路由, 由 `SupportedNodeStates` 与服务端的组过滤决定)。

## Alternatives considered

- **什么都不做, 只把行的摘要文案改好看一点。** 它最强的理由是这次的"不美观"看起来纯属呈现问题, 而文案改动零风险。否决理由: 缺陷一是**行为**问题而不是观感问题 —— 同一份表单默认值能把 436 个成员的组变成 0 个, 我在生产库副本上跑出来了。改文案不会让那件事不发生。

- **给每个服务加一个"类型"字段, 由操作员手工选择。** 它最强的理由是显式: 操作员知道自己在建池还是建专线, 直接告诉系统比让系统猜更可靠, 而且能表达"这个订阅被我当池用"。否决理由: 它**引入第二个事实来源**。订阅已经有 `source_class`, 而且那个字段是有代价的判据 (它决定失败后是隔离还是重试)。给组再加一个类型, 两者会不一致 —— 一个 `subscription` 类订阅放在声明为"池"的组里, 该按哪一套失败策略? 答案是订阅那套 (策略是**源**的属性, 见 `NodePolicyFor`), 于是组的类型字段从一出生就是装饰。现在从 `source_class` 派生, 两者不可能不一致。

- **按服务名/组名猜类型 (名字里有 pool 就是池)。** 否决: 名字是操作员随手写的文本, 不是判据。实测组名里恰好有 `proxyscrape-pool`, 所以"猜"在这个库里看起来会很准 —— 这正是它危险的地方, 第一次改名就失效。本仓已有一条明确纪律: 判据必须落在数据上 (见 `2026-10-01-api-cannot-grow-the-group-graph.md` 里"判据是'谁在调用', 不是'写什么'")。

- **隐藏池的 `最大延迟` 输入框, 但仍然照常下发。** 它最强的理由是改动最小, 且保留了"以后想按延迟筛"的可能性。否决理由: 这正是当前缺陷的精确形态 —— 输入框背后的值仍然到达服务端。隐藏一个开关而不改变它发出的内容, 是让用户看不见故障而不是消除故障。**隐藏与不下发必须是同一件事。**

- **把 `maxLatencyPredicate` 改成"无样本时放行"。** 它的最强理由是这看起来才是规则的语义 bug: 没有样本不等于慢。否决理由有两条: 其一, 对住宅源"未测速即放行"会把一条从未验证过的出口编进配置, 而这条规则的既有含义是保守的 (宁可不选也不选没验证的); 其二, 真正的问题不是规则对不对, 是**这个旋钮对一个池根本没有意义** —— 池成员的筛选应交给数据面健康检查, 而不是控制面的最后一次测速快照。改规则会在一个已经正确的判断上冒回归风险。

- **让钉节点的服务也显示锚组成员 (现状), 只在标题上写"已钉节点"。** 否决: 用户明确要的是"不同类型的节点有不同的样式用于区分", 而这份成员表与这个服务的出口**无关** —— 它不是样式问题, 是错误信息。锚组是 schema 细节 (`proxy_group_id` 是 NOT NULL), 不该出现在任何界面上。

- **给 `proxy_service.update` 让 `node_id` 的空串表示"清除"。** 否决: 与建表时踩过的坑同源。SQLite 的复合外键在列为 NULL 时整体判定为真, 而空串会被当成真实值去比对, 所以这张表的写入路径用 `NULLIF(?, '')`、读取用 `COALESCE`。"空串 = 无引用"在这张表上是隐雷, 加一个 `clear_node_pin` 布尔比复用空串更难写错。

## Consequences

**新增能力**: 界面按来源类别区分服务形态 (池 / 订阅节点 / 住宅出口 / 指定节点 / 固定节点组 / 服务器直连); 新建与编辑订阅可选来源类别; `PUT /api/v1/proxy-services` 接受 `listener.node_id` 与 `listener.clear_node_pin`, 并在省略时保留存库的 pin; `web/src/lib/service-shape.ts` 是形态与旋钮的唯一判据。

**行为变化 (需要操作员知道的)**:
- 池 / 混合来源的创建表单不再发送 `max_latency_ms`。既有组里**已经存在**的该字段不受影响 —— 这次没有迁移去改写存量 spec。生产库里池组的 spec 实测**没有** `max_latency_ms` (所以它没被清空), 这一条修的是下一次创建。
- 行计数从 `N/M 可用`(只数 healthy) 改为 `N/M 可路由`(health/degraded/candidate)。同一个池从 `0/493` 变成 `355/355`(实测, 数值随探测漂移)。**分母也变了**: 它反映完整的成员数, 不再被 1000 条分页截断。
- 钉节点的服务在成员表里只列那一个节点, 并显示它的形态说明。

**代价与风险**:
- **`serviceClasses` 依赖 `subscriptions` 列表已加载。** 若订阅请求失败, 形态会退化成 `direct`/`pinned` 而不是抛错。选中"退化成不显示类别"而不是"报错": 列表在任何情况下都必须能渲染, 而一个错误横幅比一个不精确的徽章更妨碍操作。**未覆盖**: 没有为"订阅接口失败"写 e2e (需要注入网络故障)。
- **`mixed` 的 `knobs` 是保守交集, 不是并集。** 一个订阅 + 一个池的组, 其 `maxLatency` 取 false —— 因为池那一半会因无样本被整片丢弃。这是刻意的方向: 少给一个筛选, 而不是给一个会让组变空或让操作员误判的筛选。
- **`nodePages` 门禁断言的是"发出了第二页请求", 不是"计数等于 1305"。** 计数随探测漂移, 断言具体数字会得到一个每天都在变的门禁。分页行为才是回归点。

**实测验证 (2026-10-01, 对运行中的实例 `127.0.0.1:28341` + 控制面 `19090`, 数据目录 `.tmp/hxwebx/rundata`)**:

- 缺陷一 (决定性, 生产库副本上跑真实 `ResolveNodeIDs`):
  ```text
  candidates=1308
  no-filter        members=436
  2000ms cap       members=0        <- 创建表单的默认值
  ```
- 缺陷二与三 (e2e `web/e2e/routing-service-shapes.mjs`, 全绿):
  ```text
  routing-service-shapes: ok — 池/订阅/指定节点分行分型, 节点列表分页取全, 钉节点的服务只有 1 个成员
  ```
  其中"钉节点的服务只有 1 个成员"是本次最关键的一条: 修前它列出 10 个 (锚组的成员)。
- 缺陷四 (后端单测, `internal/proxyservice/service_test.go`): `TestUpdateKeepsStoredNodePinWhenCallerOmitsIt` / `TestUpdateMovesNodePinWhenCallerNamesOne` / `TestUpdateClearsNodePinOnlyWhenAsked` 三条全过。
- 界面效果 (截图, 同一页面, 修前四行全一样):
  ```text
  良心云 入口          订阅节点   订阅节点 5/5 可路由    自动测速  良心云 · Top 全部
  WsPxy-colo          住宅出口   VLESS   0/3 可路由    手动选择  outlook-ip、outlook-resi 等 3 个源 · 不参与自动淘汰
  ioa-vm-mixed        指定节点   MIXED   0/1 可路由    固定出口  钉死节点 ioa-vm-socks5
  proxyscrape-pool-mixed 代理池   MIXED   355/355 可路由 自动测速  proxyscrape-datacenter · 全量
  ```
- 门禁: `npx tsc -b` 无输出; `go build ./...` 干净; `go test ./internal/proxyservice/ ./internal/listener/ ./internal/api/` 全 ok; `node scripts/verify-admin-catalog.ts` 与 `node scripts/verify-consumer-contract.ts` 全 ok。

**未覆盖**:
- 没有改写存量 `source_spec`。若某个既有组带着 `max_latency_ms` 且成员无样本, 它今天就已经是空的 —— 本次不主动改数据, 但那类组现在会在行上显示 `0/N 可路由`, 与真正的池区分开。
- 没有为"订阅请求失败时形态退化"写 e2e (见上)。
- 混合来源形态没有真实数据 (生产库没有同时引用两类来源的组), 只由 `shapeForSelection` 的推导覆盖。

## Related

- [节点失败策略按订阅源的显式分类](../feature/2026-09-26-source-class-drives-probe-policy.md) — `source_class` 的来源, 以及它为什么是**源**的属性而非节点的属性
- [listener 可以直接钉一个节点](../feature/2026-09-26-listener-pins-a-node.md) — `listener.node_id` 的语义, 以及"空串不等于无引用"的踩坑记录
- [第 4 类「HTTP/SOCKS 代理列表池」](../feature/2026-09-26-proxy-list-pool-subscription-source.md) — 池为什么是一条 Inline 订阅而不是新实体
- [机机凭据不许再长代理组](../architecture/2026-10-01-api-cannot-grow-the-group-graph.md) — "判据必须落在数据上, 不能落在名字上"的同一条纪律
