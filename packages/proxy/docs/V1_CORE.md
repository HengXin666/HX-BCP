# HX-Pxy v1 核心功能文档

## 1. v1 目标

构建一个可长期运行在个人 Linux 服务器上的代理管理服务：统一导入机场订阅，持续检测节点质量，按规则组合代理组，为每个代理组提供独立的 HTTP / SOCKS5 / Mixed 入口，并支持将当前服务器作为直连出口对外提供代理节点。

v1 必须满足以下底线：

- [ ] 服务器重启后自动恢复全部订阅、代理组、监听端口和统计任务；持久化恢复与双 systemd unit 已完成，真实整机重启验收仍待执行。
- [x] 任意一次订阅刷新、配置生成或数据面重载失败，都不得破坏上一版可用配置。
- [x] 代理协议由成熟数据面实现，控制面不得自行实现协议编解码和加密传输。
- [x] 生产部署仅保留 Go 控制面、Mihomo 数据面和静态前端，不运行 Node.js 开发服务器。
- [ ] 所有后台任务均具备并发上限、超时、取消、重试退避和可观测状态。
- [x] 提供可重复执行的 `install.sh`，并注册控制面和数据面两个 systemd 服务。

---

## 2. 术语

- **订阅 Subscription**：一个远程 URL 或手工配置源，能够产生若干代理节点。
- **节点 Node**：一个具体的代理出站，包含协议、地址、端口、凭据、传输层和 TLS 等配置。
- **节点快照 Snapshot**：某次订阅成功刷新后得到的不可变节点集合。
- **质量检测 Probe**：通过节点执行延迟、连通性、速度、地区或 IP 质量检测。
- **规则流水线 Rule Pipeline**：按顺序执行节点补充、过滤、评分、分组和排序的可扩展处理链。
- **代理组 Proxy Group**：由若干订阅、节点、子代理组和规则组成的逻辑出站。
- **入口 Listener**：对外监听的 HTTP、SOCKS5、Mixed、VLESS、VMess、Trojan 等服务端入口。
- **控制面 Control Plane**：HX-Pxy 后端，负责配置、调度、状态、统计和管理 API。
- **数据面 Data Plane**：Mihomo 进程，负责实际连接建立、协议处理、转发和流量统计。
- **直连出口 Direct Egress**：不经过机场节点，直接使用当前服务器公网 IP 访问目标。

---

## 3. 订阅管理

### 3.1 基础能力

- [x] 创建、编辑、启用、禁用和删除订阅。
- [x] 支持 HTTP / HTTPS 订阅 URL。
- [x] 支持为订阅请求配置自定义 `User-Agent`、Header 和超时。
- [ ] 支持为订阅请求指定上游代理；当前 Fetcher 明确直连。
- [x] 支持保存和刷新手工粘贴的 URI、Base64、Clash / Mihomo YAML 和 sing-box JSON 原始内容。
- [x] 支持从本地普通文件导入订阅内容，并拒绝符号链接。
- [x] 支持手动刷新单个订阅。
- [x] 支持批量手动刷新全部或选定订阅，使用有界 worker pool 并返回逐订阅结果。
- [x] 支持固定间隔的自动刷新计划，使用 SQLite 租约在重启后恢复。
- [x] 支持 Cron 表达式（标准 5 字段，UTC 求值，内置解析器无外部依赖）。
- [x] 支持请求超时和失败指数退避。
- [x] 支持最大重试次数和随机抖动：退避带 ±20% 抖动，连续失败超过 8 次后回落到常规调度间隔。
- [x] 支持 `ETag`、`Last-Modified` 和条件请求，减少无效下载。
- [x] 限制响应体与文件大小，防止异常订阅耗尽内存或磁盘。
- [x] 保存最近一次成功快照和连续失败次数。
- [x] 持久化最近一次失败的结构化错误原因（稳定错误码 + 脱敏消息 + 时间，成功后清除）。
- [x] 刷新失败时继续使用最近一次成功快照。
- [x] 订阅 URL、Header 和内联内容使用 AEAD 加密存储，API 不回显来源配置，请求错误不输出完整 URL。

### 3.2 格式与协议边界

- [x] 对原始订阅执行格式识别，并将解析结果写入不可变快照摘要。
- [x] 支持 Mihomo `payload`、内联、HTTP 和 File Proxy Provider 展开，并设置深度、数量和累计大小上限。
- [x] 支持 Clash / Mihomo YAML 中的 `proxies` 节点列表。
- [x] 支持 sing-box JSON 中的 `outbounds` 节点列表，并对内置直连等非代理出站保留结构化失败原因。
- [x] 支持 VLESS、VMess、Trojan、Shadowsocks、HTTP(S)、SOCKS5、Hysteria、Hysteria2 / Hy2、TUIC、AnyTLS、SSH 分享 URI 和 Base64 包装 URI 列表。
- [x] 解析器通过独立包和服务注入点接入，可继续扩展其他订阅格式。
- [x] 导入时保留无法识别的节点及失败原因，不静默丢弃。
- [x] 关于页展示当前数据面版本和控制面可解析协议列表，最终可用性由该 Mihomo 构建校验。
- [x] 不使用“支持所有协议”作为不可验证承诺；准确边界归档在解析兼容矩阵中。

### 3.3 节点规范化与去重

- [x] 为每个节点生成稳定 `node_fingerprint`，不依赖展示名称。
- [x] 指纹覆盖当前解析器识别的协议、服务地址、端口、身份凭据、传输层和 TLS 规范字段。
- [x] 同一订阅内重复节点自动合并。
- [x] 跨订阅相同节点保留来源关系，并复用一个底层节点记录；数据面实例生成仍待 Mihomo 编译器完成。
- [x] 节点改名不创建新指纹记录，为后续历史质量数据连续性保留稳定主键。
- [x] 订阅节点从新快照消失时进入 `retired` 状态，不立即删除历史数据；住宅渠道节点由渠道自己管理寿命，订阅刷新不得退场它们。
- [x] 节点规范配置使用 AEAD 加密，API 和普通日志不输出身份凭据；证书与私钥格式仍需后续兼容测试。

