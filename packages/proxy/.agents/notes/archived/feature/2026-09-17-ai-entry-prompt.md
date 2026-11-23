# Agent Note: 「交给 AI」文案是可复制的入口, 且由门禁钉在运行版本上

Status: implemented

Archived: 2026-09-26

## Problem

接入这套控制面需要读文档, 而文档是**给别人看的**: `docs/RESIDENTIAL_AI_QUICKSTART.md`
开头就写着「给 AI / 自动化」, `.agents/skills/` 下有三篇 skill。但管理员在面板上
**看不到它们的入口** —— `install.sh` 只发布 `web/`、`deploy/systemd/` 和二进制,
`grep -c docs install.sh` 是 **0**, 生产机上根本没有 `.agents/` 和 `docs/`。
于是每次接入都退化成一次口头解释: 管理员知道要说什么, 但说不成一句可以复制的话。

用户的原话是「让用户复制粘贴一段话(很简单很短), 让 AI 能自己使用 skill 并且能理解
用户的诉求」。要解决的不是「写一段提示词」, 而是三个具体的失败:

1. **入口不可发现**。文档在仓库里, 但不在用户手边; 用户要先去 GitHub 找。
2. **诉求与文档的映射靠人记**。「接入代理」对应 `hx-consumer-api`, 「接入住宅代理」
   对应 `RESIDENTIAL_AI_QUICKSTART.md` —— 这层映射只存在于维护者脑子里。
3. **粘出去的东西会过期**。这是最要命的一条: 一段写死路径的文案, 在文件改名、
   或文件还没进 Release tag 时, 会变成别人会话里的一条 404 —— 而**管理员永远看不到**
   这个失败。前两次的教训已经写明这种漂移只能靠门禁, 不能靠纪律:
   `scripts/verify-consumer-contract.ts` 锁三方的词元, `scripts/verify-admin-catalog.ts`
   锁 skill 与目录。

## Decision

**1. 文案的事实来源是一个不导入任何模块的 TS 文件。** `web/src/lib/ai-entry.ts` 定义
场景注册表 (诉求名 / 一句话意图 / 该读哪些文档), 并导出 `buildAiEntryPrompt()`。
它**刻意不 import 任何东西**: `scripts/verify-ai-entry.ts` 直接 import 它来断言,
不需要 Vite、不需要 `npm ci`、不解析 `@/` 别名。文案与门禁读的是同一份数据,
所以「文案里的路径」和「门禁检查的路径」在结构上不可能分叉。

**2. 管理员的诉求是选项, 不是填空题。** 入口现为独立的「接入 AI」页 (`#/ai-entry`,
见 [live-source note](2026-09-17-ai-entry-live-source.md)): 选一个真实节点源,
文案里**直接写好**「我的诉求」与实时 `GET /nodes/<token>`。三个场景注册表仍在
`ai-entry.ts` 里, 作为文档索引与「配置控制面」这条不带令牌的入口。

**3. 文档地址钉在运行版本上, 而不是 main。** 这是本决策的核心, 也是唯一有技术难度的地方。
文案里的 URL 形如 `.../raw/<ref>/<path>`, `ref` 来自 `SystemInfo.version`。
如果钉 main, 一个 `v0.12.0` 的程序会让 AI 读到 main 上**尚未发布**的接口面 ——
正是要避免的「口径不一」。

但「钉版本号」有一个反直觉的边界条件, 门禁把它逼了出来: **文件可能不在那个 tag 里**。
`hx-proxy-admin/SKILL.md` 是本轮才加的, 它**不在** `v0.12.0` 里。钉上去就是死链。
所以 `AiEntryDoc.unreleased` 标记这类文件, 让它们回落到 main; 而门禁**不信任这个标记**,
直接问 git: `git ls-tree <newest-tag> -- <path>`。标记与事实不符 (无论哪个方向) 都失败 ——
下一个 Release 收进该文件后, 门禁会失败并逼人删掉标记, 地址自动钉回版本号。

**4. 本机有源码时给绝对路径。** 后端只在 checkout **确实含有所需文档**时才输出
`SystemInfo.source_root` (`resolvedSourceRoot()`): 生产机没有这些文件, 于是字段为空,
文案自动只剩 URL。有源码时文案额外附上绝对路径, 并说明「与运行版本完全一致, 优先读」。
`run.sh` 显式传 `--source-root "${SCRIPT_DIR}"`。

**5. 门禁查五件事**, 见 `scripts/verify-ai-entry.ts`: 路径在磁盘上存在且非空、已被 git
track (未 track 的文件在 GitHub 上不存在, `hx-proxy-admin` 就长期如此)、`unreleased`
标记与最新 tag 的事实一致、注册表自洽、以及**生成的文案真的带上每个场景的地址**
(拼字符串最容易错, 而单元测试看不见拼接结果)。

**6. 顺带修掉一个真实缺陷。** `web/src/lib/types.ts` 的 `CreateProxyServiceRequest` /
`UpdateProxyServiceRequest` 漏了 `dialer_proxy_group_id`, 而 `edit-proxy-service-form.tsx`
一直在传它 —— 后端 `proxyservice.UpdateRequest` 收这个字段 (链式代理那一轮加的),
类型却对不上, `tsc -b` 直接报 TS2353。表单的注释写明「这个表单不编辑链路, 但要原样带过去,
否则一次无关的保存会静默丢掉链路」, 所以漏掉类型不只是编译错误, 它会掩盖那条真实风险。

