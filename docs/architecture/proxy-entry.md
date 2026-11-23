# 统一出口代理: 入口方言与槽位

`packages/proxy` (由 hx-pxy 并入) 把三类出口聚合成**一个入口**, HTTP 代理与 SOCKS5 共用一个端口:

| Pool | 来源 | 说明 |
|---|---|---|
| `resi` | 住宅代理平台 (网关账号) | 真住宅, 按流量计费, 计入租户住宅用量与配额 |
| `subresi` | 订阅节点 | 源被声明为 residential, 或出口探测判定为住宅网络 (非机房, 非已知代理), 判定 72 小时内有效 |
| `sub` | 订阅节点 | 全部订阅节点 (含 subresi) |
| `direct` | 本机 | 把本机当出口; 只放行公网单播地址, 内网, 保留段和本机地址一律拒绝 |
| 代理组 | 代理组 | 用 `group-<组名>` 直接走某个已建代理组, 需单独授权 `group:<组名>` (或 `group` 授权全部) |

## 上游: 住宅平台 = 网关 + 用户名方言

住宅平台没有统一协议, 但几乎都是 HTTP/SOCKS5 网关, 参数写在用户名里, 各家只差拼法。
接一家平台只写一条用户名模板, 不写代码 (`internal/residential/template.go`):

```text
{user}_area-{region}_life-{ttl|min}_session-{session}
```

- 占位符: `user` `session` `region` `country` `city` `ttl`
- 过滤器: `lower` `upper` 改大小写; `sec` `min` `hour` 把秒数的 TTL 向上取整换单位

预设登记在 `internal/residential/presets.go`。入口直接拨 session-template 与 per-request 两类网关;
API 提取 / CF Worker / WsPxy 仍经住宅渠道使用。

## 下游: 入口也说同一种方言

接入方只需要会配 `http/socks5 + 用户名`, 不用调 API (`internal/entry/username.go`):

```text
socks5://acme-pool-resi-cc-us-session-job42-ttl-30:<租户密码>@127.0.0.1:7880
http://acme-pool-sub-cc-jp:<租户密码>@127.0.0.1:7880
hxpxy://acme:<租户密码>@127.0.0.1:7880?pool=subresi&cc=us&session=a1   (HX-BCP 浏览器的简写)
```

| 键 | 别名 | 值 |
|---|---|---|
| (第一段) | | 租户名 |
| `pool` | | `resi` / `subresi` / `sub` / `direct`, 缺省 `sub` |
| `group` | `g` | 代理组名, 不能与 resi 以外的 pool 同用; 与 `pool-resi` 同用时表示住宅平台名 |
| `cc` | `country` `region` `area` | ISO 两位国家码 |
| `city` / `asn` | | 进一步筛选 (需出口探测数据) |
| `session` | `sessid` `sid` `slot` | 槽位 ID; 不带则每连接轮换 |
| `ttl` | `sesstime` `stime` `life` | 槽位保持分钟数, 1..1440, 缺省 30 |
| `loss` | | 槽位节点消失时: `rebind` (缺省) / `hold` / `fail` |

键不分大小写、顺序任意; 未知键和重复键直接拒绝 (HTTP 403 / SOCKS 认证失败)。
租户默认只授权 `sub` 与 `subresi`: `resi` 花钱, `direct` 暴露本机网络, 代理组可能是别人的住宅渠道, 都要显式授权。
明文 HTTP 代理每个连接只转发一个请求 (强制 `Connection: close`), 后续字节不会带着代理凭据发给目标。

## 槽位与节点分离

节点 ID 来自配置指纹, 上游刷新会让它消失。客户端不再钉节点, 而是声明槽位 (`internal/slot`):

- 槽位 ID 由客户端给, 永远不变; 背后绑哪个节点是服务端状态, TTL 内每次使用都续期。
- 绑定记住当时的条件 (pool / 国家 / 城市 / ASN) 和节点的端点身份; 同名槽位换了条件就重新绑定。
- `rebind`: 换到满足同一 pool / 国家 / 城市 / ASN 的节点; **同一 server:port:协议的继任节点优先**,
  所以上游只改名或改参数时出口不变 (旧节点已从快照消失也能找到)。
- `hold`: 节点被禁用或隔离后 5 分钟宽限内继续用旧节点, 之后再换; 节点从订阅里消失则立即换。
- `fail`: 直接报错, 给不能容忍出口变化的场景。
- 住宅平台的槽位: 供应商 session id 由 (租户, 槽位, 平台, 绑定时间) 派生; 供应商的 session 从首次使用起计时,
  所以绑定到 ttl 就结束换新 session, 不随使用续期。
- 运维可在控制台或 `DELETE /api/v1/entry/slots/<租户>/<槽位>` 释放槽位, 下次连接换新出口。

需要多服务互斥同一住宅 IP 的自动化, 继续用住宅渠道 `/ctl` 的 claim / heartbeat / release 与 `alloc_version` CAS。