### 3.4 验收标准

- [x] 订阅服务返回超时、非 2xx、空内容或超大文件时，系统保持上一版成功原始快照。
- [x] 非法内容、仅含不支持节点或部分单节点解析失败时，不破坏上一版活动节点集合；有效节点与结构化失败可同时保存。
- [x] 连续多次刷新相同原始内容不会产生重复快照。
- [x] 连续刷新相同内容不会产生重复节点，并可为解析器上线前的旧快照重建节点关系。
- [x] SQLite WAL 辅助进程在未提交事务中被强制终止后，可恢复最后已提交状态并通过完整性检查。
- [x] 外部 Mihomo 重载失败时恢复上一版配置；控制面关闭不终止健康数据面。
- [ ] 完成整机断电、快照文件发布中断和磁盘满的环境故障注入。
- [x] 刷新任务支持 Context 取消，自动调度使用有界 worker pool，不为每个订阅创建永久 goroutine。

---

## 4. 节点生命周期与质量保证

### 4.1 节点状态

- [x] 支持 `candidate`：已导入但尚未完成检测。
- [x] 支持 `healthy`：最近一次真实代理请求成功且延迟不高于当前阈值。
- [x] 支持 `degraded`：高延迟或近期检测失败，但尚未达到隔离阈值。
- [x] 支持 `quarantined`：连续三次检测失败后进入隔离，后续成功可恢复。
- [x] 支持 `disabled`：管理员可禁用/启用节点，变更后立即重新编译并下发数据面配置。
- [x] 支持 `retired`：已从最新活动订阅快照移除，或由住宅渠道显式退场，仅保留历史记录。
- [x] 检测历史记录时间、结果、延迟、错误分类，并在节点表保存最新状态。

### 4.2 检测指标

- [ ] 独立 TCP 建连延迟指标；当前延迟来自完整代理 HTTP 检测。
- [ ] 可选 ICMP 延迟；仅在权限和目标支持时启用。
- [x] 通过 Mihomo 指定节点执行 HTTP/HTTPS 请求并记录端到端延迟。
- [x] 默认使用 HTTPS 204 测试地址，底层 API 支持 URL、超时和期望状态范围；自定义管理配置仍未开放。
- [ ] 可选小流量下载测速，配置测试文件、最大字节数、超时和频率。
- [ ] 可选上传测速；默认关闭，避免消耗订阅流量。
- [ ] 出口公网 IP 探测。
- [ ] 国家 / 地区 / ASN / 运营商识别。
- [ ] IPv4、IPv6、UDP 和 DNS 可用性探测。
- [ ] IP 质量检测扩展点：风险评分、代理/VPN 标记、数据中心/住宅属性、黑名单和目标站可访问性。
- [ ] 每个指标保存最新值、滑动窗口统计和检测时间。

### 4.3 检测调度

- [x] 使用单一轮询调度器批量选择到期节点，不为每个节点创建永久定时 goroutine。
- [ ] 支持全局和按订阅的最大并发数；全局并发可配置并使用有界 worker pool，按订阅覆盖仍未开放。
- [ ] 支持每分钟最大探测流量预算。
- [ ] 健康节点降低探测频率，异常节点执行有限次数快速复测后进入退避；当前统一为 10 分钟周期。
- [ ] 代理组正在使用的节点优先检测。
- [x] 探测请求通过 Mihomo 的指定节点延迟 API 真正由被测节点发送。
- [x] 当前串行检测保证同一进程内相同检测任务不并发重复执行。
- [x] 系统关闭时通过共享 Context 取消自动检测，并受进程关闭超时约束。

### 4.4 可扩展规则流水线

规则流水线按以下固定阶段运行：

`Normalize -> Enrich -> Predicate -> Score -> Bucket -> Sort -> Limit`

- [x] `NodeNormalizer`：已提供名称空白规范化与协议小写化；别名与更多字段修正可继续扩展。
- [x] `NodeEnricher`：已提供基于名称的地区标签富集；结构化 Geo/ASN/IP 质量 Enricher 待指标采集上线。
- [x] `NodePredicate`：按订阅、协议、生命周期、名称关键词、地区和最大延迟过滤。
- [x] `NodeScorer`：可配置加权评分，指标归一化，缺失指标使用显式策略。
- [x] `NodeBucket`：按订阅、地区或协议分桶，未知值进入 `UNKNOWN` 桶。
- [x] `NodeSorter`：按评分、延迟或名称稳定排序，节点 ID 恒为最终决胜键。
- [x] `NodeLimiter`：支持总量与每桶数量上限，超限节点记录排除原因。
- [x] 每个阶段均通过注册表发现和构造，禁止在代理组逻辑中写死大量 `switch`。
- [x] 每条规则输出“命中/未命中/修改前后值/排除原因”（`ResolveMembers` 返回完整 Trace 与 Exclusion）。
- [x] 规则失败默认只影响当前节点（fail-open + error trace），不会清空数据面配置。

建议接口：

```go
type NodeContext struct {
    Node      *Node
    Metrics   MetricSnapshot
    Labels    map[string]string
    Trace     []RuleTrace
}

type Enricher interface {
    Enrich(context.Context, *NodeContext) error
}

type Predicate interface {
    Match(context.Context, *NodeContext) (bool, string, error)
}

type Scorer interface {
    Score(context.Context, *NodeContext) (float64, ScoreBreakdown, error)
}
```

### 4.5 默认评分模型

