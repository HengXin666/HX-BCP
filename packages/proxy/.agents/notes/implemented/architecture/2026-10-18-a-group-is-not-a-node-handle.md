# Agent Note: 单节点组在领域服务里直接拒绝, 并把正确用法写进错误里

Status: implemented

**2026-10-18 收紧**: 本文的「判据只看静态 `node_ids`」已由
`.agents/notes/implemented/architecture/2026-10-18-a-group-size-is-its-members.md`
改为「按解析后的成员数判」。下面关于单订阅、`allow_empty`、`limit=1` 仍可放行的段落
是**上一版**的记录, 不再是当前行为; 其余决策 (在 `normalize` 里判、`listener.node_id`、
`AllowSingleNode`、存量行可编辑) 仍然有效。

推翻
.agents/notes/implemented/architecture/2026-10-01-api-cannot-grow-the-group-graph.md
中「不在 `proxygroup.Service.Create` 里按成员数拒绝」的那条判断。该 note 的其余部分
(`guard.deny_api_group_create` 开关、`MachineCredential`、三个建组路径的闸门)仍然成立,
本 note 不动它们, 只在它们**下面**补一层内容判据。

## Problem

用户原话:

> 「修改一下，默认拒绝 size=1 的代理组创建，并且返回正确用法
> (代理组节点必须>1, 建议使用一个大节点组+不同节点进行锚点, 禁止创建大量代理组)」

> 「因为傻逼ai总是创建傻逼代理组」

同一个形状在仓库里有**三次**实测记录, 每次都清理干净, 每次都会长回来:

| 日期 | 代理组 | 其中单成员组 | 覆盖的不同节点 |
|---|---|---|---|
| 2026-09-26 | 61 | **57** | 28 (同一节点被包了 5 次) |
| 2026-10-01 | 12 | **6** (12 天内新建) | — (`香港专线04` 被包了两次) |
| 2026-10-18 | 6 | 0 (存量已收敛) | — |

2026-10-01 的结论是「闸门按**身份**判, 不按**内容**判」: 领域服务分不清「操作员想建一个
恰好只选一个节点的组」和「脚本把组当成指向一个节点的句柄」, 因为两者请求体可以逐字节相同。
于是加了 `guard.deny_api_group_create`(默认 false), 只拦 API Key 的三个建组端点。

**这个判断在生产里被证伪了两次。**

第一次: 闸门默认关闭, 于是它默认什么都没做 —— 一个需要操作员主动打开、而且打开后会打断
现存脚本的开关, 在「AI 每次会话都从零开始」的环境里等于不存在。

第二次更关键: 2026-10-11 用户报「乱码节点组」, 20 个 listener 里 13 个是上一个 Agent 会话
逐个 `POST /api/v1/listeners` 建的, 而代理组始终只有 5 个 —— 闸门一次都没响, 因为那次的病
不在建组。**但反过来同样成立**: 当调用方走的是浏览器会话、或者用 API Key 而开关没开时,
闸门对单节点组零作用。真正稳定不变的事实是**请求的形状**, 不是调用方的身份。

而「按身份判」还有一个更硬的成本: 它要求每个新调用方都知道并配置一个开关。用户要的是
**默认就拒绝**, 也就是把默认值反过来 —— 而这只能靠内容判据做到。

## Decision

**一、`proxygroup.Service.Create` 与 `Service.Update` 在 `normalize` 之后拒绝
「只含一个节点」的组, 返回 `ErrInvalid`(HTTP 422 `validation_failed`)。**

判据落在 `normalize` 里, 因为那是**所有**调用方唯一的收敛点: HTTP、proxyservice、
quickstart、住宅渠道全部经过它。判据本身在 `internal/proxygroup/single_node_rule.go`:

```go
len(spec.NodeIDs) == 1 && len(spec.SubscriptionIDs) == 0 &&
len(spec.GroupIDs) == 0 && !spec.IncludeDirect && !spec.AllowEmpty
```

放在 `normalize` 的**去重之后**, 所以 `["n1","n1"]` 算一个节点, 而不是两个。

**二、错误消息必须带正确用法, 不能只说「非法」。**

用户明确要求「返回正确用法」。消息里写清三件事: 节点必须 > 1; 正确形态是「一个大节点组 +
用 `listener.node_id` 把服务钉到具体节点」; 禁止为每个节点批量建组。只报 `invalid source_spec`
会把调用方推去找另一个字段, 而它真正该做的是**不建这个组**。

**三、`listener.node_id` 是唯一被推荐的替代形态, 而 `proxy_service.create` 现在也接受它。**

