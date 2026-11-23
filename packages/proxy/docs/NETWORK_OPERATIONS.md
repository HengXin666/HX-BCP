# TUN、Docker 网络与出口隔离

## 默认行为

TUN 默认关闭。控制面只生成配置和调用受管 Mihomo，不转发 TUN 数据。
「全局配置 → 运行」可探测当前接口、选择出口代理组、启用 TUN 并保存应用。
DNS 必须开启；出口组必须存在且启用。站点规则优先，TUN 默认规则仅影响 TUN 入站。
私有网段、环回、链路本地地址排除；不启用 auto-redirect、不自动修改 Docker 防火墙。
应用会检查 `hx-pxy-tun` 是否 UP，失败走既有回滚；接口 UP 不等于互联网可达。

## systemd 数据面权限（显式启用）

生产安装默认仍无网络管理 capability。管理员决定启用后执行：

```sh
sudo install -d -m 0755 /etc/systemd/system/hx-pxy-dataplane.service.d
sudo install -m 0644 /usr/local/lib/hx-pxy/current/deploy/systemd/hx-pxy-dataplane-tun.conf \
  /etc/systemd/system/hx-pxy-dataplane.service.d/tun.conf
sudo systemctl daemon-reload
sudo systemctl restart hx-pxy-dataplane
```

此 drop-in 只给 Mihomo 开放 `/dev/net/tun`、AF_NETLINK、CAP_NET_ADMIN/CAP_NET_RAW；
不会给控制面授权。控制面自己的 unit 本来就带 AF_NETLINK：Go 通过 NETLINK_ROUTE 读取接口表，
去掉它控制面会看到 0 个接口，Docker 网桥 / LAN 发现和 Docker 入口编译会全部失效。内核需支持 TUN，容器部署还需要宿主显式传入设备和能力。
页面显示的设备可见性来自控制面沙箱，不能代替数据面权限验证。
回退：先在页面关闭 TUN 并应用，再移除上述 `tun.conf`、daemon-reload、重启数据面。
远程操作前保留备用管理连接。不要在未验证路由的情况下同时开启多个 TUN。

## 排除其他本地代理

受管子进程与生产 Mihomo unit 均清除大小写 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/
FTP_PROXY/NO_PROXY。此措施不改变控制面订阅抓取策略，也不能绕开内核的其他 TUN 路由。

其他代理若捕获 Mihomo 的流量，应优先在其配置中排除专用 `hx-pxy` 服务 UID。
若该代理不支持，可由宿主管理员审查 `ip rule`、`ip -6 rule`、main 表默认路由后，
添加优先于其他 TUN、晚于 local 表的 UID 策略。以下为示例，**不是自动修复命令**：

```sh
id -u hx-pxy
ip rule show
ip -6 rule show
ip route show table main
ip -6 route show table main
# 将 UID 和 PREF 替换为实际数值；先确认 PREF 未占用且先于其他代理规则。
# sudo ip rule add pref PREF uidrange UID-UID lookup main
# sudo ip -6 rule add pref PREF uidrange UID-UID lookup main
# 撤销使用完全相同的选择器，将 add 替换为 del。
```

不要对桌面登录用户 UID 做全局绕过：它会让该用户的其他应用也绕过代理。
main 表本身被其他代理改写、基于防火墙重定向或无原生 IPv6 出口时，上述方法不充分。
验证应在专用服务用户身份下进行出站请求，并观察路由与其他代理连接列表；不能仅凭出口 IP
或网卡绑定就宣称隔离成功。此版本不自动执行这些宿主策略修改。

## Docker 地址与诊断

服务行的「Docker 使用方式」由服务端判定（`internal/dockeraccess`），不会停在“不能直连”的提示上：

- **可直连**（`state=ready`）：带认证、非环回的 HTTP/SOCKS/Mixed 入口（含共享 Mixed 成员）。弹窗用
  宿主网桥候选 IP 和实际端口复制**包含认证的完整 URI**，不会退回 Cloudflare 公网域名。Mixed
  同时给出 `http://`（填 `HTTP_PROXY` 与 `HTTPS_PROXY`，HTTPS 走 CONNECT）和 `socks5h://`
  （填 `ALL_PROXY`，由代理解析 DNS）。`0.0.0.0` 只是绑定范围，不是客户端连接地址。
- **需要 Docker 入口**（`state=needs_entry`）：环回绑定、无认证或 VLESS/VMess/Trojan 服务。弹窗预览
  一个与本服务出口完全相同（同代理组 / 固定节点）的 Mixed「Docker 入口」：共享 Mixed 入口已启用
  且非环回时作为其成员（新用户名，不新开端口）；否则只绑定所选 Docker 网桥网关地址
  （docker0 优先，可切换到 compose 的 br-*），端口取 17890–17989 中空闲者，绝不绑 0.0.0.0。
  确认后才创建，凭据随机生成；同一窗口可一键撤销。删除源服务时一并删除其 Docker 入口。
- **住宅渠道**（`client_landing`）：沿用订阅 / 自动化控制 URL。WS 服务也可展开“在容器里用 Mihomo
  客户端落地订阅”：客户端配置 `mixed-port: 7890`，共享网络命名空间用 `http://127.0.0.1:7890`，
  不同容器用私有网络的 `http://mihomo:7890` 并开启 `allow-lan: true`。
