# fingerprint

指纹采集与判定, 供自检与流水线评分使用。

> 把「我以为换了指纹」变成「我测到了指纹确实变了」。本目录是**度量**, 不是伪装。
> 分工: `fp-audit/` 管浏览器层质检红线 (环境能不能用), 本目录管**跨层一致性**
> (协议层说的话与浏览器层说的话是否自洽), 并给出同机真身基准做终审。

## 为什么需要它

本工作区栽过两次「以为换了, 其实没换」, 两次的共同形态都是**没有度量回路**:

| 日期 | 事故 | 缺少的度量 |
|---|---|---|
| 2026-09-14 | 换 seed 没换指纹 (platform/UA/cores/screen/WebGL **6/7 字段恒定**) | 浏览器层字段级 diff |
| 2026-09-17 | `impersonate=chrome146` 与浏览器真身不同 (ja3 `6fb2220e` vs `4bbab334`) | 协议层与真身的并排对照 |

2026-09-22 本机复测又暴露第三个坑 (见下), 说明这条回路必须常备。

## 核心纪律 (2026-09-22 实测, 全部可复现)

**1. ja3_hash 不是身份 —— 连真 Chrome 每次也不同。**
同一 `impersonate="chrome"` 连发 3 次: ja3_hash 三次全不同; 本机 **Chrome 153 headless 同样如此**
(`e9a8910b` / `d5c476f4`)。原因是 Chrome 自 110+ 起对 ClientHello 做随机化 (GREASE 之外
还会打乱部分扩展顺序)。

```
去 GREASE 后逐段比对 (curl_cffi chrome x3):
  ciphers    顺序相同=True   集合相同=True
  extensions 顺序相同=True   集合相同=True
  groups     顺序相同=False  集合相同=True     <- 扩展顺序被随机化了
```

-> **跨请求比较一律用 ja4**。去 GREASE (ja3n) 是必要条件但**不充分**, 它同样不稳定。
-> 反过来说: 「ja3 每次都变」**不是**伪造失败的证据, 拿它当判据会得出相反结论。

**2. 只有同出口的比对才有效。** 出口 IP 不同则指纹对照无意义 (本机出口与 peet 之间是 NAT,
端到端口会变, 比对已做归一)。报告里 `C5` 专门守这条。

**3. 浏览器层不可省。** 协议层只覆盖 L2/L3。L4/L5 (字体数、webdriver、时区) 只能从真浏览器取,
而它们往往才是决定性信号 —— 本机 headless 实测一次就抓到 3 条红线 (见下)。

## 六层模型

通过自检不是「某个功能」, 是六层同时自洽。任何单点工具只作用在其中一层。

| 层 | 检测什么 | 分析/伪装手段 | 本机现状 |
|---|---|---|---|
| L1 出口 | IP 信誉 / ASN 属地 | 出口探针, `cdn-cgi/trace` | WsPxy 住宅渠道; 出口恒 AS13335, 对 CF 自有网段无效 |
| L2 TLS | JA3 / JA4 / JA4S, ClientHello 字节序 | `tls.peet.ws`, curl_cffi / uTLS | **本目录覆盖** |
| L3 HTTP/2 | 帧序 Akamai 指纹, header 序, SETTINGS | 同上接口 `http2` 段 | **本目录覆盖** |
| L4 浏览器环境 | Canvas/WebGL/字体/UA-platform/时区 | Camoufox (C++ 层) / Patchright (协议层) | `vendor/camoufox-reverse-mcp`, js-reverse-mcp |
| L5 运行时 | CDP 可探测性, `isTrusted`, 调用栈探针 | js-reverse-mcp 断点/网络 | 已固化进 `browser-auto/AGENTS.md` |
| L0 令牌绑定 | cookie/token 与设备类绑定 (DataDome Picasso) | 抓包 + 消融实验 | `gh-registrar/knowledge/` 结论链 |

判定的**顺序**是先 L1 再 L2/L3 再 L4/L5: 出口脏的时候, 后面几层怎么修都不出结果
(2026-09-18 已定论: `hard_block` 是出口层现象)。

## 实测数据 (2026-09-22, 本机, tls.peet.ws/api/all)

同出口 23.237.50.28 (AS30058 FDCservers.net) / colo=LAX。

| 客户端 | ja4 | akamai h2 hash | UA 平台 |
|---|---|---|---|
| **本机真身** Chrome 153 headless | `t13d1517h2_8daaf6152771_cb7bf5808d99` | `52d84b11737d980aef856699f885ca86` | linux |
| curl_cffi `chrome` | `t13d1516h2_8daaf6152771_806a8c22fdea` | `52d84b…` | **macos** |
| curl_cffi `chrome150` | `t13d1516h2_8daaf6152771_806a8c22fdea` | `52d84b…` | macos |
| curl_cffi `chrome146` | `t13d1516h2_8daaf6152771_d8a2da3f94cd` | `52d84b…` | macos |
| curl_cffi `firefox` | `t13d1717h2_5b57614c22b0_3cbfd9057e0d` | `6ea73faa8fc5…` | macos |
| curl_cffi `safari` | `t13d2013h2_a09f3c656075_7f0f34a4126d` | `c52879e43202…` | macos |
| 裸 curl 8.22 (`native`) | `t13d2712h2_b4f9224df7c1_cbb9361b6bf9` | `52d84b…` | 无 (curl/8.22.0) |

可读出的三条结论:

1. **真身基准与 FoxIO 官方 JA4 文档的 Chrome 示例逐字节相同**
   (`t13d1517h2_8daaf6152771_cb7bf5808d99`) —— 用它当家族基准是可引用的, 不是自造。