## 人用: Clash 订阅

`GET /entry/sub/<租户id>/<token>` (控制台「统一入口」页一键复制) 返回 Clash 配置 (只含 proxies 与分组, 不写本地端口):
每个节点是入口上的一个预置槽位 (`SUB-US-01` ...), 用户名是 pool / 国家 / 序号的纯函数,
所以重拉订阅不会改名, 节点背后的出口只在槽位换绑时变。参数: `pool` `cc=us,jp` `slots=5` `protocol=http` `host` `port`;
不带 host / port 时用 `HX_PXY_ENTRY_PUBLIC_ADDRESS`, 再退回面板域名与入口端口。
token 由租户密码派生, 轮换密码后旧链接失效。

## 程序用: 管理 API

| 路由 | 作用 |
|---|---|
| `GET /api/v1/entry` | 入口状态, 各池可用数, 代理组与住宅平台 |
| `GET /api/v1/entry/nodes?pool=subresi` | 可路由节点及国家, ASN, 探测时间 |
| `GET/POST /api/v1/entry/tenants` | 租户列表 / 新建 (密码只返回一次) |
| `PUT/DELETE /api/v1/entry/tenants/<id>` | 改池, 配额, 启停, `rotate_secret` / 删除 |
| `POST /api/v1/entry/tenants/<id>/reset-usage` | 新计费周期, 清零住宅用量 |
| `GET /api/v1/entry/tenants/<id>/share` | Clash 订阅路径 |
| `GET /api/v1/entry/slots` / `DELETE .../slots/<租户>/<槽位>` | 槽位列表 / 释放 |

## 链路

```text
客户端 --(http/socks5, 方言用户名)--> 入口 (HX_PXY_ENTRY_LISTEN)
   入口 --sub/subresi/组--> 127.0.0.1:27890 Mihomo hub (每节点/每组一个内部用户) --> 节点
   入口 --resi--> 住宅网关 (可经平台配置的上游代理组, 同样走 hub)
   入口 --direct--> 目标
```

入口只解析代理握手, 不碰 TLS 与上层协议; 节点协议仍全部由 Mihomo 处理。
hub 只监听环回, 内部用户共享一个控制面生成的密钥 (存在 system_metadata)。
hub 路由全部收在 `sub-rules.hxe-hub` 里, 顶层只多一条 `SUB-RULE,(IN-NAME,hxe-hub),hxe-hub`, 现有 listener 的匹配开销不变;
未知用户命中子规则末尾的 REJECT。入口流量不经过站点别名等 routing 规则。
hub 端口被 listener 占用或带 hub 的配置被 Mihomo 拒绝时, 控制面去掉 hub 重新下发, 现有服务照常更新, 控制台显示原因。
入口自己的外连 (direct 与直连住宅网关) 绑定与 Mihomo 相同的出口网卡, 宿主 TUN 不会截获。

## 配置

| 环境变量 | 缺省 | 说明 |
|---|---|---|
| `HX_PXY_ENTRY_LISTEN` | 空 (关闭) | 入口地址, 如 `0.0.0.0:7880`; 为空时入口与 hub 都不启用, 数据面配置与旧版完全一致 |
| `HX_PXY_ENTRY_PUBLIC_ADDRESS` | 空 | 写进 Clash 订阅的 `host:port` |
| `HX_PXY_ENTRY_HUB_PORT` | `27890` | 环回 hub 端口 (避开 7890/7891 与 docker 伴随端口窗口 17890-17989) |
| `HX_PXY_ENTRY_EXIT_PROBE` | `0` | `1` 开启出口探测: 经节点请求 ip-api.com (明文, 节点可伪造应答), 每分钟至多 30 个, 24 小时复测 |

入口端口被占, 租户密钥解不开或入口初始化失败时, 只记错误 (控制台标红), 控制面与数据面照常运行。
每租户最多 1024 条并发连接, 空闲 10 分钟断开; 住宅流量边传边计, 超出配额立即断开;
停用, 轮换密码或删除租户会立即断开它的连接, 删除还会释放它的槽位。

## 兼容

全部增量, 默认关闭: 现有 Listener、端口、`/sub/` 与 `/ctl/` 接口、住宅渠道与预设、声明节点不变; 迁移 v43 只新建两张表。
v43 之后旧二进制会因 schema 更新拒绝启动, 回滚版本需先恢复升级前的数据库备份。
旧的钉节点 listener 与单节点组规则保留, 等接入方迁到入口、旧入口流量归零后再删。

## 发版

`packages/proxy` 走独立 tag 线 `proxy-vX.Y.Z`, 根目录 `.github/workflows/proxy-release.yml` 构建,
发布包仍叫 `hx-pxy_vX.Y.Z_linux_<arch>.tar.gz`, 已安装机器的升级路径不变。
