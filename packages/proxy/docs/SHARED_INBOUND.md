# 共享入口（一个客户端、两条 URL）

> 20260910 用户决策：取消「每个代理服务绑定一个本地端口、开一个客户端」的模型。
> 现在所有服务复用同一个 Mixed 端口与同一组 WebSocket 端口，由 Mihomo 按
> 用户名（`IN-USER`）分流。一个客户端进程即可覆盖所有代理组与协议。

## 1. 问题

旧模型为每个 Proxy Group 创建一条独立 Listener：

```text
服务 A -> 127.0.0.1:18080 (mixed) -> 组 A
服务 B -> 127.0.0.1:18081 (mixed) -> 组 B
服务 C -> 127.0.0.1:18082 (vless ws) -> 组 A
```

控制面本身只需要一个数据面进程，但客户端侧必须为每个组启动一个客户端实例、
维护多份配置。协议、端口、凭据三者被绑死，增加一个业务就多一个端口。

## 2. 共享模型

一条服务的身份来自**它自己的凭据**，而不是它绑定的端口。因此每个服务仍然保留
自己的 Listener 行（自己的名称、协议、凭据与 Proxy Group），但这些行共享同一个
绑定地址与端口；编译器把它们合并成少量聚合 Listener，并为每个成员生成一条
`IN-USER` 规则：

```text
服务 A --\
服务 B ----> 0.0.0.0:7890 (mixed, users: svc-group-a, svc-group-b)
                              |
                              +-- IN-USER=svc-group-a -> 组 A
                              +-- IN-USER=svc-group-b -> 组 B

服务 C --\
服务 D ----> 127.0.0.1:7891 (vless / vmess / trojan, ws-path /__hx-proxy__/shared)
                              +-- IN-USER=svc-group-a -> 组 A
                              +-- IN-USER=svc-group-c -> 组 C
```

### 端口布局

| 家族 | 协议 | 默认端口 | 监听范围 | 面向客户端 |
| --- | --- | --- | --- | --- |
| standard | http / socks / mixed | 7890 | 可配置（默认 `0.0.0.0`） | 直连或经反代 |
| websocket | vless / vmess / trojan | 7891 | 只允许环回 | 经雷池 / Cloudflare 反代到 443 |

同一家族的 WebSocket 协议共用一个端口与一个 ws-path，因此**一条 URL 同时是
VLESS / VMess / Trojan**：客户端按自己的协议解析，用户名决定落到哪个组。
### 家族载体：一个家族一个监听器

一个家族只对应**一个** Mihomo Listener，它的类型由家族决定，而不是由成员的协议决定：

| 家族 | 成员协议 | 载体 Listener |
| --- | --- | --- |
| standard | http / socks / mixed | `mixed`（一个） |
| websocket | vless / vmess / trojan | 每种协议各一个（共享端口与 ws-path） |

Mihomo 的 Mixed Listener 在**同一个 socket** 上同时讲 HTTP 代理与 SOCKS5，这正是
standard 入口承诺的协议集合。因此 `kind=http` 或 `kind=socks` 的代理服务不会
再生成一个同类型的聚合 Listener，而是由那唯一的 Mixed Listener 承载，靠用户名分流。
这由 `listener.SharedInboundCarrierKind` / `SharedInboundCarrierKey` 统一定义，
控制面与编译器共用同一份规则。

> **20260913 修复**：在此之前，聚合行按「成员协议」建索引，而编译器只接收
> `kind=mixed` 成员。结果是一个 `kind=http` / `kind=socks` 的服务被迁移进共享
> 家族后，行仍在、**数据面里却什么都没有**：既没有 Listener 也没有 `IN-USER` 规则，
> 客户端按订阅里的 URL 连接时被拒（HTTP 代理返回 403）。同一个家族还会因为成员
> 协议不同而生成多个聚合行。现在家族只认载体，一个家族永远只有一个 standard 聚合行；
> 升级时旧的「每协议一行」聚合行会被自动回收（保留家族的 Mixed 行），不会留下占着
> 端口的死行。

### 为什么用户名能区分服务

Mihomo 的 Listener 支持 `users` 列表，规则引擎提供 `IN-USER` 匹配。住宅代理早已
使用同一机制（每个声明会话一个 `IN-USER` 路由），共享入口只是把它应用到普通服务。
编译顺序保证成员选择先于站点路由：

```text
1. 住宅会话路由      AND,((IN-NAME,...),(IN-USER,hx-session-...)),<出口>
2. 共享入口成员选择  AND,((IN-NAME,hx-in-shared-...),(IN-USER,svc-<group-id>)),<组名>
3. 管理员站点别名    AND,((IN-NAME,...),(DOMAIN-SUFFIX,...)),REJECT|DIRECT|<组>
4. 兜底              MATCH,DIRECT
```

成员用户名由 Proxy Group ID 派生（`svc-<group-id>`），因此一个服务无论被编辑
多少次、无论它的 Listener 行被重建多少次，订阅里的用户名与密码都保持稳定。

## 3. 默认归属与迁移