v1 提供可配置的默认加权模型，不将权重写死在代码中：

```text
score = availability_score
      + latency_score
      + speed_score
      + ip_quality_score
      + stability_score
      - failure_penalty
```

- [x] 所有指标先归一化，再应用权重（延迟按可配置上限线性映射到 [0,1]）。
- [x] 缺失指标使用显式缺失策略（skip / neutral / zero），不直接当作 0 或满分。
- [x] 评分结果展示分项贡献（ScoreBreakdown 进入规则 Trace）。
- [x] 支持按代理组覆盖权重和阈值（`rule_pipeline_json` 中的 `score` 配置）。
- [x] 支持硬过滤（Predicate）与软评分（Score + Sort）组合。

---

## 5. 自定义代理组

### 5.1 代理组来源

- [x] 一个代理组直接引用多个订阅，并在每次配置编译时从活动快照解析成员。
- [x] 一个代理组可引用多个单独节点。
- [x] 一个代理组可引用其他代理组，形成有限层级组合。
- [x] 支持加入当前服务器的 `DIRECT` 直连出口。
- [x] 管理面可不创建任何订阅，直接以当前服务器 `DIRECT` 出口创建代理服务。
- [x] 支持按来源订阅、名称地区标签、协议、生命周期和最新延迟动态筛选、排序与 Top N；结构化 Geo、ASN、自定义标签和完整规则 Trace 仍待规则流水线补齐。
- [x] 禁止形成代理组循环依赖，并在保存时返回完整依赖链。
- [x] 代理组为空时支持 `fail-closed` 或回退到 `DIRECT`；指定节点/组回退仍未完成。
- [x] 默认拒绝「解析后只含一个成员」的代理组（创建与更新同判）：判据看当前快照解析出的成员数，不只看 `node_ids` 长度。一个 `node_id`、当前只有一个节点的订阅、筛选项或 `limit=1` 之后只剩一个、以及 `allow_empty` 配一个节点都会被拒；0 个成员（休眠/未刷新）放行。组是在一批节点里挑一个，不是指向某个节点的句柄。正确形态是一个大组 + `listener.node_id` 钉具体节点；`proxy_service.create` 的 `listener.node_id` 不建任何组。`include_direct` 单独成组与单个 `group_ids` 组合是策略例外；住宅渠道等由聚合托管成员的组走 `AllowSingleNode` 豁免。
- [x] 代理组 ID 与节点 ID 是两套 id 空间：`node_ids` / `group_ids` / `subscription_ids` 各只收对应类型，放错按**类型**错误报（点名该放哪个字段），不被数量规则抢先回答；`GET /api/v1/capabilities` 的 `object_model` 段机器可读地陈述该区别。

### 5.2 出站策略

- [x] `manual`：使用 Mihomo `select` 组，成员由管理员配置。
- [x] `url-test`：使用 Mihomo 原生 URL Test 策略。
- [x] `fallback`：使用 Mihomo 原生 Fallback 策略。
- [x] `round-robin`：使用 Mihomo Load Balance 轮询策略。
- [x] `consistent-hashing`：使用 Mihomo Load Balance 一致性哈希策略。
- [x] `sticky-sessions`：使用 Mihomo Load Balance 会话粘滞策略。
- [ ] `random`：当前 Mihomo 版本未在本控制面开放该策略。
- [x] 策略变更经过完整配置编译、`mihomo -t` 校验、原子发布、就绪检查和失败回滚。
- [x] 支持全局站点别名与逐 Proxy Group 独立路由动作，并路由到 `REJECT`、`DIRECT` 或指定 Proxy Group。

### 5.3 会话模式

v1 将用户提出的会话需求映射为可组合策略：

- [ ] `sticky_node`：相同来源客户端与目标尽量固定到同一节点。
- [ ] `sticky_subscription`：先在订阅子组之间做一致性哈希，再在选中订阅内选择节点。
- [ ] `sticky_region`：先在地区子组之间做一致性哈希，再在选中地区内选择节点。
- [ ] `target_affinity`：相同目标域名尽量使用同一节点。
- [ ] 支持配置会话 TTL。
- [ ] 节点失效时立即重新选择，不等待会话 TTL 过期。
- [ ] 会话粘滞只保证“尽量”，不得以牺牲可用性为代价。
- [x] 住宅 sticky 渠道支持一个 Listener/token 下的显式客户端 `session_id`，按 Mihomo
  `IN-USER` 隔离按需分配的住宅节点，并可单会话切换到 `DIRECT` 和关闭旧连接。
- [x] 住宅渠道创建时不预取 IP；客户端建立会话时实时分配，并按供应商 TTL 配置选择
  到期终止会话或保留会话并换 IP。
- [x] 住宅供应商和渠道支持固定地区与手动候选的应用层随机地区；随机选择使用 Go
  `crypto/rand`，并在 API 提取请求前覆盖供应商地区参数。
- [x] API 提取住宅节点可通过高权限 control token 下发当前 IP:port 和节点级鉴权，由客户端本机
  Mihomo 直接承载业务流量；普通订阅、管理列表和账密网关不暴露该字段。
- [x] 住宅供应商支持 CF Worker 面板（BPB-Worker-Panel）：管理员只填一个面板/raw 订阅链接到
  `worker_url`，控制面经可选出口代理拉取并解析 VLESS/Trojan WebSocket 节点，客户端主动
  `next` 才重新拉取并轮换出口地址，TTL 强制 0 不自动刷新。
- [x] 住宅声明节点支持服务器端独占租约（`/ctl/.../claim|heartbeat|release`）：每个声明节点
  是一个互斥 IP 窗口，多个服务并发对接时同一窗口只被一个持有者使用；被他人持有返回
  `409 lease_held`，租约失效返回 `409 lease_expired`。
