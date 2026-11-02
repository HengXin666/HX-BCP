# Agent Note: hx-browser 出口泄露与指纹空转 —— WebRTC 真拦截 + 代理凭据注入 + CDP 指纹注入

Status: implemented

## Problem

用户报告 (2026-10-10):
> 「https://ipi6.com/ 看一下我的这个ip为什么会泄露……以及看看当前项目的所谓的指纹浏览器,
> 以及配合了缓存代理服务器, 到这个我们的代理服务的这个指纹浏览器, 它在浏览的时候是否还会遇到这个指纹泄露的问题?」
> 「以后浏览器的话, 你注意一下它这些指纹你要记得要拿linux的指纹」

## Decision

### 一、WebRTC 泄露: 启动参数在 Chrome 154 上**零效果**, 只能在主世界真拦截

四组交叉实测 (裸 Chrome 154 + 可用无凭据中继 + 台湾住宅出口 114.33.139.168):

| 参数 | HTTP 出口 | WebRTC srflx |
|---|---|---|
| 仅 `--proxy-server` | 114.33.139.168 | 203.10.99.10 + 14.154.58.59 |
| `+ --proxy-bypass-list=` | 114.33.139.168 | 同上 |
| `+ bypass + --force-webrtc-ip-handling-policy=disable_non_proxied_udp` | 114.33.139.168 | 同上 |
| 无代理 + rtc flag | 203.10.99.10 | 同上 |

⇒ `--force-webrtc-ip-handling-policy` 在本机 **google-chrome-stable 154 上完全不生效**。
对照: 同参数下 **CloakBrowser 146** 七个 STUN 全 empty (零 srflx) ⇒ 差异在**二进制是否打过补丁**,
不在参数。旧实现 `fingerprint.py` 只对 SDP 做字符串替换, 且整段被 `(webrtc_public_ip || hide_webrtc)`
包着, 默认 `webrtc_mode="real"` ⇒ **一个字节都不改**; 就算改, STUN 包在 createOffer 之前已发出。

**采纳**: 构造 RTCPeerConnection 时**清空 iceServers + iceTransportPolicy=relay**,
让 srflx 候选根本不产生 (改的是"不发", 不是"事后粉饰")。

### 二、指纹扩展在 Chrome 154 上**整体空转**

`window.__hx_fp_injected__ === false`, `getConfiguration()` 里 iceServers 原样保留。
四组开关全试过: 仅 `--load-extension` / `+--disable-extensions-except` /
再加 `--enable-unsafe-extension-debugging` / 只留 unsafe ⇒ **全部无效**。
⇒ Chrome 136+ 收紧、154 彻底移除 `--load-extension`。hx-browser 里 UA / platform / WebGL /
Canvas / Audio / WebRTC 的伪装**一直是空转**。

**采纳**: 改走 CDP `Page.addScriptToEvaluateOnNewDocument` + `Runtime.evaluate`
(主世界, 新文档前注入), 这是全版本官方通道。`fingerprint_script()` 与扩展路径
**共用同一份 `_fp_json_for()`**, 不另写第二套覆盖逻辑。

### 三、代理凭据内嵌进 `--proxy-server`, Chrome 一律拒收

`browser.py:221-222` 旧实现拼 `user:pass@host` ⇒ `ERR_NO_SUPPORTED_PROXIES`,
页面**完全发不出去**。仓库早知此事 (`proxy_auth_runner.py` 文件头), 但 hx-browser 没接。

**采纳**: `proxy_args()` 绝不内嵌凭据 + 恒定加 `--proxy-bypass-list=`;
凭据由 `_start_auth_injector()` 起**常驻独立进程**注入 (CDP Fetch 域)。

★ 接入后连栽三处, 都是"静默失败":
1. **`websocket-client` 缺失** ⇒ 注入器一起来 ModuleNotFoundError 死掉, 服务端零报错。
   (此前我曾用一条**无条件 print** 的假验证宣布它存在 —— 教训: 验证必须真的 import 一次。)
2. **`_AUTH_LOG` 用了 `parents[3]`**(指到 `services/` 而非 `hx-browser/`) ⇒
   `open(mode="a")` 抛错被外层 except 吞掉 ⇒ Popen 从未执行。目录先 mkdir。
3. `_post_start` 里 `await _apply_dark_mode_cdp` 最长 ~24s, 之后才 spawn 注入器 ⇒
   任何等待 <24s 的验证都会**误判成"没生效"**。

### 四、指纹平台默认 Windows 与宿主矛盾

