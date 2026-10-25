# Agent Note: 指纹环境质检独立成 fp-audit 目录, 而不是塞进 antibot

Status: implemented

## Problem

本工作区反复栽在同一件事上: 环境看着正常, 上线才发现被识别。两个具体教训:

1. **2026-09-14**: 改指纹时才发现「换 seed 其实没换指纹」—— 3 个不同 seed 的
   `platform/UA/cores/screen/WebGL` **6/7 完全相同**, 根因是启动器硬编码了
   `--fingerprint-platform=windows`。若早有统一质检, 这条在第一次改动时就该被抓到。
2. 判定「环境行不行」此前**没有统一口径** —— 靠临时脚本 + 人眼看输出,
   不同会话的判据不一致, 也无法回归比对。

已有的 `antibot/` 是**攻击**伞目录 (协议重放/破靶场), 回答的是「怎么过这个检测」,
不是「我这环境现在能不能用」。两者需要的东西相反: 攻击要 payload 与案例,
质检要稳定的红黄绿与修复动作。

## Decision

新建 `workspaces/browser-auto/fp-audit/` 作为**通用**质检环境 (不绑定任何业务):

- `audit.py` — CLI: 拉起/连接受测环境 → 跑四站 → 出红黄绿报告 + 修复动作;
  退出码 0/1/2 可直接当流水线闸门。
- `rules.py` — **规则引擎**, 把原始读数翻译成 R1-R10 判定。探针只采集不下结论,
  判定集中在一处 (本仓库栽过「判定逻辑散落各处、两处不同步」的跟头)。
- `probes/` — `cdp.py` 极简 CDP 驱动 (不引自动化框架, 避免框架自身注入污染被测对象)
  + `sites.py` 四站探针。
- 四站: creepjs (自动化/谎言/trust) · browserleaks (泄漏型信号) ·
  amiunique (唯一性, **不参与判定**) · pixelscan (**跨信号一致性**, 最有区分力)。

两条硬规则写进代码而不是只写文档:

- `NOISE_SIGNALS` 显式常量列出**无区分力**的信号 (`masking_detected` 等)。
  实测依据: 干净系统 Chrome 也被 pixelscan 判 Masking detected
  (`antibot/pixelscan/RESEARCH-2026-09-12.md`)。写成常量是为了挡住日后「顺手加回来」。
- **探针 `unknown` 不等于通过** —— 抓不到就给 ⚪ 并列出, 绝不静默放行。
  否则站点一改版红线就静默失效, 比没有红线更危险。

时区对齐做成了**自洽判定**: 首选从 pixelscan 报告里读出口时区 (经被测环境自己报出,
零外部依赖); `ip-api.com`/`ipapi.co` 实测都不可靠 (http 被拦 / 免费额度耗尽),
只作兜底。此前让人手填 `--expect-tz`, 实测会**自己制造**时区不一致 (填东京而出口在新加坡)。

## Alternatives considered

1. **什么都不做 / 复用现有临时脚本** —— 最强理由: 环境质检不是交付物, 每次临时写脚本
   也能得出结论, 而当前真正的瓶颈是**出口 IP 层封锁**(修质检不解决注册);
   且 `antibot/` 里已有 pixelscan 探针可复用。
   **否决**: 临时脚本的结论不可回归、判据不一致 —— 「换 seed 没换指纹」这条已经证明
   人眼+临时脚本会漏。把判据固化成代码与常量, 是唯一能防止它再犯的方式。
   且本次验证中, 该工具**立刻就抓到了 4 个真实问题** (headless 被识别、时区不一致、
   WebGL 字段缺失、重启不生效)。
2. **塞进 `antibot/` 作为子目录** —— 复用现有靶场目录, 少一个顶层目录。
   **否决**: 两者用途相反 (攻击 vs 自检), 混在一起会让「哪些是我们的资产、
   哪些是靶场」失去边界; 且 antibot 隶属 `antibot-protocol-replay` skill 的路由,
   而质检要对**所有** browser-auto 任务生效, 不该被 skill 路由限定。
3. **直接用现成工具 (如 fingerprintjs 的库 / puppeteer-extra-stealth 的检测页)** ——
   省开发。**否决**: 要的恰恰是**第三方站点的外部视角**(creepjs/pixelscan 等),
   自建检测页测不出真实站点的判据; 而 ref 实现引入自动化框架会**污染被测对象**。
4. **让每个探针自己判红黄绿** —— 代码更分散但也更"内聚"。
   **否决**: 判定逻辑必须集中, 否则又会退化成本仓库栽过的「两处判据不同步」
   (egress.py 的 gen/age 就是这么错的)。探针只采集, 判定在 rules.py。
5. **只做 creepjs 一站** (最关键的一站) —— 更快落地。
   **否决**: 四站各有不可替代的维度 (一致性/泄漏/唯一性/自动化), 且交叉验证是
   本工具的价值所在 —— 实测中 amiunique 失败、pixelscan 成功, 正是靠多站才定位清楚。

## Consequences

- 新增顶层目录 `workspaces/browser-auto/fp-audit/`, 已登记进 `docs/PROJECT-MAP.md`。
- 实测 5 轮, 每轮都发现真实问题 (headless 被识别 / 时区不一致 / WebGL 为空 /
  重启不生效 / geo 端点不可用), 全部记入 `KNOWN-ISSUES.md` 的 K1-K6。
- **已知边界**: `--launch headless` 必被 creepjs 识别 (K5) —— 它是用来验证工具链路
  与做快速回归的, **不能**代表实战环境; 实战质检须用 xvfb/headed。
- 时区判定现在有**两条**互补规则: R4b (JS 时区 vs 出口时区, 自洽, 无需外部查询)
  与 R5 (JS 时区 vs 用户指定的期望值, 需要人给对期望)。R4b 是主力。
- 还没有做的事: 未接入 gh-registrar 的 `ensure_signup_ready` (出口体检后自动跑红线),
  未建立 `reports/BASELINE.json` 做「指纹字段是否真的变了」的自动守卫。
  这两条写在 README 待办里, 是下一步的价值点。

## 关联

- 代码: `workspaces/browser-auto/fp-audit/` (audit.py / rules.py / probes/)
- 文档: `fp-audit/README.md` (用法+规则表) · `fp-audit/KNOWN-ISSUES.md` (K1-K6 实测坑)
- 前序教训: `antibot/pixelscan/RESEARCH-2026-09-12.md` (有区分力 vs 营销噪音的实测依据)
- 触发: 用户要求「实现一个通用环境, 专门做这个, 相当于日后的本地风控质检红线」

决策已随代码迁至 HX-BCP 的 .agents/notes/implemented/architecture/2026-09-14-fp-audit-standalone-quality-gate.md。
