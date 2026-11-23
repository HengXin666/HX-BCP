# Agent Note: 代理组的 size 是它解析后的成员数, 不是 node_ids 的长度

Status: implemented

收紧
`.agents/notes/implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md`
的判据。那篇 note 的其余部分 (为什么在 `proxygroup.Service.normalize` 里判、为什么拒绝而不是
静默改写、为什么 `listener.node_id` 是替代形态、`AllowSingleNode` 只对 Go 调用方可见) 仍然
成立; 本文只改「什么算 size=1」。

## Problem

2026-10-18 用户问:「这个功能实现了吗? 为什么我看 ai 还是会创建 size=1 的?」

上一版判据是**静态**的:

```go
len(spec.NodeIDs) == 1 && len(spec.SubscriptionIDs) == 0 &&
len(spec.GroupIDs) == 0 && !spec.IncludeDirect && !spec.AllowEmpty
```

它只拦「请求里恰好写了一个 node_id」这一种写法。对运行中的服务做实测, 以下四种请求全部
返回 201 (或应用失败但组行已落库), 且 `GET /proxy-groups/<id>/members` 的 `count` 都是 1:

| 请求 | 旧判据 | 实际成员数 |
|---|---|---|
| `{"node_ids":["n1"],"allow_empty":true}` | 放行 | 1 |
| `{"subscription_ids":["sub-one-node"]}` (快照只有一个节点) | 放行 | 1 |
| `{"subscription_ids":["sub-pool"],"limit":1}` | 放行 | 1 |
| `{"subscription_ids":["sub-pool"],"name_keywords":["HK 01"]}` | 放行 | 1 |

这不是边角料。`GET /api/v1/capabilities` 当时还明确写着「one node_id ... (no subscription,
no group, no include_direct, no allow_empty) is rejected」, 等于把 `allow_empty` 写成了
一条绕过指引; `source_spec.allow_empty` 的说明也只说「休眠组」, 没说它只对 0 个成员成立。
AI 照着目录拼请求时, 加一个 `allow_empty: true` 就能得到它想要的单节点组。

## Decision

**判据改为: 请求在当前快照上解析出的成员数 == 1 时拒绝; 0 个成员放行 (休眠/未刷新).**

- 成员数 = 显式 `node_ids` + 订阅经过同一套 `ResolveMembers` 规则流水线后的节点 +
  `group_ids` 的引用数 + `include_direct`。与编译期看到的成员集合同源, 不在 API 层另写
  一套近似。
- 两个形态仍然放行, 因为它们不是「节点句柄」:
  - `include_direct` 单独成组: DIRECT 是服务器自己的出口, 也是钉住 listener 最便宜的锚组;
  - 单个 `group_ids` 引用且没有其它成员: 这是「一个组的组合策略」, 不是把节点包起来。
    一个 `node_id` + 一个 `group_ids` 是 2 个成员, 不受影响。
- `allow_empty` 只对 0 个成员成立。`allow_empty` + 一个节点现在和其他单成员形状一样被拒。
- `Create` 与 `Update` 都判。存量单成员行 (包括旧的 `allow_empty` 洞、单订阅组) 仍可改名/
  停用/换策略: `legacySingleMember` 看的是**库里已有的** spec, 不是候选 spec, 所以不能拿
  一个两节点组改成单节点。DIRECT-only 锚组不享受这条豁免, 否则可以在一次编辑里把它变成
  节点句柄。
- 未刷新订阅解析出 0 个成员, 允许先建组等快照; 快照之后变成 1 个是状态变化, 不是这次
  调用创建出来的形状。用户要拦的是「创建 size=1 的组」这个动作。
- 错误消息继续给出正确用法, 并点名这些路径都算数。`capabilities`、`SKILL.md`、创建表单
  同步: rule 模式预计命中 1 个节点时禁用提交并提示改用 `listener.node_id`。`quickstart`
  在刷新后发现来源只有 1 个节点时提前拒绝 (仍会回滚它刚建的订阅), 而不是让调用方从
  组服务的包裹错误里找补救方式。

## Alternatives considered