`os` 默认 `"Windows"`, 46 个存量环境**全是 Windows**, 而宿主是 Linux;
`WEBGL_POOL` 6 条里 5 条是 `Direct3D11`(Windows 专属后端)。
`cloak_launch.py` 早已认定同一问题并注明"实测直接导致 /signup 被 DataDome 拦"。

**采纳**: `os` 默认 Linux; `WEBGL_BY_OS` 按 OS 分池(`webgl_for()` 单一真源); 46 个存量环境迁移。

## Consequences

- hx-browser 现在才第一次真的伪装指纹。此前 UA / platform / WebGL / Canvas /
  Audio / WebRTC 全部空转而面板显示正常 —— 属静默失败, 靠读代码永远发现不了。
- WebRTC 泄露从三个身份并存 (台湾住宅 + 日本 GSL + 中国电信) 收敛为单一代理出口。
- 带账号密码的代理环境从页面完全发不出去变为可用 (凭据由 CDP Fetch 注入)。
- 代价: 启动后多一个常驻子进程 (凭据注入器); stop() 必须先回收它再关浏览器,
  顺序反了会让 Fetch 未决请求挂死。
- 代价: 指纹注入在 _post_start 里用 create_task 轮询约 24s, 只覆盖启动期;
  之后的新标签页依赖 Page.addScriptToEvaluateOnNewDocument, 未做长期验证。
- 新增依赖 websocket-client (hx-browser/pyproject.toml)。
- 46 个存量环境指纹由 Windows 迁到 Linux。它们此前从未真正生效过伪装, 所以
  不存在账号身份断裂风险; 但已绑定账号的环境 UA/platform 实际取值会变, 需知会使用者。
## Alternatives considered

- **什么都不做**: 现状是"代理配了等于没配 + WebRTC 裸发真实 IP + 整套指纹伪装空转",
  且面板显示一切正常。三条都是**静默**的, 用户无从发现。最强理由: 这是历史存量, 动它有回归风险。**否决**。
- **复用现有 (proxyargs.py / proxy_auth_runner.py)**: `proxyargs.py` 的两条不变量被直接采纳,
  凭据注入器**按文件路径复用** gh-registrar 的唯一实现而非另写一份 ——
  "同一事实两处定义必然不同步"是本仓栽过的坑。但 `cloak_launch.py` 本身**不能**直接复用:
  它启动的是 CloakBrowser(合规浏览器), hx-browser 走 google-chrome-stable(见 browser_gate 命中),
  两者注入机制已分叉(扩展 vs CDP), 强行合并会引入更大耦合。
- **改用 CloakBrowser 替换 hx-browser 的内核**: 这是方向上最正确的解(合规 + 自带反检测)。
  但 hx-browser 是独立常驻服务(8325, 46 个存量环境), 换内核属架构级改造,
  超出本次"修泄露"范围。**留作后续**, 不在本次落地。
- **关掉 clash-verge TUN**: 初版建议即此条, **已自我推翻**。理由: ① 工作区铁律
  "严禁操作 clash(切节点/改规则都禁止)"; ② 实测推理 — 关掉后直连出口变成真实家宽 IP,
  WebRTC 泄露的**从日本机房 IP 变成家宽**, 更糟; 系统 DNS 仍是 `192.168.5.1` 家路由, DNS 泄露照旧。

## 验证 (A卷, 独立可跑可证伪)

`/tmp/a-exam/exam.py` 9 条断言, 全部采集**实际值**而非读代码。终判 **9/9 PASS**:
HTTP 出口 = 代理出口 / ≠ 宿主直连 / WebRTC 零 srflx / bypass-list 在真实 cmdline /
proxy-server 无内联凭据 / platform=Linux x86_64 / WebGL 无 D3D11 /
带凭据代理走得通 / 带凭据场景同样零 srflx。

★ 盲审卷自身抓出我三个测量错误(全部已修): probe.html 经 JS 模板字面量写入时
\`\\b\`/\`\\d\` 被 JS 吞成退格符与字面 d ⇒ IPv4 正则全失效, 一度让 A3/A9 变成**假通过**;
另两处是 `startswith` 匹配被 Chromium 重写过的 cmdline、以及对 async 方法漏 `await`。

## 复现

```
cd /tmp/a-exam && python3 exam.py      # 需 hx-browser(8325) 在跑
```

## 遗留 (未做, 需用户决策)

- hx-browser 走 google-chrome-stable, **browser_gate 判其为违规浏览器**(存量棘轮内)。是否换 CloakBrowser 内核待定。
- 注入器轮询窗口 24s 只覆盖启动期; 之后的新标签页靠新文档钩子, 未做长期验证。