- [x] 声明节点分配版本 `alloc_version` 单调递增，`next`/`route` 支持 `expected_alloc_version`
  CAS 护栏：基于过期版本的轮换返回 `409 alloc_version_changed`，防止同一窗口被重复轮换；
  不带护栏的旧客户端在空闲窗口上行为不变。
- [x] 并发对接标准文档与集成验收清单：[住宅代理并发集成标准](RESIDENTIAL_INTEGRATION_STANDARD.md)。

### 5.4 监听端口

- [x] 每个代理组可创建一个或多个 Listener。
- [x] 支持 HTTP CONNECT 代理。
- [x] 支持 Mihomo SOCKS5 TCP/UDP Listener。
- [x] 支持 Mixed 端口，同时接受 HTTP 和 SOCKS5。
- [x] 支持绑定环回、指定网卡地址或未指定地址；非环回地址强制启用认证。
- [x] 数据库拒绝重复端点，Mihomo 校验与启动就绪检查处理系统端口占用。
- [x] 端口由部署配置管理，变更经过候选配置校验、受控重启和失败回滚；全局设置及代理服务新建/编辑页只读展示固定端口，不提供端口编辑控件。
- [x] Listener 通过 Mihomo `proxy` 字段直接绑定代理组；普通入口流量无需经过 Go 控制面，Cloudflare / 雷池 WebSocket 入口可选经过固定路径 Edge Relay。
- [x] 新住宅渠道可选择 VLESS、VMess 或 Trojan over WebSocket；内部环回端口、WS 路径和引导凭据由控制面自动分配，管理 API 不再接受公网 HTTP/SOCKS/Mixed 直连入口。
- [x] 住宅 Listener 强制只绑定环回地址；WS 公网端点固定为雷池 HTTPS 443，渠道订阅和自动化控制 URL 的复制入口位于「代理服务」。
- [x] 支持为 Listener 配置用户名和密码。
- [x] 新建代理服务入口默认绑定 0.0.0.0 并启用用户名/密码认证（随机 UUID 密码），供内网/Docker 网络访问；公网暴露由防火墙保护。
- [x] 代理服务编辑通过原子 `PUT /api/v1/proxy-services/{group_id}` 一次保存组与入口，失败自动回滚，不再出现两步保存冲突。
- [x] 同端口绑定地址变更（如 127.0.0.1 -> 0.0.0.0）不再被数据面可用性预检误拦截；编辑失败时 Listener 数据库记录同步回滚，接口返回具体数据面错误（502 dataplane_apply_failed）而非笼统 500，前端失败后自动刷新版本号避免重试 409。
- [x] API 不回显凭据，后端以 AEAD 密文保存并仅在配置编译时解密。
- [ ] 支持连接数、空闲超时、最大并发和速率限制。

### 5.5 订阅导出

- [x] 为代理服务的每个 Listener 生成不可预测的订阅 Token。
- [ ] 支持吊销、轮换和设置过期时间；当前支持轮换，禁用 Listener 可停止访问，独立吊销和过期时间仍未开放。
- [x] 支持生成 Clash / Mihomo YAML。
- [x] 支持生成 V2RayN 可识别的 Base64 URI 列表。
- [x] 支持生成 sing-box JSON。
- [x] 支持输出单节点 URI 和二维码所需文本数据。
- [x] 导出内容指向当前服务器 Listener，而不是泄露上游机场节点凭据。
- [x] 配置公网端点时生成路径化订阅/会话 URL；未配置公网端点的环回 Listener 不回退导出内部端口。
- [ ] 支持按客户端类型覆盖域名、端口、TLS、SNI、WS Path 和备注。
- [ ] 订阅请求支持限速、访问日志和异常频率告警。

---

## 6. 当前服务器作为代理节点

### 6.1 直连出口

- [x] 内置 `DIRECT` 出站，目标流量直接使用服务器网络出口。
- [x] 可将 `DIRECT` 加入代理组，并可作为空组的显式直连回退。
- [ ] 支持指定出站网卡、IPv4 / IPv6 偏好和路由标记；当前支持自动识别或显式指定物理出站网卡，地址族偏好和路由标记仍未开放。
- [ ] 支持检测服务器公网 IP、地区、ASN 和目标站可达性。
- [ ] 直连出口与机场节点使用相同统计、限流和告警接口。

### 6.2 服务端协议入口

v1 优先提供适合反向代理和 CDN 的成熟协议模板：

- [x] HTTP Proxy Listener；HTTPS 目标通过 CONNECT 隧道承载。
- [x] SOCKS5 Listener。
- [x] VLESS over WebSocket。
- [x] VMess over WebSocket。
- [x] Trojan over WebSocket；gRPC 尚未开放。
- [ ] VLESS / VMess over gRPC。
- [ ] 可选原生 TCP Listener，直接暴露专用端口。
- [ ] 可选 Hysteria2 / TUIC 等 UDP/QUIC Listener，但不承诺经过普通 Cloudflare 代理。
- [x] 所有已开放协议由 Mihomo Listener 提供，控制面只生成、校验和管理配置。

### 6.3 Cloudflare 与雷池兼容边界

推荐链路：

```text
客户端
  -> Cloudflare :443
  -> 雷池 / 反向代理 :443
  -> 127.0.0.1:19090 HX Edge Relay
  -> 127.0.0.1:<listener-port>
  -> Mihomo Listener
  -> DIRECT 或指定 Proxy Group
```

