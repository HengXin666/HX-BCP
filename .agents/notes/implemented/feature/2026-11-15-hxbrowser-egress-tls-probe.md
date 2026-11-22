# Agent Note: 出口侧握手探针 (ClientHello 只读捕获, 与宣称内核比对)

Status: implemented (ClientHello); H2 SETTINGS 未覆盖, 原因见下

## Problem

用户需求 (2026-11-15, plan.md 红线一第 4 条):

> 非 JS 维度纳入自检. 现有自检诚实承认看不到 TLS/H2. 补一个出口侧抓包探针 (在 relay/cache-proxy 处记录真实 ClientHello 与 H2 SETTINGS), 与宣称内核版本比对.

plan.md 第 2 条更正已经说明: 主路径不改握手, 真正的缺口是**没有观测**. UA 与真实握手只会在三种情形矛盾: ① 手写 `user_agent` 覆盖主版本, ② 非 cloak 内核 (firefox/librewolf) 配 Chrome UA, ③ `kernel_major` 探测失败回落 146.

此前唯一的 TLS 读数是 fp-audit `probes/tls.py`, 靠第三方回显站 (tls.browserleaks.com) 从服务端看, 不是出口侧, 也不和宣称版本比对.

## Decision

### 观测点只能是 relay, 不是 cache-proxy

- cache-proxy (hxcp) 的缓存在浏览器层 (CDP Fetch), 根本不在网络路径上, 看不到任何握手字节.
- hx_browser 的 relay.py 是副本唯一的网络出口 (自定义上游与 sandbox 两条路径都经它). CONNECT 隧道里客户端发的第一段字节就是明文 ClientHello.
- 直连 (非 sandbox 且无上游) 的副本没有 relay, 出口侧无观测点, 体检项报 unknown 并写明原因.

### 捕获方式: 旁路, 不改字节, 不加延迟

- `relay._pipe(src, dst, tap=None)`: 先 write + drain 转发, 再把同一段交给 tap. tap 返回 True 后不再调用. 字节流与改动前逐字节相同 (测试 `test_relay_taps_hello_without_changing_bytes` 逐段 7 字节写入验证).
- 只在 CONNECT 分支挂 tap; 明文 http 请求没有握手.
- tap 内部任何异常都吞掉并结束, 绝不影响隧道.
- 只有 `relay.ensure(env_id, ...)` 起的副本 relay 带 env_id; netcheck / proxy.check_proxy / dnsleak 用的临时 `Relay(up)` env_id 为空, 不捕获. 否则宿主 httpx 的握手会混进浏览器的样本, 判定全错.
- 每副本内存里保留最近 32 个握手 (`tlsprobe._SEEN`), `relay.stop(env_id)` 时清掉: 样本只属于一次启动.
- 解析: `tlshello.py` 纯函数, 支持跨 TCP 段与跨 TLS record 拼接, 上限 32 KB, 非 TLS 立即放弃.

### 只认顺序无关量 (沿用 fp-audit tls.py 2026-09-17 实测)

记录 ja4 / ja4_r / ja3n_hash / ja3n_text / alpn / groups / key_shares / grease. 不用 ja3 / ja4_o: Chrome 逐连接置换扩展顺序, 顺序敏感量连自己两次都不相等. ja4 实现按 FoxIO 规范 (排除 GREASE, c 段排除 SNI 与 ALPN, 签名算法保持线序); cipher 段对 Chrome 15 套件算出 8daaf6152771, 与公开参考值一致.

### H2 SETTINGS: 做不到, 如实标注

HTTP/2 的 SETTINGS / WINDOW_UPDATE / 伪头顺序都在 TLS 加密之后. 不终结 TLS 的出口只读探针**看不到**. 要看到只能 MITM, 违反 AGENTS.md "不终结 TLS", 且本仓 A/B 实测 MITM 会把扩展集合从 17 变成 16, 反而制造泄漏. 所以:
- 每个结果都带 `h2: {observable: false, note}`, detail 写明 "本项未覆盖 H2", healthy 的 value 也写 "(H2 未覆盖)", 避免 "握手一致" 被读成 "传输层全部一致".
- 出口侧唯一能看到的 H2 相关事实是 ALPN 是否提供 h2, 已记录.
- 若以后要 H2 读数, 只能走服务端回显 (browserleaks 的 akamai_hash, fp-audit tls.py 已有), 那是 "站点看到的", 不是 "出口侧".