本次实测发现: 能力目录一直声称 `proxy_service.create` 支持 `listener.node_id`, 但解码器会以
400 `invalid_request` 拒绝这个未知字段 —— 也就是说, 那条「正确用法」在**最常用的那个端点上是
做不到的**。只把错误消息写好、却让调用方必须再调一次 `POST /api/v1/listeners`, 等于把补救
变成两个请求加一次手工拼装。

所以 `ListenerCreateRequest` 增加 `node_id`, `Service.Create` 在它非空时走 `createPinned`:
**一个组都不建**, 直接把 listener 交给 listener 服务去解析锚组。同时:

- 与 `listener.proxy_group_id`(复用组)互斥, 两个都给是 422;
- 带上 name/strategy/source_spec/enabled/empty_behavior/dialer_proxy_group_id 是 422 而不是
  被忽略 —— 钉住的服务不参与选择, 那些字段没有效果;
- 失败时只清理 listener, 没有组可删; 锚组是 listener 服务自己解析的, 不由这里拥有。

**这条能力是本次才补的**, 因此 note 里所有「用 `listener.node_id`」的建议在
`listener.create`、`proxy_service.create`、`proxy_service.update` 三个端点上都是可执行的。
前端创建表单也新增了「单节点出口」模式(与「节点组」并列), 走的就是这条路径。

后端 `listener.normalize` 接受 `node_id` 并自动补家族锚组, 编译器在 `Compile` 里用
`nodeNameByID` 把控制面 ID 解析成 Mihomo 代理名, 前端编辑表单有「钉住的节点」选择器,
`.agents/skills/hx-proxy-admin/SKILL.md` 也已写明「指名单个节点用 `node_id`, 不要为它建一个
只有单个成员的组」。所以这条建议是可执行的, 不是又一次把用户支去别处绕弯子。

**四、豁免只有一条, 且不在 HTTP 契约里: `AllowSingleNode bool` (`json:"-"`)。**

它存在的原因是**有些组的成员由另一个聚合决定**, 不是操作员在挑节点:

- 住宅渠道 `ModePassthrough` 的池按设计只物化**一个**会话 (`materializePool(..., 1)`);
- sticky 渠道的池可以是空的, 但渠道被启用时仍要把当前池重新发布一次。

如果写成 JSON 字段, 调用方能发就会发, 规则立刻退化成建议 —— 这正是 `guard` 那篇 note 里
「字段没有效果就不该被接受」的同一条理由。所以它只对 Go 调用方可见, 使用点是
`internal/residential/{channel,service,client_allocation}.go` 四处, 每处都写明为什么。

**五、`Update` 也要判, 但留一条窄缝: 已经是一节点组的行仍可编辑。**

不判 `Update` 的话规则就是一个「两步建议」: 建一个两节点组, 再改成单节点。但完全按新规则
判 `Update` 会把存量行**冻死** —— 连改名、停用、换策略都做不到, 而停用正是收敛它的动作。
所以 `Update` 的豁免是 `sameSingleNode(candidate, stored)`: 成员集合与库里已存的单节点组
**逐字节相同**时才放行。从两节点改成一节点不满足它, 因此两步绕不过去。

**六、`quickstart.Create` 在注册/刷新订阅**之前**就拒绝 `node_ids` 只有一个。**
不这样做的话, 调用方会先看到订阅建好、刷新成功, 然后拿到一个来自两步之后的组服务错误 ——
回滚是对的, 但错误离原因太远。quickstart 的报错直接指向 `listener.node_id`。

**七、能力目录(`GET /api/v1/capabilities`)与前端表单同步这条规则。**
`proxy_group.create` / `proxy_service.create` / `quickstart.create` 的字段说明与 422 条目
都写明「必须 > 1 个节点」与补救方式; 创建表单在固定节点 < 2 时给出内联提示并禁用提交,
编辑表单只在「把多节点组缩成一个」时禁用(存量单节点组仍可保存)。

## Object model: a group and a node are different ids

这一节是本 note 后半部分的由来, 因为它比「文案不够清楚」更具体。

2026-10-18 实测: 把 `group-0d3cd1d5…` 放进 `source_spec.node_ids` 提交, 服务端回的是
**「代理组节点必须 > 1」** —— 也就是本条规则自己的消息。那条消息在回答一个**数量**问题,
而调用方犯的是**类型**错误; 它给出的补救(再加一个节点)会让请求更错。同一次实测里,
`sub-…` 放进 `node_ids` 得到同样的回答, 而 `node-does-not-exist` 也得到同样的回答 ——
**三种完全不同的错, 一个答案**。

原因是三条, 都在线上契约里:

1. 两种对象的 id 在 JSON 里都是不透明的 `"id"` 字符串, 光看值分不出来;
2. 字段名只差一个复数 `s`: `listener.node_id`(钉一个节点) 与 `source_spec.node_ids`
   (组内候选成员) 在同一个请求体里相邻出现, 语义却是「这个服务走那个节点」和
   「这个组在这些节点里挑」;