- [x] 提供 VLESS / VMess / Trojan 的 WebSocket 模板；gRPC 模板尚未开放。
- [x] WebSocket 模板支持配置域名（同时作为 SNI/Host）、规范化 WS Path 和外部 TLS 端口；所有路径统一使用 `/__hx-proxy__/` 前缀，gRPC Service Name 尚未开放。
- [x] WebSocket 数据面 Listener 强制仅绑定环回地址，由反向代理转发。
- [x] Edge Relay 只接受 `/__hx-proxy__/` 下的 WebSocket Upgrade，并按 Host 与完整路径转发到环回 Mihomo Listener；不接受任意 URL、TCP、UDP 或 QUIC。
- [x] 管理 API、前端以及 `/sub/`、`/rot/` 与代理路径保持独立，不经过 Edge Relay。
- [ ] 正确处理 WebSocket Upgrade、HTTP/2 gRPC 和真实来源 IP Header。
- [x] 文档明确：普通 Cloudflare 代理不转发任意原生 TCP/UDP；raw TCP/UDP 需要直连、Cloudflare Spectrum 或其他四层转发能力。
- [ ] 提供雷池健康检查路径，且该路径不泄露节点或版本信息。

---

## 7. 统计与 30 天数据保留

### 7.1 统计范围

- [x] Listener 当前连接数、累计连接数和上行、下行字节数。
- [x] 代理组流量和连接数。
- [x] 节点流量和连接数；成功率和失败率仍待连接结算事件支持。
- [ ] 节点被选中次数和切换次数。
- [ ] 订阅刷新成功率、耗时和节点数量变化。
- [ ] 健康检测次数、耗时、错误类型和状态变化。
- [ ] 控制面和数据面 CPU、RSS、文件描述符、goroutine 和重启次数。

### 7.2 存储策略

- [x] 秒级计数先在内存中聚合，不为每个数据包写数据库。
- [x] 每分钟批量落库一次；进程退出前执行有界 Flush。
- [x] 近 24 小时保存 1 分钟粒度。
- [x] 2～7 天保存 5 分钟粒度。
- [x] 8～30 天保存 1 小时粒度。
- [x] 超过 30 天的趋势数据由定时任务删除；累计值保留到本地实体删除。
- [ ] SQLite 启用 WAL、busy timeout、外键和定期 checkpoint；前三项及单写连接限制已完成，独立定期 checkpoint 策略仍未实现。
- [x] 查询接口限制为最多 30 天、500 点和每页 200 项。
- [x] UI 图表默认读取聚合数据，不扫描原始事件表。

---

## 8. 告警

### 8.1 邮件通道

- [x] 支持标准 SMTP、STARTTLS 和 SMTPS。
- [x] 支持发件人、收件人列表和测试邮件（`POST /api/v1/alerts/settings/test`）。
- [x] SMTP 密码 AEAD 加密存储，API 不回显，更新留空保持原密码。
- [x] 邮件发送失败不更新通知时间，下一评估周期自动重试并记录日志。

### 8.2 告警条件

- [x] 订阅连续刷新失败（≥3 次）。
- [x] 订阅活动快照节点数变为 0；数量突降阈值待统计上线。
- [x] 代理组解析为空（fail-closed 为严重，direct 回退为警告）。
- [ ] Listener 无法启动或端口冲突。
- [x] Mihomo 进程未运行或最近一次配置应用失败。
- [ ] 节点连续失败率超过阈值。
- [ ] 流量、连接数或资源占用超过阈值。
- [ ] SQLite 写入失败、磁盘空间不足或数据清理失败。
- [ ] 管理员登录失败次数异常。
- [ ] 订阅 Token 被高频访问。

### 8.3 告警降噪

- [x] 相同告警按 (rule, target) 去重，6 小时冷却后重复通知，恢复时发送一次恢复邮件。
- [x] 告警状态持久化于 SQLite，进程重启后不重复轰炸；已恢复记录保留 30 天。
- [x] 支持告警确认（确认后不再重复通知）；静默窗口与维护窗口待后续版本。
- [x] 告警内容仅包含规则、目标名称与计数，不包含订阅 URL、Token、节点密码或管理凭据。

---

## 9. 管理员 Web 面板

### 9.1 认证

- [x] v1 仅支持一个管理员账户（数据库单行约束）。
- [x] 首次启动生成 0600 一次性 `admin-setup-token` 文件，通过 `/api/v1/auth/setup` 创建管理员，成功后删除 Token。
- [x] 密码使用 Argon2id（OWASP 参数），存储 PHC 格式，常数时间校验。
- [x] 登录 Session 使用 HttpOnly、SameSite=Strict Cookie（TLS 下自动 Secure）；数据库仅存 Token 的 SHA-256。
- [x] 支持 CSRF 防护（写操作要求 X-CSRF-Token）、登录限速与失败锁定（5 次失败锁 5 分钟）。
- [x] 支持修改管理员账号或密码并强制注销全部 Session（password_version 递增使旧会话全部失效）。
- [x] 支持 TOTP 2FA 密钥生成、加密存储、验证码校验和 Session 级终端 step-up 解锁；关闭 2FA 需要当前验证码；兼容改名前后的 TOTP 加密上下文，旧绑定升级后无需重新配置。
- [x] 管理 API 强制绑定显式环回地址；管理员配置完成前 API 仅在环回可达。

### 9.2 页面

