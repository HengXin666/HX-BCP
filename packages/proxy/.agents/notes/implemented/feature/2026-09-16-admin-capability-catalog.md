# Agent Note: 能力目录是接口面的运行时事实来源, 并配一条一次调用跑通的写入路径

Status: implemented

## Problem

让 AI 或外部程序配置 HX-ProxyGroup, 唯一的办法是**读 Go 源码**: 每个端点的字段名、
哪些字段是封闭枚举、枚举有哪些值, 只存在于 `internal/api` 和各自的校验器里。
前端能工作是因为它和这些字段一起演进; 外部调用者没有这份上下文, 只能靠猜 —— 猜错的
代价是 `422`, 而且错误信息不会告诉它正确取值。

这带来两个具体后果:

1. **字段名与枚举值不可发现**。`proxy_group_strategy` 有六个值, 但没有任何一次 API
   响应会把它列出来。
2. **"配好一个能用的代理" 不是一次调用**。`POST /api/v1/proxy-services` 已经能把
   「建组 + 建 Listener」合成一步, 但**订阅的创建与刷新不在其中**。而顺序是硬约束:
   订阅没刷新时节点根本还不存在, 此时建组会选中空集合, 发布出一个**能连上但什么都不
   转发**的 Listener —— 从外部看它是成功的, 只有流量不通。

## Decision

**1. 把接口面当数据发布。** `GET /api/v1/capabilities` 返回
`internal/api/capabilities.go` 定义的目录: 命名空间划分、认证方式、**封闭枚举**、
每个端点的字段表(名字/类型/是否必填/非法值示例)、**一个被接受的完整示例**、错误码、
以及**产品边界** `not_supported`。

关键约束是**枚举值不是抄来的**: 每个 `CapabilityEnum.Values` 直接调用它对应校验器的
访问器(`listener.SupportedKinds()`、`proxygroup.SupportedStrategies()`、`residential.Supported*`、
`nodeparse.SupportedProtocols()`)。因此目录**在结构上不可能**宣告一个服务端会拒绝的值 ——
不是靠人记得同步, 而是两者本来就是同一个函数。

目录挂在 `/api/v1/` 下(管理空间, 需凭证), **刻意不放进公开 token 命名空间**:
它枚举的是管理写接口。

**2. 补上一次调用。** `POST /api/v1/quickstart` 把
「注册订阅 → 刷新 → 建组 → 建 Listener」合成一步, 并把结果里消费者真正需要的三样东西
显式返回: `share_path`、`consumer_nodes_path`、以及 `auth`。

**刷新发生在建组之前**, 这是这个端点存在的理由。此外它还会:
- 调用者没给凭据时**生成**凭据。共享入口模式按用户名路由成员, 没有凭据的成员会被拒绝 ——
  所以"一次调用"必须替调用者补上, 否则它只是把 `422` 换了句话说。
- 任何一步失败都**删掉这次调用自己创建的东西**(订阅/组/Listener), 所以报错后可以放心重试。

**3. 配套 skill。** `.agents/skills/hx-proxy-admin/`(`/hx-make-skill` 生成)把
"先读目录再写请求" 变成可执行的流程, 附 `scripts/hx-catalog.py`, 它**在运行时读目录**:
`enums` 列词汇表, `show` 展开一个端点, `check` **本地**校验字段名与枚举(不发坏请求),
`call` 校验后发送, `quickstart` 一次跑通。

**4. 口径同步是门禁, 不是纪律。** 两层:
- Go 测试 `TestCapabilityCatalogEnumsAreLiveVocabulary` 断言目录枚举与校验器访问器
  **逐序相等**(这是上面第 1 条得以成立的原因);
  `TestCapabilityCatalogCoversRegisteredAdminRoutes` 断言每个已注册管理路由都被目录覆盖。
- `scripts/verify-admin-catalog.ts` 覆盖 Go 测试看不见的那条缝: skill/脚本是否引用了
  目录里不存在的路径、脚本是否偷偷自带一份硬编码端点表、产品边界是否还在、
  `CapabilityCatalogPath`/`QuickstartPath` 常量与目录是否一致。
  已接入 `.github/workflows/agent-notes.yml`, 与 consumer contract 门禁并列。

