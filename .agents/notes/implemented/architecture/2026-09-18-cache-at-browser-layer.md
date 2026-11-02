# Agent Note: 缓存层落在浏览器层 (CDP Fetch), 网络层不终结 TLS

Status: implemented

## Problem

"静态资源缓存"与"指纹保真"过去被当成一对取舍: 传统缓存代理必须**终结 TLS** 才能读到明文并改写响应,
而终结 TLS 就必须以**另一个客户端**的身份重建上游连接 —— 实测那会丢掉 GREASE 扩展
(`51764`, 0xCA34), 扩展集合从 17 个变 16 个, 无法与真身相同。

于是原实现 (`cacheproxy`, 8899) 只能在"省流量"与"不改身份"之间选一个:
它选了省流量, 代价是 E2 记录的指纹泄漏, 并因此被关掉默认。

问题因此是: **有没有一种缓存位置, 既能改写响应, 又不碰握手?**

## Decision

**缓存改在浏览器层做, 网络层只做不终结 TLS 的隧道。**

- `probes/intercept.py` 提供浏览器层拦截原语: `Fetch.enable` 拦下请求,
  命中缓存则 `Fetch.fulfillRequest` 用本地 blob 应答, 未命中则 `Fetch.continueRequest` 放行。
- 被放行的请求**由浏览器自己发起原生 TLS 握手**, 代理/上游全程不参与、不解密、不重建。
- 因缓存命中而**完全消失**的请求, 连网络层都不产生 —— 既不耗流量, 也不产生任何可观测的握手。

关键实现约束 (全部来自实测, 不是设计偏好):

1. **拦截流与请求流必须同一条 CDP 会话**。分成两条时, 拦截侧读超时会静默结束放行循环,
   页面导航永远等不到响应 (`CDP Page.navigate 超时`)。
2. **`Session.send()` 在事件回调内必须降级为只发不等** (`_send_nowait`)。
   reader 线程在回调里等一个只有 reader 能 set 的响应 = 自己等自己, 永久死锁。
3. **reader 的 recv 超时绝不退出循环** —— 静默退出等于整条链路假死。

## Alternatives considered

- **网络层 MITM 缓存 (原 `cacheproxy` 的做法)** (最强理由: 已跑通 2320 行、种子缓存已入库、
  GitHub assets 实测 7.7MB→322KB/轮; 而且对**没有浏览器参与**的客户端 (Burp/纯协议重放)
  它是唯一选择)。
  否决: 实测扩展集合永远只有 16 个 (7 个 curl_cffi impersonate 全部如此, 含 edge101/safari184),
  与真身 17 个结构性不等。对**有真浏览器在场**的主场景, 它是净损失。
  保留为显式实验档, 默认关。
- **NBD 用户态服务端复现记录形态** (最强理由: 同样不终结上游 TLS, 且理论上能精确复现分片形态)。
  否决: 需要 root / `CAP_NET_ADMIN` / 新内核语义, 本机 `uid=1000` 走不通;
  而浏览器层填充**不需要任何特权**, 且已实测可行。
- **Service Worker 缓存** (最强理由: 纯 Web 层, 不依赖 CDP, 对用户透明)。
  否决: SW 只在 `https` 且**注册域内**生效, 跨域静态 CDN (github.githubassets.com) 覆盖不到;
  CDP Fetch 是**进程外**的, 覆盖全部请求且不注入任何页面可见对象 (无 JS 劫持痕迹)。
- **什么都不做 / 继续关掉缓存** (最强理由: 零风险, 且当前注册链路确实不需要它)。
  否决: 流量成本是真实的 (7.7MB/轮), 且"必须 MITM 才能缓存"这个前提已被实测证伪 ——
  没有理由继续为一个不成立的前提付代价。

## Consequences

**买到的**: 缓存命中不再产生网络流量、也不再产生握手, 而指纹保持 identity。
实测 `MIXED-MODE-PRESERVES-FINGERPRINT`: 混合模式 (`paused=11 fulfilled=1 continued=10`)
下 5/5 顺序无关量与无拦截基线完全相同。开启拦截本身也不改指纹 (`INTERCEPTION-NEUTRAL`)。
→ "一个功能做到 100%" 在此成立: 隧道档的扩展集合与随机性形状均与真身逐项相同。

**代价 (两半都要说)**:

- **需要 CDP 通道**: 环境必须可被 `--remote-debugging-port` 驱动。SDK 内部自建网络栈的客户端
  (或已断开的 CDP) 覆盖不到 —— 那类场景退回网络层 MITM 档。
- **拦截逻辑与浏览器生命周期耦合**: 浏览器重启后需重新 `Fetch.enable` 并重建会话。
- **本模块只做原语, 不含缓存策略**。什么该命中、TTL、种子导入导出仍是未实现的下一层;
  本 note 只锁定"缓存该放在哪里"这一个决策。
- **未覆盖的层**: TCP/IP 层指纹、服务端侧保真度、TLS 记录分片均不在本判据范围内。
- 全量判定一轮约 3~5 分钟 (含起停浏览器), 不适合作最短提交前钩子。

## Testing

```bash
cd workspaces/browser-auto/fp-audit
.venv/bin/python -m pytest tests/            # 若已接入; 否则用下条端到端
.venv/bin/python fp_ab.py --port 43370 --samples 3 \
  --arms "direct,tunnel=http://127.0.0.1:8898|ignorecert,mitm=http://127.0.0.1:8899|ignorecert"
```

实测证据 (`reports/` 与 `knowledge/hx-capture-proxy/AB-THREE-ARM-2026-09-17.md`):

- 三路终局: `tunnel` 扩展集合 17 个 = 真身; `mitm` 16 个 (缺 `51764`);
- 随机性形状 (n=12): `ja3_hash`/`ja4_o`/`ja4_ro` 12/12 随机, `ja3n_hash`/`ja4_r`/`akamai` 1/12 恒定, 两路逐项相同;
- 混合模式: `VERDICT: MIXED-MODE-PRESERVES-FINGERPRINT`, `LOCAL-FILL-WORKS: True`;
- 拦截中性: `VERDICT: INTERCEPTION-NEUTRAL`, `paused=12` 全部放行。

结构门禁: `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`。