### 判定 (判据只在 `tlsprobe.assess()` 一处)

输入: 捕获的握手 + 页面内实际读到的 `navigator.userAgent` (宣称) + CDP `Browser.getVersion().product` (真实二进制) + 环境内核族.

- error:
  - UA 主版本 != product 主版本 (覆盖情形 ① ③, 不依赖任何版本表, 最硬的一条).
  - UA 族与握手族不符: 有 GREASE 判 Chromium 族, 无 GREASE 且有 record_size_limit(28) 判 NSS/Firefox 族 (情形 ②).
  - UA 族与环境内核族不符.
  - 同一副本出现两族握手.
  - 握手含宣称版本不可能发的特征 (下表 absent_below 之前出现), 或已移除的特征 (Kyber 0x6399 于 132 起, ALPS 旧码点 17513 于 135 起).
  - 宣称 Chrome >= 110 而 >= 3 个握手扩展顺序完全相同.
- warning: 宣称版本应发的特征缺失 (expected_from 起). 只给黄: 企业策略可关 ECH / 后量子密钥交换, 缺失不一定是伪装.
- unknown: 没捕获到握手, 读不到 UA, 读不到真实内核版本. 沿用 "unknown 不放行".

| 特征 | 码点 | 早于此版本出现即红 | 从此版本起缺失即黄 |
|---|---|---|---|
| ECH GREASE | ext 0xfe0d | 117 | 119 |
| X25519Kyber768 | group 0x6399 | 115 (132 起出现也红) | 不要求 |
| X25519MLKEM768 | group 0x11ec | 130 | 132 |
| ALPS 新码点 | ext 17613 | 131 | 134 |

边界取自 Chrome 公开发布信息, 两端各留了余量 (红只判确定不可能的, 黄只判确定应有的). **未经本仓实测标定**: 第一次拿真实 CloakBrowser 跑出读数后, 应按实测收紧或修正这张表.

### 接入

- doctor 体检多一项 `tls_handshake` (`*(await tlsprobe.doctor_checks(env))`), 形状同其它检查项, 另带 ua / product / hellos (最近 8 个摘要) / h2 证据.
- 体检时在副本里开一个后台 about:blank 页读 UA; 已捕获握手不足 3 个时顺带 fetch 4 个不同主机 (no-cors) 制造连接, 然后关页. 不在用户当前页执行.
- Gecko 内核: 没有 CDP, 宣称 UA 退回 `fingerprint.user_agent`, 未设则 unknown; 握手仍被动捕获.

## Consequences

- relay 热路径多一次函数调用, 每条隧道只解析首个 ClientHello, 之后 tap 退出.
- 每次体检会短暂出现一个后台空白标签页; 样本不足时向 gstatic / cloudflare / bing / example.com 发请求.
- 样本只在内存, 服务重启即丢 (relay 本身也随服务重启).

## 未做

- H2 SETTINGS 出口侧读数 (见上, 不终结 TLS 前提下不可能).
- plan.md 验收判据里的 `leak-gate`: "出口侧 JA4_r / ja3n 与同内核直连基线逐项相同". 需要先建一份按内核版本存的基线; 本次的 ja4_r / ja3n_hash 已经是它要比对的量. 另一个顺手可做的比对: 出口侧 ja4_r 与 browserleaks 服务端回显的 ja4_r 相同, 即可证明上游代理没有 MITM.
- 控制台展示 (红色 badge 等): console-web 有大量未提交改动, 本次没动前端.
- fp-audit 里接入: fp-audit 对任意 CDP 端点工作, 拿不到 hx_browser 的 relay; 若要接, 走 hx_browser 体检 API.

## Verification

- tests/test_tlsprobe.py (14 项, 进程内不出网): 解析与顺序无关量, 跨段/跨 record, OpenSSL 真实握手可解析, 一致判绿, UA 与真实内核主版本不符判红, Firefox 握手配 Chrome UA 判红, 旧版本宣称配新握手判红, 缺 MLKEM 判黄, 扩展顺序固定判红, 证据不足判 unknown, relay 捕获不改字节且 stop 后清空, 临时探测 relay 不捕获.
- tests/test_proxy_relay.py 与 test_dnsleak.py 原样通过.
- 未做真实浏览器实测.