- **什么都不做, 继续用静态 `node_ids` 判据。** 它最强的理由是**不误伤动态快照**: 一个今天
  1305 个节点的订阅明天可能只剩 1 个, 按解析结果拒绝会让一次改名因为调用方无法在本次请求
  里修的事实而失败; 上一版 note 正是按这个理由放行单订阅。否决理由: 用户报的「AI 还在建
  size=1 的组」实测就是这四条路径, 而它们都不是「订阅缩水」, 是**请求本身**只选了一个成员;
  放行它们等于把规则降级成「别写 `node_ids: [一个]`」的格式建议。
- **只在 API 层列路径 (proxy-groups / proxy-services / quickstart 各判一次)。** 它最强的理由
  是分层干净, 领域服务不必知道 HTTP 语义。否决理由: 路径表会过期, 这是 `guard.go` 自己写下
  的已知成本; 而 `proxyservice`、`quickstart`、住宅渠道都经过 `proxygroup.Service`, 在
  `normalize` 里判一次即可覆盖全部。
- **在 apply/刷新时拒绝, 保证运行中的组不会只有 1 个成员。** 它最强的理由是**状态**保证:
  订阅缩到 1 个节点时也不会出现 size=1 的组。否决理由: 一次订阅刷新会让整份配置 apply
  失败, 连带打断与该组无关的服务; 而且用户报的是「创建」这个动作。apply 期的策略应该另立
  一条 (告警或降级), 不该把创建期的规则放大成全局故障。
- **把 `allow_empty` 从 HTTP 契约里删掉。** 它最强的理由是**没有字段就没有绕过**。否决理由:
  休眠组 (先建组、等订阅刷新) 是真实形态, 也是单订阅未刷新时唯一可表达的期望; 删字段会把
  它和「单节点组」一起打死。正确做法是把它收紧为「只对 0 个成员成立」。
- **只堵 `allow_empty` 和 `limit=1`, 保留单订阅。** 它最强的理由是**爆炸半径最小**: 单订阅
  是上一版明确同意放行的形态, 生产库里的 `良心云` 等组都走订阅。否决理由: 实测里单节点
  订阅同样返回 `count: 1`; 只堵两个字段, AI 换一个字段就能继续建同样的组, 规则会再次变成
  「建议」。现在的判据对多节点订阅没有任何影响, 只拒绝**当前只剩一个成员**的订阅。
- **按「重复包装同一节点」判 (时间维度)。** 它最强的理由是贴近病根: 同一节点被包五次才是
  真正的浪费。否决理由: 单次调用看不到历史; 而且它允许第一个单节点组通过, 「每个节点各建
  一个」这个最常见形态仍然不受约束。

## Consequences

**新增行为**: 以下创建/更新返回 422 `validation_failed`, 消息含 `代理组节点必须 > 1` 与
`listener.node_id`:

- 一个 `node_id` (无论是否带 `allow_empty`);
- 当前快照只有一个节点的订阅;
- `limit=1` 或筛选项 (keywords/regions/states/max_latency_ms) 之后只剩一个成员;
- 两节点组缩成一个节点 (原有行为)。

**保持不变的豁免**:

- `include_direct` 单独成组 (锚组) 与单个 `group_ids` 组合;
- 0 个成员的休眠/未刷新组;
- 住宅渠道的 `AllowSingleNode: true` (Go 调用方专用);
- 存量单成员组仍可改名/停用/换策略, 但不能从多成员变成单成员。

**实测验证**:

- `internal/proxygroup/single_node_rule_test.go`: 四条新路径全部拒绝, 多成员订阅、节点+订阅、
  未刷新订阅放行; 存量单成员订阅组仍可改名。
- `internal/dataplane/mihomo/manager_test.go` 的单上游 fixture 显式使用 `AllowSingleNode`,
  因为它测的是 listener 选点而不是建组规则。
- 用真实二进制 + 临时 data dir 实测: 上表四条请求全部 422, 两节点订阅与 `node_ids`+DIRECT
  仍可创建; 失败请求不落组行。
- `go test ./...` 全绿。

**已知边界**: 创建后订阅快照缩到 1 个节点仍然是状态变化, 本次不拦; 要拦需要 apply 期策略,
理由见上。group 的 `rule_pipeline_json` 自定义规则没有参与创建期解析 (创建时为空, 更新时
沿用库里的), 所以一个只在自定义流水线里限成 1 个成员的存量组仍可能被更新; 这与「创建动作」
的边界一致。