3. `GET /proxy-groups` 与 `GET /nodes` 都返回 `items[].id`, 一个刚读完前者的调用方
   手里握着的字符串, 看起来正是后者想要的。

**决定: 让 id 前缀承担类型, 并把类型错误作为类型错误报出来。**

- `internal/proxygroup/reference_kind.go` 从 `node-` / `group-` / `sub-` / `listener-`
  前缀读出对象种类, 在**任何成员检查之前**校验三个字段; 放错字段时返回 422, 消息点名
  「这是代理组 ID, 应该放 `group_ids`」以及「只想让一个服务走一个节点就别建组, 用
  `listener.node_id`」。
- 节点存在性检查**前移**到数量规则之前, 并点名是哪个 id 不存在。先决事实优先于形态判断:
  「这个 id 不是节点」和「这个组只有一个成员」是两个答案, 后者会把前者盖掉。
- `GET /api/v1/capabilities` 新增 `object_model` 段, 用四条把区别写在机器可读契约里
  (id 前缀、三个字段各收什么、单数 `node_id` 与复数 `node_ids` 的区别)。它只写一遍,
  不在每个端点下各抄一份 —— 四份副本必然讲出四个版本。

这不是把错误消息写长一点。**如果一条规则让调用方更分不清两个对象, 那它必须先教会调用方
区别, 再拒绝它**; 否则「默认拒绝」只是在原来的混乱上盖了一个更响的错。

## Alternatives considered

- **什么都不做, 继续靠闸门与 note。** 它最强的理由是**已经有两次清理证明这个形状可以事后
  收敛**, 而且闸门方案不误伤任何合法形态 —— 存量 6 个组里就有 2 节点组和单订阅组, 按数量判
  一定会碰到它们。否决理由: 同一个形状三次复发, 而三次的调用方都不是「不听话的人」, 是
  **不知道 `node_id` 存在、且建组比钉节点少写一个字段**的程序。只写在文档里的约束对机器
  调用方零成本, 这条已经被 2026-10-01 的 note 自己承认了。

- **复用已有的 `guard.deny_api_group_create`, 把它改成默认开启。** 它最强的理由是改动最小:
  一行默认值, 不需要动领域服务, 也不会误伤内部聚合。否决理由有两条: 其一, 它按**身份**判,
  浏览器会话和未开开关的 API Key 都拦不住, 而用户要的是「默认拒绝」; 其二, 它拦的是
  「建组」这个动作本身, 包括合法的「一个订阅一个组」, 正好和用户想要的「一个大组 + 锚点」
  相反 —— 把唯一的正确路径也堵上, 只会让调用方去找别的绕法。

- **在 API 层拒绝, 不动领域服务。** 它最强的理由是分层干净: 领域服务不该知道 HTTP 语义,
  而 API 层已经有一张建组路径表。否决理由: 路径表**会过期**, 这是 `guard.go` 自己写下的
  已知成本; 而这次要保证的是「任何调用方都绕不过」, 包括将来新增的端点与内部聚合。
  放在 `normalize` 里是唯一不需要维护清单的位置。

- **静默把单节点组改写成 listener 的 `node_id`。** 它最强的理由是用户根本不需要知道这个
  区别, 系统替他做对的事, 而且不会让调用方失败。否决理由: 这是**静默改写** —— 调用方拿到
  201、之后 `GET /proxy-groups` 却找不到它。本仓已经为「成功但对象不是你描述的那个」付过
  代价(`2026-09-29-proxy-service-update-target-and-listener-ownership.md`)。宁可直接拒绝,
  并且把替代方案写在错误里。

- **按成员数拒绝所有「成员 < 2」的组, 不设豁免。** 它最强的理由是判据最简单, 没有例外就
  没有解释成本。否决理由: 它会把住宅 passthrough 渠道(设计上就只有一个会话)、sticky 空池、
  `allow_empty` 的休眠组、以及 DIRECT-only 组一起打死 —— 这些都不是「把节点包成组」, 而是
  产品本身的形态。一个需要给四个内部调用方开洞的规则, 说明判据选错了粒度。

- **把字段改名成 `node_ids` / `group_ids` 之外的名字(例如 `members` / `anchor_node`)。**
  它最强的理由是命名本身就是这次的病根: 单复数差一个字母的两个字段承担了完全不同的语义,
  改掉就能从源头消除混淆。否决理由: 这是**线上契约的破坏性改名**, 而现有名字已经被
  前端、SDK、`.agents/skills/hx-proxy-admin/SKILL.md` 和若干脚本使用; 改名会让所有现存
  调用方在升级瞬间失败, 而失败点是「字段不存在」, 比类型错误更难诊断。本次改为**保留字段名
  并让错误与能力目录承担教学**, 代价是每个新调用方仍要读一次 `object_model`。