2. **h2 层已实测对齐** (2026-09-17 文档里还是「待验」): curl_cffi 各 Chrome 档与真身的
   `akamai_h2_hash` **完全一致** (`52d84b…`)。h2 不是当前泄漏点。
3. **curl_cffi 的 Chrome 档吐 macOS UA**。它自洽, 但若链路里还有 Linux 平台的浏览器会话,
   就构成跨层平台错配 —— 这正是 `C3` 守的东西, 也是 gh-registrar 上栽过的同类坑
   (Windows UA 覆盖在 Linux 宿主 -> 站点按 Windows 下发字体 -> `document.fonts.size=0`)。

## 判据 (compare 子命令)

| 编号 | 判定 | 级别 | 修复方向 |
|---|---|---|---|
| C1 | `navigator.webdriver === true` | 红 | 去掉自动化 launch flag |
| C2 | `document.fonts.size === 0` | 红 | UA/平台画像必须与宿主同源 |
| C3 | 协议层与浏览器层 UA **平台不同族** | 红 | 不要混用不同平台的客户端 |
| C4 | 浏览器时区 ≠ 出口时区 | 红 | `--fingerprint-timezone` + `TZ` 双保险 |
| C5 | 协议层与浏览器层不在同一出口 | 黄 | 指纹对照只在同出口下成立 |
| C6 | ja4 缺失或格式不符 | 灰 | — |
| C7 | `navigator.deviceMemory` 为空 | 黄 | 查 `--fingerprint-device-memory` 是否透传到 Blink |
| C8 | 协议层与**同机真身**不同源 | 黄 | 客户端与本机浏览器不同族 = 静态可识别 |

四条硬规则写在代码里而不是只写文档 (依据: fp-audit 的同类教训 —— 判据必须可执行且集中一处):

- **灰不是绿。** 没取到读数 = 没通过, 绝不放行。退出码区分「有红线 (1)」与
  「什么都没测出来 (2)」。
- **C4 的人工时区必须标注来源。** 出口探测失败退回 `--expect-tz` 时会在输出里显式打
  `[人工指定(出口探测失败)]` —— 人工值与出口真值不是一回事, 混用会造出假绿
  (2026-09-12 栽过: 填东京而出口在新加坡)。
- **C5 必须做端口归一。** peet 的 `ip` 带端口, `cdn-cgi/trace` 不带, 直接比会假报警。
- **C8 不一致不阻塞但必须报。** 真身是另一台机器时不一致属正常, 但它会使 C3 的
  平台一致性结论失去意义 (比的是两个不同的客户端)。

## 用法

```bash
# ① 协议层: 一次跑完全部档位 + 自动采本机真身基准 + 稳定性验证
uv run probe.py protocol

# 只跑指定档位
uv run probe.py protocol --impersonate chrome,firefox,native

# 手动给真身基准 (无 Chrome 时)
uv run probe.py protocol --reference t13d1517h2_8daaf6152771_cb7bf5808d99

# ② 浏览器层: 对任意 CDP 端点 (HX-Browser / CloakBrowser / 系统 Chrome / 无头)
uv run probe.py browser --endpoint http://127.0.0.1:43020

# ③ 跨层比对: 读最近两份报告出红黄绿
uv run probe.py compare
```

退出码: `0` 全绿 / `1` 有红线 / `2` 材料不足。

产物: `reports/protocol-*.json` · `reports/browser-*.json` · `reports/compare-*.json`
(不入库, 见 `.gitignore`)。

## 与既有资产的关系

| 资产 | 分工 | 别混用 |
|---|---|---|
| `fp-audit/` | 浏览器层**质检红线** (creepjs/pixelscan/browserleaks/amiunique) | 它只看浏览器, 不看协议层 |
| `antibot/` | **攻击**伞目录 (协议重放 / 靶场调试) | 回答「怎么过这个检测」 |
| `fp-insight/` | 站点**取证**: Hook 指纹 API, 看对方在采什么/在哪一行采 | 它观测**站点**, 本目录观测**自己** |
| 本目录 | **跨层度量** + 真身基准 | 回答「我说的和我测的是不是一回事」 |
| `hx-capture-proxy/` | 不终结 TLS 的抓包/缓存代理 | 它保指纹, 本目录测指纹 |
| `js-reverse-mcp` | 运行时分析与浏览器接管 | L4/L5 的执行面 |

## 接入流水线

```bash
# 闸门用法: 起浏览器前后各跑一次, 红线即拦
uv run probe.py protocol        || exit 1
uv run probe.py browser --endpoint "$CDP" || exit 1
uv run probe.py compare         || exit 1
```

## 已知边界

- **本目录不做伪装**, 只做度量; 修复动作在启动器 (cloak_launch / js-reverse-mcp 参数)。
- **headless 下 C2 必红** (`document.fonts.size=0`) —— 这是 headless Chromium 自身不加载
  系统字体所致, 与 fp-audit 的 K5 (headless 必被 creepjs 识别) 同源。所以本工具的
  `--endpoint` 用例适合验证**工具链路**与**真实指纹环境** (HX-Browser / CloakBrowser / xvfb),
  **不要**拿 headless 的 C2 红项去判断某套指纹方案的优劣。
- **C8 的真身基准来自 `--dump-dom` 采集**, 若站点结构变化导致解析失败会降级为灰项
  (不会静默变成绿)。
- **时区一致性只覆盖「浏览器 vs 出口」**, 不覆盖「出口 vs 目标站地理预期」——后者需要
  知道目标站按哪个国家下发内容, 属任务级判断。
- **未接入**: 尚未把 `probe.py` 接进 `gh-registrar` 的 `ensure_signup_ready`,
  与 fp-audit 的同类待办合并处理。
