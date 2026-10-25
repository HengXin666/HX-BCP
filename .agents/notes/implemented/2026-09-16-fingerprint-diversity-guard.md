# Agent Note: 指纹多样性必须由工具自动证明, 且"没看全"不等于"通过"

Status: implemented

- 影响: `workspaces/browser-auto/fp-audit/` (新增 `diversity.py` + `probes/diversity.py`;
  `README.md` / `KNOWN-ISSUES.md` 同步)

## Problem

2026-09-14 的事故: 用户质疑「每次都换完整的指纹浏览器吗?」, 实测发现 3 个不同 seed 的
`platform / UA / cores / screen / WebGL` **6/7 字段完全相同** —— 根因是启动器硬编码了
`--fingerprint-platform=windows`,`--fingerprint=<seed>` 只改 canvas 与 WebGL renderer。

当时的修复 (`derive_fingerprint`) 让指纹真的随 seed 变化了, 但**缺一个守卫**:
这类缺陷的特征是"参数传了、代码看着对、结果却没变", 只靠读代码或读启动参数都发现不了。
同一个坑还会以别的形式复发 (例如日后改了派生逻辑却漏改某个维度)。

本次实现守卫时, **守卫自己就犯了同型错误两次** —— 这正是写这篇 note 的主要理由:

1. headless 下 `gpuVendor` / `gpuRenderer` 恒为 `None` (采不到), 而守卫仍报 🟢
   "关键维度确实变化"。GPU 恰恰是事故中**会变**的字段之一 → 等于**假装验证过
   没有验证的东西**。
2. 修好 `judge()` 的降级后, CLI 末尾的汇总仍打印 🟢 —— 又一次"两处判据不同步"。

## Decision

新增 `diversity.py` + `probes/diversity.py`: 用 N 个不同 seed 各起一个环境,
在每个环境里**从浏览器实际读值**并两两比对, 判定指纹是否真的不同。

- **只读实际值, 不信任输入**: 采样走 CDP 在页面内取 `navigator/screen/WebGL/时区` 等。
  这是事故的直接教训 —— 根因就是"参数传了但没生效"。**守卫必须观测结果。**
- **采集不到的维度必须显式统计, 并降级结论**: 只要有 `CRITICAL` 维度落在
  `unavailable` 里, D1 就从 🟢 降为 🟡, detail 里写明"N 个关键维度未能采集,
  验证不完整", fix 指引改用 xvfb/headed。**在场维度全变了 ≠ 指纹隔离已验证。**
- **汇总与细则必须同源**: CLI 末尾结论按 `compare` 的 level 走 (含 yellow 分支),
  不允许再出现"细则说 🟡、结论说 🟢"这种两处不一致。
- 判据: D1 关键维度 `platform/ua/cores/screen/gpuVendor/gpuRenderer/timezone`
  全部相同 = 🔴 (事故同型); D2 整体变化面 <20% = 🟡。退出码 0/1/2 可当闸门。

### 能力边界 (写进代码注释, 不是文档补丁)

只能验证**本工具采集到的维度**。经 CDP 在页面内读, 故看得到 JS 可见维度,
但**看不到** TLS 指纹、HTTP/2 帧序、字体逐字节顺序等非 JS 维度。
所以"守卫通过" ≠ "指纹完全隔离", 只等于**"JS 可见维度上确实换了"**。
把边界写在 `probes/diversity.py` 的模块 docstring 里, 是为了让它随代码被读到的概率最大化。

## Alternatives considered

1. **什么都不做 / 复用现有的 `--fingerprint` 参数检查** —— 最强理由: 事故已在 09-14 修复,
   代码里也有注释解释; 而这个守卫要起多个浏览器 (慢、耗资源), 属于"已知结论的重复验证"。
   **否决**: 正是本次实现过程证明了这个理由不成立 —— 守卫**自己**就两次得出错误结论
   (headless 采不到 GPU 却报通过、汇总与细则不一致)。"已知结论"如果没有自动守卫,
   下次改动派生逻辑时没有任何东西会拦住回归。而且该缺陷的特征就是"读代码看不出来"。
2. **读启动参数来验证** (解析 `/proc/*/cmdline` 拿 `--fingerprint-platform` 等) ——
   比起浏览器快得多, 也不用 CDP。
   **否决**: 这正是事故的成因 —— 参数在、值也对, 但结果是恒定的。
   守卫若也读参数, 就会对事故**报通过**。必须观测结果。
3. **把采样维度扩到几十个 (TLS/字体/HTTP2 等)** —— 更全面。
   **否决**: 那些维度经 CDP 页面内 JS **根本取不到**, 需要抓包/端侧方案, 属另一个工具的
   范畴。与其塞半个不可靠的实现, 不如**明确写下边界**(已写进 docstring)。
4. **headless 下采不到 GPU 就直接判 🔴** —— 更"严格", 不会漏。
   **否决**: 会把"工具环境限制"误报成"被测环境有问题", 制造狼来了。
   正确做法是区分**三种**状态: 变了 / 没变 / **没看到** —— 第三种给 🟡 并说清楚。
   这与 `rules.py` 里"探针 unknown 不等于通过"是同一条原则。
5. **只保留 `--check-baseline` 基线比对, 不做两两比对** —— 更贴近实际用法
   (检查"某账号指纹是否被动过")。
   **否决**: 不是二选一。基线已实现 (`BASELINE.json`), 但**多样性的原始问题是
   "不同 seed 之间是否不同"**, 那必须两两比对才答得了; 基线单点值证明不了多样性。

## Consequences

- fp-audit 现在有两道闸门: `audit.py` (环境好不好用) + `diversity.py` (指纹换没换)。
- **在 headless 下 `diversity.py` 永远只能给出 🟡** —— 因为 GPU 维度采不到。
  这是刻意设计: 它如实反映"验证不完整", 而不是给一个让人安心的假 🟢。
  要拿 🟢 必须用 xvfb/headed。
- 两次"守卫自己犯错"都记入 `KNOWN-ISSUES.md` K7, 一般化为一条通则:
  **一个"证明差异"的工具必须区分"证明了存在差异"与"证明了不存在差异";
  看不到的维度永远不能计入"相同", 也永远不能让结论升级为"通过"。**
- 未做: 未把 `diversity.py` 接进注册流水线做自动拦截 (同 `audit.py`, 见 README 待办)。

## 关联

- 代码: `fp-audit/diversity.py` · `fp-audit/probes/diversity.py` (边界写在 docstring)
- 文档: `fp-audit/README.md` (用法+判据) · `fp-audit/KNOWN-ISSUES.md` K7 (两次自犯错误)
- 事故来源: `.agents/notes/implemented/bug-fix/2026-09-14-fingerprint-must-actually-vary.md` ·
  `workspaces/browser-auto/knowledge/gh-registrar/ERROR_LESSONS_指纹系统没在换指纹-2026-09-14.md`
- 同源原则 (unknown 不等于通过): `fp-audit/rules.py` 的 R9 ·
  `.agents/notes/implemented/architecture/2026-09-14-fp-audit-standalone-quality-gate.md`
- 触发: 用户「做好项目上下文, 写到根 AGENTS.md, 然后 commit 开始下一个任务」后的自主推进
