# VPNGate 免费家宽节点池 — 核验、真根因与 HX-Pxy 侧修复

日期: 2026-10-05
来源: nodeloc 帖子 `https://www.nodeloc.com/t/topic/106747` (作者 1panel)
状态: **已定位真根因并完成修复; 修复经 A/B 对照与端到端验证**

> 本文档历经三次结论修正, 保留修正过程以防重犯:
> v1 误判为「mihomo 的 5s dial 常量是根因」→ 被标准客户端对照证伪;
> v2 误判为「mihomo 的 openvpn 出站实现有缺陷」→ 被 `remote-dns-resolve` 单变量实验证伪;
> v3 (本文) 真根因 = **缺 `remote-dns-resolve`**, mihomo 端**无需任何代码修改**。

---

## 1. 帖子在讲什么

把两个免费资源串成链式代理: `本地 → 免费前置入口 → VPNGate 家宽出口 → 目标`。
VPNGate 出口以订阅直供 mihomo, 节点类型是 **mihomo 原生 `type: openvpn`** (内联 ca/cert/key):

```yaml
proxy-providers:
  provider1:
    type: http
    url: https://789.nx.kg/openvpn.php?token=<token>   # 实测 200 / 368KB / 69 节点
    override: { dialer-proxy: <前置名> }
```

前置方案两条: **Cloudflare MASQUE** (端点纯 IPv6, 本机无 v6) / **Tunnet** (第三方客户端)。
两条本机都不直接可用, 但**前置的作用可被任何可用节点替代** —— 本仓库用 hx-pxy 现成节点即可。

---

## 2. 真根因 (单变量 A/B 钉死)

**症状**: mihomo 加 `type: openvpn` 节点后请求超时, 报
`dial tcp <隧道IP>:xxxxx->198.18.0.83:443: context deadline exceeded`。
注意源地址是**隧道内的 IP**, 说明 **OpenVPN 隧道其实已经建立成功**。

**根因**: mihomo 默认启用 fake-ip DNS, 把目标域名解析成 `198.18.0.0/16` 的假地址; 而 OpenVPN
出站是 L3 设备, 必须**在隧道内解析域名**。假地址交给 tun 设备自然无法路由 → 超时。

**修复**: 给 openvpn 节点加 `remote-dns-resolve: true` + `dns: [...]`。
**mihomo 侧一行代码都不用改。**

### A/B 对照 (同一节点, 唯一变量)

| 组 | `remote-dns-resolve` | 结果 |
|---|---|---|
| A | 有 | `115.36.204.213 [200]` ×2 稳定 |
| B | 无 | `[000]` — `dial tcp 198.18.0.83:443: context deadline exceeded` |

### 被证伪的假设 (留档防重犯)

| 假设 | 证伪方式 |
|---|---|
| `DefaultTCPTimeout = 5s` 是根因 | 标准 OpenVPN 2.7.7 叠加前置 RTT 后**成功**; 若真是 5s, 它也该失败 |
| mihomo openvpn 实现与 SoftEther 不兼容 | 同一节点, 只要补上 `remote-dns-resolve` 就通 |
| 需要 `vpn`/`vpn` 用户名口令 | ovpn 模板第 113 行 `#auth-user-pass` 是**注释掉的**; 无凭据成功 (`AUTHFAIL:0`) |

### 独立基准 (标准客户端, 用于交叉验证)

容器内 `openvpn 2.7.7` + hx-pxy 节点做 `--socks-proxy` 前置:

```
Initialization Sequence Completed              # 隧道建立
PUSH_REPLY: ifconfig 10.238.165.137, redirect-gateway def1
cdn-cgi/trace: ip=219.100.37.238 loc=JP        # 日本家宽
第二国:        ip=211.52.54.185 loc=KR          # 韩国家宽
```

---

## 3. HX-Pxy 侧修复

### 3.1 协议白名单 (`internal/nodeparse/parser.go`)

`nativeProtocols` 增加 `openvpn`, `SupportedProtocols()` 同步。
`masque` **刻意不加** —— 它需要 IPv6 端点与另一套字段 (private-key/public-key/ip), 本仓库消费的
订阅源里不存在这种节点, 加进来只会扩大受管面而不产生可用出口。

