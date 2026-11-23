# Agent Note: 订阅刷新不得退场住宅节点 (退场清扫按 origin 分流)

Status: implemented

## Problem

2026-10-15 晚 gh-pool 的注册连续三次在**解析出口**这一步 0 秒失败 (run `4525f63ce069` /
`f7ea0de861d4` / `1e67c977a99a`, 21:35:40 起), 报:

```text
阶段 pre_login 指定的出口 resi-linkup 不可用 (组 residential-linkup 内无可选节点)
```

现场实测 (22:00 复现): `egress_health.node_health('residential-linkup')` 回
`alive=false / history_len=0 / tcp_reachable=true / err="组 residential-linkup 内无可选节点"`,
而同一时刻 `curl -x http://resi-linkup:<pw>@127.0.0.1:7890 https://api.ipify.org` **是通的**。
Mihomo 里该组 (`Selector`, `empty_behavior=fail-closed`) 的成员列表就是 `['REJECT']`。

数据面证据链:

| 事实 | 实测值 |
|---|---|
| 住宅节点 `node-residential-…23bbbeccf605` | `retired_at = 2026-10-15T14:26:47.062526183Z` |
| 同一时刻的订阅快照 | `snap-7ed24c32d42fcd5472a5b932ca7498e6` `fetched_at` **完全相等** (`sub-14b0b1ce…`) |
| 组声明 | `proxy_groups.source_spec_json` 仍指向该节点, `version` 45→47 |
| 历史 10 个住宅节点 | **9 个** `retired`, 退场时刻与整点订阅刷新完成时刻**差 −60s ~ +0s** |
| 住宅节点在 `subscription_nodes` 里的行数 | **0** (它们由住宅渠道自己 materialize, 从不进订阅快照) |
| 22:27:05 轮询 | `members=['REJECT']` (与 22:26:47 退场对上) |
| 22:37:26 (下一次轮换后) | `members=['hx-node-8cd3eb8e6366317f']` —— 自愈, 但已空窗约 10 分钟 |

根因: `CommitParsedSubscriptionSnapshot` / `ActivateParsedSubscriptionSnapshot` /
`DeleteSubscription` 三处各写了一份**全局退场清扫**:

```sql
UPDATE nodes SET lifecycle_state='retired', retired_at=?, version=version+1
WHERE lifecycle_state NOT IN ('disabled','retired')
  AND NOT EXISTS (SELECT 1 FROM subscription_nodes sn
                  JOIN subscription_snapshots ss ON ss.id=sn.snapshot_id
                  WHERE sn.node_id = nodes.id AND ss.status='active')
```

它的语义是"不在任何 active 订阅快照里 = 该退场"。对订阅节点成立, 对**住宅节点恒成立** ——
住宅节点的生命周期由 `residential` 服务拥有 (会话删除/替换时才退场), 与订阅快照毫无关系。
于是每一次订阅刷新都会把当前唯一那条住宅出口退掉。

为什么"看起来时好时坏": 住宅会话有 600s TTL, 过期轮换 (`MaintainExpiredClientSessions`,
`session_expiry_policy=rotate`) 会 `UpsertResidentialSessionNode` 建新节点并把 `retired_at` 清成 NULL,
所以退场只持续到下一次轮换。**空窗期恰好覆盖整点刷新后的 ~10 分钟**, 注册撞上就 0 秒失败。

## Decision

把三处清扫合并成**一份** `retireDisappearedNodesSQL`, 并加 `origin` 分流:

```sql
  AND origin <> 'residential'
```

- 判据只有一处: 常量 `retireDisappearedNodesSQL` (`internal/store/nodes.go`), 三个调用点共用。
  改前是同一段 SQL 抄三份, 改一处漏两处正是本次能长期潜伏的原因。
- 住宅节点的退场权**完全**交回 residential 服务 (`DeleteResidentialSessionNode` /
  `ReplaceResidentialSessionPool`), 订阅侧不再碰它。
- 与既有策略一致而非新增特例: `SourceClassResidential.RetireAfter = 0` 早就写明"住宅节点
  永不被自动退场" (`internal/store/source_policy.go`), 探针侧已经遵守; 本次是让**退场清扫**
  也遵守同一条, 修的是两处判据不同步。

## Alternatives considered

- **什么都不做 / 靠轮换自愈。** 最强理由: 实测 10 分钟后组自己恢复了, 影响面只是"偶发失败重试一次"。
  否决: 空窗期与整点订阅刷新**结构性对齐** (9/10 命中), 而整点正是运维与批量注册的常用时段;
  用户看到的是"住宅代理注册无法进行", 且 gh-pool 铁律要求阶段出口不可用时**明确报错不回落**,
  于是每一次撞上都是整轮失败而不是降级。
- **在 gh-pool 侧加"组空了就等一会再试"。** 最强理由: 不动 hx-pxy, 改动面最小。
  否决: 这是**控制面状态错误**, 消费者只能看到症状; 且 gh-pool 若等待/回落就违反"阶段出口钉死"
  的语义 (等一个不该出现的空窗 = 把控制面 bug 变成业务侧超时)。
- **给住宅组设 `empty_behavior=direct`。** 最强理由: 组空了也不会断流。
  否决: 那是**静默回落直连** —— 用户选住宅就是为了出口身份, 直连等于失去阶段语义,
  正是 gh-pool 铁律明令禁止的那类静默降级。
- **改成"清扫只处理本订阅的节点" (按 subscription_id 收窄)。** 最强理由: 语义更精确。
  否决: 现有语句的用途正是清理**跨订阅**孤儿 (订阅被删/节点从所有快照消失), 收窄会漏掉它们;
  真正的缺陷是"没排除非订阅来源的节点", 加 `origin` 条件即可, 不必改语义。

## Consequences

- 订阅刷新/删除**不再**退场任何 `origin='residential'` 节点; 住宅出口的存活只由住宅会话决定。
- 订阅侧孤儿清理行为**逐字不变** (测试同时断言孤儿订阅节点仍被退场)。
- 遗留数据: 库里已有 9 个被误退场的住宅节点历史行, 保持原样 (它们确实已不是当前会话节点);
  当前活跃节点不受影响。
- 住宅渠道若真的失去全部节点, 现在会表现为"住宅服务自己没节点", 而不是"订阅刷新顺手删掉"。

## Testing

- `internal/store/residential_test.go::TestSubscriptionRefreshNeverRetiresResidentialNodes`
  走真实 `CommitParsedSubscriptionSnapshot` 路径, 同时断言: 住宅节点**存活** + 孤儿订阅节点
  **仍被退场**。**无修复时该用例失败** (已实测: 把 `origin <> 'residential'` 换成 `1 = 1`,
  用例报 `subscription refresh retired the residential node`), 修复后通过。
- `go test ./internal/store/ ./internal/subscription/ ./internal/residential/ ./internal/proxygroup/ ./internal/dataplane/mihomo/`
  全绿。
- 现场复验: `egress_binding.resolve_for(phase='pre_login')` 回 `ok:true`;
  mihomo `residential-linkup` 成员恢复为真实节点名。

## Code

- toolbox/hx-pxy/internal/store/nodes.go
- toolbox/hx-pxy/internal/store/subscriptions.go
- toolbox/hx-pxy/internal/store/residential_test.go
