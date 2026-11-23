# Agent Note: 新建代理服务可以复用一个已存在的代理组, 住宅渠道因此有了入口

Status: implemented

## Problem

用户原话:

> 「6. 新建代理组 没有住宅代理的入口」
> 「7. 新建新建代理组 无法凭证住宅代理」

已确认的期望形态: 在「新建代理服务」的 **节点来源** 里增加「住宅代理」, 选中一个**已存在的住宅渠道**,
提交后生成一个新的服务/入口, 复用住宅页已经建好的渠道 (provider + channel)。

三种既有来源 (服务器直连 / 跨订阅规则 / 固定节点) 都在回答"成员从哪来", 而住宅渠道不是一份来源快照:
建渠道时控制面就一并建好了它的代理组 (`residential-<渠道名>`), 成员是渠道按会话声明出来的住宅节点,
并随会话分配与轮换变化。于是一个操作员想要第二个入口时只有两条路 —— 为同一批出口**再建一个组**
(正是 `.agents/notes/implemented/architecture/2026-10-01-api-cannot-grow-the-group-graph.md` 要止住的"重复地为同一目的建组"),
或者再建一个渠道 (等于复制一份供应商会话与凭据, 不是他要的东西)。

住宅频道创建的组是普通代理组；该事实由 internal/residential 的组创建路径及复用服务测试约束，不依赖具体部署的对象 ID。

## Decision

**一、`POST /api/v1/proxy-services` 的 `listener.proxy_group_id` 表示"复用这个已存在的组"。**

字段放在 listener 里而不是顶层 `group_id`: 它与 `listener.CreateRequest.proxy_group_id` 语义完全相同 (一个 listener 的出口锚点),
而顶层 `group_id` 在兄弟端点 `UpdateRequest` 里已经表示"正在被更新的那个服务"。同一个名字承担两个相反的角色
(要写的对象 vs 被复用的出口) 正是本仓栽过的坑。

**二、复用时不再建组, 也不清理别人的组。**

`proxyservice.Create` 在 `listener.proxy_group_id` 非空时跳过 `groups.Create`, 直接建入口; 入口创建失败或共享入口
发布失败时**不删除**该组 (普通创建路径会删掉它刚建的那个)。那个组属于住宅渠道, 删它等于因为一个新入口
发布失败而拆掉一条既有渠道的出口 —— 而且 listener 服务本来也会拒绝这个删除 (新成员行已经引用了它)。

**三、校验落在服务端, 错误是 422 而不是 500。**

- 组不存在 / 组已停用 → `proxygroup.ErrInvalid` → HTTP 422 `validation_failed`, 消息指名 group id。
- 读取组本身的存储故障**不**伪装成校验错误 (原样上抛), 否则一个坏掉的库会永远回答 422。
- 一个"既复用又描述"的请求 (同时带 `name` / `strategy` / `source_spec` / `enabled` / `empty_behavior` /
  `dialer_proxy_group_id`) → 422, 消息列出违规字段。**拒绝而不是忽略**: 依据是
  `.agents/notes/implemented/simplification/2026-10-01-service-shape-follows-source-class.md` 那条"隐藏与不下发是同一件事" ——
  安静地忽略 `source_spec` 会让调用方以为自己建了它描述的那个组。

**四、前端在 节点来源 增加第四个 chip「住宅代理」。**

选中后列出全部住宅渠道 (名称 / 地区 / 会话 / 可用性), 单选; 渠道为空给出明确空态; **停用的渠道列出但不可选**
(它的组在数据面同样停用, 服务端也会 422 —— 前后端用的是同一条判据 `enabled`, 前端不另立更严或更松的一套)。
提交时只下发 `listener` 与 `proxy_group_id`, 不下发 `name`/`strategy`/`source_spec`; 出站策略选择器对住宅来源
**既不显示也不下发** (策略属于那个组, 一个改不了的选择器只会骗人)。

**五、渠道不是订阅, 所以不走「跨订阅规则」那条路。**

渠道没有 URL、没有节点快照, 它的身份是 provider + channel + 会话; 复用订阅那条路要求先把渠道拍平成一份
由订阅快照拥有的节点列表, 于是"这个服务走哪些出口"就有了两个事实来源。

## Alternatives considered

