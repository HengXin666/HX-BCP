---
name: hx-consumer-api
description: 把 HX-ProxyGroup 的节点接进你自己的程序：拉取冻结的节点清单 JSON（GET /nodes/<share-token>），拿到每个节点的地址、凭据、协议与传输方式，用于 HTTP/SOCKS 直拨或本机内核落地；同时说明控制面不做哪些事（探测、选点、轮询、落地端口）。Use when 需要「用 HX-ProxyGroup 的代理跑自动化流量」——注册机、爬虫、浏览器自动化（Playwright/Selenium）、多租户服务要接 HX 的节点；或该程序要写「代理接入 / 节点清单 / 代理池」相关代码；或遇到 HX 订阅与节点对接、share token、协议不兼容、ws 节点不能直拨等问题时。
---

# HX-ProxyGroup 程序内接入

> **本文件的词元（端点、字段名、protocol 枚举、错误码）受
> `scripts/verify-consumer-contract.ts` 门禁约束，与
> `docs/CONSUMER_INTEGRATION_CONTRACT.md` 逐字对齐。**
> 两者不一致时，**以契约文档为准**，并把差异报给控制面维护者 —— 你的副本可能已经过期。

## 1. 你只需要一个请求

```text
GET /nodes/<share-token>
```

- `token` 就是订阅链接 `/sub/<share-token>` 里的那一段；从控制面管理员那里拿，和订阅是同一个凭据。
- 这是**公开端点**：不需要登录、不需要 Cookie、不需要 CSRF。token 就是全部凭证。
- 只支持 `GET`。其他方法返回 405。
- **照抄管理员给你的完整 URL，不要自己把 host 和 `/nodes/` 拼起来。** 控制面常被反向代理挂在
  一个**子路径**下（例如 `https://panel.example.com/proxy/hx-proxygroup/nodes/<token>`），
  那个前缀是路径的一部分。丢掉它，请求会落到面板自己的 SPA 兜底上，你拿到的是 `<!doctype html>`
  而不是 JSON —— 而且 HTTP 状态码是 200，你不会觉得出错了。
- 响应 `Cache-Control: no-store`，**别缓存**：节点清单是**每次任务开始时重新 GET** 的东西，
  不是一次性配置。订阅一刷新，里面的节点就变了；把它写死进配置等于把自己钉在昨天的出口上。
- 走 HTTPS。token 等价于密码，不要写进日志、截图、公开仓库。

### 返回什么

```json
{
  "name": "香港专线",
  "share_path": "/sub/8f3a...c1",
  "subscription_urls": {
    "clash": "/sub/8f3a...c1?format=clash",
    "v2rayn": "/sub/8f3a...c1?format=v2rayn",
    "sing-box": "/sub/8f3a...c1?format=sing-box",
    "uri": "/sub/8f3a...c1?format=uri"
  },
  "nodes": [
    {
      "name": "香港专线-01",
      "protocol": "mixed",
      "host": "proxy.example.com",
      "port": 7890,
      "auth": { "username": "svc-3f9c", "password": "…" },
      "transport": "tcp",
      "tls": true,
      "browser_compatible": true,
      "uri": "http://svc-3f9c:…@proxy.example.com:7890#香港专线-01"
    },
    {
      "name": "香港专线-02",
      "protocol": "vless",
      "host": "proxy.example.com",
      "port": 443,
      "auth": { "password": "…" },
      "transport": "ws",
      "ws_path": "/__hx-proxy__/shared",
      "tls": true,
      "server_name": "proxy.example.com",
      "browser_compatible": false,
      "uri": "vless://…@proxy.example.com:443?security=tls&type=ws&…"
    }
  ]
}
```

`nodes[]` 的顺序**是稳定的**：同一 token 连续两次请求，顺序逐字节一致。你可以依赖它做分片或轮转。

## 2. 字段速查

| 字段 | 取值 | 备注 |
| --- | --- | --- |
| `name` | string | 服务/渠道显示名，长期不变 |
| `share_path` | string | `/sub/<token>` |
| `subscription_urls` | object | 固定四个键：`clash` `v2rayn` `sing-box` `uri`，相对路径 |
| `nodes[].name` | string | 节点显示名；**轮换住宅出口后也不变** |
| `nodes[].protocol` | enum | `mixed` `http` `socks` `vless` `vmess` `trojan`。**遇到别的值就报错，别猜** |
| `nodes[].host` / `.port` | string / int | 你真正要连的地址 |
| `nodes[].auth` | object \| null | `username` / `password` |
| `nodes[].transport` | enum | `tcp` 或 `ws` |
| `nodes[].ws_path` | string | 仅 `transport=ws`；已规范化，原样使用，别改前缀 |
| `nodes[].tls` | bool | |
| `nodes[].server_name` | string | 仅 `tls=true`；SNI / Host |
| `nodes[].browser_compatible` | bool | `ws` 恒为 false |
| `nodes[].uri` | string | 单节点分享 URI，可直接塞进已有配置 |

