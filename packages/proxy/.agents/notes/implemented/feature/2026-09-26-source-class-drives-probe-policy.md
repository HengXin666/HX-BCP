# Agent Note: 节点失败策略按订阅源的显式分类 (source_class) 取值, 不再一条判据套所有源

Status: implemented

## Problem

用户的原话:「你连场景都没有办法分类区分吗?」「这只针对于 proxy-pool 并且是明确的状态, 你其他的东西怎么能这么做呢?」「你怎么能把好端多的代理给他删了呢? 代理根本就没几个节点, 你出几个错, 你把他删了干嘛?」

服务端对**所有源**用同一个失败判据, 写死在 `internal/store/node_quality.go`:

```sql
WHEN consecutive_probe_failures + 1 >= 3 THEN 'quarantined'
```

这个 3 一视同仁, 但三类源的可替代性根本不同。生产库实测 (改动前):

| 源 | 节点数 | 状态 | 连续失败 |
|---|---|---|---|
| ioa-vm | 1 | quarantined | 248 |
| outlook-ip | 1 | quarantined | 249 |
| outlook-resi | 1 | quarantined | 249 |
| 良心云 | 41 | 12 healthy / 28 quarantined / 1 degraded | — |
| resi-pool-5k | 5000 | 4934 candidate / 66 degraded | — |

两个各自可实测的缺陷:

**缺陷一: 唯一的出口被判死。** ioa-vm / outlook-ip / outlook-resi 各只有 1 个节点, 各自是「整源只有这一条路」。抖动 3 次就被 quarantine, 而 quarantine 会让 `proxygroup.SupportedNodeStates` 过滤掉它 —— 也就是整个源从此选不出任何节点。

**缺陷二: 弃了还在无退避地重试。** 三个被隔离的节点仍在被反复探测, 失败计数一路涨到 248/249。实测失败不是一次瞬断被重复计入: ioa-vm 的 254 次检查横跨 2026-09-19T17:59 至 09-26T14:15 共 7 天, 每小时约 5-6 次, 与 600 秒的 `CheckIntervalSeconds` 吻合。`DueNodeIDs` 只按 `last_checked_at <= before` 选, 对已判定死亡的节点没有任何退避或停止条件。

**实测这 3 个节点现在到底通不通** (改动前, 用 `POST /api/v1/nodes/<id>/check`):

| 节点 | pcp 探测 | 独立证据 |
|---|---|---|
| ioa-vm (socks5 → 192.168.250.93:11081) | 503 失败 | 主机 ping 通, 但 TCP 11081 拒绝; 是内网 VM 上的 socks5, 服务没在监听 |
| outlook-ip (vless → 172.67.218.177:443, ws path 指向某个 residential channel id) | 503 失败 | 该 channel id 在 `residential_channels` 里**不存在** |
| outlook-resi (vless → pxy.woa.qzz.io:443, 同一个失效 path) | 503 失败 | 同上 |

对照组: 同一时刻探测一个 healthy 节点返回 `success=true` latency=365ms, 说明探测链路本身正常。三个节点**确实不通**, 不是「历史失败堆积」。mihomo 自己的判断也是 `alive:false`。

所以处置是: **保持隔离但停止无效重试** (任务书给的两条路里的第二条), 而不是重置为 healthy。

## Decision

**给订阅源加显式分类字段 `subscriptions.source_class`, 三类各自的失败策略不同。**

迁移 39 `source_class_drives_probe_policy`。回填**按 active 快照的 node_count** —— 因为它就是「可替代性」的物理含义:

```sql
1..4   条 -> residential
>= 100 条 -> pool
其余      -> subscription
```

生产库 5 个源的实际回填值及依据:

| 源 | active 快照 node_count | 回填 source_class | 依据 |
|---|---|---|---|
| 良心云 | 41 | subscription | 长期订阅资产, 掉了多半是临时抖动, 值得退避重试 |
| ioa-vm | 1 | residential | 唯一节点, 弃了整源就废 |
| outlook-ip | 1 | residential | 同上 |
| outlook-resi | 1 | residential | 同上 |
| resi-pool-5k | 5000 | pool | 海量、一次性、免费/额度会耗尽, 失败即弃 |

没有 active 快照的源拿到 `-1`, 两个条件都不命中, 落到 else 的 `subscription`。这是刻意选的保守方向: 把池成员误当订阅成员只多花几次探测, 把唯一出口误当池成员则会删掉运维唯一的路。`DefaultSourceClassPolicy()` 出于同样理由取 subscription, 而不是取更便宜的 pool。