- **按「重复包装同一节点」判(时间维度)。** 它最强的理由是最贴近病根: 2026-09-26 的
  `香港专线04` 被包了两次, 这才是真正的浪费。否决理由: 它需要历史对照, 单次调用看不到;
  而且它允许第一个单节点组通过, 于是「每个节点各建一个」这个最常见的形态完全不受约束。

- **只拦 `NodeIDs`, 不看 `IncludeDirect` / `AllowEmpty` / `SubscriptionIDs`。** 这是上一版
  采用的实现, 2026-10-18 收紧后已不成立。它最强的理由是动态快照: 一个订阅今天有 1305 个
  节点、明天可能只有 1 个, 按快照拒绝会让一次改名因为调用方无法在本次请求里修的事实而
  失败。否决理由: 实测中「单订阅当前只有 1 个节点」「`limit=1`」「筛选后只剩 1 个」都是
  **请求本身**只选一个成员的形状, 不是快照缩水; 放行它们等于规则只拦一种 JSON 拼写。

## Consequences

**新增行为**: 单节点组创建与「缩成一个节点」的更新返回 422, 消息含 `代理组节点必须 > 1`、
`listener.node_id` 与「禁止批量建组」。`quickstart` 的单节点请求在注册订阅之前就返回 422。

**豁免与兼容边界**:

- 住宅渠道的四个写点显式传 `AllowSingleNode: true`, 行为不变;
- 存量的单节点组仍可改名/停用/换策略(成员集合不变时), 因此收敛路径不被堵死;
- 单组组合、DIRECT-only、0 个成员的 `allow_empty` 休眠组不受影响。**单订阅与
  `allow_empty`+单节点不再豁免**: 它们当前解析出的成员数是 1, 现在由
  `2026-10-18-a-group-size-is-its-members.md` 拒绝。多节点订阅组
  (`良心云` / `proxyscrape-pool` / `shadowmere` / `doulor-gh-pool`)、2 节点的
  `residential-linkup`、3 订阅的 `residential-WsPxy-colo` 仍然不受影响。

**实测验证**:

- `internal/proxygroup/single_node_rule_test.go`: 拒绝单节点、拒绝 `["n1","n1"]` 去重后仍
  只有一个、拒绝两节点缩成一个、放行两节点/节点+DIRECT/DIRECT-only/0 成员的 `allow_empty`、
  放行 `AllowSingleNode`、放行存量单节点组的改名停用。单订阅与 `allow_empty`+单节点的拒绝
  见 `2026-10-18-a-group-size-is-its-members.md`。
- `internal/api/proxy_group_single_node_test.go`: 走真实 store + 真实领域服务的 HTTP 路径,
  断言 422 `validation_failed`、消息含补救方式、且**被拒绝的请求没有留下任何组行**。
- `internal/proxyservice/reuse_test.go`: `proxy_service.create` 在单节点来源上返回
  `ErrCreateFailed`、错误含 `listener.node_id`、且没有发布 listener、没有写组。
- `internal/quickstart/service_test.go`: 单 `node_id` 在注册订阅之前被拒。
- 全量 `go test ./...` 通过; 前端 `npm run check` 与 `npm run build` 通过。

**已知边界**:

- 判据只看**静态** `node_ids` 的版本已被收紧为「解析后的成员数」。当前行为见
  `2026-10-18-a-group-size-is-its-members.md`: 单订阅快照只有一个节点、`limit=1`、筛选项
  只剩一个、`allow_empty`+一个节点都会被拒; 创建**之后**快照缩水成 1 个仍是状态变化,
  不在创建期拦截。
- 存量单节点组不会自动清理。它们的收敛仍然要靠把 listener 改成 `node_id` 再删组
  (顺序不能倒: `listeners.proxy_group_id` 是 `ON DELETE RESTRICT`)。
- 钉住的服务仍然在行上引用一个**锚组**: `listeners.proxy_group_id` 是 NOT NULL 外键, 编译器
  也要求那个名字存在。这个锚组不承载钉住服务的流量, 所以「不建组」指的是不建**属于这个服务的
  组**; 库里仍然需要一个已启用的组作为锚, 否则 listener 服务会拒绝
  (`create and enable at least one proxy group before naming a node directly`)。这是既有语义,
  本次没有改动, 但把它写下来, 免得下一个人以为「pinned 不需要任何组」而把锚组删掉。
- 前端现在也在「跨订阅规则」模式下拦截预计命中 1 个节点的情况 (含 `limit=1` 与筛选后只剩
  一个), 提示改用 `listener.node_id`; 服务端才是最终判据, 前端只是提前说明。