## 3. 三种用法，按你的场景挑一种

### ① 直接拨号（HTTP / SOCKS）—— 最省事

`protocol` 是 `mixed` / `http` / `socks` 时，`host:port` + `auth` 就能直接用：

```text
Playwright   --proxy-server=http://user:pass@host:port
curl         -x http://user:pass@host:port          # mixed 也接受 socks5://
HttpClient   Proxy = new WebProxy("http://host:port") { Credentials = ... }
```

`browser_compatible: true` 说的就是这条路走得通。

### ② 逐节点落地（自己起内核）

遍历 `nodes[]`，把每个节点写进你自己的 Mihomo / sing-box 配置。
`transport=ws` 的节点由**你的内核**负责 WS 隧道 —— 控制面只给地址、路径和凭据。

### ③ 整段导入（连内核配置都不想写）

把 `subscription_urls.clash`（或 `sing-box`）拼上控制面 host 直接喂给内核。

> `transport=ws` 的节点**不能**当作 `--proxy-server` 使用：浏览器不认 WebSocket 代理。
> 必须先用内核把它落地成本地 HTTP/SOCKS 端口，见用法 ②③。

## 4. 控制面**不会**替你做的事

这不是能力缺失，是职责边界。以下全部由**你**负责：

| 你要自己做的 | 为什么 |
| --- | --- |
| 探测哪些节点能用、延迟多少、出口 IP 是什么 | 控制面不知道你要访问哪个目标站，"好节点"是目标相关的 |
| 选点、排序、轮转节奏 | 你的业务决定 |
| 重试与退避 | 你的容错策略 |
| 在本地起内核 / 落地端口 | 控制面不得进入数据转发路径 |

**唯一例外**是住宅渠道：出口 IP 只有服务器能换（供应商凭据在服务器上）。换出口有两个入口，
按你**是不是唯一**使用该渠道的消费者来选：

| 入口 | 何时用 | 语义 |
| --- | --- | --- |
| `POST /ctl/<control-token>/nodes/<index>/next` | **新代码一律用这个** | 按**声明节点**换出口，可配 `lease_id` + `expected_alloc_version` CAS 与 `claim` / `heartbeat` / `release` 租约 |
| `POST /rot/<rotate-token>/next` | 仅当你是**唯一**使用该渠道的消费者，或对接历史集成 | 按**渠道**换出口：推进的是渠道级游标，不区分调用者 |

`/rot/` 是兼容接口，**多服务共用同一渠道时它会互相踩** —— 两个消费者各自"换一个出口"，
拿到的是同一个被推来推去的渠道窗口。只有 `/ctl/` 的租约模型能表达"这个窗口是我的"。
`/rot/` 也只对 sticky 渠道存在（非 sticky 渠道换出口由供应商决定，没有对应接口）。

两个都是**你主动调用**的接口：控制面不会替你建立后台轮换任务，也不会告诉你"该换了"。
并发租约模型与验收清单见控制面仓库的 `docs/RESIDENTIAL_INTEGRATION_STANDARD.md`。

## 5. 代理池：几百上千个 HTTP/SOCKS 代理，你只要一个地址

你手上有一份**几百上千条 HTTP/SOCKS 代理的清单**（供应商导出、检测器结果、爬来的资源），
想知道怎么让程序最快用上它们。**不要把这几千条代理塞进你自己的代码**：逐条探测、排序、
重试、剔除死节点，这套逻辑写一遍就要维护一辈子，而且每条代理的存活期可能只有几小时。

正确做法是让控制面把它们**收成一条订阅**，你只拿一个地址。控制面那边的操作（一次性配置）：
把清单作为一条 **Inline 订阅**导入，建一个代理组，再发一个 Listener。清单就是**普通文本**，
一行一个端点，下面这些行都认：

```text
1.2.3.4:8080                      # 裸端点：无协议，按 HTTP 导入
1.2.3.4:8080:user:pass            # 带鉴权的裸端点（主机须是 IP 字面量）
http://user:pass@1.2.3.4:8080     # 带 scheme：按 scheme 决定协议
socks5://1.2.3.4:1080
socks5://1.2.3.4:4145#住宅-风险81% | AS22773   # 导出标注追加在 # 片段里，能自动剥离
OK|http|http://u:p@1.2.3.4:3129|1.2.3.4        # 检测器的多列输出，取其中一列
```

整池要走 SOCKS 就**每行都带** `socks5://`：裸端点没有协议信息，默认按 HTTP 导入，
控制面不靠端口号猜协议。

### 你拿到的是什么

