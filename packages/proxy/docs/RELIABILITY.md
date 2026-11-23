# HX-Pxy 可靠性、安全与部署规范

## 1. 目标

该文档定义 v1 在个人 Linux 服务器上的运行底线。重点不是“服务能启动”，而是：

- 服务器随时重启后能够自动恢复。
- 订阅源、网络、数据面或配置失败时仍保留上一版可用代理。
- 安装、升级和回滚可重复执行。
- 资源占用可控，后台任务不会拖垮代理转发。
- 管理面、订阅凭据和代理认证不被意外暴露。

---

## 2. 故障域与恢复策略

| 故障 | 必须行为 | 禁止行为 |
| --- | --- | --- |
| 订阅下载失败 | 使用最后成功快照，记录失败并退避重试 | 清空节点或生成空配置 |
| 订阅解析失败 | 保存拒绝原因，不切换快照 | 部分解析后静默覆盖旧快照 |
| 节点检测服务失败 | 保留旧指标并标记过期 | 将全部节点立即判死 |
| 配置编译失败 | 保留当前配置，返回结构化错误 | 写坏生效配置文件 |
| Mihomo 校验失败 | 不应用候选配置 | 强制重启数据面 |
| Mihomo 应用后不健康 | 回滚上一版配置 | 继续运行未知状态配置 |
| 控制面崩溃 | systemd 重启；数据面继续工作 | 控制面退出时主动杀死健康数据面 |
| 数据面崩溃 | systemd 恢复；控制面发出告警 | 无限无退避重启 |
| SQLite 短暂锁竞争 | busy timeout + 有界重试 | 无界重试或阻塞请求线程 |
| 磁盘空间不足 | 停止非关键写入和测速，发出告警 | 持续写日志直至磁盘耗尽 |
| SMTP 不可用 | 保存告警状态并有限重试 | 阻塞核心任务 |
| 服务器断电 | WAL 恢复，加载最后有效配置 | 依赖内存状态才能启动 |

---

## 3. 启动状态机

控制面启动按固定顺序执行：

```text
BOOT
 -> load minimal config
 -> acquire singleton lock
 -> open SQLite
 -> run migrations
 -> verify filesystem permissions
 -> load active desired state
 -> verify current runtime config
 -> check/start dataplane
 -> reconcile desired state and runtime state
 -> start API
 -> start schedulers with jitter
 -> READY
```

要求：

- [x] 使用数据目录内的 `control-plane.lock` 文件锁保证单实例，避免两个控制面同时写数据库和配置；锁文件记录当前 PID，进程退出后内核释放锁。
- [ ] 数据库迁移失败时服务进入 `FAILED`，不得继续运行旧二进制写新结构。
- [ ] 当前 runtime 配置缺失但数据库状态完整时，重新编译并校验。
- [ ] 数据库不可用但当前数据面健康时，控制面不得破坏数据面。
- [ ] API 的 `/health/live` 可在初始化早期返回。
- [ ] `/health/ready` 只有数据库、配置和数据面协调完成后返回成功。
- [ ] 后台任务启动时添加随机抖动，避免重启后同时刷新和测速全部节点。

---

## 4. 配置事务与回滚

### 4.1 文件状态

```text
runtime/
├── active.yaml
├── previous.yaml
├── active.meta.json
└── candidates/
    └── <version>.yaml
```

### 4.2 应用流程

1. 从数据库 Desired State 生成候选配置。
2. 计算内容哈希；与当前哈希相同则跳过应用。
3. 将候选写入同一文件系统下的临时文件。
4. `fsync` 文件和必要目录。
5. 调用 Mihomo 配置检查。
6. 保存当前 `active.yaml` 为 `previous.yaml`。
7. 原子重命名候选文件为 `active.yaml`。
8. 触发数据面重载或受控重启。
9. 检查进程、External Controller、关键 Listener 和代理组。
10. 成功后将版本标记为 Active；失败则恢复 `previous.yaml` 并再次检查。

要求：

- [ ] 所有配置版本具有单调版本号和内容哈希。
- [ ] 只有一个配置 Apply 事务能够同时运行。
- [ ] 新请求到来时合并为最新 Desired State，避免重复连续重启。
- [ ] 回滚也必须经过校验和健康检查。
- [ ] 连续回滚失败时停止自动变更并进入人工干预状态。
- [ ] UI 展示候选配置 diff、校验结果、应用时间和回滚原因。

