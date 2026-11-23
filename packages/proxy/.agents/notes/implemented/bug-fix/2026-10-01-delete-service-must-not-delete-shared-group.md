# Agent Note: 删服务不许删共享组 —— 锚组是实现细节, 不是归属

Status: implemented

## Problem

**用户原话**: 「DELETE /api/v1/listeners/listener-c38b41fc88bc612c686048e0?version=2 这个都失败了,
是为什么? 为什么你没有专门的判断呢? 这些路由你都没有红线去判断了吗?」

### 现象与归因 (日志实测)

那次 DELETE **本身是成功的** (`backend.log` 15:47:50.293, `duration_ms=181`)。失败的是紧跟其后的第二发:

```text
15:47:50.293  DELETE /api/v1/listeners/listener-c38b41fc...           → 成功
15:47:50.300  DELETE /api/v1/proxy-groups/group-0d3cd1d5e38bc799608598e4  → ★ 去删「良心云」★
15:47:50.598  DELETE /api/v1/listeners/listener-c38b41fc...           → 404 (重试)
15:47:52.139  DELETE ... → 404
15:47:55.536  DELETE ... → 404
15:48:20.866  DELETE ... → 404
```

**前端 `removeService()` 在删完 listener 后顺手删它"自己的"组**。这条逻辑写于
「一个服务配一个专属组」的旧模型 (当时是对的)。它不再成立的原因是 2026-09-26 引入的
`node_id` 钉节点: 一个被钉住的 listener **仍然**带一个非空的 `proxy_group_id`
(`listeners.proxy_group_id` 是 NOT NULL, 钉住的 listener 会回落到 `egressAnchorGroup` ——
id 最小的启用组)。于是六个被钉住的 listener 全都指向 `良心云`, 而前端把那个共享锚组
当成了"这个服务的专属组"。

### 三道该有的红线, 一道都没有

| 该有 | 实际 |
|---|---|
| 前端: 只删本服务独占的组 | 无。`if (deleteTarget.group) delete` —— 有组就删 |
| 后端: 删组前问"还有 listener 在用吗" | 无。`referencedBy` **只走组间边** (member / dialer), 完全看不见 listener |
| 后端: 拒绝要可判定 | 无。只有 SQLite 的外键裸报错, `mapStoreError` 不认它, API 落进 default 分支回 **500 internal_error** |
| 前端: 失败后清掉待删目标 | 无。`setDeleteTarget(null)` 在删除**之后**执行, 抛错就不执行 —— 对话框一直armed, 操作员再点几次就重发几次 (即上面那 4 个 404) |

### 为什么没出事: 外键, 不是设计

`listeners.proxy_group_id REFERENCES proxy_groups(id) ON DELETE RESTRICT` 拦住了它。
在库副本上实测:

```text
PRAGMA foreign_keys=ON;  DELETE FROM proxy_groups WHERE id='group-0d3cd1d5...';
  -> Error: FOREIGN KEY constraint failed      (组保住)
PRAGMA foreign_keys=OFF; DELETE FROM proxy_groups WHERE id='group-0d3cd1d5...';
  -> 剩余组数=5                                 (组没了)
```

**一条领域规则不该靠一个数据库约束来兜底**: 约束挡住的是数据损坏, 不是"这个操作是否合法";
而且它给出的信号 (`duration_ms=0` + 未映射错误) 无法告诉调用方该改什么。

### 被同一根因带出来的第二个缺陷: 行标题借用了组名

`ServiceRow` 的行标题是 `residential?.name || group?.name || listener.name`, 且**优先组名**。
钉住的 listener 的组是共享锚组, 于是四个不同的服务全都显示成「良心云」:

```text
修复前: 良心云(入口) / WsPxy-colo / 良心云 / 良心云 / gh-resi-clean / ... / 良心云 / ... / 良心云
修复后: 良心云入口 / WsPxy-colo / ioa-vm-mixed / outlook-mixed / gh-resi-clean / ... / soloHK04-entry / us-exit-entry / hk-new-01-entry
```

操作员**看不出自己在删哪一个** —— 这是"删除误伤"这件事的另一半: 不只是删错对象,
而是界面上根本分辨不出对象。

## Decision

**一、后端: `proxygroup.Service.Delete` 先问 listener, 再问组间引用。**