- [ ] 总览：已完成服务状态、当前连接、秒级流量和路由拓扑；可用节点、告警和资源占用仍待补齐。
- [x] 订阅与节点库存页：在同一页面按标签管理订阅来源和去重节点，支持新增、编辑、刷新、调度、筛选、单节点/批量渐进复测、实时排序与禁用；刷新历史、节点变化 Diff 和历史趋势详情仍未完成。
- [x] 全局配置主题页：支持明亮、黑夜、跟随系统模式，以及颜色画板、HEX 输入、预设色和恢复默认主题色。
- [x] 基础 Backup / Export 页：创建、列表、下载、完整性校验和删除。
- [x] 代理组页：显式选择节点/DIRECT，支持动态来源筛选以及 manual、url-test、fallback 与负载均衡策略；子组引用和完整流水线由 API/编译器支持，页面直接编辑仍可继续完善。
- [x] Listener 页：创建、编辑和删除 HTTP/SOCKS/Mixed/VLESS-WS/VMess-WS/Trojan-WS 入口，配置绑定、认证、域名，并展示连接、流量与实时日志。
- [x] 服务器节点：在代理服务页提供 DIRECT 出口、服务端协议模板、公开域名和客户端订阅导出。
- [x] 代理服务行提供「复制 Docker 使用地址」：使用宿主网桥 / LAN 候选 IP、实际共享端口和认证生成完整 URI；剪贴板不可用时弹窗支持手动复制，WS 提供订阅与客户端落地说明，不伪造 HTTP 代理。地址由 GET /api/v1/system/network 提供，仍需验证容器网络和防火墙。
- [x] 代理组 API 返回 `type`（residential / subscription / custom），`GET /api/v1/proxy-groups?type=` 可筛选；住宅渠道组与订阅组严格区分。
- [x] 「Docker 使用方式」改为服务端判定（ready / needs_entry / client_landing / blocked）：环回、无认证或 WS 服务不再止于报错，预览并一键创建与源服务同出口的 Mixed「Docker 入口」（共享 Mixed 成员或仅绑定所选网桥网关，绝不 0.0.0.0），可撤销；只有可直连入口才显示代理 URI 复制按钮（住宅渠道 client_landing 仍提供订阅 URL 复制）。见 docs/NETWORK_OPERATIONS.md。
- [ ] 统计：近 24 小时趋势已完成，7 天和 30 天前端视图仍待补齐。
- [x] 告警页：当前告警、历史、确认与 SMTP 邮件设置；静默与维护窗口待后续。
- [x] 关于页：控制面版本、数据面版本、GitHub 仓库、解析协议边界、一行升级命令，以及页面内可输入 6 位 TOTP 完成 2FA step-up 的一键更新。
- [ ] 系统诊断整合页：配置校验、日志、备份和更新历史仍分散在现有页面。
- [ ] 实时任务使用 SSE 推送进度，避免高频轮询；批量节点复测和秒级总览已接入，其他长任务仍待迁移。

### 9.3 浏览器终端页面

- [x] 浏览器内终端（本机 PTY Shell；生产通过受限 Unix Socket 使用独立 root PTY helper，跳板式 SSH 远程连接不在本迭代范围）。
- [x] 基于成熟组件实现：前端 xterm.js，后端 creack/pty + coder/websocket，不自行实现终端协议。
- [x] 默认开启浏览器终端并保留紧急关闭开关（`HX_PXY_TERMINAL=0`），强制管理员认证和 TOTP 2FA step-up 验证；默认不因空闲断开，保留会话寿命上限（2 小时）、并发上限、结构化审计日志和页面内高风险操作提示；生产终端支持 `su` / `sudo`。
- [x] 切换侧栏页面不断开终端连接（终端页常驻挂载，页面切换只隐藏/显示）。
- [x] 服务器系统监控与文件面板仅在终端连接后显示；未连接时展示占位提示。
- [x] 文件面板与 Shell 当前目录双向同步：面板导航向 Shell 发送 `cd`；Shell 目录变化通过连接时和不可解析 `cd` 后的 `pwd` 探测以及输入侧 `cd` 命令识别驱动面板更新（探测基于时间预算，兼容慢速横幅和花式提示符）。
- [x] 文件树使用 vscode-icons 图标（web/public/vscode-icons 内置子集，MIT 许可）。
- [x] 双击文件夹不再触发第二次导航：同一目录项 400ms 内去重，避免从已导航路径重复拼接目录名导致列表 400 错误。
- [x] 修复重连后输入重复回显（重复 onData 监听器泄漏）。
- [x] 高丢包弱网兼容：服务端 WebSocket 心跳（15s ping + 15s pong 超时）回收静默死连接释放会话槽；客户端应用层 `ping`/`pong` 探测半死连接；异常断开后前端指数退避自动重连（保留 xterm 缓冲区）；文件面板瞬时失败自动重试并保留上次列表；系统监控 SSE 自动重连。
- [x] 会话目录保留：前端上报确认的 Shell 目录（`cwd` 控制帧），服务端按管理员节流持久化到 `system_metadata`；重连、重新登录或控制面重启后新会话 Shell 从上次目录启动，文件面板随 `pwd` 探测同步。
- [x] 文件操作与 PTY 会话解耦并发上限：helper 文件操作使用独立信号量（8），不再与 MaxSessions 争抢，两个 Shell 会话占满时文件列表/上传/下载仍即时可用（修复 `read unix .../terminal.sock: i/o timeout`）。

---

## 10. 管理 API

- [x] 管理 API 统一使用 `/api/v1` 前缀；健康检查、每个代理服务的 Token 化订阅与住宅渠道控制使用独立公开路径。
- [ ] OpenAPI 文档由代码生成并纳入 CI 校验。
- [ ] 列表接口支持分页、排序和结构化过滤。
- [ ] 写操作支持幂等键或版本号，避免重复提交。
- [x] 资源使用不可变 ID，不使用名称作为关联主键。
- [x] 更新接口防止并发覆盖：root helper 对更新请求单飞互斥（进程内锁 + `systemctl is-active hx-pxy-update` 跨重启检测），更新进行中重复请求直接返回"更新进行中"错误；调度异步化，helper 先确认再在独立 goroutine 运行 `systemd-run`（30s 上限），busy systemd 不再导致控制面握手超时误报更新失败。
- [ ] 长任务返回任务 ID，并通过 SSE 获取进度。
- [x] 错误响应包含稳定错误码、用户可读消息和追踪 ID。
- [x] 敏感字段在 API 响应中默认隐藏。
- [ ] 所有配置写操作产生审计记录。