---

## 5. systemd 服务规范

### 5.1 控制面 Unit

建议关键项：

```ini
[Unit]
Description=HX-Pxy Control Plane
Wants=network-online.target
Requires=hx-pxy-dataplane.service hx-pxy-terminal.service
After=network-online.target hx-pxy-dataplane.service hx-pxy-terminal.service

[Service]
Type=simple
User=hx-pxy
Group=hx-pxy
ExecStart=/usr/local/lib/hx-pxy/current/hx-pxyd \
  --listen 127.0.0.1:19090 \
  --data-dir /var/lib/hx-pxy \
  --config /etc/hx-pxy/config.yaml \
  --web-root /usr/local/lib/hx-pxy/current/web \
  --mihomo /usr/local/lib/hx-pxy/current/mihomo \
  --mihomo-external \
  --terminal-socket /run/hx-pxy/terminal.sock
Restart=on-failure
RestartSec=5s
StartLimitIntervalSec=300
StartLimitBurst=10
TimeoutStartSec=30s
TimeoutStopSec=20s
LimitNOFILE=1048576
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/var/lib/hx-pxy
LockPersonality=true
RestrictSUIDSGID=true

[Install]
WantedBy=multi-user.target
```

### 5.2 数据面 Unit

```ini
[Unit]
Description=HX-Pxy Mihomo Data Plane
Wants=network-online.target
After=network-online.target

[Service]
User=hx-pxy
Group=hx-pxy
ExecStartPre=/usr/local/lib/hx-pxy/current/mihomo -t -d /var/lib/hx-pxy/runtime -f /var/lib/hx-pxy/runtime/active.yaml
ExecStart=/usr/local/lib/hx-pxy/current/mihomo -d /var/lib/hx-pxy/runtime -f /var/lib/hx-pxy/runtime/active.yaml
Restart=on-failure
RestartSec=3s
StartLimitIntervalSec=300
StartLimitBurst=20
LimitNOFILE=1048576
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/var/lib/hx-pxy/runtime

[Install]
WantedBy=multi-user.target
```

### 5.3 终端 Helper Unit

生产安装额外运行 `hx-pxy-terminal.service`：它以 root 启动同一控制面二进制的
`--terminal-helper` 模式，在 `/run/hx-pxy/terminal.sock` 提供 PTY。Socket 为
`root:hx-pxy`、`0660`，helper 同时通过 Linux `SO_PEERCRED` 校验对端 UID；它不监听
TCP/HTTP，也不承载代理流量。控制面退出或关闭时，`PartOf` 会回收 helper 和其 root PTY。
helper 还接受一个与 PTY 帧分离的无参数更新请求；它只在校验 root 所有、不可被组/其他用户写入的
安装器后，通过 `systemd-run` 调度 `/usr/local/sbin/hx-pxy-install upgrade`，不接受浏览器
提供命令、路径或目标版本。更新请求先确认后调度：helper 立即回复 `frameReady`，再在独立 goroutine
中运行 `systemd-run`（30 秒上限），systemd 繁忙不会拖住控制面握手（早期同步实现曾让客户端 5 秒
超时报 `read .../terminal.sock: i/o timeout` 误报更新失败）。同一时刻只允许一个更新在飞：进程内
单飞互斥，配合 `systemctl is-active hx-pxy-update` 覆盖 helper 重启后的重复请求，更新进行中
的再次点击直接返回"更新进行中"错误；`systemd-run` 失败只记日志，不影响已确认的请求。

仓库中的 `deploy/systemd/` 是发布包的权威 unit。控制面和数据面均以非 root 用户运行，管理 API 只监听 `127.0.0.1`，External Controller 使用 Unix Socket，并启用 systemd 文件系统、设备、内核和 capability 沙箱。浏览器终端另有一个 root PTY helper，只监听控制面用户可访问的本机 Unix Socket；它不接收网络流量，终端 WebSocket 仍必须通过管理员登录和 TOTP 2FA。

### 5.4 服务协作

- 数据面必须能独立加载最后有效配置启动。
- 控制面可以检查和修正数据面状态，但不应成为数据面启动的硬前置。
- 安装器切换完整 Release 时会重启控制面、终端 helper 和数据面以保证二进制契约一致；日常配置热重载和控制面自身退出不会停止健康数据面。
- 数据面升级前先验证新二进制和当前配置兼容性。
- systemd 重启次数和最近退出码写入系统状态与告警。

