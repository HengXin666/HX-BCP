# 住宅代理客户端与自动化 API

每个住宅渠道使用稳定节点模型：客户端通过该渠道的 `/sub/` 获取节点，自动化程序通过该渠道的 `/ctl/` 指定节点
轮换出口。供应商会话和住宅 IP 是 HX-Pxy 内部实现，不是客户端生命周期。

完整字段契约见 [`RESIDENTIAL_V2_CONTRACT.md`](RESIDENTIAL_V2_CONTRACT.md)，机器可读版本见
[`openapi/residential-control.openapi.yaml`](openapi/residential-control.openapi.yaml)（与订阅服务的 OpenAPI 分开维护）。
**多服务并发对接标准（租约 + 版本护栏）见 [`RESIDENTIAL_INTEGRATION_STANDARD.md`](RESIDENTIAL_INTEGRATION_STANDARD.md)**：
这是多个服务同时使用同一渠道时的唯一并发契约。

## 渠道订阅

渠道管理 API（管理员认证）返回该渠道自己的 `subscription_url`，并可独立轮换 share token：

```http
GET  /api/v1/residential/channels/<channel-id>
POST /api/v1/residential/channels/<channel-id>/rotate-share
```

客户端导入单个渠道：

```http
GET /sub/<share-token>?format=clash|v2rayn|sing-box|uri
```

每个渠道订阅只包含本渠道的住宅声明节点，不聚合普通 Listener 或其他渠道。住宅节点名称与凭据
稳定，调用 `next` 后不需要重新拉取订阅。响应带 `Cache-Control: no-store`；share token 可以读取
代理凭据，必须按密码管理。管理员在「代理服务」页面复制 Clash/Mihomo 订阅；「住宅代理」页面
不再提供全局订阅入口。

## 声明节点控制

sticky 渠道设置 `session_count` 后，服务端持久化 `s01..sNN` 逻辑节点。管理员 Session API：

```http
POST /api/v1/residential/channels/<channel-id>/sessions/<index>/next
POST /api/v1/residential/channels/<channel-id>/rotate-share
POST /api/v1/residential/channels/<channel-id>/rotate-control
```

供 OutlookRegister 等程序使用的 control-token API：

```http
GET  /ctl/<control-token>/nodes
POST /ctl/<control-token>/nodes/<index>/next
POST /ctl/<control-token>/nodes/<index>/route
POST /ctl/<control-token>/nodes/<index>/claim
POST /ctl/<control-token>/nodes/<index>/heartbeat
POST /ctl/<control-token>/nodes/<index>/release
Content-Type: application/json

{"route_mode":"residential|upstream|direct"}          # route
{"holder":"service-a","ttl_seconds":300}              # claim
{"lease_id":"...","ttl_seconds":300}                  # heartbeat
{"lease_id":"..."}                                    # release
```

`next` 与 `route` 可携带可选护栏 body：`{"lease_id":"...","expected_alloc_version":N}`。
节点列表的每个节点包含 `alloc_version` 与 `lease`（holder/expires_at，不含
`lease_id`）。并发语义、租约生命周期与多服务共存规则以
[并发集成标准](RESIDENTIAL_INTEGRATION_STANDARD.md) 为准。

`next` 只替换指定逻辑节点背后的住宅出口，保留客户端节点名称和认证，并通过 Mihomo Controller
关闭该入站用户的旧连接。其他节点和连接不受影响。轮换过快返回 429，供应商容量耗尽返回 409，
窗口被他人租约占用返回 `409 lease_held`，基于过期版本轮换返回 `409 alloc_version_changed`。

节点列表不向普通订阅用户暴露出口 IP。`/ctl/` 返回的 `exit_ip` 也可能为空，因为读取列表不会
为了填充字段发起 N 个外部探测；调用方需要真实 IP 时应从该节点的数据面代理执行出口探测。

## 数据面入口