### 10.1 备份、恢复与便携导出

- [x] 提供备份与导出 Artifact 的创建、列表、详情、下载、删除和完整性校验 API。
- [x] Artifact 使用不可预测 ID、`0600` 权限、SHA-256 元数据和内嵌 Manifest。
- [x] 归档采用临时文件、`fsync` 与原子重命名发布，失败时不留下半成品。
- [x] 默认导出不包含订阅凭据、运行时节点配置、主密钥等敏感源。
- [x] 未配置归档加密器时，明确拒绝 `include_secrets=true`，不得生成明文敏感包。
- [x] 使用 SQLite Online Backup API 生成事务一致的数据库快照，并执行完整性与 Schema Version 校验。
- [ ] 支持带密码或接收方公钥加密的完整备份，包含恢复所需主密钥和敏感配置。
- [ ] 支持备份定时计划、保留策略、空间预算和自动校验。
- [ ] 支持恢复预检、内容 Diff、停写窗口、原子恢复和失败回滚。
- [ ] 支持便携配置导入，并对版本、能力、端口冲突和外部路径执行迁移检查。
- [x] 备份用于同实例恢复；便携导出用于跨实例迁移，两者的秘密处理和兼容性语义保持分离。

---

## 11. 配置生成与数据面管理

- [x] 数据库是订阅、节点、代理组和 Listener 的控制面事实来源。
- [x] 每次变更从数据库重新编译完整 Mihomo 配置，不直接拼接修改生效文件。
- [x] 候选配置写入受保护临时文件并执行 `fsync`。
- [x] 调用 `mihomo -t` 验证语法与引用完整性。
- [x] 校验通过后使用原子重命名发布候选配置。
- [x] 本地开发模式执行受控重启；生产外部模式通过 Unix Controller 热重载；配置内容未变化时跳过应用。
- [x] 重载后检查 Unix 管理 Socket 和全部 Listener 端口是否就绪。
- [x] 启动、热重载或就绪检查失败时恢复上一版配置并重新加载。
- [x] 默认绑定 Linux 主路由物理出口，避免与本机 Clash / Mihomo TUN 形成数据面回环；支持显式覆盖或关闭。
  - 已知边界（2026-10-14 实测）：无线路由默认出口会被跳过（绑无线网卡会让出站 WebSocket 被 reset），此时数据面走系统默认路由；若本机存在 Clash 式 TUN，其 ip rule 会把**所有**本机发起的 TCP 送进 TUN，且 SO_BINDTODEVICE 与绑定源地址都无法绕过（实测均超时）——这需要宿主侧规则排除（如 ip rule add uidrange <hx-pxy uid> lookup main priority 8999），控制面无法在无 root 情况下解决。
- [x] 受管 Mihomo 不继承控制面环境里的代理变量（http_proxy/https_proxy/all_proxy 等），避免本机代理经环境改写数据面自己的 provider / rule-set / geo 抓取路径。
- [x] 拒绝上游节点直接指回同地址、同端口 Listener，并限制 Mihomo 默认 CPU 调度核与滚动日志大小。
- [ ] 保存最近若干个可用配置版本和变更摘要；当前只保留 active 与 previous 文件。
- [x] 生产外部模式由独立 systemd unit 管理 Mihomo，控制面异常退出不会终止数据面。
- [x] Mihomo 异常退出后由数据面 systemd unit 有界重启，控制面状态 API 和告警反映异常。

---

## 12. 安装、更新与服务注册

### 12.1 `install.sh`

- [ ] 支持 Debian / Ubuntu 和主流 systemd Linux 发行版。
- [x] 检测并支持 Linux `amd64`、`arm64`。
- [x] 默认解析最新 Release 为固定 tag，下载架构包和 `SHA256SUMS` 后校验。
- [x] 支持带 `VERSION`、Release 包和 `SHA256SUMS` 的离线安装。
- [x] 创建不可登录的系统用户 `hx-pxy`。
- [x] 创建 `/etc/hx-pxy`、`/var/lib/hx-pxy`、`/var/log/hx-pxy`。
- [x] 配置目录权限为最小可用权限。
- [x] 安装控制面、Mihomo、静态前端、安装器自身和控制面/数据面/终端 helper 三个 systemd unit。
- [x] 切换版本前用新 Mihomo 校验最后生效配置，服务启动执行数据库迁移。
- [x] 注册并启动双服务，确认两者 active 且 `/health/ready` 成功后结束。
- [x] 重复执行时只更新版本文件和服务定义，不主动删除用户数据。
- [x] 支持 `install`、`upgrade`、`repair`、`status`、`uninstall`、`purge` 子命令。
- [x] 更新 readiness 失败时原子恢复上一版完整 Release 链接；数据面配置应用失败恢复 `previous.yaml`。
- [x] 安装后通过 `sudo hx-pxy-install upgrade` 一行命令更新，成功升级时同步替换安装器自身。
- [x] Git tag `v*` 工作流执行 Go/前端校验，构建 amd64/arm64 包，固定 Mihomo 版本并发布 `SHA256SUMS`。
- [x] 生产前端可在管理员最近完成 2FA 后请求更新；root helper 只调度固定安装器 `upgrade`，不接受任意命令或版本参数。

### 12.2 systemd

- [x] `hx-pxy.service` 管理控制面。
- [x] `hx-pxy-dataplane.service` 独立管理 Mihomo。
- [x] 配置 `After=network-online.target` 和 `Wants=network-online.target`。
- [x] 配置 `Restart=on-failure`、合理退避和启动限速。
- [ ] 支持 systemd Watchdog。
- [x] 设置文件描述符上限并以不可登录的非 root 用户运行。
- [x] 使用 `ProtectSystem`、`ProtectHome`、`PrivateTmp`、`NoNewPrivileges` 等沙箱选项。
- [x] 数据面服务重启前先校验并加载 `runtime/active.yaml`，不依赖控制面先启动。
- [x] 提供 `systemctl status` 与 journald 结构化日志用于定位失败原因。