---

## 6. `install.sh` 规范

### 6.1 基本原则

- [x] 使用 `set -Eeuo pipefail`。
- [x] 安装根目录、配置、数据、日志、unit、Release 源、版本和离线来源均可覆盖。
- [x] 内部不使用 `curl | sh`；先下载固定 Release 包和校验文件，再校验、解包和执行。
- [x] 所有 Release 包校验 SHA-256；额外签名校验尚未实现。
- [x] 使用临时阶段目录、版本目录、原子 `current` 链接和 readiness 失败回滚。
- [x] 重复执行不会重置数据库、管理员密码、主密钥或用户配置。
- [x] 非交互模式下不读取 stdin。
- [x] 关键步骤输出明确日志，失败时输出双服务状态和最近 journald 日志。

### 6.2 命令

```text
install.sh install   [--version X] [--offline-dir PATH]
install.sh upgrade   [--version X]
install.sh repair
install.sh status
install.sh backup    [--output PATH]
install.sh uninstall
install.sh purge     --confirm-purge
```

当前 `backup` 生成包含配置、数据库、主密钥、运行配置和快照的敏感灾难归档并排除递归备份目录；尚未提供自动 Restore 子命令，恢复仍需人工维护窗口。

### 6.3 安装流程

```text
preflight
 -> detect os/arch/systemd
 -> verify required commands
 -> resolve versions
 -> download artifacts
 -> verify checksums/signatures
 -> stop only services that must stop
 -> create system user/directories
 -> install versioned binaries
 -> install static assets
 -> install/update systemd units
 -> run database migration
 -> generate/validate initial config
 -> atomically switch current symlinks
 -> daemon-reload
 -> enable --now services
 -> wait for readiness
 -> commit installation
```

### 6.4 版本化布局

```text
/usr/local/lib/hx-pxy/
├── versions/
│   ├── 1.0.0/
│   │   ├── hx-pxyd
│   │   ├── mihomo
│   │   └── web/
│   └── 1.0.1/
└── current -> versions/1.0.1
```

使用版本目录和原子符号链接切换，升级失败时恢复旧链接。至少保留上一版可用版本。

稳定 Release 包名为 `hx-pxy_<tag>_linux_<amd64|arm64>.tar.gz`，固定包含 `bin/hx-pxyd`、`bin/mihomo`、`web/`、`deploy/systemd/` 和新版 `install.sh`。升级成功时安装器自身同步更新，因此后续版本继续使用：

```bash
sudo hx-pxy-install upgrade
```

`.github/workflows/release.yml` 是在线发布入口。推送 `v*` tag 后先执行 `go test ./...`、
`go vet ./...`、前端类型检查与构建，再分别构建 Linux amd64/arm64 控制面，下载固定 Mihomo
`v1.19.29`，按发布包契约打包并生成 `SHA256SUMS` 和 `VERSION`，最后创建 GitHub Release。
下载的第三方 Mihomo 文件名与版本固定，不使用浮动 latest 资产。

生产安装的「关于」页可调用 `POST /api/v1/system/update`。接口要求有效管理员 Session、CSRF
和最近完成的 TOTP 2FA step-up，只返回 202 表示升级任务已调度；实际下载、校验、原子切换、
readiness 和失败回滚仍全部由安装器执行。非 systemd 本地开发环境不提供自动更新。

### 6.5 卸载语义

- `uninstall`：停止并移除程序与 systemd unit，保留 `/etc`、数据库和备份。
- `purge`：明确确认后删除全部配置、数据库、凭据和备份。
- 卸载前默认创建一次带时间戳的备份。

### 6.6 自动化恢复证据

- 发布包结构和可复现 SHA-256 由 `TestPackageReleaseBundleContract` 验证。
- 双 unit 的私有监听、独立生命周期和 systemd 沙箱由根包安装测试验证。
- SQLite WAL 在未提交事务中强制终止辅助进程后的恢复由 `internal/store/recovery_test.go` 验证。
- 外部 Mihomo 热重载失败恢复上一版配置，以及控制面关闭不终止数据面，由 `internal/dataplane/mihomo/manager_external_test.go` 验证。
- 真实发行版 VM 安装、整机断电和数据面吞吐仍属于发布前人工/环境测试，见 [`BENCHMARKS.md`](BENCHMARKS.md)。

---

## 7. 数据库可靠性

### 7.1 SQLite 设置

