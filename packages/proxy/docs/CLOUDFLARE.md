# Cloudflare 与雷池接入

HX-Pxy 的公网推荐拓扑是：

```text
客户端 -> Cloudflare 橙云 :443 -> 雷池 HTTPS :443
       -> 127.0.0.1:19090 -> HX Edge Relay
       -> 127.0.0.1:<Mihomo WS Listener>
       -> Proxy Group -> 机场或住宅节点
```

HX Edge Relay 只接受固定 `/__hx-proxy__/` 命名空间内、能匹配已启用 Listener 的 WebSocket
Upgrade，并按原始 `Host + Path` 转发到环回 Mihomo。它不解析 VLESS、VMess、Trojan，不接受
任意 TCP/UDP/URL 目标，也不进入代理协议的数据转发逻辑。

## 协议边界

| 客户端入口 | CF 橙云 + 雷池七层 443 | 说明 |
| --- | --- | --- |
| VLESS over WebSocket | 支持 | 协议与认证由 Mihomo 处理 |
| VMess over WebSocket | 支持 | 协议与认证由 Mihomo 处理 |
| Trojan over WebSocket | 支持 | 协议与认证由 Mihomo 处理 |
| HTTP CONNECT | 不支持 | 需要真实 TCP 端口或本地 Mihomo 落地 |
| SOCKS5 | 不支持 | 需要真实 TCP 端口或本地 Mihomo 落地 |
| Mixed | 不支持 | 其 HTTP/SOCKS 字节流不能被七层 WS 链路转换 |
| Hysteria2/TUIC/QUIC | 不支持 | 需要 UDP/四层产品或直连 |

Cloudflare 和雷池不会把 HTTP CONNECT、SOCKS5 或 Mixed 自动转换成 WebSocket 协议。需要这些
入口时，选择以下一种方式：

1. 显式创建带用户名密码的公网直连 Listener，并在防火墙中只开放所需 TCP 端口。该方式绕过
   Cloudflare/WAF 并暴露源站地址。
2. 客户端导入对应代理服务或住宅渠道的 VLESS/VMess/Trojan WS 订阅，由客户端本机 Mihomo 提供
   `127.0.0.1:7890` 等 HTTP/SOCKS/Mixed 落地端口。

## HX-Pxy 入口配置

在普通代理服务或住宅渠道中选择 `VLESS WS`、`VMess WS` 或 `Trojan WS`：

- 绑定地址固定为 `127.0.0.1` 或 `::1`。
- 本地端口只供 Mihomo 监听，例如 `18088`，不向公网开放。
- 公网端点填写客户端实际连接的橙云域名、TLS 和 443。
- WebSocket Path 会规范化到 `/__hx-proxy__/<segment>`；必须原样保留完整路径。
- VLESS/VMess 使用 UUID，Trojan 使用高强度随机密码。Path 只是路由标识，不是认证。

住宅 sticky 渠道的声明节点使用独立稳定凭据，不能发布渠道 Listener 的 provisioning 凭据。
出口 IP 在服务端内部轮换，客户端节点配置不随 `next` 变化。

## Cloudflare 设置

1. 为代理域名创建 A/AAAA 记录并开启橙云。
2. SSL/TLS 使用 `Full (strict)`，雷池部署有效源站证书。
3. 保持 WebSocket 可用；代理 Path 不启用缓存、Body 改写或交互式质询。
4. 防火墙只允许 Cloudflare 官方地址段访问公开 80/443；Mihomo WS Listener 始终只监听环回。
5. 管理页面和 `/api/v1/` 不应放进公开代理站点。公开站点只转发订阅、住宅控制和 WS 路由。

## 雷池或 Nginx 路由

以下路径都回源 HX 控制面 `127.0.0.1:19090`：

```nginx
location ^~ /sub/ {
    proxy_pass http://127.0.0.1:19090;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_read_timeout 30s;
}

location ^~ /ctl/ {
    proxy_pass http://127.0.0.1:19090;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_read_timeout 30s;
}

# 旧客户端兼容；新客户端使用 /sub/ + /ctl/。
location ^~ /rot/ {
    proxy_pass http://127.0.0.1:19090;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_read_timeout 30s;
}

location ^~ /__hx-proxy__/ {
    proxy_pass http://127.0.0.1:19090;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_read_timeout 300s;
}
```

雷池必须保留原始 Host、完整 URI Path、查询参数和 WebSocket Upgrade。所有这些路径都回源
19090；不要把 `/__hx-proxy__/` 直接写死到某个 Mihomo 端口，HX 会根据 Host/Path 精确选择
环回 Listener。路由需放在 SPA fallback 或默认站点之前。

## 订阅与住宅控制

管理员在「代理服务」页面找到对应住宅渠道服务，并复制该渠道的 Clash/Mihomo 订阅：

```text
https://proxy.example.com/sub/<share-token>?format=clash
https://proxy.example.com/sub/<share-token>?format=v2rayn
https://proxy.example.com/sub/<share-token>?format=sing-box
```

每个订阅只发布所属普通 Listener 或住宅渠道的节点，不跨渠道聚合。住宅页面不承担订阅复制职责。
默认格式为 v2rayN Base64 URI；`format=uri` 用于诊断。share token 可以读取客户端代理凭据，
不得进入日志、截图或工单。

OutlookRegister 使用独立控制地址：

```text
https://proxy.example.com/ctl/<control-token>
```

该地址同样从「代理服务」页面的“复制自动化控制 URL”动作获取。`/ctl/` 只控制住宅逻辑节点的
出口映射，不承载代理流量。OutlookRegister 读取返回的 VLESS/VMess/Trojan WS 端点并托管本机
Mihomo，将其落地为浏览器使用的环回代理。

## 导入诊断

使用脱敏 token 从 Cloudflare 外部网络请求：

```bash
curl -i -A 'clash-verge' \
  'https://proxy.example.com/sub/REDACTED?format=clash'
```

正确响应应为 200、`application/yaml; charset=utf-8`，包含
`X-HX-Subscription-Format: clash` 和顶层 `proxies:`。如果返回 HTML、质询页或没有 HX 响应头，
请求没有到达订阅处理器，应先修正 CF/雷池路由。完成诊断后清理包含真实 token 的 Shell 历史。

分发订阅前还必须从外部网络完成真实代理请求；订阅下载成功只证明控制路径可达，不证明
WebSocket 数据路径、Mihomo 配置或住宅供应商出口可用。
