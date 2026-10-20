# 已知限制与坑 (fp-audit)

> 这份文件记录**实测撞到**的坑, 以免日后重复踩。每条都注明发现方式。

## K1. 重启环境必须先 `stop <port>`, 不能只 terminate 父进程

**现象**: 给环境传了正确的 `--timezone Asia/Singapore`, 重启后 JS 时区**仍是**
`Asia/Shanghai` (2026-09-14 实测, `--expect-tz auto --fix-tz` 第一次跑就撞到)。

**根因**: `cloak_launch` 用 `start_new_session=True` 拉起浏览器 (它自己的注释说明了原因:
否则拉起它的脚本一结束浏览器就被带走)。副作用是浏览器**脱离父进程会话**,
于是 `subprocess.terminate()` 杀不掉它 —— 旧实例仍占着 CDP 端口, 新实例起不来,
而 audit 连的还是旧环境 → 「重启了但没生效」。

**正确做法**: 调 `cloak_launch.py stop <port>` (它按 `--remote-debugging-port` 精确定位,
先发 `Browser.close` 再按 `/proc` 匹配杀), 并**确认端口真的空了**再起新实例。

**一般化**: 任何时候"重启一个自守护(daemonized)的进程", 都要用它的**官方停止入口**,
不要假设 kill 父进程就等于停掉服务; 并且要**验证**旧实例真的没了。

## K2. `--expect-tz` 手工给错会**自己制造**时区不一致

**现象**: 手工传 `--expect-tz Asia/Tokyo` 而出口其实在新加坡 → pixelscan 报
`Timezone spoofed` (2026-09-14 实测)。这条**不是**被测环境的缺陷, 是测试者的输入错误。

**正确做法**: 用 `--expect-tz auto` —— 在**被测浏览器内部**查出口地理 (`ip-api.com`),
因为出口绑在浏览器上, 宿主 curl 可能走另一条链路, 查到的是错的时区。

## K3. amiunique 是 SPA, 等"长度"等不到数据

**现象**: 等 25s 后 `innerText` 仍只有 `MY FINGERPRINT / MY EXTENSION` 两行
(2026-09-14 实测), 探针 status=unknown。

**原因**: 它首屏就是导航壳, 真数据要异步算完才渲染。
**做法**: 等**内容特征** (`innerText.length > 800` 且含 `MY FINGERPRINT`) 而不是等长度阈值。

**注**: 即便取到, amiunique 的"唯一性"也**不参与红线判定** —— 人人几乎唯一,
那是隐私指标不是限流指标 (见 rules.NOISE_SIGNALS)。

## K4. pixelscan 报告是「标签 \n 取值」逐行排布, 不是 `label: value`

**现象**: 用 `re.search("Timezone from JS[:\s]*(...)")` 抓到的永远是**下一行的标签**,
不是取值 (2026-09-14 实测)。

**做法**: 按行对解析 —— 找到 label 行后, 取**其后第一个非空行**作为取值。

## K5. headless 必被 creepjs 识别 (这是模式问题, 不是缺陷)

**实测**: `creepjs` 对 headless 环境稳定报 `bot` + `headless`。
**含义**: `--launch headless` 只适合**验证工具链路本身**(快、不占桌面),
**不能**用来判断"实战环境是否达标" —— 实战环境必须用 `xvfb` 或 `headed` 跑质检。

## K7. headless 下 GPU 维度采不到, 多样性守卫必须降级不能假装通过

**现象** (2026-09-16 实测): `diversity.py` 用 headless 采样时,
`gpuVendor` / `gpuRenderer` 恒为 `None`, 但守卫一开始仍报了 🟢"关键维度确实变化"。

**为什么这是缺陷**: GPU 恰恰是 2026-09-14 事故中**会变**的字段之一
(`--fingerprint-gpu-renderer`)。守卫看不到它却宣称验证通过 —— 等于**假装验证过
没有验证的东西**, 与本工具"探针 unknown 不等于通过"的第一原则直接冲突。

**修法**: `judge()` 显式统计 `unavailable` 里的关键维度; 只要有关键维度采不到,
D1 就从 🟢 降为 🟡 并在 detail 里写明"有 N 个关键维度未能采集, 验证不完整",
fix 指引改用 xvfb/headed。**在场维度全变了 ≠ 指纹隔离已验证**。

**一般化**: 一个"证明差异"的工具, 必须区分"证明了存在差异"与"证明了不存在差异"。
看不到的维度永远不能计入"相同", 也永远不能让结论升级为"通过"。

## K6. WebGL 为空会同时拖垮多项

**实测**: headless 下 pixelscan 报 `WebGL Vendor = -` / `WebGL Renderer = -`,
且 creepjs trust 分同时偏低。已加 R10 红线覆盖此情形 (真实浏览器不应为空)。
