# Agent Note: listener 可以直接钉一个节点

Status: implemented

## Problem

用户原话:
> 「那你就直接给服务端提供这种能力啊，就是能直接指定使用哪一个节点这种模式啊，
> 而不是像你说的这种又要弯子，又要搞这么麻烦，显然就是加个能力就能做了。」
> 「代理组只需要有个位数...最多是一个订阅属于一个代理组，这是没问题的」

实测生产库 (收敛前) 的形态:

| 项 | 数量 |
|---|---|
| 代理组 | 61，其中 **57 个是 `strategy=manual` 的单节点组** |
| 单节点组指向的不同节点 | 只有 **28** 个 (同一节点最多被建了 5 次组) |
| listener | 78 |

链路是: `listener.ProxyGroupID` -> 组 -> 组成员。**listener 只能指向「组」**,
所以「让这个服务从那个节点出网」被迫要先为那个节点建一个只有一个成员的组。
组在这里被当成了「指向一个节点」的句柄。

服务端其实**早有完整的节点解析能力** (`proxygroup.SourceSpec.NodeIDs` /
`NameKeywords` / `Regions` / `MaxLatencyMS` / `Limit` / `SortBy`), 只是 listener 这条路径用不上。

**关键前提 (实测确认, 决定了整个方案可行)**: Mihomo 的规则目标可以是 `proxies` 里的**节点名**,
不必须是 `proxy-groups` 的组名。用本机 mihomo 1.19.30 实测:

- 正例: `AND,((IN-NAME,<listener>),(IN-USER,u1)),hx-node-<fp16>` -> `test is successful`
- 负对照: 目标名不存在 -> `error: proxy [hx-node-does-not-exist] not found`, 配置**整份被拒**

负对照是关键: 它证明规则目标确实按 proxy 名解析, 而不是被静默忽略成直连。
本仓也已有先例 —— `compiler_residential.go` 早就在用节点名做规则目标 (住宅 per-session 路由),
所以这不是新机制。

## Decision

**一、listener 增加 `node_id`, 与 `proxy_group_id` 二选一, `node_id` 优先。**
只给 `node_id` 时, 行上那个 NOT NULL 的组会被自动填成家族的锚组
(`egressAnchorGroup`: 第一个启用的组, 按 id 取, 保证稳定) —— 该锚组**不承载这个服务的流量**,
它只是为了满足列约束与聚合行的回落语义。若要求调用方二选一必须同时给组, 就等于没去掉那个弯子。

**二、`node_id` 存的是控制面节点 ID, 编译器在编译期把它解析成 Mihomo 代理名。**
代理名是 `hx-node-` + fingerprint[:16] (`nodeProxyName`)。**绝不能把 ID 当名字塞进规则** ——
名字是提交进 `proxies` 的那个字符串, ID 只在控制面存在。`Compile` 在装配 `proxies` 的同一次循环里
填 `nodeNameByID`, 规则生成只读这张表, 因此两者不可能漂移。

**三、节点不存在/被停用时, 钉住它的那条 listener 从文档里摘除, 其余配置照常发布。**
接口边界 (`internal/listener/service.go:425-435`, `normalize`) 已经拒绝 missing/disabled/retired 的
pin, 所以编译期只可能遇到「创建之后才失效」的那一个 pin —— 订阅刷新把节点退休, 而 pin 留在行上。
这种行是**一条死数据**, 不是一份坏文档: 为它拒绝整份配置, 代价是其余 listener、以及每一个
`Apply` 调用方 (包括每 60s 跑一次的节点体检) 一起失败, 而且不会自愈 (2026-10-28 实测:
probe-resi-01..04 的四个 pin 指向被订阅刷新退休的节点, 12 天里刷出 5,389 行这类失败)。
摘除的 listener 不进 `listeners`、不占端口、不产生 client route/rule, 经
`WithheldEntry{Reason: WithheldPinnedNodeUnavailable}` 上报, `Manager.Apply` 每次应用打**一条**
Warn 列出被摘除的 id 与原因。**绝不回落到它行上的组**: 钉住一个出口却到不了, 就不能改成发布
另一个出口 —— 那样调用方拿到的仍是 200 和一个能用的出口, 只是出口错了。

