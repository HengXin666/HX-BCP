# Agent Note: 机机凭据不许再长代理组 —— 闸门落在请求路径上, 而不是领域服务里

Status: implemented

本 note 中「不在 `proxygroup.Service.Create` 里按成员数拒绝」的那条判断已被推翻, 现在由
.agents/notes/implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md
在领域服务的 `normalize` 里拒绝单节点组。下面描述的 `guard.deny_api_group_create` 开关、
`MachineCredential` 判据与三个建组路径的闸门**仍然有效**, 只是不再是唯一的防线。

## Problem

**同一件事被禁止过两次, 两次都只落在文字上。**

用户 2026-09-26 的原话:

> 「代理组只需要有个位数...最多是一个订阅属于一个代理组」
> 「那你就直接给服务端提供这种能力啊, 就是能直接指定使用哪一个节点这种模式啊」

当时据此做了两件事: listener 增加 `node_id`, 允许一个服务直接钉一个节点 (见
`implemented/feature/2026-09-26-listener-pins-a-node.md`); 并把 61 组 (其中 **57 个是单成员组**,
指向的**只有 28 个**不同节点) 收敛成 12 组。

**2026-10-01 复查: 又长回来了。** 12 组里有 6 个是单成员组, 且是 09-29 之后新建的:

| 组 | 成员数 | 唯一成员 |
|---|---|---|
| `solo香港专线01` | 1 | 香港专线01 |
| `solo香港专线04` | 1 | 香港专线04 |
| `solo香港专线05` | 1 | 香港专线05 |
| `hk-new-01` | 1 | 香港专线04 (**同一个节点被包装了两次**) |
| `ioa-vm` | 1 | ioa-vm-socks5 |
| `outlook` | 1 | outlook-01 |

指认得到的确切来源只有一处 —— `gh-registrar/proto/watch_t.py` 的 `ensure_listeners()`: 给每个
healthy 住宅节点 `POST /api/v1/proxy-groups` 建一个单成员组, 再 `POST /api/v1/listeners`
指过去。那两行代码在仓库里躺了 12 天, 期间每一次"想探测某个节点"的执行都多留一个组。

**为什么"已经禁止"没用: 禁止写在 note 和 AGENTS.md 里, 而调用方是脚本。**
脚本不读 note。`listener.node_id` 这个能力**已经存在且可用**, 但没有任何机制让一个不知道它
的调用方失败 —— 它按老路建组, 服务端照单全收。所以这不是"有人不听话", 是**能力可达性与
约束强度不匹配**: 旧形状比新形状更容易到达 (少写一个字段就能做到"让这个服务走那个节点"),
而约束只存在于文档中, 对机器调用方零成本。

## Decision

**一、新增全局开关 `guard.deny_api_group_create` (默认 false)。开启后, 用 API Key 调用
三个"会创建代理组"的端点一律 403。**

三个端点是 `POST /api/v1/proxy-groups`、`POST /api/v1/proxy-services`、`POST /api/v1/quickstart`。
后两个必须一起拦: 两者的**第一步都是建组**, 只拦字面上的组端点等于把用户要堵的捷径原样留着
(实测 `proxyservice.Create` 与 `quickstart.Create` 都直接调 `proxygroup.Create`)。

**二、判据是"谁在调用", 不是"写什么"。闸门因此落在 `requireAuth` 的请求路径上,
而不是 `proxygroup.Service.Create`。**

这是本决策的核心。领域服务**无法**区分「操作员想建一个恰好只选一个节点的组」和「脚本想用组
当成指向一个节点的句柄」—— 两者请求体可以逐字节相同。唯一能区分它们的输入是**凭据身份**:
浏览器登录 = 有人看着; API Key = 无人值守的循环。凭据在 `internal/auth` 解析, 在 api 层
可用, 所以闸门写在 api 层。

代价说清楚: 闸门是**按路径的白名单**, 它会随着新的建组端点出现而过期。这是刻意选择的:
把它做成通用的 RBAC 会得到第二套手工维护的权限系统, 而那正是本仓已经栽过的坑
(「同一事实两处判据必然不同步」)。所以路径表集中在一个文件、一个 map, 并在 `guard.go`
顶部写明它不许长大。

**三、为区分两者, `auth.Session` 增加 `MachineCredential bool`, 由 `AuthenticateAPIKey` 置位。**

