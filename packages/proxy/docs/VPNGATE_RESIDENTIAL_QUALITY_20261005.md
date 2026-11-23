# VPNGate 节点"住宅代理级别"验证报告

日期: 2026-10-05
问题: VPNGate 的免费家宽节点, 是否达到**真正的住宅代理级别**?
方法: 全量节点 ASN 归属普查 (ip-api batch, 97/97 命中) + 真实隧道实测出口 IP (7 节点) + 共享度统计
结论: **网络层是真住宅, 信誉层不是干净住宅。混入 15% 项目自建设施。不作为干净的注册/风控规避出口。**

---

## 1. 决定性判据: 节点命名即可分离两类

| 命名模式 | 数量 | SoftEther 自家 ASN | `219.100.37.x` | 被标 proxy |
|---|---|---|---|---|
| `public-vpn-*` | 15 | 7/15 | **15/15** | 7/15 |
| `vpnNNNNNN` | 79 | **0/79** | **0/79** | 54/79 |

`public-vpn-*` **全部**落在 `219.100.37.0/24`, 该网段属
**AS36599 SoftEther Telecommunication Research Institute, LLC** —— 项目自建基础设施, 不是家庭宽带。
`vpnNNNNNN` 则**无一**属于 SoftEther ASN, 全部是真实 ISP。

---

## 2. 全量 ASN 分类 (97 节点)

| 类别 | 数量 | 占比 |
|---|---|---|
| 住宅 ISP (Korea Telecom / SoftBank / KDDI / SK Broadband / Sony / TOKAI / Rostelecom…) | 57 | 58% |
| 其他 / 未归类 | 24 | 24% |
| **SoftEther 自建 (AS36599 等)** | 15 | **15%** |
| 机房 (M247 Europe, AS9009) | 1 | 1% |

**58% 的节点 ASN 确实归属真实住宅接入 ISP** —— 这部分"住宅"属性成立。

---

## 3. 真实隧道实测出口 IP

| 节点 | 出口 IP | ASN | 归属 | 判定 |
|---|---|---|---|---|
| n2 (JP) | `183.86.87.204` | **AS10010 TOKAI Communications** | 静冈县藤枝市 | ✅ 真实住宅 ISP |
| n4 (KR) | `211.185.142.93` | **AS4766 Korea Telecom** | 江陵市 | ✅ 真实住宅 ISP |
| n6 (KR) | `119.203.61.190` | **AS4766 Korea Telecom** | 大田市 | ✅ 真实住宅 ISP |
| n1 (JP) | `219.100.37.234` | **AS36599 SoftEther** | — | ❌ 项目自建 |
| n3 (JP) | `219.100.37.239` | **AS36599 SoftEther** | — | ❌ 项目自建 |
| n0 (JP) | AUTH_FAILED | — | — | 连不上 |
| n5 (KR) | 连不上 | — | — | 连不上 |

**关键观察**: 出口 IP **不等于** `remote` 里写的服务器 IP。
`vpn473931202` 声明 `183.86.87.204` 且实测出口就是它 (真实 ISP);
`public-vpn-*` 声明 `219.100.37.x` 且实测出口仍在该段 (项目自建)。
**所以"节点是住宅"必须看 ASN, 不能看它自称**。

---

## 4. 信誉层: 这才是致命的一条

在 ASN 归属真实住宅 ISP 的 57 个节点里:

**43/57 (75%) 已被 ip-api 标记为 `proxy=true`。**

原因不言自明: VPNGate 是开放的公共 VPN 项目, 全世界的反欺诈/风控库早已把整个项目 (含其 IP 段)
整体拉黑。**"IP 属于住宅 ISP" ≠ "IP 信誉干净"** —— 前者是物理归属, 后者是历史行为记录。
VPNGate 能给你前者, 拿不到后者。

**共享度** (NumVpnSessions):

| 指标 | 值 |
|---|---|
| 中位数 | **20 人** |
| 均值 | 33 人 |
| 最大 | **174 人** |
| 共享 <10 人的节点 | 22/97 |

中位 20 人共享同一出口 IP, 且是**开放注册、无人审核**的共享 —— 一个 IP 上同时跑着你、以及
任意数量的滥用行为。即使某刻信誉干净, 下一秒就可能被污染。

---

## 5. 结论

| 判据 | 回答 |
|---|---|
| IP 是否归属真实住宅 ISP? | **部分是** —— 58% 是 (KT / SoftBank / KDDI / TOKAI…), 15% 是 SoftEther 自建 |
| 是否"干净的"住宅代理? | **不是** —— 75% 已被公开标记为代理, 中位 20 人共享 |
| 能否用于需要干净住宅 IP 的场景 (注册/风控规避)? | **不能** |
| 能用于什么? | 需要**地理位**而非**信誉**的场景: 地区内容访问、轻度地理测试、补充性出口 |

**一句话**: 它是"真住宅网段上的脏 IP", 不是"住宅代理级别"。
"住宅级"要同时满足 ASN 是住宅 **且** 信誉干净、独享/低共享 —— VPNGate 只满足前一条的一半。

---

## 6. 与 hx-pxy 已有住宅渠道的对比

| 维度 | VPNGate | hx-pxy `residential` 域 (WsPxy / 供应商) |
|---|---|---|
| IP 归属 | 58% 住宅 ISP + 15% 自建 | 供应商签约住宅 |
| 信誉 | 75% 已被标代理 | 可由供应商换 IP |
| 独享性 | 中位 20 人共享, 开放注册 | 会话级独占 |
| 轮换 | 无 API, 靠池子自然更替 | 有轮换契约 (session-template / api-list) |
| 可用性承诺 | 无, 掉线常态 (本批 2/7 连不上) | 供应商 SLA |
| 凭据 | 无 (证书认证) | 有 |

**定位不变**: VPNGate 只能作为**补充出口**, 不能承担需要干净住宅信誉的关键路径 ——
这也正是 `residential` 域被单独建模的原因。

---

## 附: 复现命令

```bash
# 1) 全量 ASN 普查 (ip-api batch, 100/次)
curl -s -A 'Mozilla/5.0' 'https://www.vpngate.net/api/iphone/' -o api.csv
# 取第 2 列 IP -> POST http://ip-api.com/batch
#   字段: query,status,countryCode,isp,org,as,asname,proxy,hosting,mobile

# 2) 命名规律验证
#   public-vpn-*  -> 全部 219.100.37.x (AS36599 SoftEther)
#   vpnNNNNNN     -> 全部真实 ISP ASN

# 3) 真实隧道测出口 (容器)
docker run --rm --cap-add=NET_ADMIN --device /dev/net/tun -v <dir>:/n alpine:latest sh -c '
  apk add --no-cache openvpn curl
  openvpn --config /n/nX.ovpn --daemon --log /tmp/o.log
  sleep 14
  grep -q "Initialization Sequence Completed" /tmp/o.log || exit 1
  printf "nameserver 1.1.1.1\n" > /etc/resolv.conf
  E=$(curl -s https://api.ipify.org); echo "egress=$E"
  curl -s "https://ipinfo.io/$E/json"    # 看 org/ASN 判住宅还是机房'
```