配置完成后，你**还是只调同一个端点**（§1 的 `GET /nodes/<share-token>`），
但它的 `nodes[]` 里是**一个**节点，不是几百个：

```json
{
  "nodes": [{
    "name": "代理池",
    "protocol": "mixed",
    "host": "proxy.example.com",
    "port": 7890,
    "auth": { "username": "svc-...", "password": "..." },
    "transport": "tcp",
    "browser_compatible": true
  }]
}
```

**这一条就是「统一入口」**：它一个地址同时讲 HTTP 代理和 SOCKS5（`protocol` 为 `mixed`），
池子里几千条代理的挑选、健康检查、故障转移全在控制面背后完成。你要做的只有两件事：

```text
curl        -x http://svc-...:...@proxy.example.com:7890 https://target/
curl        --socks5-hostname svc-...:...@proxy.example.com:7890 https://target/
Playwright  --proxy-server=http://svc-...:...@proxy.example.com:7890
```

只要 `transport: tcp` + `browser_compatible: true`（混合入口天然如此），浏览器和任意 HTTP 客户端通吃。

### 什么时候你反而要拿到池子里的单条

如果你需要**自己控制出口**（例如每个账号钉一条不同 IP、按地理位置分片、自己判定哪条还活着），
那就别用统一入口 —— 拿到的是**订阅**而不是 Listener 时，用 `subscription_urls.uri`（§3 用法③）
或让管理员把池子逐条发布成订阅导出的节点。**两条路的取舍是`谁决定用哪条出口`**：
控制面决定 → 用统一入口（一条地址，最省事）；你决定 → 用逐条节点（自己探测、自己挑）。

### 别做的三件事

| 别做 | 为什么 |
| --- | --- |
| 把上千条代理硬编码进你的程序 | 它们的存活期通常以小时计，写进配置等于把自己钉在昨天的出口上；控制面会刷新，你的硬编码不会 |
| 自己实现一遍去重/健康检查 | 控制面的 Proxy Group 已经在做（去重按节点指纹，健康检查按策略周期），重复实现只会与它不一致 |
| 把统一入口写死成常量 | 它是活地址：每次任务开始时重新 `GET /nodes/<share-token>`，`Cache-Control: no-store` 已经说明它不该被缓存 |

代理池用的**不是新实体**：它就是一条 Inline 订阅，所以节点去重、检测、Proxy Group、Listener、
`/sub/`、`/nodes/` 全部沿用同一套路径，没有第二套节点模型。
控制面侧的导入细节见 `docs/SUBSCRIPTIONS.md` §2 Inline。

## 6. 错误处理

只看 HTTP 状态码，**不要解析错误消息文本**。

| 状态 | 含义 | 你该怎么做 |
| --- | --- | --- |
| 200 | 成功 | 用 §1 的 JSON |
| 404 | token 未知 / 已禁用 / 不属于任何可导出的服务 | 检查凭据。**不要重试循环** —— 这不是"稍后可能好" |
| 405 | 用了非 GET | 改客户端 |
| 500 | 控制面内部失败 | 指数退避后重试 |

## 7. 参考实现（复制即用）

```python
import json, urllib.request

def fetch_nodes(base_url: str, token: str):
    """返回 (name, nodes)。失败抛 HTTPError，按状态码分支处理。"""
    request = urllib.request.Request(
        f"{base_url}/nodes/{token}",
        headers={"Accept": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        payload = json.load(response)
    known = {"mixed", "http", "socks", "vless", "vmess", "trojan"}
    for node in payload["nodes"]:
        if node["protocol"] not in known:
            raise ValueError(f"unknown protocol {node['protocol']!r}; contract changed?")
    return payload["name"], payload["nodes"]

def direct_dialers(nodes):
    """只挑能直接喂给浏览器/HTTP 客户端的节点。"""
    return [n for n in nodes if n["transport"] == "tcp" and n["browser_compatible"]]
```

```go
// 逐节点落地的核心：host:port + auth，ws 的交给内核。
for _, node := range payload.Nodes {
    switch node.Protocol {
    case "mixed", "http", "socks":
        // 可直接拨号
    case "vless", "vmess", "trojan":
        // transport=ws：由本机内核落地
    default:
        return fmt.Errorf("unknown protocol %q; contract changed?", node.Protocol)
    }
}
```

## 8. 契约稳定性

- `nodes[]` 字段**只增不改不删**。加字段不会破坏你。
- 字段改名、改类型、改 `protocol` 枚举值 = 破坏性变更，控制面会开**新路径版本**
  （`/api/v2/nodes`）而不是改这个。
- 因此：**遇到未知 `protocol` 值要报错并上报**，那是契约破裂的信号，不是让你猜的余地。
- 完整契约（字段表、稳定性承诺、变更流程）：
  `docs/CONSUMER_INTEGRATION_CONTRACT.md`。
