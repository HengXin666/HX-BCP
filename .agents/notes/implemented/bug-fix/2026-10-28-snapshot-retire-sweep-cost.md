# Agent Note: 订阅刷新不得按节点逐个扫全表 (退场清扫的索引与快照保留)

Status: implemented

Decision-ID: snapshot-retire-sweep-cost

## Problem

2026-10-28 用户报 "hx-pxyd 为什么 CPU 占用有时候会这么高"。实测 (生产库副本, 单进程
`CommitParsedSubscriptionSnapshot`, 2199 节点的订阅):

| 场景 | 耗时 | CPU |
|---|---|---|
| 现状提交 | 16.129s | 16.120s (100% 一个核) |
| 同一库加 `subscription_nodes(node_id)` 索引后提交 | 183ms | 同比例 |
| 只提交 1 个节点的订阅 | 15.965s | —— |
| 删掉退休快照的关联行后清扫 | 0.098s | —— |

第二、三行是判据: 耗时与本次提交的节点数**无关**, 它全部花在退场清扫上。清扫的语句是

```sql
UPDATE nodes SET lifecycle_state='retired', ...
WHERE lifecycle_state NOT IN ('disabled','retired') AND origin <> 'residential'
  AND NOT EXISTS (SELECT 1 FROM subscription_nodes sn
                  JOIN subscription_snapshots ss ON ss.id=sn.snapshot_id
                  WHERE sn.node_id = nodes.id AND ss.status='active')
```

它是**按节点**写的 ("还有没有 active 快照提到我"), 而 `subscription_nodes` 上只有
`subscription_nodes_subscription(subscription_id, node_id)` 与主键 `(snapshot_id, node_id)`,
两者都不以 `node_id` 打头。于是 `EXPLAIN QUERY PLAN` 是:

```text
SCAN nodes USING INDEX nodes_due_probe
CORRELATED SCALAR SUBQUERY 1:
  SCAN sn USING COVERING INDEX subscription_nodes_subscription
```

每个候选节点都把整张关联表扫一遍 —— O(节点数 x 关联行数)。当时 `nodes` 17,468 行,
`subscription_nodes` 84,632 行, 其中 **82,129 行 (97%) 属于已退休的快照**, 只有 2,503 行是
active。也就是说 97% 的扫描量是**永远不可能命中**的行。

## Decision

两件事一起做, 因为它们修的是同一个不变量的两端:

**一、给 `subscription_nodes` 加 `(node_id)` 索引 (迁移 42)。**
清扫是按 `node_id` 反查, 索引必须按这个顺序打头。加上之后同一个查询的规划变成
`SEARCH sn USING COVERING INDEX subscription_nodes_node (node_id=?)`, 每次反查 O(该节点的行数)。

**二、关掉"退休快照的关联行"这个恒不变量 (迁移 42 的 DELETE + 三个写入点就地清理)。**
`subscription_nodes` 的**每一个**读取方都把它们和 active 快照连起来
(`retireDisappearedNodesSQL`, `source_count`, `ListActiveNodeSources`,
`ListGroupNodeCandidates`, `nodeSourceClasses`), 所以退休快照的关联行没有任何可达语义。
保留它们只有一个作用: 让上面那条语句多扫 97% 的行。

写入点用同一份 `pruneInactiveSnapshotLinks` (`internal/store/nodes.go`), 在**关闭旧快照的
同一个事务里**删, 覆盖面是三条把快照变灰的路径:

- `retireActiveSnapshot` (用于 `CommitParsedSubscriptionSnapshot` 与
  `CommitSubscriptionSnapshot`);
- `ActivateParsedSubscriptionSnapshot` (not-modified / 同 hash 刷新走这条);
- `ActivateSubscriptionSnapshot`。

保留判据是"active **或** 本次即将激活的那个快照 id", 所以正在被写的那份关联行不会被自己删掉。

**边界: 这是保留策略, 不是生命周期策略。** 删的是关联行, 从不碰 `nodes.lifecycle_state`,
所以"节点还在 active 快照里"这件事的判定结果逐字不变 (测试同时断言节点没有被退场)。