**四、版本 38 用 `ALTER TABLE ADD COLUMN`, 不用 33 号迁移的 create/copy/drop/rename。**
本表在线上库里的唯一索引有**可证的重复项** (`listeners_dedicated_endpoint` 与
`listeners_share_token` 各出现两次), 按重建写法重放会失败。`ADD COLUMN` 不触碰既有列与索引。

**五、该列必须可空, 不能用 NOT NULL DEFAULT '' 表示「没有钉节点」。**
这是本次实测出来的坑, 见下。

### 实测出来的失败模式: 空串不等于「无引用」

SQLite 的**复合*外键在外键列含 NULL 时整个约束判定为真**, 而空串 `''` 会被当成一个**真实值**
去比对, 找不到就报错。本机 sqlite3 两种行为都跑了:

```
-- FK 列写 '' :
INSERT INTO l VALUES('a','',NULL);
  -> Error: FOREIGN KEY constraint failed

-- FK 列写 NULL :
INSERT INTO l VALUES('a',NULL,NULL);
  -> ok
```

所以 `node_id` 存 NULL 表示未钉, 写入路径统一用 `NULLIF(?, '')`, 读取路径统一用
`COALESCE(node_id, '')`。**任何「用空串表示无引用」的写法在这张表上都是隐雷** ——
`listeners.proxy_group_id` 今天没炸只是因为它 NOT NULL 且永远有值。

## Alternatives considered

- **什么都不做, 继续为每个节点建一个单成员组。** 它最强的理由是**零改动**: 现有调用方、
  导出、日志归属 (`logs.go` 的 groupIdentity) 全都不动, 兼容性是满分。
  否决理由: 组数量与节点数量线性增长, 实测已经涨到 61 组/28 节点, 且「哪个组是活的」无法回答;
  用户的原始诉求正是要个位数的组。这是**形态**问题, 不是数量问题。
- **让 listener 直接指向组, 但组的成员只有一个 (即现状), 只是删掉重复的组。** 否决: 不改变形态,
  下次还会长回来。
- **listener 加 `node_ids` (复数) 而不是 `node_id`。** 否决: 多节点就是「一个组」的语义,
  现有 `proxy_group_id` 已经完整表达它, 加一份重复表达会让两条路径长期分叉。
  单节点是唯一无法被组表达的形态, 所以只加单数。
- **规则目标用节点的控制面 ID 而不是名字。** 否决: `proxies` 里只有名字, 用 ID 会得到
  `proxy [...] not found` 并**整份配置被拒** (即数据面全挂)。
- **让 `node_id` 与 `proxy_group_id` 必须同时提供, 由服务端校验两者一致。** 否决: 这正是
  用户要消灭的弯子; 而且「组」与「节点」在语义上并非同一层, 强求一致没有可判定的含义。
- **节点消失时回落到组。** 它最强的理由是**不丢服务**: 端口继续开着, 调用方不会被 502 打脸,
  而且实现只是把 `egressName` 留在组名上。否决: 会静默把流量送到**另一个出口**。对「注册全程
  同 IP」这类硬需求, 静默换出口比不服务危险得多 —— 故障是可观测的, 换出口不是。
- **节点消失时让整份配置失败 (本次改掉的旧行为)。** 它最强的理由是**绝不发布半份文档**:
  Mihomo 不接受指向不存在 proxy 的规则, 失败即说明数据面确实没法完整提供服务。
  否决: 整份失败把**一条**死数据的代价放大到**全部** listener 和每个 `Apply` 调用方,
  而且它不会自愈 —— 没人改那一行, 每次重试都以同样方式失败 (12 天 5,389 行)。

## Consequences

**新增能力**: `POST/PUT /api/v1/listeners` 接受 `node_id`, 单独给即可, 不需要组。