不能用"CSRFToken 为空"代替。API Key 会话的 CSRF token 为空, 原因是**头凭据不需要 CSRF 防护**,
不是"调用方是机器" —— 拿一个巧合当判据, 下次谁改了 CSRF 逻辑就会静默失效。

**四、只拦"创建", 不拦其余写操作。**

刷新订阅、探测节点、轮换 share token、编辑/删除既有对象**全部保持对 API Key 开放**。
理由: 这些正是无人值守调用方存在的意义, 关掉它们等于为了防一个建模错误而打断真实业务;
而这个建模错误在本闸门下已经不可能犯 —— 没有组可以建。

**五、默认关闭。**

升级安装必须保持原行为, 直到操作员在前端打开。默认开启会在下次重启时打断所有现存脚本,
而失败会出现在离改动很远的地方。

**六、读也保持开放。**

`GET /api/v1/proxy-groups` 不受影响。闸门约束的是"谁可以长对象图", 不是"谁能看"。

## Alternatives considered

- **什么都不做, 只把 6 个壳组删掉。** 它最强的理由是本次已经证明删得干净: 把 listener 改成
  钉节点后删组, 出口身份 (`svc-*`) 与规则目标逐字不变。否决理由: 这已经是**第二次**清理
  同一现象 —— 09-26 收敛过 57 个单成员组, 12 天后又出现 6 个, 来源是一个当时就在仓库里的
  脚本。只删不堵, 第三次只是时间问题。

- **在 `proxygroup.Service.Create` 里拒绝"成员只有一个"的组。** 它最强的理由是判定点最靠内,
  任何调用方都绕不过, 不需要区分身份。否决理由有两条, 且都是硬伤: 其一, 「一个节点」不是
  任何人的错误 —— `resi-hk-0405` 只有 2 个节点、`proxyscrape-pool` 只有 1 个订阅, 组的价值
  由**它表达的意图**决定而不是成员数, 按数量拒绝会在合法的边缘案例上误伤; 其二, 真正的病
  不是"组有一个成员", 是"重复地为同一个节点建组" (`香港专线04` 被包了两次), 而"重复"这个
  判据依赖于**时间**, 领域服务在单次调用里看不到。

- **自动把单成员组改写成 listener 的 node pin, 不让用户建。** 它最强的理由是用户根本不需要
  知道这个区别, 系统替他做对的事。否决理由: 它是**静默改写**。调用方建了组、拿到 201、之后
  去 `GET /proxy-groups` 却找不到它 —— 一个"成功但对象不存在"的 API 比一个 403 难查得多。
  本仓已有先例教训 (`2026-09-29-proxy-service-update-target-and-listener-ownership.md`:
  地址被丢弃、body 说了算, 结果"改这个"变成了"改任意一个")。宁可直接拒绝。

- **做成"API Key 只读"的通用开关。** 它最强的理由是语义简单, 且一次性解决"脚本乱写"这类
  全部问题。否决理由: 它同时打死了刷新订阅、探测节点、轮换 token —— 即无人值守调用方的
  全部正当用途。为了避免一个建模错误而砍掉整类能力, 是拿大炮打蚊子; 而且用户要的是
  "只能用户在前端手动操作 [建组]", 不是"API 只读"。

- **只拦 `POST /api/v1/proxy-groups`, 不管另外两个端点。** 它最强的理由是范围最小、最不容易
  误伤。否决理由: 实测 `proxyservice` 与 `quickstart` 都以内联方式建组, 调用方换个端点就能
  绕开 —— 一个能被一行改动绕过的闸门不是闸门。

- **在 `guard.go` 里做通用的按路由权限表。** 否决: 见 Decision 二。它会把一个三行的判断
  变成一套需要持续维护的授权系统, 而维护它的人不会知道它当初为什么存在。

- **把闸门状态缓存在内存里, 避免每个请求读一次 settings。** 否决: 操作员打开开关后期望
  **下一个请求**就生效。settings 服务本身不缓存, 而这三个路径本来就是低频写操作, 每次多读
  一次 metadata 的开销相对写操作本身可以忽略。用"可能读到旧值"换取微秒级收益是错的取舍。

## Consequences

**新增能力**: `PUT /api/v1/settings` 的 `guard.deny_api_group_create`; 前端「设置 → 性能与运行」
新增开关「禁止 API 新增服务组」。开启后被拦的请求得到
`403 api_group_create_denied`, 消息里指明去前端手动建、或清除该开关。

