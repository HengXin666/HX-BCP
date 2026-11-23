# Agent Note: 住宅供应商用 HX-CF-WsPxy 拿 Cloudflare colo 出口, 不把 WSP1 教给 Mihomo

Status: implemented

## Problem

HX-CF-WsPxy 把 Cloudflare anycast/colo IP 当成住宅出口: 本机 SessionPlane
`POST /session` 开会话, 返回 `http://127.0.0.1:<port>` CONNECT 监听; 同一条
WS + 同一 pin 软粘滞到同一 colo, `POST /rotate` 换 pin, `DELETE` 拆会话。
WSP1 是 WsPxy 与 Worker 之间的私有协议。

住宅控制面原先只有账密网关、api-list、cf-worker 三种接入。cf-worker 走的是
BPB 面板订阅里的 VLESS/Trojan, 出口仍是 CF, 但每次 next 都重新拉订阅、换节点
URI, 既没有 SessionPlane 的会话生命周期, 也不能原地换 colo。把 WSP1 写进
Mihomo outbound 会让数据面承担一种没有官方 schema 的协议, 违反 v1 数据面契约。

## Decision

新增轮换模式 `hx-cf-wspxy`。供应商 `api_url` 只接受本机 SessionPlane origin
(`http://127.0.0.1:port` / `::1` / `localhost` + 端口, 无路径/查询/用户信息)。
这是对 `validateAPIURL`「必须公网 HTTPS」的显式例外, 不是通用 SSRF 开口。

控制面职责停在 SessionPlane HTTP:

- 分配: `POST /session`, 把返回的 CONNECT 写成普通 Mihomo `http` 节点
  (`server=127.0.0.1`, `port=会话端口`)
- `next`: 优先 `POST /session/:id/rotate`, 指纹和 CONNECT 端口不变; rotate
  失败再开会话并换节点
- 释放 / 渠道删除 / 探测结束: `DELETE /session/:id`
- 探测与懒创建可达性: `GET /health`, 不靠 mint 再丢弃来探活

会话 id 加密进节点 canonical(`hx_wspxy_session_id`), Mihomo 编译器在
`convertNodeConfig` 里剥掉, 与 `hx_dialer_proxy_group_id` 同一条剥离路径。
指纹哈希 `wspxy_session` id, 不哈希 host:port, 因为 rotate 保持端口。
TTL 强制 0。`residential_endpoint` 不下发本机 CONNECT, 客户端仍走渠道
Listener。

SQLite `rotation_mode` CHECK 重建为含 `hx-cf-wspxy` 的 v35。

## Alternatives considered

**把 WSP1 做成 Mihomo outbound / Go 数据面协议。** 一次拨号少一跳, 客户端也能
直接连 Worker。否决: v1 数据面是 Mihomo, 控制面不实现 CONNECT/SOCKS/VLESS;
WSP1 没有官方 schema, 教给 Mihomo 等于把私有协议锁进 YAML 编译器。

**复用 `cf-worker` 路径, 把 SessionPlane 当另一种面板 URL。** 少一个供应商类型。
否决: cf-worker 拉的是 VLESS/Trojan 订阅, next 换的是节点 URI; WsPxy 要的是
会话生命周期和原地 rotate。硬塞进去会把两种出口语义搅在同一套 fetch 里。

**`api_url` 沿用 `validateAPIURL` 的公网 HTTPS 规则, 控制面走反向代理。** 看起来
不用开 loopback 例外。否决: SessionPlane 绑在 127.0.0.1, 公网 URL 校验会直接
拒绝唯一合法目标; 若放宽到任意内网主机, 供应商保存就变成 SSRF。例外收窄到
loopback origin + 端口。

**每次 next 都 `POST /session` 新开会话。** 实现更短。否决: 产品要的是同一
CONNECT 端口上换 colo pin; 新开会话会换端口, 节点指纹跟着变, Mihomo 成员集合
抖动。rotate 失败才回退到新会话。

## Consequences

- 住宅供应商多了一种必须本机跑 SessionPlane 的接入。没有本机
  `HX-CF-WsPxy` 进程, 渠道分配会失败。
- 控制 URL 泄漏到远程客户端会暴露本机 CONNECT。canonical 加密保存,
  管理 API 只回显「已配置」, `residential_endpoint` 不返回该 URL。
- 会话泄漏会占 colo pin。分配失败、发布失败、渠道删除、探测结束都必须
  DELETE; 刷新成功后才拆旧池, 刷新失败拆的是新池。
- 出口多样性受 Cloudflare colo 数量限制, 不是真住宅 IP 池。文档必须写清
  这是 colo 出口, 不是账密住宅网关。

## Testing

- `go test ./internal/residential/ -run 'TestCreateHXCFWsPxy|TestHXCFWsPxy|TestValidateWsPxy'`:
  供应商保存、loopback URL 拒绝公网、粘滞分配、next 走 Rotate 不 Create、
  释放 Destroy、rotate 失败回退新会话、探测结束后无泄漏。
- `go test ./internal/dataplane/mihomo/ -run TestConvertNodeConfigStripsWsPxySessionID`:
  编译器不把 `hx_wspxy_session_id` 写进 YAML。
- `go test ./internal/store/ ./internal/api/`: v35 迁移与能力目录枚举含
  `hx-cf-wspxy`。
