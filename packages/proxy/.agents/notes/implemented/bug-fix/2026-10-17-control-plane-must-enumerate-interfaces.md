# Agent Note: 控制面必须能读到本机接口表 (网桥不是"没有", 是"读不到")

Status: implemented

## Problem

2026-10-17 用户在 Docker 网桥齐全的宿主机上打开「Docker 使用方式」, 弹窗断言:

> 本机未检测到 Docker 网桥（docker0 或 compose 的 br-*）。请先在本机启动 Docker 或创建 compose 网络，再重新打开此窗口。

同一台机器、同一 netns 的实测: `ip -o link` 有 `docker0` 和 9 个 `br-*`; 用真实发现函数在宿主上跑
得到 `bridges=10`。差别只有沙箱 —— 生产 unit 把地址族限制成:

```text
# deploy/systemd/hx-pxy.service (修复前)
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
```

Go 的 `net.Interfaces()` / `net.InterfaceAddrs()` 在 Linux 上经 NETLINK_ROUTE 读接口表
(`netlinkRIB`), `AF_NETLINK` 不在白名单里就整表读不到。用真实二进制、空数据目录, 只改沙箱的
`RestrictAddressFamilies` 跑同一条链路 (建组 → 建一个 127.0.0.1 的 mixed 服务 → 取判定):

```text
$ systemd-run --user -p RestrictAddressFamilies="AF_UNIX AF_INET AF_INET6" ./hx-pxyd ... --mihomo ./mihomo
    GET /api/v1/listeners/<id>/docker-access
    state=blocked     reason=bridge_scan_failed bridges=0  plan=no
$ systemd-run --user -p RestrictAddressFamilies="AF_UNIX AF_INET AF_INET6 AF_NETLINK" ./hx-pxyd ... --mihomo ./mihomo
    state=needs_entry reason=loopback_bind      bridges=10 gateway=172.17.0.1 plan=yes
```

宿主同时有 `docker0` 和 9 个 `br-*`。左列的 `bridge_scan_failed` 是本次新增的诚实回答; 修复前同一
环境下弹窗说的正是用户引用的那句 `no_docker_bridge`。

修复前 `DiscoverBridges()` 把 `net.Interfaces()` 的错误吞成空列表, 判定层再把空列表读成"本机没有
网桥" —— 一次"读不到"被说成了"不存在"。同一个沙箱还影响 `internal/hostnetwork`(设备 / TUN 探测)、
`internal/api/system_network.go`(网络诊断) 和 `internal/dataplane/mihomo/compiler_docker_entries.go`
(网桥地址是否还在, 决定 Docker 入口是否被 withheld)。

## Decision

1. 控制面 unit 放行 `AF_NETLINK`: `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK`。
   数据面的 TUN drop-in 早为同一原因放行了它 (`deploy/systemd/hx-pxy-dataplane-tun.conf`), 控制面是
   唯一漏掉的一处; 非 TUN 场景不需要数据面默认放行, 因此只改控制面。
2. `DiscoverBridges()` 返回 `([]Bridge, error)`, 不再把错误折成空列表; `Input` 增加
   `BridgeScanFailed`, 新增 reason `bridge_scan_failed`。只有真读过接口表才允许说 `no_docker_bridge`;
   没读到就说读不到。判定优先级不变: 直连 → 伴随入口 → 计划 → `bridge_scan_failed` →
   `no_docker_bridge` → `no_free_port`; 已有网桥入口的 `entry_unreachable` 同样只在真读过之后才允许。
3. `install_test.go` 增加 unit 契约断言, 防止以后"顺手收紧"沙箱又把它拿掉。

## Alternatives considered

- **什么都不做, 只让用户去改 unit。** 最强理由: 一行配置, 零代码风险, 而且这看起来是部署问题。
  否决: 自带 unit 就是错的, 每个 systemd 安装都命中; 更根本的是"空列表"这个表示法把"读不到"和
  "不存在"压成了同一个值, 换成容器网络命名空间等任何受限环境都会重现同一句谎话。
- **用 sysfs 兜底枚举 (`/sys/class/net/*`)。** 最强理由: 不依赖 netlink, 在更严的沙箱里也能列出网卡。
  否决: 名字能从 sysfs 拿到, 地址和掩码要另找来源(`/proc/net/fib_trie`), 等于在 netlink 之外再养一套
  接口枚举, 而且仍拿不到 `net.InterfaceAddrs()` 的语义; 该修的是沙箱漏放的地址族。
- **数据面 unit 也默认加 AF_NETLINK。** 最强理由: Mihomo 同样会枚举接口。否决: 非 TUN 场景不需要,
  TUN 场景已有显式 drop-in(它同时要 CAP_NET_ADMIN 与 `/dev/net/tun`), 默认放开只是扩大非必需的权限面。
- **只加日志, 不改 API 契约。** 最强理由: 改动最小, 前端与 OpenAPI 都不用动。否决: 用户看的是弹窗,
  不是 journald; 判定由哪一层产生, 结论就该由哪一层给出, 否则下一次"读不到"还是会被当成"没有"。

## Consequences

`bridge_scan_failed` 进入 API 契约: `web/src/lib/types.ts` 的 `DockerAccessReason`、
`internal/api/capabilities_catalog.go` 与 `docs/NETWORK_OPERATIONS.md` 同步。`install.sh` 按版本目录安装
`deploy/systemd/*`, 所以 systemd 安装升级到带此修复的版本后拿到新 unit, 重启控制面生效。
控制面的可见性不等于容器可达性: 网桥地址只是候选, 防火墙、隔离网桥与 rootless Docker 仍需现场验证。

## Testing

`internal/dockeraccess/assess_test.go` 覆盖"扫描失败不得报 `no_docker_bridge`"、"扫描失败但已读到网桥
仍照常出计划"、"扫描失败不把已有网桥入口判成 `entry_unreachable`"; `internal/dockeraccess/service_test.go`
经真实 `Assess` 路径断言 `state=blocked` / `reason=bridge_scan_failed`; `install_test.go` 断言控制面 unit
放行 `AF_NETLINK`。上面 `systemd-run` 的 before/after 是真实二进制、真实沙箱、真实 HTTP 接口下的
手工验证(探针是临时文件, 已删除)。
