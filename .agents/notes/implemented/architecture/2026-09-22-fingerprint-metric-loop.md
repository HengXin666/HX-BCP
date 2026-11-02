# Agent Note: 指纹度量回路独立成 fingerprint 目录, 并确立「ja4 才是身份」

Status: implemented

## Problem

用户要求「把这些都做好文档沉淀, 缺少的功能补充」。复盘后发现, 前两次指纹相关结论
**都缺一个可复验的度量动作**, 因而都只能停在「看起来对了」:

1. **2026-09-14 换 seed 没换指纹** —— 3 个 seed 的 `platform/UA/cores/screen/WebGL`
   6/7 字段完全相同。当时没有字段级 diff, 是人眼比对+临时脚本才发现的。
2. **2026-09-17 `impersonate=chrome146` 与浏览器真身不同** ——
   `knowledge/hx-capture-proxy/RESEARCH_TLS_FINGERPRINT_2026-09-17.md` §2 已测出
   `6fb2220e` vs `4bbab334`, 但那份表的最后一列写着「**待验**」:
   「这和 curl_cffi 一致, 不代表和真浏览器一致 —— 需要对照真身复测」。
   也就是说, 全仓**没有任何动作能回答「我现在的协议层像不像本机浏览器」**。

2026-09-22 补测时又撞到第三个坑, 它反转了一条本会被写进纪律的判据:

3. **同 `impersonate="chrome"` 连发 3 次, ja3_hash 三次全不同**
   (`fec60be9`/`b003a6f3`/`74e8242c`)。若照直写下「ja3 每次都变 = 伪装失败」,
   结论会**正好错反**: 实测本机 **Chrome 153 headless 同样如此**
   (`e9a8910b` vs `d5c476f4` 两次)。去 GREASE 也不够 —— 逐段比对显示
   Chrome 还会随机化**扩展顺序** (groups 段去 GREASE 后顺序仍不同, 集合相同)。

所以缺的不是一条结论, 而是**一个能反复回答「变了没有」的回路**。

## Decision

新建 `workspaces/browser-auto/fingerprint/` 作为跨层度量工具 (**只测不改**, 不做任何伪装):

- `probe.py protocol` —— 协议层: 一次跑完 curl_cffi 各档位 + 裸 curl 对照,
  记录 ja3/ja3n/ja4/peetprint/akamai h2, 并**自动采集本机 Chrome 真身基准**。
- `probe.py browser` —— 浏览器层: 对任意 CDP 端点取 UA/平台/时区/字体数/webdriver/WebGL。
- `probe.py compare` —— 跨层一致性: C1-C8 判据, 红黄绿 + 退出码可当闸门。

### 一、ja4 是身份, ja3 不是

写进代码与文档的硬判据:

- 跨请求比较**一律用 ja4** (实测同档位 ja4 三次恒定)。
- ja3n (去 GREASE) **同样不稳定**, 只把它当"必要条件不充分"的证据保留。
- `--repeat` 的稳定性输出把 `ja3 恒定=False` 标注为**期望值**, 并写明理由,
  防止下一个 agent 把它当缺陷去"修"。

### 二、真身基准是本机 Chrome 现采的, 不是抄文档的

`detect_chrome()` 用本机 `google-chrome-stable --headless=new --dump-dom` 采一次,
得到 `t13d1517h2_8daaf6152771_cb7bf5808d99`。这个值与 FoxIO 官方 JA4 文档里的
Chrome 示例**逐字节相同** —— 所以它是可引用的家族基准, 不是自造数据。
无 Chrome 时允许 `--reference` 手动给, 取不到则记**灰**而不是静默放过。

### 三、三条判据写死在代码里 (依据 fp-audit 的同类教训: 判据必须可执行且集中一处)

- **灰不是绿**: 没取到读数 `!= `通过。退出码区分「有红线 (1)」与「什么都没测出来 (2)」,
  且只有**拿到绿项**才算通过 (原来写成「全灰也算 0」, 已被本次实现改掉)。
- **人工时区必须标注来源**: 出口探测失败退回 `--expect-tz` 时显式打
  `[人工指定(出口探测失败)]`。人工值与出口真值不是一回事, 混用会造出假绿
  (2026-09-12 栽过: 填东京而出口在新加坡)。
- **出口 IP 必须做端口归一**: peet 的 `ip` 带端口, `cdn-cgi/trace` 不带,
  直接比会假报警 (实测第一次跑就触发了)。

### 四、明确不做的事

本目录**不实现任何伪装**。修复动作留在启动器 (cloak_launch / js-reverse-mcp 参数)。
把它做成"又能测又能改"会重演 `cacheproxy` 的错误 —— 那个组件既抓包又 MITM,
最后因为制造指纹泄漏被判为负资产 (`ERROR_LESSONS_github-register-2026-08-23.md` E2)。

## Alternatives considered