- **什么都不做 (或在住宅页手工再建一条渠道)。** 最强理由: 出口链路今天已经可用, 住宅页能建渠道, 零代码零风险;
  而且底层能力其实"已经存在" —— `POST /api/v1/listeners` 一直可以带 `proxy_group_id` 指向任何启用的组, 这也是机器调用方
  今天的现成路径。否决: 用户报的缺陷就是"界面里没有这个入口", 而操作员的真实意图是"给这条住宅出口再加一个入口";
  让他复制一条渠道等于复制一份供应商会话与凭据, 不是同一件事。
- **把住宅渠道做成一个订阅源 (`source_class=residential`), 复用现成的「跨订阅规则」chip。** 最强理由: 不必新增 chip,
  `source_class` 里本来就有 `residential`, 服务形态表也已经认这个类别。否决: 渠道不是快照。伪装成订阅就要把渠道当前的
  会话节点复制成一份由订阅快照拥有的列表, 而渠道自己会随分配/轮换改组成员 —— "这个服务走哪些出口"从此有两个事实来源。
  用户要的是选**一个渠道**, 不是选一份被拍平的节点清单。
- **用顶层 `group_id` 而不是 `listener.proxy_group_id`。** 最强理由: 复用是"服务级别"的决定, 放在顶层读起来更像服务
  的形状, 也不必在 listener 子对象里加一个只在创建时有意义的字段。否决: `UpdateRequest.group_id` 已经表示"正在被更新的
  服务", 同名不同角色是定位错误的温床; 而 listener 的 `proxy_group_id` 在整个 listener API 里语义一致, 复用请求只是
  把同一个字段从"由服务端填"改成"由调用方填"。
- **新增端点 `POST /api/v1/proxy-services/from-group`。** 最强理由: 契约最清楚, 不必让既有 create 拥有两种互斥形状。
  否决: 同一个资源 (服务 = 组 + 入口) 会有两条创建路径, 校验、共享入口发布与失败清理都要各写一份, 而它们必然漂移;
  既有端点本来就有一个"哪个组"的位置。
- **复用时忽略多余的 name/strategy/source_spec, 只回一个 warning。** 最强理由: 更宽容, 把一个完整表单原样发过来的调用方
  也能成功。否决: 这正是 2026-10-01 那条纪律的形态 —— 没有效果的字段被安静接受, 调用方就会以为它生效了;
  他会一直以为那个 `source_spec` 是那个服务的策略, 直到某天发现入口走的是别人定义的出口。
- **顺带放宽 `guard.deny_api_group_create`, 让 API Key 也能走复用路径。** 最强理由: 复用根本不建组, 按路径拦它拦掉了
  一个它本来不针对的操作; 而且那条 note 的字面理由 ("`POST /api/v1/proxy-services` ... 第一步都是建组") 在复用之后不再对每个
  请求成立。否决: 闸门的判据是**谁在调用**, 不是请求体写了什么, 这一点它在 2026-10-01 明确采纳并为此否决过按内容判定;
  让闸门去读 body 判断"这次会不会建组"就是重新引入那条被否决的设计。机器调用方另有现成路径 (`POST /api/v1/listeners`
  带 `proxy_group_id` 不被闸门拦, 那条 note 的"已知边界"里写明这是刻意的)。

## Consequences

**新增能力**: `POST /api/v1/proxy-services` 接受 `listener.proxy_group_id`, 把一个新入口挂到一个已存在且启用的组上,
不再为它建组; 「新建代理服务」的 节点来源 多了「住宅代理」, 单选一个住宅渠道即可。

**行为变化 (需要操作员知道)**

- 复用请求里出现任何组创建字段都会被 422 拒绝 (消息列出字段名), 而不是被忽略。
- 复用路径在入口创建失败或共享入口发布失败时**不做**回滚: 组和已建的 listener 行都留着, 由操作员删掉那个服务行。
  普通路径仍然回滚它刚建的组。
- 删除这个服务**不会**动那个组: 渠道自己的入口 listener 还指向它, 前端 `isSoleOwnerOfGroup` 因此判为共享
  (见 `.agents/notes/implemented/bug-fix/2026-10-01-delete-service-must-not-delete-shared-group.md`)。
- `guard.deny_api_group_create` 开启时, API Key 调 `POST /api/v1/proxy-services` 仍然一律 403, **包括复用请求**;
  这是刻意的, 闸门按路径 + 身份判定, 不读请求体。

**代价与风险**