> **20260926 用户决策：共享入口是默认值，不是可选开关。**
> 「永远只应该占用 1 个端口，也就是 hx-proxy 它的前端入口的那个端口作为整体的入口」。
> 其他端口对外不可知、不可用；被外部使用即等于共享入口被绕过，是事故。

- **不传 `shared_inbound` = 加入家族**（http / socks / mixed → standard）。
  此前空值等于「独占一个端口」，于是每个不知道共享入口的调用方都在产出旧模型：
  一次实测 61 个 mihomo 监听端口，其中 59 个是没有任何标记的 Mixed Listener。
- **高级协议（vless / vmess / trojan）保持自带端口**。这是实测结论：本 build 里唯一会创建的
  高级协议 Listener 是住宅渠道入口，它自带 per-session `IN-USER` 路由与专属 ws-path，
  聚合入口表达不了；而创建请求里没有任何字段能把它和普通 VLESS 服务区分开。
  它是数据面内部端口（仅环回），不是对外入口。
- **成员行不保留调用者给的端口**：成员不拥有 socket，行上记的必须是被真正服务的入口端点。
- **升级迁移（schema 37）直接改写存量行**，不做过渡层：把所有
  `shared_inbound=''` 且 `kind IN (http, socks, mixed)` 的行并入 standard 族。
  改写**以该安装确实在用共享入口为条件**——`per_service` 是管理员的显式选择，
  按共享改写会让那些行两边都不编译（直接路径跳过成员、聚合路径未启用）。
- **缺凭据的成员在收敛时自动补发凭据**。共享入口按用户名分流，无凭据成员会让编译器
  **拒绝整份配置**（不是跳过它），从而把整个数据面带停。迁移前的实测里有 58/60 行属于这类。
- **升级不移动端口**：已存储的全局配置若不含 `shared_inbound` 段，加载时被标记为
  `per_service`，保持旧行为；只有管理员在「全局配置 → 性能与运行」显式打开共享入口
  才会迁移。
- **打开共享入口时自动迁移**：`internal/sharedinbound` 把现有服务逐个改写到聚合
  端口，并与聚合行收敛在同一次原子 Apply 中；失败会连同设置一起回滚。
- **迁移跳过两类服务**：
  - 未启用用户名/密码认证的服务（共享入口靠用户名分流，无凭据会让聚合端口对所有
    连接放行）；被跳过的服务会以 `WARN` 日志列出名称与原因，不会被静默忽略——
    否则操作者只能靠试错才能发现某个服务为什么没被迁移；
  - 住宅渠道托管的 WebSocket 入口（它们有自己的会话级 `IN-USER` 路由）。
- **关闭共享入口**：聚合行被删除、端口释放，服务行保留（仍标记为成员，重新打开即
  恢复），不影响任何已有订阅以外的流量。

## 4. 订阅导出

成员服务的 `/sub/<token>` 不再导出自己的内部端口，而是导出聚合入口：

- standard 成员导出共享 Mixed 的绑定地址/公网主机名；
- websocket 成员导出 `shared_inbound.ws_public_host`，未配置时该链接返回
  `ErrShareDisabled`（404），避免把内部环回端口写进订阅。

## 5. 端点唯一性

- 专用（`per_service`）Listener 之间仍然禁止端口冲突；
- 同一共享家族（且仅有同一家族）允许复用端口——这正是协议多路复用的前提；
- 不同家族之间冲突仍然报错。

## 6. 配置参考

```json
{
  "shared_inbound": {
    "mode": "shared",
    "mixed_bind_address": "0.0.0.0",
    "mixed_port": 7890,
    "ws_port": 7891,
    "ws_public_host": "proxy.example.com",
    "ws_public_port": 443,
    "mixed_public_host": "",
    "mixed_public_port": 0,
    "mixed_public_tls": false
  }
}
```

`mode` 取值 `shared` 或 `per_service`。仅当 `mode=shared` 时其余字段参与校验与编译。

## 7. 验证

```bash
# 单元测试：编译形状、成员规则、端口复用
go test ./internal/dataplane/mihomo/ ./internal/listener/ ./internal/sharedinbound/

# 真实 Mihomo 校验：配置能被 mihomo -t 接受
PATH=$PATH:/path/to/mihomo go test -run TestCompileSharedInboundPassesMihomoValidation ./internal/dataplane/mihomo/

# 运行时证明：同一端口上两个用户名走到两个不同的组
PATH=$PATH:/path/to/mihomo go test -run TestSharedInboundRoutesOnePortPerService ./internal/dataplane/mihomo/

# 家族载体：http / socks / mixed 三个成员都必须由同一个 Mixed 入口真正代理
PATH=$PATH:/path/to/mihomo go test -run TestSharedInboundCarriesEveryStandardProtocolAtRuntime ./internal/dataplane/mihomo/

# 端到端：真实数据库 + 真实迁移 + 真实编译，HTTP 服务必须被承载
go test -run TestSharedInboundMigrationCarriesAnHTTPServiceThroughToTheCompiledConfig ./internal/dataplane/mihomo/
```
