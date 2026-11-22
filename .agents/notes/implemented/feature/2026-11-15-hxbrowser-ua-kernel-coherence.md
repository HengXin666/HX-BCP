# Agent Note: TLS 指纹 = UA 与内核真实握手一致, 矛盾即拒绝启动

Status: implemented

## Problem

用户需求 (2026-11-15):

> 补 TLS 指纹 (v0.2 路线图提前)。 这是当前最大的可被直接检测的泄漏面。

字面上的 v0.2 条目是 packages/browser/README.md 的 "TLS 指纹(uTLS 代理)", 对应 models.Fingerprint.tls_mode 的 `chrome(tls-proxy)` 取值和控制台 "TLS 模式" 下拉. 读源码后确认这条路不能照做:

- 出口握手本来就是真内核: relay 与 hxcp 都不终结 TLS (relay._pipe 纯字节转发, note 2026-09-18-cache-at-browser-layer), 浏览器自己的 BoringSSL / NSS 直接对站点握手.
- uTLS 改 ClientHello 必须终结 TLS 再重建上游连接, 违反 AGENTS.md "不终结 TLS"; 本仓 A/B 实测 MITM 档扩展集合恒为 16 (缺 GREASE 51764), 7 个 curl_cffi impersonate 档全部如此, 伪装层反而制造泄漏 (plan.md 1.2 更正).
- 控制台 "Chrome 指纹 (tls-proxy)" 选项后端从未实现, 选了什么都不发生, 属于 "传了参数 != 生效了".

真正会被直接检测的 TLS 面是 **宣称与握手矛盾**: 站点看到 Chrome/150 的 UA 却收到 146 的握手, 或 Chrome UA 配 NSS 握手. 在当前代码里只有一个来源能造成它: 手写 `fingerprint.user_agent`.

- 未手写 UA: Chromium 路径 guard.effective_user_agent 按真实内核主版本拼 UA, 读不出版本时不传 --user-agent (原生 UA); Firefox 路径不写 override. 两者天然一致.
- 手写 UA: browser._launch_chromium 原样传 --user-agent 并经 CDP 注入 navigator.userAgent; build_firefox_userjs 原样写 general.useragent.override. 主版本跟不上内核升级, 或 Firefox / LibreWolf 内核配 Chrome UA, 或 Safari 等 UA 配任何内核, 都会与握手矛盾.
- 2026-11-15 已有的出口侧握手探针 (note 2026-11-15-hxbrowser-egress-tls-probe) 只能在运行后体检时发现, 已经带着矛盾身份连过站点了.

另: cloak.derive / cloak.launch_args 与 fingerprint.gen_random_fingerprint 当前没有任何调用方, plan.md 里 "kernel_major 探测失败回落 146" 只存在于这段死代码, 本次不动.

## Decision

- 判据只在 `tlsprobe.ua_conflicts(ua, kernel_family, real_major)` 一处: UA 不是 Chrome / Firefox 系即矛盾; UA 族 != 内核族即矛盾; 真实主版本已知且 != UA 主版本即矛盾. `tlsprobe.assess` 改为调用它, 运行后体检与启动前拦截同一判据.
- `launch_hooks.ua_kernel_conflicts(env, settings)` 解析可执行文件并取真实主版本 (guard.kernel_major, 按路径缓存), `prepare()` 在起 relay 之前调用, 有矛盾抛 ValueError 拒绝启动, 不自动改写用户填的 UA (静默改写会让用户以为自己的 UA 生效了).
- 读不出真实主版本时只判族, 放行; 版本一致性由运行后的 tls_handshake 体检兜底 (那里没有 product 即 unknown, 不放行). 理由: `--version` 失败就拒绝启动会让所有手写 UA 的环境不可用, 而族判据已经拦住最硬的情形.
- doctor 的 kernel 项用同一函数, 未启动时就能看到红色 "User-Agent 与内核矛盾, 启动会被拒绝".
- browser.py 只把 self.settings 传进 prepare, 行数不变 (533).
- `tls_mode` 保留字段以兼容旧数据与账号包, before 校验器把任何取值归一为 none; 控制台去掉 "TLS 模式" 下拉, UA 输入框写明约束; README v0.2 条目改成实际做法.

## Consequences

- 已存在的手写 UA 环境在内核升级后会被拒绝启动, 错误信息给出两个主版本并提示清空 UA. 这是有意的: 带着矛盾身份启动比不启动更糟.
- 不覆盖 H2 SETTINGS / 伪头顺序: 同样由内核原生发出, 不改就不矛盾; 出口侧看不到 (见 egress-tls-probe note).
- 不覆盖 Sec-CH-UA 品牌版本单独被改的情形: 本仓没有手写 Client Hints 的入口.

## 未做

- plan.md leak-gate 的 "出口侧 ja4_r 与同内核直连基线逐项相同" 需要真实浏览器与按内核版本存的基线, CI 没有上游代理, 未做.
- CHROME_FEATURES 版本边界仍未经本仓真实 CloakBrowser 实测标定.

## Verification

- tests/test_ua_kernel.py: 判据各分支, 假内核可执行文件输出 146 时手写 Chrome/150 被 prepare 拒绝且未起 relay, 同版本放行, Firefox 内核配 Chrome UA 拒绝, 读不出版本时只判族, tls_mode 旧值归一.
- tests/test_tlsprobe.py 既有用例的断言文字未变 (矛盾信息由 ua_conflicts 产出, 文案相同).
- 按 AGENTS.md 未在本地跑测试, 交给 CI; 本地只做了 py_compile.