迁移执行后, 运维按明确指令删除了 `resi-pool-5k` (实测那 5000 条 0 通: TCP 通、认证过、CONNECT 超时; 且配额耗尽的池子本就该删)。因此生产库现在只剩 4 个源, `pool` 类那一行不再存在。上表的回填值记录的是**迁移执行时**的真实结果, 迁移规则本身仍由 scratch 副本与单测覆盖 —— 不要因为生产库看不到 pool 行就以为回填逻辑未验证。

每类策略 (`internal/store/source_policy.go` 的 `ClassPolicies()`):

| | pool | subscription | residential |
|---|---|---|---|
| `QuarantineAfter` | 3 | 5 | 0 (永不) |
| `ProbeWhileQuarantined` | false | true | true |
| `RetireAfter` | 5 | 60 | 0 (永不) |
| `BackoffBase` | 15m | 10m | 5m |
| `BackoffCap` | 6h | 6h | 24h |

数值理由:

- **pool 3/5/停探**: 5000 条免费池的成员一直在换血, 连续失败 3 次就是死的, 下次刷新会带新的来。停止探测是重点 —— 继续探测一个已判死的池成员纯粹是浪费。
- **subscription 5/60/继续探**: 付费源的节点有钱和手工配置的价值, 1 小时的上游抖动不该让它出局。5 次 × 10 分钟 ≈ 50 分钟持续失败才离开路由集, 比典型的上游重启长、比运维愿意容忍的故障短。60 次 (约 10 小时) 才是「这条真没了」的点。
- **residential 永不/永不/永远探**: 这些节点不可替换。退避到 24h 上限后, 一个彻底死掉的住宅出口的探测成本从 144 次/天降到 1 次/天, 而恢复探测不能停 —— 那是唯一能发现它活回来的方式。
- **统一公式**: 第 N 次连续失败后的重试间隔 = `BackoffBase * 2^(N-1)`, 上限 `BackoffCap`。

退避落在 `nodes.next_probe_at`, 由 `DueNodeIDs` 做闸门, 而不是在探测循环里 sleep: 一个退避 6 小时的死节点不能占住 worker 或拖住整批。另加 `nodes.probe_paused` 表示「已判定耗尽, 别再探」—— 用节点级布尔而不是在 SQL 里重算分类策略, 是因为策略阈值只在 Go 里 (`ClassPolicies`), 在 `DueNodeIDs` 里复写一份「哪些分类隔离后还探」必然不同步; 本仓库已经栽过判据散落两处不同步的亏。

**分类是源的属性, 不是节点的属性**, 因为同一个端点串在一个源里是废品、在另一个源里是唯一的路。节点级策略取所属源分类的**逐维度并集** (`NodePolicyFor`), 而不是按分类优先级整取: `0` 在「0 表示永不」的约定下比任何有限值都更保护, 却数值最小, 所以合并阈值用的是 `moreProtectiveThreshold` 而不是 `max` —— 否则 pool 的「3 次隔离」会盖掉 residential 的「永不」, 共享节点会被退掉。

API: `CreateRequest`/`UpdateRequest`/`Subscription` 增加 `source_class`; `quickstart.Request` 增加 `source_class`; capabilities 目录新增 `source_class` 枚举与 `subscription.update` 端点。

**创建时空值取默认 (`subscription`) 而不是报错**: 管理 UI (`web/src/components/create-subscription-form.tsx`) 建订阅时不发这个字段, 而本次改动范围禁止碰 `web/`。硬性必填会在同一时刻打断所有既有调用方。默认值是**面向安全的那个** (subscription, 永不自动退役的唯一路径最短的类), 并且目录里明确写出这个默认。`quickstart.create` 没有这种历史调用方, 它改为**显式必填**。

## Alternatives considered

**什么都不做 (继续用 `>= 3`)**: 最强的理由是它最简单, 而且对 pool 类其实是对的 —— 三类里只有 pool 真能用「失败 3 次即弃」。否决: 代价不对称到不可接受。同一个阈值让一个 5000 条的池和一个只有 1 个节点的住宅源共用「失败 3 次判死」, 而后者判死等于整源失效, 且 quarantine 状态被 `SupportedNodeStates` 过滤后无法自动恢复。实测已经发生: 3 个唯一节点全部 quarantined, 失败计数 248/249。这是用户明确否定的现状。

**每个源一个可调的失败阈值 (在源上直接存 `quarantine_after`)**: 最强的理由是它最灵活, 能表达三类之外的源。否决: 用户已经明确「这只针对于 proxy-pool 并且是明确的状态」——他要的是**分类**, 不是给每个源多一个旋钮。逐源裸数值会让「这个源为什么永不退役」变成只能读数值猜, 而分类名自带语义 (`residential` 一眼就是「不可替代」)。而且逐源阈值不能解决「同一节点共享于多源」的并集问题, 那是分类才有的结构。