- **新入口在服务行上的形态徽章是「固定节点组」而不是「住宅出口」。** 形态由被复用组的 `source_spec` 派生
  (`residential-linkup` 的 spec 只有 `node_ids`, 实测 1 个成员 → `manual`); 形态判据只有一处
  `web/src/lib/service-shape.ts` (由 2026-10-01 那条 note 管), 本次没有动它 —— 要把"这个组来自哪个住宅渠道"接进
  形态推导是另一条决策。这是已确认的现状, 不是未知。
- UI 只按 `enabled` 过滤渠道 (与服务端同一条判据), 于是一个 enabled 但零出口的渠道仍可选 —— 它会在编译时
  fail-closed REJECT。列表把出口数与会话数显示出来并标黄, 但**不**把它变成第二个闸门。
- 服务端不检查"这个组是不是住宅渠道的": 复用能力是通用的 (任何启用的组), 只有界面把它收窄到住宅渠道。
  机器调用方可以复用一个普通组 —— 这是既有能力的自然延伸, 不是新增的开口。
- 一个渠道可以挂多个入口 (本功能), 也可以完全不挂 (住宅页自己的入口), 两者都合法。

## Testing

- `go test ./internal/proxyservice/`: `TestCreateReusesAnExistingGroupWithoutCreatingOne` (不建组、入口绑到被复用的组)、
  `TestCreateRejectsUnknownReuseGroup` / `TestCreateRejectsDisabledReuseGroup` (均为 `proxygroup.ErrInvalid`, 且拒绝时零写入)、
  `TestCreateRejectsReuseThatAlsoDescribesAGroup` (name / source_spec / enabled 三个字段各自 422 且消息指名)、
  `TestCreateDoesNotDeleteReusedGroupWhenListenerFails` (复用组在失败后不被删)、
  `TestCreateReportsReuseLookupFailureAsAnInternalError` (负对照: 存储故障不得被伪装成校验错误)。
- `go test ./internal/api/`: `TestCreateProxyServiceReusingAnEnabledGroupReturns201` /
  `...ReusingADisabledGroupIsAValidationError` / `...ReusingAnUnknownGroupIsAValidationError` 把**真实的**
  `proxyservice.Service` 接在真实 HTTP handler 后面, 断言 201 与 422 `validation_failed` —— 这条链路不是靠 stub
  返回一个哨兵错误验证的。
- `cd web && npx tsc -b` 通过; `node scripts/verify-admin-catalog.ts` 通过。
- 只读实测 (2026-10-14, 运行中的 `127.0.0.1:19090`, `X-API-Key`): 见 Problem 里的两条 GET —— "停用的渠道不可选"
  与"启用的渠道可复用"两条判据对得上现场数据 (`linkup` 的组 enabled=true, `WsPxy-colo` 的渠道与组都 disabled)。
- **没有做的**: 对运行中的控制面发真实 POST。那个进程 (pid 4075, 启动于 21:24) 早于本次改动, 不认识
  `listener.proxy_group_id`; 而 `decodeJSONBody` 用 `DisallowUnknownFields`, 对旧进程发这个字段只会得到 400,
  对新行为没有证明力 (发一个不带该字段的请求, 则会在线上真建一个组)。完整链路的结论由上面的 api 集成测试给出;
  对活进程的端到端复验需要重建并重启控制面, 本轮不做。
- 前端没有新增浏览器自动化: 表单的住宅分支由 `npx tsc -b` 与代码审查覆盖, 没有截图。

## Related

- [机机凭据不许再长代理组](../architecture/2026-10-01-api-cannot-grow-the-group-graph.md) — 闸门按"谁在调用"判定, 复用路径不放宽它,
  以及机器调用方现成的 `POST /api/v1/listeners` 路径
- [服务的形态由来源类别派生](../simplification/2026-10-01-service-shape-follows-source-class.md) — "隐藏与不下发是同一件事",
  以及形态徽章为什么仍是「固定节点组」
- [删服务不许删共享组](../bug-fix/2026-10-01-delete-service-must-not-delete-shared-group.md) — 组的归属按"谁还在用"判定
- [listener 可以直接钉一个节点](../feature/2026-09-26-listener-pins-a-node.md) — listener 的出口锚点为什么必须有显式来源

共享入口发布失败时只补偿删除新建 Listener，不删除复用组；reuse_test.go 钉住此边界。
复用的是频道出口池，不为新入口凭据自动创建 sticky client 身份；专属粘性身份仍由住宅频道页面管理。