**后续 (2026-10-01)**: 本能力**已经存在却仍被绕过** —— `gh-registrar/proto/watch_t.py` 继续为每个
节点建单成员组, 12 天内又积累了 6 个。原因与对策 (机机凭据不许建组的闸门) 见
`implemented/architecture/2026-10-01-api-cannot-grow-the-group-graph.md`。那篇同时修掉了该调用方。
所以本 note 描述的是**能力**, 那篇描述的是**约束**; 两者必须一起读, 否则会得出"能力已有 ⇒ 问题已解"
的错误结论。

**实机端到端实测** (服务经 HX-WebX 重启后):

- 只带 `node_id` 建 listener -> `201`, 返回体里没有组语义; 生成规则为
  `AND,((IN-NAME,hx-in-shared-standard-mixed),(IN-USER,lcs-tw01)),hx-node-396543982fe168ca` ——
  **目标落在 `proxies` 的节点名上, 不是任何组名**。
- **差分测试 (决定性)**: 同一端口 7890、同一份文档, 两个只差 `node_id` 的 listener:
  `node-c1b1…9299`(台湾) -> 出口 `211.20.18.216`;
  `node-dd40…e399`(香港) -> 出口 `209.9.200.90`。
  换个 `node_id` 就换个出口, 且都真实连通 —— 这就是「钉住了指定节点」的判据。
- 同一端口上既有的未钉 listener 不受影响(`ghreg-us03` -> `23.237.50.28`, `ghreg-hk02` -> `42.200.176.57`)。
- **创建后自动 reconcile, 不需要重启**: `Create` 触发 `reconciler.Apply`, 7890 在创建前已停、
  创建后立即在监听。这一点经实测确认 (对照组: 手动重启后才恢复监听)。

**坏 pin 的代价被限制在那一条 listener 上**: 创建期的 `normalize` 仍会拒绝 missing/disabled/retired
的 pin (实测过一次: 中间版本里节点名映射没填进 `Compile`, 创建即失败并回滚了已建的行);
编译期碰上「创建后才失效」的 pin 时只摘除那一条, 其余文档照常发布。
两处判定因此必须**语义一致**: 创建期是拒绝 (调用方就在现场, 可以立刻改), 编译期是摘除
(能改这行的人不在现场, 而整份文档的可用性必须优先)。
摘除结果随每次编译重算, 所以节点回来或 pin 被改掉之后, 那条 listener 会自己回到文档里。

**依赖数据流的纪律**: 节点名映射必须是 `Compile` 里的局部变量一路**显式传参**
(`compileSharedInbounds` 的 `nodeNameByID` 形参), 不能放在 `Compiler` 结构体上由 `Compile` 顺手赋值 ——
后者在改动中很容易漏掉赋值, 而漏掉的后果是「所有 pin 全部变成 missing node」这种大面积误报。

**同一个「只解析一次」的模式**: 住宅链路的环检测 (`validateResidentialDialerChains`) 原本在
自己文件里复制了一份成员推导, 本次改为调用编译器的 `groupNodeIDs`。两处独立推导必然漂移,
而环检测走错图时**恰好在该报错时通过**。

**会被 pin 改变行为的既有路径**: 钉住节点的 listener 不再依赖 `proxy_group_id` 指向的组存在,
因此那些组可以被停用/删除而不影响它; 未钉的 listener 行为完全不变 (老契约保留)。

**未覆盖 / 待后人验证**:

- 只有单节点 pin, 没有「多个节点按优先级」的表达 —— 那仍属于组的职责。
- 被摘除的 listener 只在 `Manager.Apply` 的日志里可见; API 的 listener 列表仍按行返回它,
  不区分「在服务」与「被摘除」。Docker 伴生 entry 那条路径有 `DockerAccessWithheld` 之类的显式
  字段, pin 这条还没有 —— 要让调用方看见同一个事实, 得再开一个只读投影, 本次没做。
- 摘除只针对 pin。未钉 listener 的组失效、端点重复、WebSocket 路由重复、依赖环仍旧是致命错误。