- 新住宅渠道创建时选择 VLESS、VMess 或 Trojan over WebSocket，经过 CF 橙云、雷池 443 和 HX Edge Relay。
- HTTP CONNECT、SOCKS5 仅作为客户端本机 Mihomo 的环回落地，不再由住宅渠道公开监听。
- `/sub/` 和 `/ctl/` 是 HTTPS 控制/配置地址，不是 Playwright 的代理地址。
- `/ctl/` 的每个节点都返回协议中立的 `endpoints[]`；旧 `proxy_url` 继续表示第一个浏览器兼容端点。
  托管渠道的 `proxy_url` 为 `null`，但 `endpoints[]` 包含该渠道协议的标准 URI。

```json
{
  "index": 1,
  "node_name": "residential-us-01",
  "endpoints": [
    {
      "protocol": "vless",
      "transport": "ws",
      "uri": "vless://<uuid>@proxy.example.com:443?...",
      "browser_compatible": false
    }
  ],
  "proxy_url": null,
  "residential_endpoint": {
    "protocol": "http",
    "server": "203.0.113.10",
    "port": 8000,
    "username": "<node-user>",
    "password": "<node-password>",
    "tls": false
  },
  "route_mode": "residential"
}
```

`residential_endpoint` 只适用于 `api-list` 提取供应商，并且只在逻辑节点已有分配时出现：延迟分配
的节点在 `GET nodes` 中省略该字段，`POST .../next` 成功分配后返回。它不会出现在 `/sub/`、
管理员渠道/供应商列表或请求日志中。账密网关模式不会下发供应商主账号派生凭据。CF Worker 面板
（BPB）供应商同样不返回该字段：其住宅节点是 VLESS/Trojan WebSocket URI，客户端应使用
`endpoints[]` 经本机 Mihomo 落地。HX-CF-WsPxy 供应商也不返回该字段：CONNECT 端口绑在
本机 127.0.0.1，只给 Mihomo 数据面拨，不经 control API 下发给远程客户端。

代理流量始终由 Mihomo 转发。Go 控制面只维护映射、编译 `IN-USER` 规则、应用候选配置并调用
Mihomo Controller，不实现 HTTP CONNECT、SOCKS5、VLESS、VMess 或 Trojan 协议。

## OutlookRegister 调用顺序

```text
1. GET /ctl/<token>/nodes，读取固定节点池（含 alloc_version 与 lease 状态）
2. 在 OutlookRegister 进程内互斥租用一个空闲 index
3. POST /nodes/<index>/claim，向服务器认领独占租约（holder + ttl）
4. POST /nodes/<index>/next，携带 lease_id 与 expected_alloc_version 刷新住宅出口
5. 按 TTL/3 间隔 POST /nodes/<index>/heartbeat 续租
6. API 提取渠道优先把 residential_endpoint 配置给受管本地 Mihomo；其他渠道从 endpoints[]
   选择 VLESS/VMess/Trojan WS 端点
7. 从选定端点探测出口身份并运行完整 flow
8. flow 结束后 POST /nodes/<index>/release 归还租约；不删除服务端节点
```

多实例共享同一 control URL 时，服务器租约是**跨进程互斥**的唯一权威：
两个实例对同一 index 的 `claim` 只有一个成功，另一个收到 `409 lease_held`。
通道 `session_count` 和供应商 `max_concurrent_sessions` 都应不小于业务并发数。OutlookRegister
的第一个本地实例优先监听 `127.0.0.1:2334`；并发或端口冲突时每个租约使用不同的随机环回端口，
不会让多个 flow 共享无认证入口。控制 token 可执行轮换、消耗供应商配额，并在 API 提取模式读取
临时节点鉴权，权限高于只读订阅 token，应独立轮换且不得进入截图或日志。

## 兼容 `/rot/` 接口

以下旧接口继续服务已有集成，但新代码不得依赖它们创建临时服务端会话：

```text
GET|POST /rot/<token>
PUT|GET|DELETE /rot/<token>/sessions/<session-id>
POST /rot/<token>/sessions/<session-id>/next
POST /rot/<token>/sessions/<session-id>/route
GET  /rot/<token>/sessions/<session-id>/config?format=clash
```

旧 token、会话凭据和响应均不得写入日志。访问日志统一脱敏为 `/rot/[redacted]`；未知、禁用
或不适用的 token 对外统一为 404。旧接口的存在不改变 `/sub/` + `/ctl/` 作为推荐模型的结论。