**自动推断分类 (按源名/协议/节点数在生产代码里猜)**: 最强的理由是零迁移成本, 存量行不用回填。否决: 用户问「你连场景都没有办法分类区分吗」的**答案必须是人给的显式分类**, 而且猜错的代价不对称 —— 把 residential 猜成 pool 会静默退役运维唯一的路。回填时用 node_count 是**一次性迁移的数据判据**, 不是运行时的持续推断: 迁移里用它是因为存量行必须有一个可辩护的来源, 且这个量正好就是分类定义的物理含义。

**把「是否继续探测」写成 SQL 里的 CASE (不新增 `probe_paused` 列)**: 最强的理由是少一列、少一处写入点。否决: 策略阈值住在 Go 的 `ClassPolicies()`, 在 `DueNodeIDs` 的 SQL 里复写一份「哪些分类隔离后还探」必然与它不同步。本仓库已经因为「同一事实的判据散落两处」栽过 (`cached()` 与 `verdict_of()` 的 stale 判据不一致)。

**重置那 3 个被误隔离的节点为 healthy**: 任务书列出的候选处置之一, 但前提是「实测通」。实测三个都不通 (见 Problem 的对照表), 所以走另一条: 保持隔离 + 停止无效重试。把它们重置成 healthy 只会让它们重新进入路由集并立刻拖慢选择策略。

## Consequences

- 三类源的失败行为从此不同, 且差异在代码里有名有据; 新增源必须显式选择 (quickstart) 或落到文档化的默认 (subscriptions)。
- 误隔离的住宅节点有了明确的修复路径: `PUT /api/v1/subscriptions/<id>` 改 `source_class` 为 `residential`, 再 `POST /api/v1/nodes/<id>/enable` (`SetNodeLifecycleState` 现在会一并清掉失败计数、`next_probe_at` 与 `probe_paused`, 因为手工 enable 是运维的策略覆盖)。分类只改未来行为, 不改过去状态。
- 被隔离的 pool 成员不再被探测, 探测负载下降; 死掉的住宅节点退避到 24h 后约每天 1 次。总探测量随死节点数**次线性**增长, 而不是现在的线性。
- 节点 API 新增两个字段 `next_probe_at` / `probe_paused`; capabilities 目录新增 `source_class` 枚举与 `subscription.update` 端点。目录只增不改不删, 不构成破坏性变更。
- **本次未验证**: 三类策略在生产库上的真实长期行为 (只做了单测与 scratch 库回填实测); 退避在重启后的持续正确性 (依赖 `next_probe_at` 落盘, 已单测但未跨真实重启观测)。生产库的 3 个节点按实测结论保持隔离且已停止无效重试。
- **已测**: 分类生效后的三类行为在 live 库对照实测 (pool 在 3 次失败 quarantine+paused、5 次 retired; residential 即使 253 次失败仍不 quarantine 不 paused 且退避到 24h 上限); `probe_paused` 确实在 due 集闸门生效 (强制 `last_checked_at=2020` 后, 未暂停的控制节点被调度器探测, 已暂停节点完全未被探测); `POST /nodes/<id>/enable` 会清空失败计数、`next_probe_at` 与 `probe_paused`。
- **暴露的相邻缺口 (不在本次范围)**: 服务端**没有 audit log**。正是靠"两次直读同一库"才定位到 `resi-pool-5k` 的消失时间窗, 而不是靠任何服务端记录。删订阅会连带 retire 该源的全部节点 (生产库一次 5000 个), 这种破坏性操作在当前实现里不留任何操作者/时间/来源记录 —— 排查只能靠外部时间线取证。这值得单独立项, 但不应塞进本次改动。
- 未触碰 `session_index` (会话状态不落服务端是另一件事)、`web/`、`cmd/`、轮转/共享入口/Listener 逻辑。

## Verification

- `go build ./...` / `go vet ./...` 干净。
- `go test ./internal/... -count=1` 31 个包全过。
- 迁移 39 在**生产库的 scratch 副本**上实测: ALTER + 回填 + 新列 + 索引全部成功, `PRAGMA integrity_check` = ok, 5 个源回填值与上表一致。
- `node scripts/verify-consumer-contract.ts` / `node scripts/verify-admin-catalog.ts` 通过。
- `node .agents/skills/hx-agent-notes/scripts/verify-all.ts` 通过。
