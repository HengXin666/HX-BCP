# Agent Note: 接入说明只有一个正规形态 —— skill, 前端不再复制提示词

Status: implemented

## Problem

「接入 AI」页让管理员选一个接入源, 由 `web/src/lib/ai-entry.ts` **拼出一段中文提示词**
交给外部 AI, 内容是「记得去读下面这几个文档地址」。它花了两轮迭代做到自洽: 文档 URL 钉在
运行版本 tag 上、实时节点清单 URL 带上 share token、还有一个
`scripts/verify-ai-entry.ts` 门禁盯着路径不漂移
(见 [prompt note](../../archived/feature/2026-09-17-ai-entry-prompt.md) 与 [live-source note](../../archived/feature/2026-09-17-ai-entry-live-source.md))。

用户否定了整件事: 「之前我们还采用的就是说在前端页面上提供一个提示词复制的功能。现在我们
已经有 skill 了，直接内化到 skill 上去，不要再有这种提示词了，根本没用」。

这不是「文案写得不好」, 是**形态选错了**。skill 与提示词是同一件事的两个版本, 而提示词是劣化版:

1. **提示词是 skill 的一次性快照。** 它把「AI 该知道什么」编码成一段自由文本, 而 skill 是那份
   知识的**唯一事实来源**。两者同时存在时, 快照必然落后, 而且落后是静默的 —— 提示词由
   `buildAiEntryPrompt()` 拼装, 只要没人在拼接里加字段, 它就跟不上 skill 的新内容。
   本仓库已经为这件事写过两轮门禁, 那本身就是不对劲的信号: **需要门禁去追平的重复, 应该被删掉而不是被看住。**
2. **提示词要求 AI 先学会「去读文档」, 而 skill 是直接被加载的。** 复制粘贴的路径是
   人 -> 剪贴板 -> 某个 AI 会话。任何支持 skill 的宿主都直接读 `.agents/skills/`, 中间那一跳
   只增加了失败面 (管理员忘了粘、粘到了不认 skill 的对话框、粘的是过期版本)。
3. **它把凭据塞进了剪贴板。** 实时节点 URL 含 share token (等同订阅 URL), 提示词明确要求管理员
   把它粘给外部 AI。而消费方真正需要的是**一条可吊销的活链接**, 那件事后来由消费方看板承担。
4. **它替 AI 做的判断是错的。** 提示词里写着「控制面不探测、不选点、不轮询」—— 这句话 skill
   里本来就有, 而且是**同一份结论的第二份副本**。真正的风险在于: 哪天控制面的职责边界变了,
   提示词会继续用一个过时的口径去指挥别人的 AI。

## Decision

**前端不再生成、不再复制任何提示词。** `web/src/pages/ai-entry-page.tsx`、`web/src/lib/ai-entry.ts`
整个删除, 挂载它的「关于」页 tab 一并去掉, `web/src/App.tsx` 不再注册 `ai-entry` 路由与侧边栏项。
接入说明只有两个正规形态: `.agents/skills/hx-consumer-api/SKILL.md` (消费方视角) 与
`.agents/skills/hx-proxy-admin/SKILL.md` (管理视角)。

**提示词里真正有价值、而 skill 尚未写明的三条事实被并入 `hx-consumer-api`** —— 并入而不是新建 skill,
因为这三条都属于「程序内接入」这一个诉求, skill 本来就有对应的章节:

1. **§1 新增: 照抄完整 URL, 不要自己拼 host + `/nodes/`。** 控制面常被反向代理挂在子路径下
   (例如 `https://panel.example.com/proxy/hx-proxygroup/nodes/<token>`), 前缀是路径的一部分。
   丢掉它请求会落到面板自己的 SPA 兜底, 拿到 `<!doctype html>` 而**状态码是 200** —— 这是原来只有
   提示词才会说的一句实话, 也是本轮最实质的补充。
2. **§1 新增: 节点清单是每次任务开始时重新 GET 的东西, 不是一次性配置。** 提示词反复强调
   「实时」, skill 原文只说了「别缓存」, 没有说清代价(订阅一刷新节点就变, 写死等于钉在昨天的出口上)。
3. **§4 改写: 换出口的两个入口与它们的取舍。** 原文只推荐 `/ctl/<control-token>/nodes/<index>/next`。
   实际上消费方还有 `POST /rot/<rotate-token>/next`, 而提示词**恰恰把消费方引向了它**。
   两者语义不同, 必须写清: `/ctl/` 按**声明节点**换、可配 `lease_id` + `expected_alloc_version` CAS
   与租约; `/rot/` 按**渠道**换、推进渠道级游标、**不区分调用者**, 只在「你是该渠道唯一消费者」
   或对接历史集成时才成立。多服务共用同一渠道时 `/rot/` 会互相踩 —— 这条事实此前只存在于
   `docs/RESIDENTIAL_INTEGRATION_STANDARD.md`, 而提示词给的是相反的建议。

**与之配套的专属管道一并删除**, 不留只能追平历史的死代码:

- `scripts/verify-ai-entry.ts` 门禁脚本, 以及 `.github/workflows/agent-notes.yml` 里调用它的那一步;
- 后端整条 `source_root` 链路: `SystemInfo.SourceRoot`、`config.Config.SourceRoot`、
  `--source-root` flag、`resolvedSourceRoot()`、`HX_PROXYGROUP_SOURCE_ROOT`、
  `run.sh` 的对应传参、`cmd/hx-proxygroupd/main_test.go` (它只测这个函数)、
  前端 `SystemInfo.source_root` 字段。**它唯一的消费者就是那段提示词** —— 用来给管理员
  「本机源码绝对路径, 优先读」。删除 `web/src/lib/ai-entry.ts` 后, 实测全仓再无第二个读取方。

