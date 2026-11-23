# Agent Note: 删除消费方看板 —— 用户否定的功能整套移除

Status: implemented

## Problem

「消费方看板」是 20260918 上线的一块 token 寻址的免登录固定看板：控制面发布
`/board/<token>`（快照 + SSE 流量流 + 切换成员三个端点），前端用 hash 路由
`#/board/<token>` 渲染，管理员侧另有 `/api/v1/boards` CRUD 来铸造与吊销它。
它的设计意图记在已归档的
[archived/feature/2026-09-18-consumer-board.md](../../archived/feature/2026-09-18-consumer-board.md)。

用户对它的判决是功能否定，不是"不好用"：

> 它那个消费方看板这个功能也是没用的，也是之前 AI 乱加的，我也非常不喜欢这个功能

除了"不喜欢"，它还有一条独立的技术理由支持删除：免登录入口。
`web/src/App.tsx` 里 `#/board/<token>` 的分流点在 **authGate 之前** return ——
这不是疏忽，是旧 note 明写的设计（"消费者没有管理员账号，把公开面放进认证闸门
会让一条完全有效的链接死在登录页前面"）。但代价是：仓库里从此存在一条**完全
绕开管理员会话**的公开面，凭据写在 URL 片段里。URL 片段会被复制粘贴、进浏览器
历史、进聊天记录；一旦泄漏，持有者能读到全部代理拓扑，而 `allow_switch` 打开
时还能改变所有人的出口。留着一个用户不想要、又自带绕过面的功能，是纯负资产。

## Decision

整套删除，后端 / 前端 / 文档 / 门禁脚本 / e2e / 截图一并移除。

后端删除：`internal/board/`（service / snapshot / lifecycle / random 及测试）、
`internal/api/consumer_board.go` 及其测试、`internal/store/consumer_board.go`
及其测试、`internal/scheduler/board.go`（常驻轮询调度器）、
`internal/dataplane/mihomo/runtime.go` 及其测试。
`internal/api/server.go` 去掉 `consumerBoards` 字段、`registerConsumerBoardRoutes`
调用、请求日志里对 `/board/` 的脱敏分支（前缀常量随文件一起没了，分支留着不能编译）；
`cmd/hx-proxygroupd/main.go` 去掉 board service / scheduler 的构造与
`runBackground("board_scheduler", ...)` 注册。

前端删除：`web/src/pages/board-page.tsx`、`web/src/pages/boards-page.tsx`、
`web/src/lib/board-api.ts`、`web/src/lib/types.ts` 里 118 行的 board 类型块、
`App.tsx` 的 `BoardPage` 懒加载 / `boardToken` state / `configureBoardAdmin`
注入 / `#/board/<token>` 免鉴权分支（**安全隐患的源头，一并删**）、
`about-page.tsx` 的「消费方看板」tab、`web/vite.config.ts` 的 `/board` dev 代理。

文档与门禁：`docs/CONSUMER_BOARD.md`、`scripts/verify-board-contract.ts`
（三处词元比对的门禁）、`web/e2e/board-dialog.mjs`、`web/e2e/board-layout.mjs`、
`docs/screenshots/consumer-board-{desktop,tablet,mobile}.png`、README 文档索引里
的那一行。残留的旧 note 引用（`confirm-dialog.tsx`、`cf-account-group.tsx`、
`App.tsx` 三处由本次改动清掉）就地移除：它们引用的是当时就地记下的 CSS/交互结论，
那些结论本身已经在注释正文里说清楚了，不需要靠一篇被归档的 note 背书。

**数据库表刻意保留，不做反向迁移。** `migration 36 (consumer_board)` 建的三张表
（`consumer_boards`、`consumer_board_groups`、`consumer_board_rotation_state`）
留在 schema 里，全库零读写方。这不是遗漏，实测依据两条：

- 本机实例库 `.tmp/hxwebx/rundata/hx-proxygroup.db`（只读打开）：
  `select max(version) from schema_migrations` = **36**，
  `select count(*) from consumer_boards` = **2** —— 已经有真实数据落在这张表上。