- **无法自动处理**（`blocked`）：服务已停用（已有 Docker 入口时仍可在弹窗撤销）；需要新建入口但本机
  没有 Docker 网桥（`no_docker_bridge`；已绑定通配地址且带认证的服务仍可用 LAN 地址直连）；控制面
  读不到本机接口表（`bridge_scan_failed`，此时不能断言没有网桥——应检查控制面部署，而不是 Docker
  是否在运行）；所选网桥上 17890–17989 已无空闲端口（`no_free_port`）；或 Docker 入口所在网桥已消失
  / 共享入口已关闭（`entry_unreachable`，撤销后按当前网络重建）。弹窗说明需要先做什么，不提供代理
  URI 复制按钮。
- 数据面只编译仍有效的 Docker 入口：源服务停用或网桥地址已不在本机时，该入口不会进入配置，
  不会拖垮其他服务的应用。被剔除的网桥入口每 30 秒复查一次，网桥地址出现（例如 dockerd 晚于
  hx-pxyd 启动）后自动重新应用；在此之前弹窗报告 `entry_pending`，不给复制按钮。
  `GET /api/v1/listeners` 用 `docker_access_for` 标出 Docker 入口行，用
  `docker_access_withheld: true` 标出当前未被数据面加载的入口（消费者不应把它当作可用出口）。
  通过 API 删除源服务时先按 `version` 校验，过期返回 409 且不动 Docker 入口；源服务删除成功后
  再删除其 Docker 入口。

API：`GET /api/v1/listeners/{id}/docker-access[?gateway=IP]` 返回判定与预览（`plan.id`）；
`POST` 同路径 `{plan_id, gateway, confirm:true}` 创建，预览过期返回 409；`DELETE` 撤销；
`GET /api/v1/docker-access` 列出源服务 → Docker 入口的关联。浏览器拒绝剪贴板权限时可在弹窗中
选中文本手动复制。控制面不修改宿主防火墙；连接超时时弹窗给出仅放行该网桥子网 → 该端口的示例命令。

认证 URI 和订阅 URL 都是敏感凭据，不要放进公开截图、日志或仓库。需要可达的监听绑定和
允许访问的防火墙；环回入口不能供独立网络命名空间中的容器直连。自定义网桥、隔离网络、
rootless Docker 未必能访问 docker0，候选地址不等于已验证连通。

全局运行设置、代理服务新建和编辑页只展示固定入口端口，不提供端口输入框。已有部署的端口
保留，WS 通过公网固定端口和 URL Path 路由，普通 HTTP/SOCKS 通过认证用户名路由。
SMTP 和住宅供应商网关属于远端地址配置，不是 HX 入口端口。

- `GET /api/v1/system/network`：候选 IPv4 地址，不保证可达。
- `GET /api/v1/system/network/inspect`：接口、当前 TUN、IPv4 转发状态。
- `POST /api/v1/system/network/operations`：`action` 为 `diagnose`、`enable-forwarding`、
  `disable-forwarding`；可选 `container_id` 仅诊断使用，接受 12–64 位十六进制 ID。
- 空容器 ID 诊断宿主路由和 Docker 网络；指定容器则读取内部 DNS、路由并解析 example.com。
  无 shell/getent/nslookup 的镜像可能不能完成诊断，返回退出码和输出。
- 修复需要 `confirm: true`，浏览器须启用并近期验证 2FA；管理 API key 可用于自动化。
  所有操作限单个并发、命令 20 秒；修复只修改运行态 IPv4 转发，不持久化 sysctl，
  不重启 Docker、不清空防火墙。关闭转发会影响全机容器，不能当作任意修复的通用回滚。

## 活跃连接与日志

`GET /api/v1/connections?origin=docker` 返回最多 500 条 Mihomo 当前采样，
`origin` 可取 local/docker/external/unknown。总数是未筛选连接数，带 `sampled_at`。
页面展开时每 3 秒读取，10 秒无新样本提示过期。不会保存逐连接历史；
NAT、host 网络与反向代理会隐藏真实来源，external 包括其他 LAN 主机。

生产 systemd 要求 >=245，三项 HX 服务使用专属 `hx-pxy` journal namespace：

```sh
journalctl --namespace=hx-pxy -u hx-pxy -n 100
journalctl --namespace=hx-pxy -u hx-pxy-dataplane -f
```

该命名空间按 30 天保留、最长 1 天轮转，并有容量上限；容量不足时可能提前回收。
配置在 `/etc/systemd/journald@hx-pxy.conf`。旧的共享 journal 不会被本产品全局 vacuum，
避免删除其他服务的审计日志；旧记录仍遵从宿主原策略。非 systemd 部署的 stdout 保留由调用方管理。

托管文件日志每次写入按大小或 24 小时轮转，后台每天清理过期 `mihomo.log*` 备份及
可确认失活的旧短实例目录，当前文件和活跃 socket 受保护。`log_retention_days` 默认 30，
仅控制文件清理，不修改 journald 策略。文件 mtime 与每日扫描是文件级保留，并非逐行精确到秒；
持续重启会影响活动文件轮转年龄。数据库、密钥和配置不在清理匹配范围。