## Alternatives considered

**什么都不做, 让管理员自己解释。** 这是零成本的现状, 而且对一次性接入确实够用:
维护者自己就不需要这段文案。但接入是**重复发生**的 (每个新程序、每个新同事、每次换会话),
而且失败模式不对称 —— 解释错一个路径, 代价全在对方那边。用户明确要的是「很简单很短、
复制粘贴」, 否决。

**复用现有的 `docs/RESIDENTIAL_AI_QUICKSTART.md` 当作唯一入口。** 这是最省事的一步:
它本来就是写给 AI 的, 内容也对。但它只覆盖住宅代理一个场景, 「接入代理」和「配置控制面」
没有对应物; 而且它同样不在生产机上, 管理员依然拿不到。它成为文案**引用的对象**之一,
而不是入口本身。否决。

**把 skill 正文 embed 进二进制, 由端点直接吐给 AI。** 这是最强的同步保证: 磁盘上是什么,
AI 就读到什么, 中间没有 tag 这一层。先验证了 `go:embed`: **显式写文件名时可以嵌 `.agents/`**
(`//go:embed .agents/skills/x/SKILL.md` 编译通过且有内容), 但**目录模式会静默排除点目录**
(`//go:embed .agents/skills` 报 `no matching files found`, 加 `all:` 前缀也一样)。
于是只能逐文件列名, 而 `internal/` 内的包禁止 `..`, 得在仓库根新建一个非测试 `.go` 包,
再由 `install.sh` 把这些文件复制进发布包 —— 为了「一段提示词」改发布流水线, 代价明显过大。
当前方案用「URL 钉运行版本 + 本机源码优先」拿到同等效果, 零发布改动。否决。

**文案写死在组件里, 门禁正则扫字符串。** 不需要新文件, 改动最小。但那样路径就是第二份
真相, 门禁只能检查「文案里出现的路径存在」, 查不出**注册表里加了一个场景而文案没跟上**;
而且字符串拼接的结果仍然没人验证。事实来源集中到 TS 模块后, 两个方向都被覆盖。否决。

**把文案放 Settings 新开一个标签页。** 当时否决是因为「只为一段文字」; 后一轮变成
按节点源列出的动态列表, 用户选择独立「接入 AI」子页, 见 live-source note。此处保留
当时的理由, 以免下一次又把列表塞回 Settings。

**`source_root` 用环境变量 `HX_PROXYGROUP_SOURCE_ROOT` 自动探测 CWD。** 不新增 flag 更省事,
但 CWD 在生产上恒为 `/var/lib/hx-proxygroup` (systemd `WorkingDirectory`), 探测它等于靠巧合;
而 `resolvedSourceRoot()` 的两个探针文件让「有没有源码」变成**可验证的事实**而不是猜测。
否决。

## Consequences

- **加场景或改路径只改一处** (`web/src/lib/ai-entry.ts`), 页面与门禁自动跟随;
  但漏改 `unreleased` 会被门禁拦下, 这是刻意的失败方向: 宁可 CI 报错, 也不要在
  别人的 AI 会话里留一条 404。
- **文档索引本身不含凭据**; 选中接入源之后, 文案会带上 share token
  (见 [live-source note](2026-09-17-ai-entry-live-source.md))。`/ctl/` 仍不进这段文字。
- **`source_root` 泄露本机路径给已登录的管理员**。这是刻意的: 管理员本就能用终端,
  路径不构成新的权限边界; 而它换来的是 AI 读到与运行版本逐字一致的文档。
- **文案是中文的**。三个 skill 的 `description` 与正文都是中文, 场景名也是用户原话
  (「接入代理」「接入住宅代理」), 保持同一语言比翻译成英文更不容易产生歧义。

## Testing

- `npx tsx@4 scripts/verify-ai-entry.ts`: 五类断言全部通过。**已双向注入验证非空洞**:
  路径打错一个字 → 「does not exist」失败; 把版本解钉 → 「must pin the docs to that tag」失败;
  把场景的 doc 循环置空 → 「prompt omits …」失败; 还原即通过。
- 该门禁**当场抓到一个真实的死链风险**: `hx-proxy-admin/SKILL.md` 未被 git track,
  且不在 `v0.12.0` 里 —— 两者都会让复制出去的 URL 返回 404。前者由本次提交解决,
  后者由 `unreleased` 标记 + 门禁的 tag 断言解决。
- `go test ./cmd/hx-proxygroupd/ -run TestResolvedSourceRoot -v`: 完整 checkout 解析为自身、
  空目录与「只缺一个探针」都解析为空、占位目录不算文件、空白配置解析为空。
  **注入验证**: 删掉第二个探针 → 测试失败并指出缺的是住宅指南; 还原即通过。
- `npx tsc -b`: 通过 (并顺带修掉 `dialer_proxy_group_id` 的 TS2353)。
- `npx tsx@4 scripts/verify-admin-catalog.ts` / `verify-consumer-contract.ts`: 保持通过。
- `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`: 本 note 使覆盖率门禁通过。
- 浏览器实测(第一轮): About 页三个场景各复制一次。入口已迁到「接入 AI」页, 见 live-source note。