- `internal/store/migrations.go` 的 `migrate()`：
  `if currentVersion > latestVersion { return fmt.Errorf("database schema version %d is newer than supported version %d" ...) }`。
  删掉 version 36 这条迁移，`latestVersion` 降到 35，而这些库的
  `schema_migrations` 写着 36 —— 它们会在**启动时直接失败**。

所以删除只发生在功能层：表还在，但没有任何代码路径读它或写它。将来要清掉这些表，
必须新加一条 version 37 的 `DROP TABLE` 迁移（保持版本单调递增），而不是删掉 36。

被保留的相邻能力（删除时逐条核对过，不是它们属于看板）：

- `Manager.SelectProxy`（`internal/dataplane/mihomo/controller.go`）保留：
  住宅轮换仍在用，实测 `internal/residential/rotate.go:335` 是它的调用方。
- `internal/metrics` 的 `RuntimeSnapshot` / `TrafficSnapshot` 保留：总览页在用，
  与看板删掉的 `mihomo.ProxyRuntimeState` 是两套不同符号。删除前实测
  `ProxyRuntimeState` 在全仓只有一个非测试引用，
  `internal/board/service.go` 里的接口声明 —— 它随 board 一起走了。

## Alternatives considered

- **什么都不做，留着看板。** 它已经上线、有三层测试（store 10 项 / api 19 项 /
  service 21 项）、有真实落库数据，删掉要动后端 6 个包加前端 6 个文件，是最贵的一条路；
  而且它服务的需求是真实的 —— 消费者确实需要一条"活的、可吊销的链接"，而不是一段
  会被复制粘贴弄脏的文案。否决：用户否定的是**这个功能**而不是它的实现质量，理由
  不因测试齐全而改变；更关键的是它带着一条 `#/board/<token>` 的 authGate 前置分流，
  保留它等于长期维护一条已知的免鉴权读取面（`allow_switch` 打开时还是写入面），
  而这条面服务的正是用户不要的功能。
- **只删前端，保留后端能力。** 后端看起来是"能力"，将来或许有别的消费者；前端才是
  用户说不喜欢的部分，这样改动面最小，也不会丢掉已经写好并通过测试的 store / snapshot /
  SSE 实现。否决：这留下 `/board/<token>` 公开面和它整套测试、调度器、脱敏分支，
  维护成本一分不省，还让路由目录里多一个没有 UI 的免鉴权公开面 —— 恰好是这次要
  消除的东西。
- **保留看板，但把 authGate 前置分流去掉，让它走管理员会话。** 这是消除免鉴权入口
  的最小改动，一处就能补上安全缺口，功能也能留。否决：这与该功能的唯一前提直接
  冲突 —— 消费者没有管理员账号，这正是旧 note 写明的东西；套上会话后这条链接对目标
  人群根本打不开，功能名存实亡，却仍占着三张表、一个调度器和一套端点。
- **把看板收窄成纯只读（去掉 `switch` 与轮询）。** 去掉写权限后，泄漏一条链接
  最坏只是读到拓扑，风险等级的确实质下降，而读视图可能还有价值。否决：用户否定的
  是整块看板而不是它的写能力，收窄之后 `/board/<token>` 免鉴权读取面、store 层、
  快照拼接和 SSE 端点一样要留着，省下的只有 `internal/board/lifecycle.go` 一个文件，
  换不到一次架构性简化。
- **写一条反向迁移把三张表 DROP 掉。** 彻底清干净，schema 里不留无主表，最符合
  "删就删干净"的直觉。否决：实测 `migrate()` 会对 `currentVersion > latestVersion`
  直接报错退出，而本机与线上库已经记到 36；删掉 36 会让这些库起不来。正确做法是
  保留 36、需要清理时另加 37 —— 破坏性迁移的代价远大于留下三张不读写的表。

## Consequences

