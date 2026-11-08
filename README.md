<div align="center">

<h1>HX-Pxy</h1>

**面向个人 Linux 服务器的代理订阅、节点质量评估、Proxy Group 编排与多协议出口控制平面。**

让多个订阅、去重节点、质量检测、路由规则、独立 Listener、流量统计和故障恢复进入同一个可审计的管理面。

[![Go](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-20232A?style=flat-square&logo=react&logoColor=61DAFB)](https://react.dev/)
[![TypeScript](https://img.shields.io/badge/TypeScript-7-3178C6?style=flat-square&logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![Mihomo](https://img.shields.io/badge/Data_Plane-Mihomo-2F6FEB?style=flat-square)](https://github.com/MetaCubeX/mihomo)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?style=flat-square&logo=sqlite&logoColor=white)](https://sqlite.org/)
[![systemd](https://img.shields.io/badge/Service-systemd-5A5A5A?style=flat-square&logo=linux&logoColor=white)](https://systemd.io/)
[![License: MIT](https://img.shields.io/badge/License-MIT-2EA44F?style=flat-square)](LICENSE)

[快速开始](#快速开始) · [生产安装](#生产安装) · [核心能力](#核心能力) · [架构](#架构) · [兼容性](#订阅与协议兼容性) · [文档](#文档导航)

</div>

![HX-Pxy 全局总览](docs/screenshots/global-overview-desktop.png)

> [!IMPORTANT]
> HX-Pxy 是代理控制平面，不是新的代理协议内核。VMess、VLESS、Trojan、Hysteria、TUIC、SOCKS5、HTTP CONNECT 等协议仍由当前安装的 Mihomo 构建负责。普通 Listener 直接由 Mihomo 提供；Cloudflare / 雷池 WebSocket 拓扑可选经过 HX 的受限 Edge Relay，Relay 只做固定路径的连接转发，不解析或实现代理协议。

## 网络运维与加载

代理服务列表独立加载节点库存，常用列表有 5 秒会话内缓存；刷新与写操作会失效缓存。
支持住宅频道复用入口、Docker 候选地址复制与按来源分类的当前连接采样。
TUN 默认关闭，启用权限、Docker 诊断/修复与 30 天日志保留见 [网络运维说明](docs/NETWORK_OPERATIONS.md)。
生产 systemd 安装要求 >=245；不会自动接管其他 TUN 或清空宿主防火墙。

## 为什么使用 HX-Pxy

普通订阅客户端适合在单台终端上选节点；HX-Pxy 面向长期运行的服务器场景，把“订阅输入”转化为可恢复、可解释、可组合的 Desired State：

| 需求 | HX-Pxy 的处理方式 |
| --- | --- |
| 多个机场和自定义节点 | 统一刷新、规范化、稳定指纹去重，同时保留来源关系 |
| 节点质量不断变化 | 经 Mihomo 指定节点执行真实 HTTP 检测，支持批量复测、降级和隔离 |
| 不想手工维护静态代理组 | 按订阅、名称、地区、协议、状态、延迟、评分和 Top N 动态选取 |
| 每个业务需要独立代理端口 | 为 Proxy Group 创建 HTTP、SOCKS5 或 Mixed Listener |
| 配置失败不能影响现有代理 | 完整编译、Mihomo 校验、原子发布、热重载和上一版回滚 |
| 控制面升级时代理不能一起退出 | 控制面与 Mihomo 由两个独立 systemd unit 管理 |
| 需要知道流量和故障原因 | 秒级总览、30 天分层统计、结构化日志、告警和审计 |

## 界面

<table>
  <tr>
    <td width="50%"><strong>订阅来源</strong><br><sub>加密来源、刷新计划、快照、节点与流量集中展示。</sub></td>
    <td width="50%"><strong>节点库存</strong><br><sub>按订阅分组，支持协议/状态筛选、排序、批量检测和禁用。</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/subscriptions-desktop.png" alt="订阅来源页面"></td>
    <td><img src="docs/screenshots/nodes-filters-medium.png" alt="节点库存与筛选页面"></td>
  </tr>
  <tr>
    <td><strong>代理服务</strong><br><sub>组合 Proxy Group、Listener、认证、路由和客户端订阅。</sub></td>
    <td><strong>全局配置</strong><br><sub>检测、DNS、性能、主题与运行参数集中管理。</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/proxy-services-expanded-desktop.png" alt="代理服务页面"></td>
    <td><img src="docs/screenshots/settings-dark-desktop.png" alt="深色模式全局配置页面"></td>
  </tr>
  <tr>
    <td><strong>浏览器终端</strong><br><sub>本机 PTY Shell、系统监控、文件管理（vscode-icons 图标，目录与 Shell 实时同步）。</sub></td>
    <td><strong>住宅代理</strong><br><sub>动态住宅 IP 渠道、会话轮换、渠道订阅与控制 URL。</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/terminal-file-panel-desktop.png" alt="浏览器终端与文件面板"></td>
    <td><img src="docs/screenshots/residential-channel-protocol-desktop.png" alt="住宅代理页面"></td>
  </tr>
</table>

管理面支持明亮、黑夜、跟随系统和自定义主题色；桌面侧栏可折叠为图标模式，移动端使用紧凑顶部导航。关键页面均有 Playwright 横向溢出与响应式回归测试。

## 核心能力

### 订阅与节点库存

- Remote、Inline、File 三类订阅来源，支持固定间隔和 UTC 五字段 Cron。
- 远程请求支持自定义 Header、User-Agent、超时、条件请求与受控重定向。
- 来源配置和节点规范配置使用 AEAD 加密，API 不回显订阅 URL、Token 或节点凭据。
- 不可变成功快照、内容哈希去重和活动快照切换；刷新失败保留上一版可用节点。
- Clash/Mihomo YAML、Mihomo Provider、sing-box JSON、分享 URI 和 Base64 URI 列表。
- HTTP、File、Inline Provider 展开，带 SSRF、符号链接、深度、数量和累计体积限制。
- 稳定节点指纹、跨订阅去重、来源关系、candidate/healthy/degraded/quarantined/disabled/retired 生命周期。
- 单节点检测、批量 SSE 渐进检测、自动复测、延迟阈值、连续失败隔离和成功恢复。

### Proxy Group 与 Listener

- 一个 Proxy Group 可组合多个订阅、固定节点和本机 `DIRECT` 出口。
- 固定阶段规则流水线：

  ```text
  Normalize -> Enrich -> Predicate -> Score -> Bucket -> Sort -> Limit
  ```

- 支持 `manual`、`url-test`、`fallback`、轮询、一致性哈希和会话粘滞策略。
- 支持按订阅、名称/地区标签、协议、生命周期、延迟和评分动态筛选成员。
- HTTP CONNECT、SOCKS5 和 Mixed Listener 可直接绑定 Proxy Group。
- VLESS、VMess、Trojan over WebSocket 服务端入口由 Mihomo 提供。
- Cloudflare / 雷池可将 `/__hx-proxy__/` WebSocket 路由统一转发到控制面，由受限 Edge Relay 精确转给本机 Mihomo Listener；控制面不解析 VLESS、VMess 或 Trojan。
- 每个代理服务独立发布自己的订阅；住宅渠道也只发布本渠道的声明节点，不存在跨渠道的全局统一订阅。
- 路由规则可将站点集合指向 `DIRECT`、`REJECT` 或指定 Proxy Group。

### 可观测性与运维

- SSE 秒级总览：上下行速率、活动连接、入口和路由拓扑。
- Listener、Proxy Group、节点和稳定住宅渠道维度的流量与连接统计。
- 近 24 小时一分钟、2-7 天五分钟、8-30 天一小时分层聚合。
- 订阅、空快照、空代理组和数据面异常告警，支持 SMTP、冷却、确认和恢复通知。
- SQLite Online Backup、Portable Export、SHA-256 完整性校验与敏感灾难备份。
- 浏览器本机 Shell：PTY + WebSocket + xterm.js，默认开启；必须完成管理员登录和 TOTP 2FA 解锁，无空闲断开、无会话寿命上限，保留并发与审计限制，集成系统资源监控与文件管理面板（文件树采用 vscode-icons 图标，目录与 Shell 实时同步）；切换页面不断连，服务器数据仅连接后显示。

### 安全与可靠性

- 管理 API 与 Mihomo External Controller 默认只监听环回地址或 Unix Socket。
- 单管理员 Argon2id 密码、HttpOnly Session、SameSite Cookie、CSRF、登录限速和锁定。
- Remote Provider 每次 DNS 解析、重定向和连接都执行 SSRF 边界检查。
- 数据库是 Desired State 的事实来源；候选配置不通过校验就不会发布。
- 配置文件临时写入、`fsync`、原子替换；数据面重载失败自动恢复 `previous.yaml`。
- SQLite 使用 WAL、外键、busy timeout、短事务和批量统计写入。
- 所有 goroutine 都有所有者和取消路径；刷新与检测使用有界 worker pool。

## 架构

```mermaid
flowchart LR
    Admin[管理员浏览器] -->|REST / SSE / WebSocket| CP[Go 控制面]
    CP --> DB[(SQLite WAL)]
    CP --> Compiler[Desired State 编译器]
    Compiler -->|validate + atomic publish| Config[active.yaml]
    CP <-->|Unix Controller| DP[Mihomo 数据面]
    Config --> DP
    DP --> Nodes[订阅节点]
    DP --> Direct[服务器 DIRECT 出口]
    Clients[代理客户端] -->|HTTP / SOCKS5 / Mixed / WS| DP
    RP[Cloudflare / 雷池] -->|fixed WebSocket path| CP
    CP -->|Edge Relay| DP
```

```text
systemd
├── hx-pxy.service              # Go 控制面、管理 API、静态前端
├── hx-pxy-dataplane.service    # Mihomo 数据面、真实代理转发
└── hx-pxy-terminal.service     # 仅经 Unix Socket 提供 root PTY
```

生产环境只有一个控制面进程和一个数据面进程，root PTY helper 是只处理管理员终端的隔离辅助进程。默认代理入口仍由 Mihomo 直接承载；启用 Cloudflare / 雷池拓扑时，控制面内的 Edge Relay 只接收固定 `/__hx-proxy__/` 前缀下的 WebSocket Upgrade，并把连接转发到环回 Mihomo Listener，不承担协议解析。控制面重启时，直接监听的健康数据面继续使用最后有效配置工作；经 Edge Relay 的新连接会在控制面恢复后继续可用。

详细的模块边界、领域模型和配置事务见 [架构设计](docs/ARCHITECTURE.md)。

## 快速开始

### 环境要求

- Go 1.23 或更高版本。
- Node.js 22.12 或更高版本，仅本地前端开发需要。
- Linux 建议安装 Mihomo；没有 Mihomo 时管理面仍可启动，但代理、检测和数据面 Apply 不可用。

### 本地运行

```bash
git clone https://github.com/HengXin666/hx-pxy.git
cd HX-Pxy
bash run.sh
```

`run.sh` 会在项目目录中构建控制面、启动 Vite，并在缺少依赖时根据锁文件安装前端依赖。它不要求 root、不注册 systemd，也不写入 `/usr/local`、`/etc` 或 `/var/lib`。

启动日志会打印本次随机选择的管理界面地址，例如：

```text
http://127.0.0.1:<49152-65535 之间的随机端口>
```

本地后端和前端默认分别使用不同的随机高端口；需要固定地址时可显式传入
`--listen 127.0.0.1:<port>` 和 `--frontend-port <port>`。按 `Ctrl+C` 后，
`run.sh` 会终止本次启动的后端、Mihomo、包管理器和 Vite 进程组并释放端口。

本地运行会在 `.tmp/run/` 保留本次启动的日志：`run.log` 记录启动、就绪、子进程退出和整体退出状态；
`logs/backend.log` 收集 Go 控制面输出；`logs/frontend.log` 收集 Vite 和前端开发进程输出。生产 systemd
部署仍通过 journald 收集结构化日志，控制面会记录启动、正常退出、异常退出、已知后台任务 panic 及其堆栈。

### 住宅代理（动态住宅 IP）

侧栏「住宅代理」页管理动态住宅 IP 渠道，整体模型是：

```text
供应商(厂商账号/API + TTL) -> 渠道(1 个托管 WS Listener + N 个声明节点)
                                 -> 渠道订阅 / 自动化控制 URL
```

需要通过海外网络访问住宅网关时，可在供应商中选择一个已启用的海外
Proxy Group。实际数据面链路为：

```text
客户端 -> HX-Pxy Listener -> 住宅节点 -> 上游海外 Proxy Group -> 住宅网关 -> 目标站点
```

住宅节点配置中的上游组使用稳定 ID 保存，Mihomo 编译时解析为当前组名并输出
`dialer-proxy`。上游组缺失、禁用或与住宅渠道形成循环时，配置会拒绝应用。

- **供应商**：四种接入方式。
  - 账密网关（粘滞会话）：填网关地址、子用户账号密码，用户名模板自动拼
    `账号_area-国家_life-分钟_session-会话ID`（BestProxy）或
    `账号-residential-国家-session-会话ID-stime-分钟`（RapidProxy）；同一会话 ID
    出口 IP 不变，换 ID 即换 IP。
  - API 提取：粘贴厂商面板的完整提取链接到 `api_url`，客户端会话建立或换 IP 时
    实时请求新的 `IP:port` 节点，无需网关账号密码（BestProxy API 提取已内置预设）。
    提取链接可能包含 `app_key`，服务端使用 AEAD 加密保存，管理 API 只返回“已配置”状态。
  - CF Worker 面板（BPB-Worker-Panel）：把 BPB 面板链接或其 `sub/raw` 订阅链接粘贴到
    `worker_url`。控制面经可选出口代理（`api_proxy_url`）请求该链接，解析返回的
    VLESS/Trojan WebSocket 节点作为住宅出口；每次用户主动 `next` 才重新请求该链接并
    轮换 Cloudflare 出口地址，TTL 强制为 0，不自动刷新。
  - HX-CF-WsPxy（Cloudflare colo 出口）：把本机 SessionPlane origin 填到 `api_url`，
    例如 `http://127.0.0.1:2470`。控制面 `POST /session` 开会话，Mihomo 拨返回的
    环回 HTTP CONNECT 端口；`next` 调用 `POST /session/:id/rotate` 换 pin（换 colo），
    释放时 `DELETE`。WSP1 只跑在 WsPxy 与 Worker 之间，住宅客户端仍走渠道 Listener。
    控制面地址必须是 loopback，TTL 强制为 0。
  - 地区策略：地区选项支持固定地区和“应用层随机地区”。随机模式要求手动填写候选地区，
    控制面每次实际获取住宅 IP 前使用 `crypto/rand` 选择一个候选值，并覆盖提取链接中的
    `cc`、`country`、`region` 或 `area` 参数；不会依赖供应商自称的随机地区。
  - 控制面 API 上游代理：可选填写 `http://`、`https://` 或 `socks5://` 代理，
    用于 BestProxy API 提取、CF Worker 面板拉取和服务端测试连接；它不转发客户端业务流量，
    地址和代理认证信息均加密保存。
- **渠道**：sticky 渠道设置 `session_count` 后发布 N 个名称和认证稳定的逻辑节点。创建时选择
  VLESS、VMess 或 Trojan over WebSocket；内部环回端口、WS 路径和引导凭据由控制面分配，
  不进入管理表单或订阅。渠道支持随时编辑名称、地区策略、节点数量、空闲释放、启停和公网
  域名；已发布节点的名称与凭据保持不变，调整节点数量只从尾部新增或释放。
- **客户端**：每个渠道拥有独立 `/sub/<share-token>`，只导出本渠道的住宅声明节点。住宅供应商
  会话、网关节点和出口 IP 对普通用户不可见；TTL、空闲释放或 `next` 只改变服务端内部映射，
  不改变订阅。
- **自动化**：`/ctl/<control-token>/nodes` 提供声明节点池，`/nodes/<index>/next` 指定节点换 IP。
  OutlookRegister 在本地互斥租用节点，结束时只归还本地租约，不删除服务端节点。
  多个服务并发对接时使用服务器端独占租约（`/nodes/<index>/claim|heartbeat|release`）与
  `expected_alloc_version` 版本护栏：每个声明节点是一个互斥 IP 窗口，被他人持有时返回
  `409 lease_held`，基于过期版本轮换返回 `409 alloc_version_changed`。完整并发契约与
  集成验收清单见 [住宅代理并发集成标准](docs/RESIDENTIAL_INTEGRATION_STANDARD.md)。
- **复制入口**：在「代理服务」页面的住宅渠道服务行分别选择“复制 Clash / Mihomo 订阅”或
  “复制自动化控制 URL”；「住宅代理」页面只负责供应商、渠道和声明节点管理。

住宅渠道使用 `Cloudflare -> 雷池 -> HX-Pxy` 下选定的 VLESS、VMess 或 Trojan over
WebSocket，实际链路为：

```text
客户端 -> Cloudflare -> 雷池 -> HX Edge Relay -> Mihomo WS Listener
       -> 动态住宅节点 -(dialer-proxy)-> 上游海外 Proxy Group -> 住宅网关 -> 目标站点
```

Edge Relay 只转发固定 `/__hx-proxy__/` WebSocket 连接，协议认证、住宅节点选择和业务流量仍由
Mihomo 负责。每个声明节点使用独立协议凭据，并通过 Mihomo `IN-USER` 规则映射到当前住宅出口。
浏览器自动化由客户端本机 Mihomo 把 WS 端点落地为环回代理，不再为住宅渠道开放公网明文
HTTP/SOCKS 入口。每个渠道对外使用雷池 HTTPS 443 下自己的订阅
`https://proxy.example.com/sub/<token>?format=clash`；链接省略默认 `:443`。
`/sub/` 和 `/ctl/` 都不是 HTTP/SOCKS 代理端点。`/ctl/` 为每个声明节点返回通用 `endpoints[]`；
对于 API 提取供应商，节点执行 `next` 后还会在仅 control token 可读的 `residential_endpoint`
中返回本次提取的协议、IP/主机、端口和节点级鉴权。OutlookRegister 优先让本机 Mihomo 直接拨号
该住宅节点，首实例监听 `http://127.0.0.1:2334`，并发或端口占用时为各 flow 分配独立环回端口：

```text
浏览器 -> 本机 Mihomo 环回 HTTP -> API 提取的住宅 IP:port -> 目标站点
          （VPS 只申请节点，不承载浏览器业务流量）
```

账密网关不会通过该字段下发供应商主凭据，仍使用服务器数据面与 `endpoints[]`。普通 `/sub/`、
管理列表和请求日志均不包含 `residential_endpoint`。control token 因此能够消耗提取配额并读取
临时节点鉴权，必须按高权限 bearer 凭据保管和轮换。

定制会话 API、并发容量、切流语义和安全边界见
[住宅代理客户端会话 API](docs/RESIDENTIAL_SESSION_API.md)。

每个声明节点单独记录供应商 TTL。供应商可配置到期后释放分配，或保留客户端认证并实时换一个
住宅 IP；后台维护任务以有界批次处理到期与空闲分配，不依赖客户端重新拉订阅。
BestProxy 的 `life` 和 RapidProxy 的 `stime` 均按分钟计算（前者 1-120，后者 1-180），
API 提取链接中的 `life` 参数优先于表单里的 TTL；通用供应商的 `session_ttl_seconds` 按秒计算。

保存供应商后先用「测试连接」确认出口 IP 真实可用，再创建渠道。

「测试连接」是控制面的单次供应商诊断；配置了上游 Proxy Group 后，最终链路仍应从
渠道 Listener 发起一次真实 HTTPS 请求验证，因为控制面诊断不会代替 Mihomo 的
`dialer-proxy` 数据面路径。对于 CF Worker 面板（BPB）供应商，控制面测试只验证
面板链接可拉取、节点可解析以及首个端点 TCP 可达；由于控制面不实现 VLESS/Trojan
协议，真实住宅出口 IP 由渠道数据面验证。

链式住宅代理的配置顺序必须是：先导入并刷新海外订阅，再用该订阅创建一个已启用的
Proxy Group，最后在住宅供应商编辑框的「上游海外 Proxy Group」中选择它。保存后检查
`runtime/active.yaml` 中每个住宅节点是否出现 `dialer-proxy: <上游组名>`；没有该字段时，
住宅网关仍然是直连，无法满足需要通过订阅访问住宅网关的网络环境。

浏览器自动化使用客户端受管 Mihomo 提供的环回 HTTP/SOCKS 落地。它不使用控制面 `19090`，
也不能把 `/sub/` 或 `/ctl/` URL 直接填入 Playwright 的 `proxy` 字段。

首次启动会生成：

```text
.tmp/run-data/admin-setup-token
```

在初始化页面输入该一次性 Token、管理员用户名和密码即可完成登录。按 `Ctrl+C` 会统一停止本次启动的前后端进程。

常用开发参数：

```bash
bash run.sh --help
bash run.sh --mihomo /path/to/mihomo
bash run.sh --backend-only
bash run.sh --no-install-frontend-deps
```

## 生产安装

生产安装面向使用 systemd 的 Linux `amd64` / `arm64` 服务器。安装脚本会下载固定 GitHub Release、校验 SHA-256、安装控制面、Mihomo 和静态前端，再注册控制面、数据面和终端 helper 三个服务。

> [!NOTE]
> 在线安装依赖 Release 中已上传符合 [发布包契约](scripts/package-release.sh) 的架构包与 `SHA256SUMS`。

```bash
curl -fsSLO https://raw.githubusercontent.com/HengXin666/hx-pxy/main/install.sh && sudo bash install.sh install
```

安装成功后，后续版本只需要一行命令：

```bash
sudo hx-pxy-install upgrade
```

生产 systemd 安装也可在「关于」页执行一键更新。页面内可输入认证器的 6 位 TOTP 完成当前
Session 的 2FA 验证；root helper 只接受固定的无参数升级请求，不接受浏览器提交命令或版本字符串。
更新请求先确认后异步调度：helper 立即受理，再通过 `systemd-run` 调度安装器，systemd 繁忙不会
导致请求超时误报失败；同一时刻只允许一个更新在飞，更新进行中重复点击会提示"更新进行中"。

安装器在切换版本前校验新 Mihomo 与当前配置；三个服务 readiness 失败时原子恢复上一版 `current` 链接并重启旧版本。升级成功后安装器自身也会更新。

从改名前的版本（`v0.12.0` 及更早，当时叫 HX-ProxyGroup）升上来时，安装器会先接管旧部署：停止旧的 `hx-proxygroup*` 单元，把 `/var/lib/hx-proxygroup` 连同数据库、配置、日志与安装根搬到新路径，服务账号一并改名，然后才安装新版本。数据不会重建，也不需要先卸载。

| 命令 | 用途 |
| --- | --- |
| `sudo hx-pxy-install upgrade` | 获取并升级到最新固定 Release |
| `sudo hx-pxy-install upgrade --version vX.Y.Z` | 安装指定版本 |
| `sudo hx-pxy-install status` | 查看三个服务与当前版本 |
| `sudo hx-pxy-install repair` | 恢复目录权限、unit 和服务 |
| `sudo hx-pxy-install backup` | 创建包含秘密的 `0600` 灾难归档 |
| `sudo hx-pxy-install uninstall` | 移除服务和程序，保留数据与备份 |
| `sudo hx-pxy-install purge --confirm-purge` | 明确确认后删除程序和全部持久数据 |

离线安装：

```bash
sudo bash install.sh install --offline-dir /path/to/release
```

离线目录需要包含 `VERSION`、`SHA256SUMS` 和当前架构的 Release 包。

默认布局：

```text
/usr/local/lib/hx-pxy/
├── versions/<release>/
└── current -> versions/<release>

/etc/hx-pxy/                 # 最小启动配置
/var/lib/hx-pxy/             # SQLite、密钥、快照、运行配置、备份
/etc/systemd/system/                # 控制面、数据面与终端 helper unit
```

## 订阅与协议兼容性

控制面当前可规范化的 Mihomo YAML 出站类型：

```text
AnyTLS      Hysteria     Hysteria2    HTTP         Mieru        ShadowTLS
Snell       SOCKS5       Shadowsocks  SSH          ShadowsocksR  Trojan
TUIC        VLESS        VMess         WireGuard
```

分享 URI 覆盖 VLESS、VMess、Trojan、Shadowsocks、HTTP(S)、SOCKS5、Hysteria、Hysteria2/Hy2、TUIC、AnyTLS 和 SSH。

“可解析”不等于数据面一定支持：最终能力取决于关于页显示的 Mihomo 版本，并由每次候选配置的 `mihomo -t` 决定。项目不承诺未知协议或所有机场私有变体自动兼容。

完整容器、Provider、sing-box 映射和 URI 矩阵见 [订阅与 Provider 兼容矩阵](docs/SUBSCRIPTION_COMPATIBILITY.md)。

## 基准、占用与恢复验证

测试环境：Linux amd64、Go `1.26.5-X:nodwarf5`、Intel Core i9-13980HX、32 逻辑 CPU。下列数据为三次运行的中间值：

| 场景 | 中间值 | 累计分配 |
| --- | ---: | ---: |
| 解析 10,000 个 Mihomo 节点 | 87.82 ms | 59.2 MB/op |
| 编译 10,000 个节点的完整配置 | 105.65 ms | 234.2 MB/op |
| SQLite 批量写入 1,000 个统计资源 | 33.80 ms | 1.04 MB/op |
| SQLite 查询 500 个统计点 | 1.30 ms | 0.13 MB/op |

同一环境下的生产构建空闲基线（2026-07-30，连续 15 秒低活动采样）：

| 进程 / 资源 | 实测值 | 说明 |
| --- | ---: | --- |
| Go 控制面 RSS | 19.9 MiB | 空数据库、静态前端、终端关闭 |
| Mihomo RSS | 43.1 MiB | v1.19.29、1 个仅环回 DIRECT Mixed Listener |
| 双进程合计 RSS | 63.0 MiB | 两个进程同时空闲时的 RSS 合计 |
| 空闲 CPU | 0 tick / 15 秒 | 两个进程分别采样；本机测量分辨率约 0.07% 单核 |
| 程序与前端 | 59.4 MiB | 控制面 12.5 MiB + Mihomo 46.1 MiB + Web 0.85 MiB |

这是开发机上的空闲实测值，不是所有 VPS 的上限或保证值。订阅刷新、10,000 节点解析、配置编译、连接统计和 Mihomo 实际转发都会产生瞬时 CPU 与内存峰值；版本保留、SQLite、快照和日志也会增加磁盘占用。轻量生产部署建议至少准备 **512 MiB RAM**，大量节点或连接建议 **1 GiB 及以上**；256 MiB 只适合经实测确认的极轻负载。

自动化故障测试覆盖：

- 辅助进程在 SQLite 未提交写事务中被强制终止，重开后恢复最后已提交状态并通过完整性检查。
- 外部 Mihomo 热重载失败后恢复上一版配置并再次加载。
- 关闭控制面 Manager 不终止 systemd 所有的数据面。
- Release 包内容、可复现 SHA-256、双 unit 私有监听和安装契约。

这些是控制面微基准与空闲资源基线，不代表 HTTP CONNECT/SOCKS5 的真实吞吐或高峰占用。真实并发连接、整机断电、磁盘满和干净发行版 VM 安装仍需环境测试。命令、三次原始结果和边界见 [基准与故障恢复报告](docs/BENCHMARKS.md)。

## 运维说明

### 与宿主机 TUN 共存

受管 Mihomo 默认从 Linux 主路由表识别物理出口，并写入 `interface-name`，避免上游拨号再次进入 Clash Verge、Mihomo Party 等宿主机 TUN。

```bash
# 显式指定物理出口
HX_PXY_MIHOMO_EGRESS_INTERFACE=eth0 bash run.sh

# 明确允许数据面跟随宿主机策略路由
HX_PXY_MIHOMO_EGRESS_INTERFACE=off bash run.sh
```

默认 `GOMAXPROCS` 最多为 4，Mihomo 日志单文件上限 8 MiB、保留 2 份。可通过 `HX_PXY_MIHOMO_MAX_PROCS`、`HX_PXY_MIHOMO_LOG_MAX_BYTES` 和 `HX_PXY_MIHOMO_LOG_BACKUPS` 调整。

### 浏览器终端

浏览器本机 Shell 默认开启。需要紧急关闭时：

```bash
HX_PXY_TERMINAL=0 bash run.sh
```

首次登录后进入「全局配置 -> 账号安全」，生成并启用 TOTP 2FA；终端连接前需要输入验证器当前的 6 位验证码。解锁状态对当前管理员 Session 有效 15 分钟。终端不因空闲自动断开、无会话寿命上限、最多 2 个并发会话，并记录建立、关闭、来源、操作者、时长和原因。面板集成系统资源监控（CPU/内存/负载/上行下行）与文件管理（目录浏览、拖拽上传、点击下载，文件树采用 vscode-icons 图标）；切换标签页不会断开连接，系统监控与文件面板仅在终端连接后显示，文件面板目录与 Shell 当前目录实时同步。文件浏览、上传、下载、新建与删除在配置了 root PTY helper 时通过同一特权 Socket 由 helper 执行，因此文件面板与 Shell 看到同一文件系统（root 视图，包括 `/home` 与 `/root`），而不是受沙箱限制的控制面视图；`run.sh` 本地模式则由当前运行用户执行。确实无法读取的目录（权限不足或已被删除）会在面板内显示内联提示并提供返回上级入口，而不是弹出错误提示。弱网适配：服务端 15s 心跳 + 前端自动重连（指数退避，保留终端滚动历史），文件面板瞬时失败自动重试，重连或重新登录后 Shell 会从上次使用的目录启动（按管理员持久化，目录已删除则回退 HOME）。它不是远程 SSH 跳板。

这是 HX-Pxy 所在服务器的本机 PTY Shell。输入与输出直接经 WebSocket 由服务端 PTY 回显，前端不做本地预测回显，避免重连后出现重复输入；vim/top 等 raw/full-screen 程序可正常显示。

WebSocket 只接受同源连接，并在会话存续期间周期性复核数据库中的管理员 Session；退出全部会话、修改账号或密码、Session 过期都会关闭已连接终端。生产安装使用 root PTY helper，但控制面本身仍以 `hx-pxy` 运行；helper 只监听权限为 `0660` 的本机 Unix Socket，并校验对端 UID。通过管理员登录和 2FA 后，终端可以直接使用 `su`、`sudo` 及完整 root 环境，因此应把它视为 root 管理入口，日常运维仍优先使用带密钥和系统级审计的 SSH。`run.sh` 本地模式没有 root helper，只提供当前运行用户的 PTY。完整威胁模型与漏洞报告方式见 [安全策略](SECURITY.md)。

### 备份语义

- 管理面的普通 Backup 使用 SQLite Online Backup，适合同实例恢复，不包含主密钥和完整敏感运行状态。
- `hx-pxy-install backup` 会短暂停止控制面，归档配置、数据库、主密钥、运行配置和快照；归档包含秘密。
- Portable Export 默认不导出秘密，用于跨实例迁移配置。
- 自动 Restore 和加密 Backup Wrapper 仍在后续清单。

## 开发与测试

```bash
# 后端
go test ./...
go vet ./...

# 前端
cd web
pnpm install --frozen-lockfile
pnpm run check
pnpm run build
```

前端关键链路使用 Playwright 验证桌面、窄桌面和移动端布局，包含页面横向溢出、筛选按钮相交、侧栏折叠宽度和 Tooltip 可见性断言。运行截图保存在 [`docs/screenshots/`](docs/screenshots/)。

代码修改应遵守 [AGENTS.md](AGENTS.md) 中的模块边界、并发、安全、测试和本地进程生命周期要求。

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [v1 核心清单](docs/V1_CORE.md) | 功能范围、验收标准、完成状态和剩余工作 |
| [架构设计](docs/ARCHITECTURE.md) | 控制面/数据面、领域模型、配置事务和模块边界 |
| [可靠性与部署](docs/RELIABILITY.md) | systemd、安装、升级、回滚、安全和资源保护 |
| [订阅管理](docs/SUBSCRIPTIONS.md) | 加密来源、刷新、快照、调度和 SSRF |
| [兼容矩阵](docs/SUBSCRIPTION_COMPATIBILITY.md) | Provider、Mihomo、sing-box 和分享 URI |
| [基准与故障恢复](docs/BENCHMARKS.md) | 环境、命令、结果、恢复测试和未测边界 |
| [探测、路由与总览](docs/PROBES_ROUTING_OVERVIEW.md) | 节点检测、路由规则与 SSE 总览 |
| [流量统计](docs/TRAFFIC_STATS.md) | 聚合粒度、查询、保留策略和精度边界 |
| [Cloudflare / 雷池](docs/CLOUDFLARE.md) | WebSocket 入口、反向代理和公网边界 |
| [备份与导出](docs/BACKUP_EXPORT.md) | Artifact、Online Backup 与秘密处理 |
| [程序内接入契约](docs/CONSUMER_INTEGRATION_CONTRACT.md) | 冻结的节点清单 JSON、字段/协议枚举/错误码与变更流程 |
| [住宅代理 AI 对接指南](docs/RESIDENTIAL_AI_QUICKSTART.md) | 给 AI/自动化的极简订阅对接与每次先 next 的轮换要点 |
| [住宅代理并发集成标准](docs/RESIDENTIAL_INTEGRATION_STANDARD.md) | 统一窗口模型、租约、版本护栏与多服务对接契约 |
| [v2 能力](docs/V2.md) | 规则流水线、认证、告警、调度与浏览器终端 |
| [安全策略](SECURITY.md) | 漏洞报告、部署边界与浏览器终端威胁模型 |

## 当前状态与路线

已经形成可运行纵向闭环：

```text
添加订阅 -> 刷新/展开 Provider -> 节点去重 -> 质量检测
-> 动态 Proxy Group -> Listener -> Mihomo Apply -> 流量与告警
```

仍在推进：

- 真实 HTTP CONNECT、SOCKS5 和高并发数据面性能报告。
- 干净 Debian/Ubuntu VM 的安装、升级、整机重启和断电演练。
- 自动 Restore、加密灾难备份和 Portable Import。
- 更多服务端入口、完整资源监控与系统诊断整合页。
- OpenAPI、CI 发布流水线、签名校验和发行版维护流程。

项目进度以 [`docs/V1_CORE.md`](docs/V1_CORE.md) 为准，不以 README 的概括替代验收清单。

## 项目边界

HX-Pxy 明确不做：

- 自行实现 VMess、VLESS、Trojan、Hysteria、TUIC、SOCKS5 或 HTTP CONNECT 转发。
- 让 Go 控制面进入实际代理流量路径。
- 将 WebSocket Path 当作认证。
- 默认公开管理 API、External Controller 或新 Listener。
- 承诺“支持所有协议”或所有未知订阅变体。
- 在 v1 引入 MySQL、Redis、Kafka、etcd 或独立任务队列。

## 参与贡献

提交 Issue 或 Pull Request 前，请先阅读 [AGENTS.md](AGENTS.md) 和对应领域文档：

1. 描述要解决的用户问题、行为边界和兼容性影响。
2. 订阅样本必须脱敏，不提交真实 URL、Token、密码、私钥或数据库。
3. 保持 `api -> application service -> domain/interface -> repository/dataplane` 边界。
4. 新行为需要单元测试；涉及 SQLite、HTTP、Mihomo 或前端主链路时增加集成/E2E 测试。
5. 提交前运行格式化、`go test ./...`、`go vet ./...` 和前端构建。

## 许可

HX-Pxy 自有代码采用 [MIT License](LICENSE)。Mihomo 由其上游独立发布，依赖项和随 Release 分发的第三方组件继续适用各自的许可证；MIT License 不会覆盖或替代这些上游条款。

---

<div align="center">

HX-Pxy · 一个控制面，一个数据面，配置可解释，失败可恢复。

</div>