- [ ] `journal_mode=WAL`。
- [ ] `foreign_keys=ON`。
- [ ] 配置合理的 `busy_timeout`。
- [ ] 写操作使用短事务和批量语句。
- [ ] 控制写连接数量；避免多个独立连接池竞争写锁。
- [ ] 按负载执行 passive / truncate checkpoint。
- [ ] 不在长事务中执行网络请求、数据面调用或文件下载。

### 7.2 迁移

- 每个迁移具有单调版本和校验记录。
- 启动时只允许向前迁移。
- 破坏性迁移前自动在线备份。
- 迁移脚本必须在空库、上一正式版本数据库和包含大数据量的数据库上测试。
- 迁移失败时不启动新版本写服务。

### 7.3 备份

- 使用 SQLite Online Backup API 或一致性快照方式。
- 备份包含数据库、最小启动配置、有效数据面配置和必要秘密文件清单。
- 敏感备份默认权限 `0600`。
- 备份完成后执行 `integrity_check` 或恢复演练。
- 默认保留最近 7 个日备份和 4 个周备份；用户可调整。
- 备份失败产生告警，但不得阻塞代理数据面。

---

## 8. 资源保护

### 8.1 后台任务

每种任务独立配置：

- 最大并发。
- 队列长度。
- 单任务超时。
- 最大重试。
- 退避策略。
- CPU / 带宽预算。
- 失败熔断。

队列满时必须返回明确的 `busy` 状态或合并重复任务，不得无限创建 goroutine。

### 8.2 内存

- 限制订阅响应体、上传文件、日志事件和 SSE 缓冲。
- 大订阅采用流式读取与有界解析；无法流式时至少先做体积限制。
- 节点配置避免同时保留多份深拷贝。
- 统计使用固定窗口或有界 LRU。
- 长任务完成后主动释放大对象引用。
- 基准测试中使用 pprof 检查峰值内存和泄漏。

### 8.3 文件描述符与连接

- Listener 最大连接数可配置。
- 控制面 HTTP Server 设置读写、Header 和 Idle 超时。
- 订阅 Fetcher 设置连接池上限和空闲连接回收。
- SMTP、Geo/IP API 客户端不得创建无界连接池。
- 数据面和控制面分别监控文件描述符使用率。

### 8.4 磁盘

低磁盘空间按阶段降级：

1. 停止非必要测速和调试日志。
2. 提前执行统计聚合和历史清理。
3. 暂停新快照和备份，保留当前代理配置。
4. 数据库不可安全写入时将管理面切为只读并告警。

禁止自动删除当前数据库、最后有效配置和唯一备份。

### 8.5 数据面回环与资源边界

- 受管 Mihomo 默认读取 Linux 主路由表的默认出口，并通过全局 `interface-name` 绑定真实网卡，避免上游拨号重新进入 Clash / Mihomo TUN。
- `HX_PXY_MIHOMO_EGRESS_INTERFACE` 可指定网卡；只有显式设置 `off` 才允许数据面跟随宿主机策略路由。
- 编译候选配置时拒绝上游节点直接指向同地址、同端口的受管 Listener。
- Mihomo 默认 `GOMAXPROCS` 不超过 4，可通过 `HX_PXY_MIHOMO_MAX_PROCS` 调整，避免异常连接风暴占满全部 CPU 核。
- `runtime/mihomo.log` 在线轮转，默认单文件 8 MiB、保留 2 份；上限和备份数可通过环境变量调整。
- 自动出口识别失败时控制面保持管理 API 可用并输出结构化告警，不伪装成已完成 TUN 隔离。

---

## 9. 日志与审计

### 9.1 运行日志

- 默认输出结构化日志到 stdout/stderr，由 journald 收集。
- 日志级别：error、warn、info、debug；生产默认 info。
- 每条请求包含 trace ID，不记录完整 Authorization、Cookie 或订阅 URL。
- 高频连接和流量事件采用采样或聚合，不逐连接写 info 日志。
- 支持临时提高单模块日志级别并自动恢复。
- 受管 Mihomo 文件日志必须在线轮转，不得依赖进程重启后清理。

### 9.2 审计日志

至少记录：

- 管理员登录、登出和密码修改。
- 订阅创建、修改、删除和手工刷新。
- 代理组、Listener 和服务器节点配置变更。
- 配置 Apply、Rollback 和数据面升级。
- 订阅 Token 创建、轮换和吊销。
- 备份、恢复和系统更新。