- **代价：`ProxyRuntimeState` 这个能力一起没了。** 它是本仓库唯一一个从**运行中**的
  Mihomo 读"这个组现在走的哪个成员"的 Go 接口，也是唯一维护了那条不变式的地方：
  Mihomo 的 `/proxies` 返回 JSON 对象，Go 的 map 迭代顺序随机，不排序会让每次轮询
  都看起来发生了变化。以后要再加"代理组当前成员"的运维视图，这两件事都要从零重写。
  它服务的旧语义（Desired State 与 Actual State 是两件事）本身没有消失，只是暂时
  没有读者了。
- **代价：schema 里留下三张无主表**（`consumer_boards` 及其两张子表），带着 2 行
  既存数据。它们不参与任何查询、不占任何运行时代价，但下一个读到 `migrations.go`
  的人会问"这个为什么还在" —— 答案写在 migration 36 的注释里，指向本篇。
- **代价：免鉴权公开面从 7 个减到 6 个**（剩 `/sub/`、`/nodes/`、`/rot/`、`/ctl/`、
  `/provision/`、WebSocket 中继）。下次有人想加公开面时，少一个"我们本来就有这种
  端点"的先例可援引。
- **换来：一条绕过 admin 会话的入口被彻底移除**，这是本次删除里唯一无法通过"不喜欢"
  论证、而必须靠工程判断成立的部分。
- **换来：`internal/board/lifecycle.go`（471 行）里那个"控制面主动改数据面运行态"
  的常驻写任务消失。** 它当时被写成仓库第一个此类先例，并附了"后续若有人想加第二个，
  应当复用这个调度器"的告诫；现在这个先例本身不存在了，告诫随功能作废。
- **什么信号出现时应该重新考虑：** 如果出现"外部消费者需要一条**不需要管理员账号**的
  实时视图"这一需求，且这次的要求里**自带认证/授权方案**（例如短期签名 URL + 独立
  凭据体系），才值得重新评估 —— 届时先读归档的那篇 note，它记录了当时识别出的三个
  真实缺口（控制面没有数据面运行态读接口、实时流量只在管理员会话后面、节点选择能力
  没有管理端入口），那些是需求，不是结论。
- **什么信号出现时应该清理表：** 如果发版节奏允许一次破坏性 schema 变更，加一条
  version 37 的 `DROP TABLE consumer_boards`（子表因 `ON DELETE CASCADE` 的外键
  需一并处理）比让它们继续留在 schema 里更干净。

## Testing

删除后实测（全部在本机执行，命令与结果原样记录）：

- `go build ./...` 与 `go vet ./...` 通过（exit 0）。宿主机无 Go 工具链，用
  `golang:1.25` 容器 + 只读 module cache 执行，因容器内 git 所有权问题加
  `-buildvcs=false`。
- `cd web && npx tsc --noEmit` 通过（exit 0）；`npm run build` 成功，
  产物里不再有 board 相关 chunk（1895 modules，`about-page` 从 7.32 kB 起，
  无 `board-page`）。
- `go test ./...`：`internal/` 全部 30 个包通过（含 `internal/api`、
  `internal/store`、`internal/scheduler`、`internal/dataplane/mihomo`）。
  根目录 3 个 `run_test.go` 用例（`TestRunScriptStartsAndStopsBackend` 等）在
  容器里失败，原因是容器内 `git safe.directory` 与缺 `npm`，**不是本次改动**：
  失败信息是 `error obtaining VCS status` 与 `required command not found: npm`，
  与 board 无关；补上 safe.directory 后这些用例越过构建阶段继续跑到前端启动，
  才在 `npm` 缺失处停下。
- 全局残留检查：`rg -i 'consumer.board|consumerBoards|ConsumerBoard|/board|boardToken|board-api|BoardPage|BoardsPage|verify-board|consumer-board'`
  在仓库内零命中（`.git`/`.tmp` 除外）。`internal/store/migrations.go` 的
  board 表定义与 `vite.config.ts` 里指向 `/nodes` 的注释是刻意保留的无关项。
- 门禁：`node .agents/skills/hx-agent-notes/scripts/verify-all.ts` 通过。