`Repository` 增加 `ListListeners`, 删除前统计"还有几个 listener 以这个组为出口锚点"。
有则拒绝, **并指名是谁** (`6 listener(s) still resolve their egress through it: ...`)。

**二、新增 `ErrReferenced` 哨兵, 独立于 `ErrConflict`。**

两者补救动作不同: `ErrConflict`("别的组引用了它") 靠改组的成员解决;
`ErrReferenced`("listener 还在用它") 靠把 listener 改指别处解决。API 映射为
**409 `proxy_group_in_use`**, 且必须在 `ErrConflict` 分支**之前**匹配 (`errors.Is` 会穿透包装)。
前端按 `code` 判断, **不读 message 字符串** —— 本仓已因"靠错误文本判逻辑"栽过。

**三、前端: 只有独占组才跟着删。**

新增 `isSoleOwnerOfGroup(listeners, listenerID, groupID)`: 数一数还有几个 listener 指向它。
判据是**计数**, 不是"组名像不像专属组"这类猜测。共享则明确提示"组会被保留",
不共享才删 —— 确认框文案也随实际情况变化, 否则操作员会以为策略跟着没了。

**四、前端: 标题只用自己的名字。**

去掉 `group?.name` 回退。组名不是服务的身份; 需要看策略时有专门那一列, 那里显示
`strategyLabel` 与成员来源。

**五、`deleteTarget` 无条件清空。**

移进 `finally`。删除失败不该让对话框保持 armed —— 那是"点几次就发几次"的成因。

## Alternatives considered

- **什么都不做, 靠外键挡着。** 它最强的理由是**它已经挡住了**: 实测 `良心云` version 43 完好,
  数据没有损坏, 一次也没真的删掉。否决理由: 挡住的是最坏后果, 不是错误本身。调用方拿到的是
  未映射的 500 与 `duration_ms=0` 的空响应, 既不知道谁在用, 也不知道该改什么; 而用户看到的是
  "删除失败 + 行还在"。更要紧的是这条防线**在 PRAGMA 关掉时会一起消失** —— 迁移代码
  (`migrations.go:1235`) 就在用 `PRAGMA foreign_keys = OFF`, 领域规则挂在它上面等于挂着。

- **前端修就够了, 后端不加检查。** 它最强的理由是范围最小: 只有一处 UI 会误删, 改掉那处即可,
  不动领域服务与接口。否决理由: 任何**其它**调用方 (脚本、未来的第二个界面) 仍会踩同一个坑,
  而它们不像 UI 那样有"确认框"最后拦一下。用户问的正是"为什么你没有专门的判断" ——
  他要的是判定存在, 不是某个界面自觉。

- **在 `referencedBy` 里把 listener 也算成一种引用, 复用 `ErrConflict`。** 它最强的理由是
  复用现有机制、不加新的错误类型, 改动最小。否决理由: 两类引用的**补救动作不同**, 而
  `ErrConflict` 的措辞与前端处理都指向"组间依赖冲突"。混在一起会让调用方只能读 message 猜,
  正是本仓记过的"同一事实两处判据"隐患。

- **删组时自动把 listener 改指到别的组, 让删除"总能成功"。** 它最强的理由是对调用方最省事,
  不需要理解锚组概念。否决理由: 这是**静默改写别人的出口**。对"注册全程同 IP"这类硬需求,
  悄悄换出口比拒绝删除危险得多 —— 与 `.agents/notes/implemented/feature/2026-09-26-listener-pins-a-node.md`
  里否决"节点消失时回落到组"是同一条理由。

- **干脆不让 listener 指向不承载流量的锚组 (把 proxy_group_id 改成可空)。** 它最强的理由是
  从根上消除"锚组被误当成归属"的可能, 概念最干净。否决理由: 那是一次**数据模型迁移**,
  要重建 `listeners` 表 (而它的唯一索引在本仓历史上有过重复项, 见 version 38 迁移的教训),
  风险和收益不成比例: 锚组本身没有错, 错的是**代码把它当成了归属**。先修正读法。

- **前端加"这个服务是否独占组"的开关让操作员自己选。** 否决: 把系统本可以判定的问题
  交给操作员, 而判据 (`proxy_group_id` 有几个使用者) 对操作员不可见, 等于让他猜。

