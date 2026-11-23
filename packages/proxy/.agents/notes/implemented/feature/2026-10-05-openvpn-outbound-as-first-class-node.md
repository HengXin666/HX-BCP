# Agent Note: OpenVPN 出站按一等节点类型支持, 并在编译期注入远程 DNS

Status: implemented

Date: 2026-10-05
Scope: `internal/nodeparse`, `internal/dataplane/mihomo`

## Problem

把 VPNGate 这类免费家宽出口作为**普通订阅 + 链式代理组**引入 hx-pxy 时, 有两个互相独立的问题:

1. **协议白名单硬拒**: `internal/nodeparse/parser.go` 的 `nativeProtocols` 不含 `openvpn`,
   这类订阅会被整批拒收 (`unsupported Mihomo node type`)。
2. **放开白名单后节点仍然不可用**: 请求报
   `dial tcp <隧道IP>:xxxxx->198.18.0.83:443: context deadline exceeded`。

第 2 点最初被误判为两件事 —— 先是「mihomo 的 `DefaultTCPTimeout = 5s` 太紧」, 后是
「mihomo 的 openvpn 出站与 SoftEther 不兼容」。**两个判断都错了**, 各自被一次对照实验证伪:

- 用一个标准 OpenVPN 2.7.7 客户端连**同一个节点** (直连, 以及经前置链式), 它成功建隧道并
  从日本/韩国家宽出网 —— 若真是 5s 窗口或协议不兼容, 它也该失败。
- 报错里的**源地址是隧道内 IP**, 说明隧道其实已经建立成功, 失败发生在隧道**之上的**转发阶段。

真根因: Mihomo 默认启用 fake-ip DNS, 把目标域名解析成 `198.18.0.0/16` 假地址; 而 OpenVPN
出站是 L3 设备, 必须**在隧道内**解析域名。假地址交给 tun 设备无法路由。加上
`remote-dns-resolve: true` 与隧道内可用的 DNS 列表后, **同一节点立刻连通, mihomo 侧零改动**。
单变量 A/B 实测: 有该字段 → `200` + 家宽 IP; 无该字段 → 上述超时。

## Decision

`openvpn` 作为一等节点类型支持, 但**接受它就要连带修复可用性**, 两者写在同一处:

1. `nativeProtocols` 与 `SupportedProtocols()` 增加 `openvpn`。
   要求节点带内联 `ca`, 否则返回单节点失败
   (`openvpn node requires an inline ca block`) —— 数据面本身会拒 (`openvpn config requires
   inline <ca> block`), 解析期拦住可把"整批静默失败"变成"可见的逐节点失败行"。
2. `convertNodeConfig` 对 openvpn 注入 `remote-dns-resolve: true` 与 `dns: [1.1.1.1, 8.8.8.8]`,
   且**仅在字段缺失时**注入, 显式配置优先。

链式不需要改动: hx-pxy 的链式是编译期注入节点级 `dialer-proxy`
(`compiler_chain.go` 的 `derivedProxySet.derive`), 由组的 `DialerProxyGroupID` 承载。
订阅节点自带的 `dialer-proxy` 仍被显式拒绝 (一个节点不能有两条出口路径), 这是刻意的。

## Alternatives considered

### A. 什么都不做 (不支持 openvpn)

最强理由: 零改动; VPNGate 是不自控的志愿者共享出口, 质量不可承诺, 掉了就掉了。
否决理由: 需求是"多一个可选的家宽出口", 而机制已被端到端证明可行 (JP/KR 均打通);
且 VPNGate 只是这类节点的**一个**来源, 任何 SoftEther 系出口都走同一格式 —— 拒绝等于关掉一整类
出口。**部分保留**: 该池的已知不稳定性写进文档, 定位为补充出口而非关键路径。

### B. 只放开白名单, 不注入远程 DNS

最强理由: 改动最小, 只动一行白名单, 不替运算符做配置决策。
否决理由: **实测这会产出一批"能显示、连不上"的僵尸节点** —— 节点入库、出现在组里、进探测又全红,
比不支持更糟: 它把配置问题伪装成资源问题, 让排障方向完全跑偏 (本项目自己就在这一步上
连续两次归因错误)。可用性是节点的属性, 不是一个可选装饰。

### C. 在订阅节点里要求运算符自己写 `remote-dns-resolve`

最强理由: 保持编译层无业务特判, 由来源端表达完整配置。
否决理由: VPNGate 的订阅由第三方生成 (内联 ca/cert/key), 运算符无法改; 而且这个字段是
"Mihomo 的 fake-ip 与 L3 出站的交互细节", 不该泄漏给任何订阅作者。

### D. 把该池划进 `internal/residential` 域

最强理由: 复用现成的供应商/轮换/租约/会话模型, 消费者侧无需新概念。
否决理由: residential 域假设**自控的供应商凭据与轮换契约** (session-template / api-list /
per-request), 而 VPNGate 是公共志愿者池: 无凭据、无轮换 API、无可用性承诺, 语义全不匹配;
强行套用会把 domain 模型的每个字段变成噪音。它是**一组普通订阅节点**, 不是住宅供应商。

### E. 同时支持 `masque`

最强理由: 帖子另一条前置方案就是 Cloudflare MASQUE, 加进来"更完整"。
否决理由: MASQUE 需要 IPv6 端点 (`2606:4700:104::2`) 与另一套字段 (private-key/public-key/ip);
本机无 v6 出口, 实测不通, 且本仓库消费的任何订阅源都不产出这种节点。
加它只扩大受管面而不产生可用出口 —— 等真有来源时再单独决策。

## 关联

- 核验全文 / 三版结论修正 / 复现命令: `docs/VPNGATE_RESIDENTIAL_POOL_20261005.md`
- 链式模型: `.agents/notes/implemented/architecture/2026-09-16-mihomo-dialer-proxy-is-node-level.md`

## Consequences

- OpenVPN 出站作为一等节点类型进入编译期, 远程 DNS 在编译期注入。