## Alternatives considered

**什么都不做, 让调用者读源码 / 直接问维护者。** 对内部改动这是零成本, 也是现状能维持的原因 ——
前端确实不需要目录。但它把"接口面长什么样"变成只有读得到源码的人才知道的事,
而这正是本次要解决的问题; 且枚举有六个值、端点有二十多个, 问答方式每次都要重新问一遍。否决。

**复用 OpenAPI / Swagger 自动生成。** 它是最标准的答案, 而且能顺带给第三方工具用。
但 Go 标准库没有官方 OpenAPI 生成器, 引入它意味着给一个依赖极少的仓库加一条构建链;
更关键的是 **OpenAPI 描述不了本项目最需要表达的东西** —— 枚举必须从校验器读出来才不能漂,
而生成的 schema 会把枚举抄成第二份真相。产品边界(`not_supported`)也不是 OpenAPI 的概念。否决。

**只加 `quickstart`, 不加目录。** 这一条能立刻解决"配好一个代理", 成本最低。
但 `quickstart` 只覆盖最主流的那条路: 要复用多个订阅、要组合多个组、要用住宅渠道的调用者
仍然要猜字段名; 而且 `quickstart` 自己也需要字段表才用得明白。它解决终点, 不解决词汇表。否决。

**把枚举值写进 skill 文本, 由文档门禁保证一致。** 纯文档方案, 不用动 Go。
但那条门禁只能检查"skill 里出现的值在 Go 里也存在", **查不出 Go 新增了一个枚举值而 skill 没写** ——
缺口方向正好是最容易出问题的那个方向。目录方案把真相放在运行时读取, 两个方向都不需要人盯。否决。

## Consequences

- **接口面多了一个需要维护的公开文档**。加管理端点时要同步更新目录, 否则
  `TestCapabilityCatalogCoversRegisteredAdminRoutes` 会失败。这是刻意选的失败方向:
  宁可门禁报错, 也不要 AI 读到一个不存在的端点。
- **目录只增不改不删**是它对调用者的承诺。字段改名/删枚举值属于破坏性变更, 要开新版本路径。
- `not_supported` 把产品边界写成了机器可读的文本, 其中三条容易被误传的能力被明确否定:
  控制面不承载流量、不替消费者轮询/选点、**链式代理目前只支持住宅渠道**
  (`upstream_proxy_group_id` 只存在于住宅供应商配置)。
- 监控类、终端类、数据面诊断类路由**刻意不进目录**, 由
  `observedOnlyRoutes` 显式列出。它们只报告状态、不改变配置, 列进去只会稀释这份
  "Agent 第一个读的文档"; `TestObservedOnlyRoutesStayExempt` 保证这个豁免不会
  在某天悄悄失效。
- 目录里 `browser_compatible` 的当前口径**只看 `transport==tcp`, 不看 `tls`**,
  所以 http/mixed-over-TLS 会被报成兼容, 而 Chromium/Firefox 实际拒绝 HTTPS 代理。
  这是已记录的已知偏差, 不在本次改动范围内。

## Testing

- `go test ./internal/api/ -run 'TestCapabilityCatalog|TestCoreAdminRoutes|TestCatalogEndpoints'`:
  枚举与校验器逐序一致、已注册路由全部被覆盖、豁免表不失效。
- `go test ./internal/quickstart/`: 刷新先于建组、失败路径回滚订阅、`subscription_ids`
  路径不重复刷新、默认只绑环回。
- `node scripts/verify-admin-catalog.ts`: 已用注入式漂移(给 skill 加一个不存在的路径、
  给目录加一个未声明的枚举引用)**双向验证过非空洞** —— 注入即失败, 还原即通过。
- `uv run scripts/validate_skill.py .agents/skills/hx-proxy-admin`: skill 结构合规。
- 端到端(真实进程 + 真 mihomo): `hx-catalog.py quickstart` 成功返回
  `share_path`/`nodes`/`auth`; 失败场景下组与 Listener 计数都为 0。

The catalog includes optional TUN settings, residential group reuse, network diagnostics/repair confirmation and transient connection sampling. Network operations describe their restricted actions and step-up boundary rather than advertising arbitrary shell execution.