新增校验: openvpn 节点**必须带内联 `ca`**, 否则逐节点报
`openvpn node requires an inline ca block`。理由: 数据面会拒 (`openvpn config requires inline
<ca> block`), 解析期拦住可把"整批静默失败"变成"可见的逐节点失败行"。

### 3.2 编译期注入远程 DNS (`internal/dataplane/mihomo/nodes.go`)

`convertNodeConfig` 对 openvpn 注入:

```go
if _, exists := config["remote-dns-resolve"]; !exists {
    config["remote-dns-resolve"] = true
}
if _, exists := config["dns"]; !exists {
    config["dns"] = []any{"1.1.1.1", "8.8.8.8"}
}
```

**这是让节点真正可用的关键**, 不能只放开白名单 —— 否则会得到一批"能显示、连不上"的僵尸节点。
运算符无需知道这个细节; 显式配置仍然优先 (注入只在字段缺失时发生)。

### 3.3 链式 (无需改动)

hx-pxy 的链式是**编译期注入**节点级 `dialer-proxy`
(`internal/dataplane/mihomo/compiler_chain.go:42`), 由组的 `DialerProxyGroupID` 承载, 并显式拒绝
节点自带 dialer (同文件 47–51, 理由: 一个节点不能有两条出口路径)。
**帖子那种"订阅里 override dialer"的写法在 hx-pxy 里不成立, 但用组表达效果等价。**

---

## 4. 验证证据

| 验证项 | 结果 |
|---|---|
| 单元测试: 缺 ca 逐节点报错 | ✅ |
| 单元测试: 内联材料保留 | ✅ |
| 单元测试: 注入 remote-dns-resolve / 显式值优先 | ✅ ×3 |
| 全量订阅解析 (作者 69 节点) | ✅ **67 节点 0 失败** |
| 真实 mihomo (本机 1.19.30) 跑 hx-pxy 生成的配置 | ✅ `115.36.204.213 [200]` |
| A/B 单变量对照 | ✅ A 通 / B 断 |
| 多节点 (4 个 JP) | ✅ **4/4 出口均为 219.100.37.x** |
| 链式 (经 hx-pxy 前置) | ✅ L2 → `219.100.37.239` |
| 全仓 `go test ./internal/...` | ✅ 31 包 ok, 0 FAIL |

---

## 5. 接入形态 (hx-pxy 控制面)

```text
订阅 "vpngate-home"       <- VPNGate API 或作者订阅 (openvpn 现已支持)
代理组 "home-pool"         <- 成员来自该订阅 (关键字筛选)
代理组 "home-pool-chained" <- DialerProxyGroupID = 前置组
Listener / 共享入口用户名分流 IN-USER=home-pool-chained
```

**定位**: 作为**独立家宽出口池**, 走普通订阅 + 链式组, **不替代也不进入 `residential` 域**
(residential 假设自控供应商凭据与轮换契约; VPNGate 是公共志愿者池, 无凭据/无轮换 API/无可用性承诺)。

---

## 6. 剩余风险 (已知, 未解)

- **不稳定性**: VPNGate 是志愿者共享家宽, `ping 3, ping-restart 10` 很激进, 掉线常态;
  本次抽样 3/7 节点 (全为 KR) 反复连不上。**不适合承担关键路径**, 适合作为补充出口。
- **URL 前缀**: 作者订阅的节点 server 有时以 URL 形式给出, 本仓库会原样传给 mihomo 由其解析。
- **两个节点被去重**: 69 → 67 是指纹去重的正常结果, 非失败。

---

## 附: 复现命令

```bash
# 节点清单 / 作者订阅
curl -s -A 'Mozilla/5.0' 'https://www.vpngate.net/api/iphone/'
curl -s 'https://789.nx.kg/openvpn.php?token=<token>'

# 真根因 A/B: 同一节点, 只差 remote-dns-resolve
#   A(有) -> 200 + 家宽 IP;  B(无) -> dial tcp 198.18.0.83:443: context deadline exceeded

# 独立基准 (标准客户端经前置)
docker run --rm --cap-add=NET_ADMIN --device /dev/net/tun -v <dir>:/n alpine:latest sh -c '
  apk add --no-cache openvpn curl
  openvpn --config /n/n0.ovpn --socks-proxy <宿主IP> <socks端口> --daemon --log /tmp/o.log
  sleep 25; curl -s --interface tun0 https://1.1.1.1/cdn-cgi/trace'
```