---

## 13. 性能与资源约束

### 13.1 架构约束

- [x] 生产环境保持一个控制面进程和一个 Mihomo 数据面进程。
- [x] 不为每个节点或代理组启动独立数据面进程。
- [x] 控制面不实现协议和目标连接转发；仅在显式启用 Cloudflare / 雷池拓扑时，经固定 `/__hx-proxy__/` Edge Relay 转发 WebSocket 连接。
- [x] 前端构建产物由控制面作为静态文件直接服务。
- [x] 订阅刷新等后台任务使用有界 worker pool。
- [x] 统计数据库写入按批次和事务执行。
- [ ] 日志使用异步或缓冲写入，并支持限速和轮转。
- [x] 产品自己写的日志（受管 Mihomo 的 mihomo.log* 字节轮转日志、~/.hx-pxy-runtime 下陈旧的短实例目录）按 mtime 保留 30 天，启动时与每 24 小时自动清理；保留天数可在全局配置的 log_retention_days 调整，0 表示关闭。
- [x] 统计按时间窗口聚合，禁止逐数据包持久化。

### 13.2 v1 基准测试场景

以下为发布前必须测量的场景，不在没有测试数据时虚构性能数字：

- [ ] 1,000 个节点、20 个订阅、32 个代理组的空闲 CPU 和 RSS。
- [x] 10,000 个节点的解析、规范化、去重和配置生成耗时。
- [ ] 100、1,000、10,000 并发 TCP 连接下的吞吐、延迟和文件描述符。
- [ ] HTTP CONNECT 与 SOCKS5 的短连接和长连接性能。
- [ ] 数据面直接配置与 HX-Pxy 管理配置的性能差异。
- [ ] 大规模健康检查对 CPU、内存、带宽和正常代理流量的影响。
- [x] SQLite 1,000 资源批量写入和 500 点图表查询基准；读写并发压力测试仍待补充。
- [ ] 服务器重启、数据面崩溃、控制面崩溃和磁盘接近满时的恢复时间。

### 13.3 性能发布门槛

- [ ] 控制面不得成为代理转发瓶颈。
- [ ] 无后台任务时控制面 CPU 应接近空闲。
- [x] 节点数量增长不得产生等数量的永久 goroutine 或独立进程。
- [ ] 健康检测必须可暂停，且暂停后资源占用快速回落。
- [x] 基准测试结果、环境、命令、分配量和未测边界已保存到 `docs/BENCHMARKS.md`。

---

## 14. 安全与可靠性验收

- [x] 订阅 Token、节点凭据、SMTP 密码和管理员密码不出现在日志中。
- [x] 管理 API 与 Mihomo External Controller 默认仅监听环回地址或私有 Unix Socket。
- [x] 公开 Listener 与管理端口分离。
- [ ] 所有外部输入均有大小、超时和格式限制。
- [x] 配置文件和数据库备份权限正确。
- [x] 恶意订阅无法通过节点名称或字段注入 Mihomo 配置结构。
- [x] 刷新、探测和导出接口具备 SSRF 防护策略和明确的管理员信任边界。
- [ ] 支持配置允许访问的订阅协议、端口和目标网段。
- [x] 默认拒绝订阅 URL 访问本机、链路本地地址和云元数据地址；管理员显式放行除外。
- [ ] 关键配置修改具有审计记录。
- [ ] 数据库定期在线备份，并验证备份可恢复。
- [ ] 服务器意外断电后 SQLite、配置快照和 systemd 服务可以正常恢复。

---

## 15. v1 完成定义

只有同时满足以下条件，v1 才能标记完成：

- [ ] 核心功能清单全部完成，或存在明确记录并经接受的删减项。
- [x] 后端单元测试、数据面配置生成测试和前端关键流程测试通过。
- [ ] 使用真实订阅完成导入、刷新、检测、代理组生成和代理访问验证。
- [ ] HTTP、SOCKS5、Mixed、VLESS-WS 至少各完成一次端到端测试。
- [ ] 完成 Cloudflare / 雷池链路的 WebSocket 端到端验证。
- [ ] 完成断电式重启、进程崩溃、订阅失败和配置回滚演练。
- [x] 完成 30 天统计保留逻辑的时间压缩测试。
- [ ] 完成安装、升级、修复、卸载和回滚验证。
- [ ] 发布基准测试报告，确认资源占用符合个人服务器部署目标。
- [ ] 文档、配置样例、故障排查和安全说明完整。

## 运行体验与可选网络运维验收

- [x] 精简节点列表投影、分页并发上限与列表短缓存；服务行不等待节点库存完成。
- [x] 住宅频道复用既有代理组创建入口，失败清理不删除原组。
- [x] Docker 地址复制不再直接拒绝环回/不兼容协议，而是给出可确认的 Docker 入口；活跃连接有来源分类和采样时间。
- [x] 文件日志默认 30 天定时清理，保护活跃路径；生产 journald 独立保留策略。
- [x] TUN 默认关闭、设置与真实 Mihomo 配置语法校验；诊断白名单、确认和认证错误路径测试。
- [ ] 特权 Linux 主机上 TUN 开启/关闭真实代理请求及与其他 TUN 共存验收（不能由语法测试替代）。
- [ ] 真实远程部署的 Docker DNS/转发修复与长时间弱网终端验收。

操作范围见 [NETWORK_OPERATIONS.md](NETWORK_OPERATIONS.md)；浏览器回归脚本位于 web/e2e。