1. **什么都不做 / 复用 hx-capture-proxy 的既有实测表** —— 最强理由:
   `RESEARCH_TLS_FINGERPRINT_2026-09-17.md` 已有协议层对照表, 且它的核心结论
   (「不要在回源路径重建 TLS」) 已经落地; 再建一个目录有重复之嫌。
   **否决**: 那张表的数值是**一次性快照**, 没有任何机制保证它下次还对;
   而它自己留的「待验」一项 (h2 层与真身是否一致) 正是本次实测才补上的
   (结论: curl_cffi 各 Chrome 档与真身的 `akamai_h2_hash` 完全一致, `52d84b…`)。
   快照会腐烂, 回路不会。
2. **塞进 `fp-audit/` 作为子命令** —— 少一个顶层目录, 且两者都是"质检"。
   **否决**: 两者**测的对象不同**。fp-audit 测**浏览器环境** (creepjs/pixelscan 等第三方站),
   `probe.py` 测**协议栈** (TLS/h2), 后者根本不需要浏览器也能跑。混在一起会让
   "环境干净"与"协议像不像"两个正交结论互相掩盖 —— 实测中二者恰好相反:
   同一时刻协议层能与真身对齐, 而 headless 浏览器层一次就报 3 条红线。
3. **塞进 `antibot/`** —— 那里已有 fpscanner/turnstile 等靶场与协议重放资产。
   **否决**: `antibot/` 是**攻击**伞目录, 回答「怎么过这个检测」; 本工具回答
   「我改的这一下到底生效没有」。用途相反, 混用会重演 fp-audit 与 antibot 的分家理由。
4. **只做协议层, 浏览器层交给 fp-audit** —— 避免功能重叠。
   **否决**: `C3` (协议层与浏览器层 UA 平台不同族) 与 `C4` (时区一致性) 是
   **跨层**判据, 必须由同一份报告同时持有两侧输入才能判。拆开就永远判不出来,
   而这恰恰是 gh-registrar 栽过的坑 (Windows UA 覆盖在 Linux 宿主 -> 字体列表为空)。
5. **把 ja3 也纳入"必须稳定"的判据** —— 更符合直觉: 指纹就该稳定。
   **否决**: 实测直接证伪 —— 真 Chrome 的 ja3 每次也变 (110+ 起随机化 ClientHello)。
   按直觉写会得出「真浏览器也是假浏览器」的荒谬结论。这条已作为纪律写进 README 与 docstring。

## Consequences

- 新增顶层目录 `workspaces/browser-auto/fingerprint/`, 已登记 `docs/PROJECT-MAP.md`。
- **补上了 2026-09-17 的「待验」**: curl_cffi 的 Chrome 档与本机真身的
  `akamai_h2_hash` **完全一致** (`52d84b11737d980aef856699f885ca86`),
  h2 层不是当前泄漏点。同时确认 curl_cffi Chrome 档与本机真身的 **ja4 版本位不同**
  (`1516` vs `1517`, 扩展计数), 且 **UA 平台不同族** (macos vs linux) —— 后者是新的、
  之前没记录过的风险点, 已变成 `C3` 判据。
- 端到端实测一轮 (headless Chrome 153 + 临时 profile): C1 绿 / C2 红 (fonts=0) /
  C3 红 (macos vs linux) / C4 红 (出口 America/Los_Angeles vs 浏览器 Asia/Shanghai) /
  C5 绿 / C6 绿 / C7 黄 / C8 黄, 退出码 1。红线数与真实缺陷一一对应。
- 实现过程中被自己的代码抓到 4 处缺陷并修复, 全部属"判定写错导致结论反向"类:
  ja4 正则把第 3 字符写成 `\d` (实际是字母 `d`/i) 导致恒判"缺失";
  `compare` 忘了把 protocol 报告里的 `reference` 传进判据, C8 永远报"无参考真身";
  出口 IP 端口未归一导致 C5 假报警; `--expect-tz` 参数声明后未注册到 browser 子命令。
  —— 这四条正是「全绿不等于功能可用」的实例: 只有真跑一次端到端才会暴露。
- 依赖: `curl_cffi` + `websocket-client`, 由 `pyproject.toml` 声明, `uv run` 直接跑。
- 未接入: 尚未接进 `gh-registrar` 的 `ensure_signup_ready`, 与 fp-audit 的同类待办合并。
- 已知边界: 真身基准依赖 `--dump-dom` 解析, 站点改版会降级为灰项 (不会静默变绿)。

## 关联

- 代码: `workspaces/browser-auto/fingerprint/probe.py` (protocol/browser/compare 三子命令)
- 文档: `fingerprint/README.md` (六层模型 + 判据表 + 实测数据) ·
  `knowledge/capture-tools/MCP-CAPTURE-TOOLING-2026-09-22.md` (抓包工具 x AI 接口层选型)
- 前序: `knowledge/hx-capture-proxy/RESEARCH_TLS_FINGERPRINT_2026-09-17.md` (§2 的待验项由本工具补上) ·
  `.agents/notes/implemented/bug-fix/2026-09-14-fingerprint-must-actually-vary.md` ·
  `.agents/notes/implemented/architecture/2026-09-14-fp-audit-standalone-quality-gate.md`
- 触发: 用户要求「这些都做好文档沉淀了吗? 缺少的功能请补充, 并说明如何使用」