## Alternatives considered

- **什么都不做, 只靠稍后重试。** 最强理由: 高 CPU 只出现在整点刷新前后约 2 分钟, 平时是 0,
  系统功能上没有坏。否决: 它**不是偶发**而是每次刷新都付 16s 满核, 且失败的刷新会按
  `refreshFailureBackoff` 更频繁地重试 —— 现场每天 350~1000 次 "apply failed" 就是这个放大器;
  用户看到的是"偶尔很高", 实际是"一直在付, 只是采样到的时候才看见"。
- **只加索引, 不删历史关联行。** 最强理由: 改动面最小, 只是 `CREATE INDEX`, 不改变任何数据。
  否决: 索引把 16s 降到 183ms, 但反查次数仍然是 17,468 次；而 97% 的行本来就不该在那 ——
  留着它们等于把"权重"当成了"历史必需"。两者都做才让这条语句的代价与库龄脱钩。
- **只删历史关联行, 不加索引。** 最强理由: 立刻见效 (6.4s -> 0.098s), 且不动 schema。
  否决: `WITHOUT ROWID` 表上剩下的查询仍然是全扫, 一旦某次刷新失败/重试导致多个 active
  快照并存, 或者库又长回来, 同一个问题会原地复发。schema 层面的缺失必须用 schema 修。
- **把清扫改写成"只处理本次订阅的节点" (按 subscription_id 收窄)。** 最强理由: 语义更精确,
  代价与库的总量彻底无关。否决: 这条语句的用途**正是**清理跨订阅的孤儿 (订阅被删、节点从
  所有快照消失), 收窄就漏掉它们; 而且这是上一次修住宅节点误退场时已经权衡过的同一个岔路
  (见 `2026-10-15-subscription-refresh-must-not-retire-residential.md`)。
- **给清扫加一个"每 N 分钟才跑一次"的节流。** 最强理由: 不动 schema, 一行条件即可。
  否决: 它把"必须及时退场的孤儿"变成"最多晚 N 分钟退场", 用一个正确性上的让步换性能;
  而真实原因是查询没有索引, 不是调用太频繁。

## Consequences

- 一次 2199 节点的订阅刷新从 16.1s 满核降到 183ms; 迁移本身在 79MB 的生产库副本上耗时
  215ms (含索引创建 51.9ms 与 82,129 行删除)。
- `subscription_nodes` 从 84,632 行降到 2,503 行 (只保留 active 快照的关联行)。
- 迁移 42 在**首次启动**时做一次全量清理; 之后每个写入点只删自己这一份订阅的旧关联行,
  所以代价是 O(该订阅上一个快照的节点数)。
- 保留策略意味着: 想回看"某个历史快照当时包含哪些节点"已经不可能 —— 快照行
  (`subscription_snapshots`) 与归档文件 (`artifact_path`) 仍然保留, 关联关系不再保留。
  这是有意的取舍: 所有读取路径本来就只认 active 快照。
- 索引会让 `INSERT INTO subscription_nodes` 多维护一棵 B 树。实测这部分代价被
  "删除旧关联行"抵消 (提交仍然是 183ms 量级, 不是在 16s 上再叠加)。

## Testing

- `internal/store/snapshot_retention_test.go::TestSubscriptionNodesLookupIndexExists`
  断言 `subscription_nodes_node` 索引存在 (清扫的规划依赖它)。
- `::TestRefreshingASubscriptionDropsRetiredSnapshotLinks` 走真实
  `CommitParsedSubscriptionSnapshot` 两次, 断言 retired 快照的关联行为 0、active 快照仍为 1,
  且节点**没有**被退场。**无修复时该用例失败** (实测: 把 prune 换成空操作, 用例报
  `retired snapshot still holds 1 node links, want 0`)。
- `::TestActivatingASnapshotDropsRetiredSnapshotLinks` 覆盖 `ActivateParsedSubscriptionSnapshot`
  这条写入点, 含一条候选状态快照的关联行也必须被清掉。
- `go test ./internal/store/` 全绿。

## Code

- `internal/store/nodes.go`