审计记录保存资源 ID、动作、结果、来源 IP、时间、变更摘要和 trace ID，不保存秘密明文。

---

## 10. 认证与秘密管理

### 10.1 管理员密码

- 使用 Argon2id。
- 参数写入密码哈希编码中，便于未来升级。
- 登录成功后按需重新哈希旧参数。
- 密码修改使现有 Session 全部失效。

### 10.2 Session

- 服务端持久化 Session ID 摘要。
- Cookie 设置 `HttpOnly`、`Secure`、`SameSite=Strict` 或经验证的 Lax 策略。
- Session 具有绝对过期和空闲过期。
- 敏感操作要求近期认证或再次输入密码。

管理员 TOTP 的 AEAD 上下文是持久化格式，不随产品显示名改变。读取同时兼容
`hx-pxy/admin-totp/v1` 与改名前的 `hx-proxygroup/admin-totp/v1`，仍要求同一主密钥与
完整的 GCM 认证；不会删除或重置现有 2FA 绑定，也不会在验证时重写密文。
错误验证码、损坏密文或错误主密钥不会获得终端权限。

### 10.3 节点与订阅秘密

节点和订阅凭据需要在重启后重新生成数据面配置，因此不能只保存不可逆哈希。

推荐方案：

- 使用本机主密钥对敏感字段进行 AEAD 加密后存入 SQLite。
- 当前部署将主密钥保存于 `/var/lib/hx-pxy/master.key`，权限 `0600`，由不可登录服务用户独占读取，不进入普通明文 Backup。
- 支持通过 systemd credential 或外部环境注入主密钥。
- 备份时明确包含或单独导出主密钥，否则备份不可恢复。

Listener 用户密码的处理取决于数据面能力：若数据面需要明文配置，则同样加密存储；UI 永远不回显原密码，只允许重置。

### 10.4 Token

- 订阅 Token 使用密码学安全随机数，至少 128 bit 熵。
- 数据库默认保存 Token 哈希和短前缀；确需重新展示时使用加密存储。
- Token 支持轮换、过期、吊销和访问限速。
- URL 日志中只保留 Token 前缀或完全隐藏。

---

## 11. 网络安全

### 11.1 监听默认值

- 管理 API：默认 `127.0.0.1`。
- Mihomo External Controller：只允许环回或 Unix Socket。
- 新建代理 Listener：默认 `127.0.0.1`，用户明确选择后才能公网监听。
- 雷池回源 Listener：固定环回地址。
- 健康检查和指标接口不得暴露秘密或内部版本细节。

### 11.2 SSRF

订阅 URL 和检测目标由管理员配置，但仍需防止误操作和云元数据泄露：

- 默认仅允许 `http`、`https`。
- 解析 DNS 后拒绝环回、链路本地、组播、未指定地址和云元数据网段。
- 重定向后重新执行目标检查。
- 限制重定向次数。
- 可通过明确的高级配置放行私网订阅源。
- IP 质量和 Geo 服务使用固定允许列表或受控 Provider。

### 11.3 反向代理

- 只有来自显式可信代理地址的 `X-Forwarded-For` / `Forwarded` 才可信。
- 管理面与代理节点使用不同域名和路由。
- WebSocket Path 使用不可预测或可轮换值不能替代真实认证。
- Edge Relay 只接受固定 `/__hx-proxy__/` 前缀下的 WebSocket Upgrade，按 Host 和完整路径
  匹配已启用的环回 Mihomo Listener；请求不能提供任意上游地址或查询参数改变目标。
- Edge Relay 活跃连接数有界；控制面重启不会杀掉 Mihomo 数据面，但经 Relay 的新连接需要
  等待控制面 readiness 恢复。
- TLS 终止点、回源协议和客户端生成配置必须一致。
- 不允许将 Mihomo External Controller 直接交给雷池公网转发。

---

## 12. 健康检查

### 12.1 控制面端点

- `/health/live`：进程事件循环可响应。
- `/health/ready`：数据库可访问、迁移完成、Desired State 可读取、数据面协调完成。
- `/health/dataplane`：仅管理员或本机可见，返回数据面详细状态。

公开健康检查只返回有限状态码，不返回版本、节点数量、端口或错误堆栈。

### 12.2 数据面健康

至少检查：