**本次同时执行的清理** (12 组 → 6 组): 6 个单成员组的 listener 先改成 `node_id` 直钉,
再删组。`listeners.proxy_group_id` 是 `NOT NULL ... ON DELETE RESTRICT`, 所以顺序不能倒。
删组后 listener 的锚组回落为 `egressAnchorGroup` (id 最小的启用组, 此处是 `良心云`)。

**实测验证 (2026-10-01)**:

- 清理前 12 组 / 13 listener; 清理后 **6 组**, 剩下每个都表达真实策略:
  `良心云`(订阅) / `gh-resi-clean`(3 节点) / `resi-hk-0405`(2 节点) /
  `us-exit`(5 节点) / `residential-WsPxy-colo`(3 订阅) / `proxyscrape-pool`(池)。
- **出口身份不变 (决定性)**: 规则目标从组名变成节点名, 且节点名与清理前的文档记载逐字一致 ——
  `svc-a8e44c…` -> `hx-node-262e112899af1264` / `svc-fc5d0d…` -> `hx-node-bf20439ce41e4f9c` /
  `svc-fe533b…` -> `hx-node-744828fca946b4e7`。11 条 IN-USER 分流全部保留。
- 数据面未中断: 清理过程中 7890 与 32000 持续监听, `listener_count` 保持 2,
  `proxy_count` 2067, 无重启。
- 部分出口 (`soloHK01`) 实测 `TLS unexpected eof`, 但**未改动的** `gh-resi-clean` 同时同档失败,
  且 mihomo 对该节点报 `alive: true` ⇒ 这是节点自身/时段现状, 不是本次改动引入的。
- 闸门测试: `TestGroupCreationGuardBlocksOnlyMachineCreates` (三端点 403 + GET 放行 + 浏览器会话放行)
  与 `TestGroupCreationGuardOffIsSilent` (负对照) 均通过。没有负对照的话, "无条件拒绝所有
  API Key 的 POST" 也能通过正例, 所以两个都要有。

**修掉的调用方**: `gh-registrar/proto/watch_t.py:ensure_listeners()` 改为用 `node_id` 直钉、
幂等 (已存在就只在节点变化时 PUT)、且不再 `except: pass` 静默吞掉失败 (静默吞掉正是
"看起来建了、其实没建"的来源)。`gh-pool` 侧只读 (`egress.py` 与 `routers/egress.py` 都是 GET),
无需改动。

**已知边界**:

- 闸门只覆盖**已知的三个**建组端点。将来新增一个会建组的端点, 必须同时更新 `guard.go` 的
  路径表 —— 这是它的维护成本, 已写在文件顶部。
- **闸门管不住 listener, 这是刻意的 (2026-10-11 实测确认)**: 2026-10-11 用户报"乱码节点组"
  时, 控制面 20 个 listener 里有 13 个 `fb-*` 是上一个交互式 Agent 会话逐个
  `POST /api/v1/listeners` 建的 (每个带 `node_id`), 而**代理组始终只有 5 个** —— 闸门
  一次都没响, 因为病不在建组。**不把 `/api/v1/listeners` 加进路径表**: 按节点钉死正是本闸门
  的上游决策所推荐的做法 (见 `implemented/feature/2026-09-26-listener-pins-a-node.md`),
  封掉它会把规则要求的那条路一起堵死。要治的是**回收** (建立者用完即删), 不是创建权。
  反例与正面样板见 `implemented/bug-fix/2026-10-11-listener-proliferation-from-agent-session.md`
  (那份在仓库根的 `.agents/notes/` 树里, 因为增殖的调用方是 workspaces 侧的服务)。
- `MachineCredential` 只是"这次调用来自 API Key"。若某个操作员把 API Key 贴进浏览器脚本里
  操作前端, 闸门不会拦 —— 但那条路径下操作员也在场, 正是闸门想放行的情形。
- settings 读取失败时闸门**判为拦截** (fail-closed)。理由是一个守门机制静默失效比一个可恢复的
  500 更危险; 操作员会在响应里看到错误并去修存储。
- 曾经未做、现已由
  `implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md` 接管:
  禁止把组**编辑**成单成员。当时的理由是"退化"需要历史对照才能判定; 现在的判据是
  `sameSingleNode`(与库里已存的单节点组逐字节相同才放行), 因此存量行仍可收敛, 而从两节点
  缩成一节点会被 422 拒绝。这条从"未做"变成"已做", 是在用户明确要求"默认拒绝 size=1"之后。