## Alternatives considered

**什么都不做, 保留提示词复制。** 它已经工作, 有门禁看着, 而且对**从不使用 skill 的宿主**
(纯网页对话框) 确实是唯一办法 —— 这是它最强的理由。但它输在两条: 用户明确否定;
以及它维护的是 skill 的**副本**, 副本越多、口径越容易分叉, 而分叉的代价落在别人的会话里。
更关键的是, 当同一件事有两种形态时, 人们会继续往错误的那一种里加功能 (本轮就是为了让提示词
「更实时」而给它接上了 share token), 删掉错的形态比看住它便宜。

**保留页面, 但改成「显示 skill 的路径 / 一键复制 skill 的安装命令」。** 这是最省事的折中:
不删 UI, 只换内容, 还保住了「入口可发现」这个原始诉求。否决理由是它没有解决任何一条根因 ——
skill 仍然要靠人工转述给别人; 而「这条链接指向哪个 skill」这件事在 skill 自己的 `description`
里已经写好了, 派发是宿主/工具的事, 不是控制面该在 UI 上再存一份映射的事。

**把提示词正文 embed 进二进制, 由一个端点直接吐给 AI。** 这是同步性最强的方案, 上一轮已经
验证过 `go:embed` 显式列文件名可以嵌 `.agents/`。它保住了「复制一段即可」的可用性, 同时
消除漂移。否决: 它把「控制面要维护一份 skill 的镜像」这件事固化成产品能力, 而正确方向是
**让 AI 直接读 skill**, 不是让控制面学会复述 skill。而且它要继续背着发布流水线改动。

**只删 UI, 不动后端 `source_root`。** 改动面最小, 且 `source_root` 不算有害。否决: 一个
「只在有源码时才有值」的字段, 在没有消费者之后只会让下一个人以为它还有用途; 实测 grep 确认
零读取方后删除, 属于同一次改动的收尾, 不是额外风险。

**把三条事实留在 `docs/CONSUMER_INTEGRATION_CONTRACT.md` 里, 不动 skill。** 契约文档确实是
节点清单的权威, 而子路径那条也算契约的一部分。否决: 契约文档锁的是**字段与状态码**, 而这三条
是**该怎么用**的纪律 (照抄 URL、重拉节奏、两个换出口入口的取舍); 消费方真正会被加载的是 skill,
写成 skill 才是「内化」。

## Consequences

- **接入说明少了两处副本。** 全仓再无 `ai-entry` / `AiEntry` / `buildAiEntryPrompt` 标识符
  (除本 note 与已归档的两篇历史 note 的正文叙述)。
- **「入口可发现」这个原始诉求没有被替代方案接住。** 管理员现在无法从面板上直接拿到一段
  可复制的接入话术 —— 这正是用户的判断: 那段话本来就是没用的。派发 skill 由宿主承担。
- **`source_root` 从 API 响应里消失。** 已登录的管理员不再能通过 `GET /api/v1/system/info`
  读到控制面的 checkout 路径。这是减少信息暴露, 不是权限边界变化。
- **`/rot/<rotate-token>/next` 的地位被写入 skill: 兼容接口, 不是新代码的首选。**
  这与 `docs/RESIDENTIAL_INTEGRATION_STANDARD.md` 的既有结论一致, 此前只是 skill 没写。
- **`hx-consumer-api` 的 §4 现在是一张对照表**, `verify-consumer-contract.ts` 的同步词元集
  (端点、字段名、protocol 枚举、状态码) 不受影响 —— 本轮新增的是路径引用与散文纪律, 不是词元。

## Testing

- `cd web && npx tsc -b`: 通过。删掉 `lib/ai-entry.ts` 与 `SystemInfo.source_root` 后无悬空引用。
- `cd web && npm run build`: 通过, 产物中不再有 `ai-entry-page-*.js` chunk。
- `go build ./... && go vet ./...`: 通过。
- `go test ./cmd/hx-proxygroupd/ ./internal/api/ ./internal/config/`: 通过 (删除
  `main_test.go` 即删除 `resolvedSourceRoot` 的全部测试, 无其他测试引用它)。
- 全仓 grep `ai-entry|AiEntry|AI_ENTRY|buildAiEntryPrompt` 在 `web/ internal/ cmd/ scripts/ .github/`
  与 `run.sh` 下: **零命中**。唯一保留的一处是 `web/src/App.tsx` 侧边栏数组上方那段说明性注释,
  它刻意写全路径以便读者找到本 note; 那是对历史的引用, 不是死代码。
- 全仓 grep `source_root|SourceRoot|source-root|SOURCE_ROOT` 在源码下: **零命中**
  (只剩本 note 与已归档 note 的正文叙述)。
- `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`: `tree` / `format` / `archive` / `coverage`
  四项通过, 本改动覆盖率门禁报 `25 guarded path(s) changed with a note in the same change`。
  `backlinks` 报 2 条失败, 但两条都指向**同一工作树里另一个并行改动**引用的
  `implemented/feature/2026-09-26-proxy-list-pool-subscription-source.md` —— 那篇 note 由该改动负责,
  与本决策无关; 本 note 自身无悬空链接。