## Consequences

**新增能力**: `DELETE /api/v1/proxy-groups/{id}` 在仍有 listener 依赖时返回
**409 `proxy_group_in_use`** 并列出占用者; 前端删除服务时按"是否独占"决定是否附带删组,
确认框文案相应变化; 服务行标题固定为服务自身名字。

**实机验证 (2026-10-01)**:

- 后端: `DELETE group-0d3cd1d5… ?version=43` -> `409 proxy_group_in_use`,
  消息指名 6 个占用者; 对照组 (建一个无人使用的组再删) -> `204`。`良心云` version 43 完好。
- 前端 e2e (`web/e2e/delete-shared-group-guard.mjs`): 建一个 `node_id` 钉住的探针服务
  (实测其 `proxy_group_id` = 锚组, 且 `sharersBefore=7` —— 确认测到了共享分支),
  经真实 UI 路径 (`服务操作` 下拉 -> `删除服务` -> 确认) 删除, 结果:
  `listenerRemoved=true` / `anchorStillPresent=true` / `groupsBefore=groupsAfter=6` / `failures=[]`。
  截图见 `docs/screenshots/delete-shared-group-guard.png`。
- 单测 `TestDeleteRefusesGroupStillUsedByAListener` 带负对照 (listener 移除后同一调用必须成功),
  否则"一律拒绝"也能通过正例。

**代价与风险**:

- 每次删组多一次 `ListListeners`。删除是低频操作, 而正确性依赖它, 这个代价值得。
- `isSoleOwnerOfGroup` 用的是前端手里那份 listener 列表 —— 它是**页面加载时**的快照。
  如果另一个客户端在两个操作之间给这个组挂上了新 listener, 前端仍会以为独占并发出删除;
  此时后端红线会拒绝 (409) 并让整次删除报错。**这是有意的**: 后端的判定是权威,
  前端的判定只是为了避免发出一个必定失败的请求。
- 标题改动会让长期用户注意到行名变了 (从组名变成 listener 名)。这是修复而非回归:
  被钉住的 listener 本来就该显示自己的名字。

### 附带发现并修掉的第三个缺陷: 闸门会被无关的保存静默清零

验证过程中实测到: 闸门开启后, 任何**不含 `guard` 键**的 `PUT /api/v1/settings` 都会把它重置为
`false`。原因是 `GuardSettings.DenyAPIGroupCreate` 是 `bool`, 而该端点是**整份文档替换** ——
缺失的键解码成零值 `false`, 于是一个只改日志级别的脚本 (或一个不认识该字段的旧前端) 就能
悄悄关掉用户刚打开的闸门:

```text
修复前: PUT settings (body 不含 guard, 只改 log_level) -> guard 变 false, 且无任何提示
修复后: PUT settings (body 不含 guard, 只改 log_level) -> guard 保持 true
```

**修法**: 该字段改为 `*bool`。`nil` 表示"本次请求未提及", 在 `Service.Update` 里合并为
已存值; 显式 `false` 仍然关闭。**合并只允许发生在这一个地方** —— 其余代码看到的永远是具体值,
否则"三态"会泄漏到每一个读它的人那里。前端 `GuardSettings.deny_api_group_create` 相应改为可选。

判据: `TestOmittedGuardFieldKeepsStoredValue` 走**真实 JSON 往返** (先序列化、删键、再解码),
而不是在结构体上直接置 nil —— 丢失信息的那一步正是解码, 结构体级测试测不到它。
负对照: 同一次保存里日志级别**必须**已生效, 否则"整个保存被忽略"也能让断言通过。

**未覆盖**:

- 没有阻止"两个 listener 共享一个**非锚组**"这种形态。它本身合法 (同一个节点策略服务两个入口),
  `isSoleOwnerOfGroup` 会正确保留该组; 但如果将来出现"删 A 服务应当连带删组、而组正好被 B 用"
  的真实需求, 需要新的判据, 不能靠放宽当前这条。
- `referencedBy` 与 `ListListeners` 是两次独立查询, 不在同一事务里。并发场景下理论上存在
  "查到没有 -> 期间被引用 -> 删除"的窗口。目前没有并发写组的调用方, 故未加锁;
  若要收紧, 应在同一个事务里做检查与删除。