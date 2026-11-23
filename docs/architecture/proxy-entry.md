# 统一出口代理: 入口方言与槽位

`packages/proxy` (由 hx-pxy 并入) 把三类出口聚合成**一个入口**:

| Pool | 来源 | 说明 |
|---|---|---|
| `resi` | 住宅代理平台 | 真住宅, 按流量计费 |
| `subresi` | 订阅节点 | 探测出口为住宅 IP 的订阅节点, 判定带采样时间, 过期不参与选路 |
| `sub` | 订阅节点 | 普通订阅节点 |
| `direct` | 本机 | 把本机当出口 |

## 上游: 住宅平台 = 网关 + 用户名方言

住宅平台没有统一协议, 但几乎都是 HTTP/SOCKS5 网关, 参数写在用户名里, 各家只差拼法。
所以接一家平台只写一条用户名模板, 不写代码 (`internal/residential/template.go`):

```text
{user}_area-{region}_life-{ttl|min}_session-{session}
```

- 占位符: `user` `session` `region` `country` `city` `ttl`
- 过滤器: `lower` `upper` 改大小写; `sec` `min` `hour` 把秒数的 TTL 向上取整换单位

预设登记在 `internal/residential/presets.go`。API 提取 / CF Worker / WsPxy 三种不走网关的来源保持原样。

## 下游: 入口也说同一种方言

接入方只需要会配 `http/socks5 + 用户名`, 不用调 API (`internal/entry/username.go`):

```text
socks5://acme-pool-resi-cc-us-session-job42-ttl-30:<密码>@127.0.0.1:7890
```

| 键 | 别名 | 值 |
|---|---|---|
| (第一段) | | 租户 |
| `pool` | | `resi` / `subresi` / `sub` / `direct`, 缺省 `sub` |
| `group` | `g` | 指定代理组, 与 pool 二选一 |
| `cc` | `country` `region` `area` | ISO 两位国家码 |
| `city` / `asn` | | 进一步筛选 |
| `session` | `sessid` `sid` `slot` | 槽位 ID; 不带则每连接轮换 |
| `ttl` | `sesstime` `stime` `life` | 槽位保持分钟数, 缺省 30 |
| `loss` | | 槽位节点消失时: `rebind` (缺省) / `hold` / `fail` |

键不分大小写、顺序任意; 未知键和重复键直接拒绝。

## 槽位与节点分离

节点 ID 来自配置指纹, 上游刷新会让它消失。客户端不再钉节点, 而是声明槽位 (`internal/slot`):

- 槽位 ID 由客户端给, 永远不变; 背后绑哪个节点是服务端状态。
- `rebind`: 换到满足同一 pool / 国家 / ASN 的另一个节点, 槽位名不变。
- `hold`: 节点死掉后宽限期内继续用旧节点, 宽限期过后再换。
- `fail`: 直接报错, 给不能容忍出口变化的场景。

人类用户的 Clash 订阅里, 每个节点就是一个预置槽位 (`US-01`...), 名字永远稳定, 不用重拉订阅。
需要多服务互斥的自动化继续用 `/ctl` 的 claim / heartbeat / release 与 `alloc_version` CAS。

## 兼容

全部增量: 现有 Listener、端口、`/sub/` 与 `/ctl/` 接口、住宅声明节点不变。
旧的钉节点 listener 与单节点组规则等新入口接完、旧入口流量归零后再删。

## 进度

| 步骤 | 状态 |
|---|---|
| 用户名模板过滤器, 修正 BestProxy / RapidProxy 预设把秒当分钟 | 已完成 |
| 入口用户名解析 `internal/entry` | 已完成 |
| 槽位绑定表 `internal/slot` | 已完成 |
| 入口服务: 解析用户名 -> 槽位 -> 编译后的节点出站 | 待做 |
| `subresi` 探测与过期 | 待做 |
| 订阅节点身份键改为 (来源, server:port, 协议, 凭据) | 待做 |
| 控制台收敛为 Source / Pool / Entry | 待做 |
| HX-BCP `hxpxy://` 改为直接拼入口方言 | 待做 |