- 进程存在且 PID 与受管实例一致。
- External Controller 可访问。
- 当前配置版本与控制面记录一致。
- 关键 Listener 已监听。
- 关键代理组存在。
- 最近一段时间没有持续重启。

不使用“访问某一个外部网站失败”作为数据面进程不健康的唯一依据。

---

## 13. 告警状态机

告警状态：

```text
inactive -> pending -> firing -> acknowledged/silenced -> resolved
```

- 条件首次命中进入 pending，达到持续时间后 firing。
- 瞬时恢复可取消 pending，减少抖动。
- firing 发送一次通知，之后按冷却周期汇总。
- 恢复时发送 resolved 通知。
- 重启后从数据库恢复状态和冷却时间。
- 邮件通道故障不改变原告警状态，只生成通道告警。

---

## 14. 更新策略

### 14.1 控制面更新

- 下载新版本并校验。
- 备份数据库和当前版本。
- 运行离线迁移预检。
- 切换版本链接并重启控制面。
- 等待 readiness。
- 失败时恢复旧版本和数据库备份。

### 14.2 数据面更新

- 新 Mihomo 二进制先执行版本检查和配置校验。
- 使用当前 `active.yaml` 做离线兼容性测试。
- 检查能力集合变化，尤其是协议、Listener 和配置字段。
- 只有兼容检查通过才切换版本。
- 更新后执行 Listener、代理组和真实代理连接冒烟测试。
- 失败立即回滚旧数据面版本。

禁止自动追踪 `latest` 并无校验升级。生产配置必须记录精确控制面和数据面版本。

---

## 15. 测试矩阵

### 15.1 单元测试

- 节点指纹稳定性和碰撞边界。
- 订阅解析失败保留旧快照。
- 规则流水线顺序、错误隔离和追踪。
- 代理组依赖环检测。
- 配置编译稳定输出。
- 指标过期和评分缺失策略。
- 告警去重、冷却和恢复。
- 统计 counter reset。

### 15.2 集成测试

- SQLite 迁移、WAL、备份和恢复。
- Mihomo 候选配置验证、Apply 和 Rollback。
- Listener 端口冲突。
- 数据面 External Controller 断连重连。
- SMTP 测试服务器。
- 固定前缀 Edge Relay 的 WebSocket Upgrade、Host/Path 路由、未知路径和双向字节转发。
- 反向代理 WebSocket / gRPC。

### 15.3 端到端测试

- 导入真实格式订阅并生成代理组。
- HTTP CONNECT 和 SOCKS5 访问测试目标。
- Sticky Session 在节点健康时保持稳定、失效时切换。
- DIRECT 出口显示服务器公网 IP。
- Clash / V2RayN / sing-box 导出订阅可导入。
- 雷池 443 -> HX `/__hx-proxy__/` Edge Relay -> 本地 WS Listener。
- 服务器重启后所有端口和策略恢复。

### 15.4 故障注入

- 刷新中断电。
- 配置写入前后进程被 kill -9。
- 数据面连续崩溃。
- SQLite 锁竞争和磁盘满。
- DNS 故障和订阅源超时。
- 质量服务返回异常数据。
- 系统时间跳变。

---

## 16. 发布检查表

- [ ] 安装脚本在干净 Debian / Ubuntu VM 上成功。
- [ ] 升级失败可自动回滚。
- [ ] systemd enable 后重启服务器自动恢复。
- [ ] 管理端口和 External Controller 未公网暴露。
- [ ] 所有秘密日志脱敏测试通过。
- [ ] 数据库备份已真实恢复验证。
- [ ] 订阅失败不会清空节点。
- [ ] 配置失败不会中断上一版代理。
- [ ] Cloudflare / 雷池兼容边界已在 UI 和文档提示。
- [x] 30 天统计清理和聚合测试通过。
- [ ] CPU、RSS、连接数和吞吐基准报告已归档。
- [ ] 至少完成一次控制面崩溃、数据面崩溃和整机重启演练。

## 网络操作与日志保留

网络修复限显式 IPv4 转发开关，要求确认、认证及浏览器 2FA，命令有超时且串行执行。
TUN 权限 drop-in 不随安装自动启用；应用失败保留既有回滚机制。
生产日志使用独立 journald namespace 保留 30 天，不全局清理共享 journal。
文件日志按大小/年龄轮转并定时清理，保护当前实例和其他存活 socket。
范围、部署与无法自动消除的其他 TUN 干扰见 [网络运维](NETWORK_OPERATIONS.md)。
