# 住宅渠道发布与自动化进度

> 状态时间：2026-08-05  
> 当前契约：[`RESIDENTIAL_V2_CONTRACT.md`](RESIDENTIAL_V2_CONTRACT.md)

## 1. 当前目标

住宅渠道是客户端发布与自动化控制的唯一所有权边界：

- 每个渠道独立选择 VLESS、VMess 或 Trojan over WebSocket。
- 每个渠道独立发布声明节点订阅和自动化控制 URL。
- 普通代理服务继续使用各自 Listener 的订阅，不与住宅渠道做全局聚合。
- 两类复制动作都位于「代理服务」页面；「住宅代理」页面只负责资源管理。

推荐公网拓扑：

```text
Clash / OutlookRegister 本机 Mihomo
  -> HTTPS/WSS 域名
  -> Cloudflare 橙云
  -> VPS:443 雷池
  -> HX-Pxy Edge Relay
  -> Mihomo 环回 WS Listener
  -> 住宅节点
```

浏览器需要 HTTP/SOCKS 时，由 OutlookRegister 或客户端本机 Mihomo 消费渠道 WS 端点并落地为
环回代理。Go 控制面不实现协议转发。

## 2. 已完成

### 2.1 声明住宅节点

- Schema v24 增加 `session_count`、`idle_release_seconds`、独立 `control_token`、历史可选
  `direct_listener_id`、声明序号和最后使用时间。
- sticky 渠道可声明 1..N 个稳定节点；缩扩容保留仍存在节点的名称和凭据。
- 空闲释放只释放供应商分配，节点身份仍保留；下一次使用或 `next` 时重新分配。
- 每个节点可执行 `next`，并可单独切换 `residential`、`upstream` 或 `direct` 路由。

### 2.2 渠道协议与订阅

- 新渠道可选择 VLESS、VMess 或 Trojan WS；内部端口、WS 路径和引导凭据自动分配。
- 每个渠道使用自己的 `/sub/<share_token>`，只发布本渠道声明节点。
- 渠道 provisioning 凭据不会进入订阅；每个声明节点使用自己的稳定凭据。
- Schema v26 删除旧 `client_subscription_token` 元数据，旧全局管理 API 返回 404。
- 前端删除住宅页统一订阅面板，在「代理服务」行提供 Clash/Mihomo 渠道订阅复制动作。

### 2.3 自动化控制与 OutlookRegister

- `GET /ctl/<control_token>/nodes` 返回声明节点池和协议中立的 `endpoints[]`；API 提取节点已分配时
  额外返回只对 control token 可见的 `residential_endpoint`。
- `POST /ctl/<control_token>/nodes/<index>/next` 刷新指定节点背后的住宅出口。
- `POST /ctl/<control_token>/nodes/<index>/route` 切换该节点路由。
- 控制 token 与订阅 token 分离；未知、禁用和不适用的 token 返回 404，访问日志隐藏 token。
- 「代理服务」行独立提供自动化控制 URL 复制动作。
- OutlookRegister 跨代理池实例共享同一 control URL 的节点租约；API 提取模式直接把住宅 IP:port
  交给短生命周期本机 Mihomo，首实例优先 127.0.0.1:2334，并发实例使用独立环回端口。其他模式
  继续从 VLESS/VMess/Trojan WS URI 落地；释放租约或验证失败时停止并清理本地实例。

### 2.4 住宅流量统计

- Schema v25 增加稳定资源类型 `residential_channel`。
- 托管 WS Listener 和历史兼容入口都归因到渠道 ID；出口 IP 轮换不会重置累计值。
- 上传、下载、连接数和趋势继续由 Mihomo 连接快照采样、内存聚合并批量写 SQLite。
- 住宅页显示渠道累计流量；统计是采样观测值，不作为精确计费账单。

### 2.5 CF Worker 面板（BPB）供应商

- 新增 `cf-worker` 轮换模式与 BPB-Worker-Panel 预设：用户只填一个 CF 链接
  （面板链接或 `sub/raw` 订阅链接）到 `worker_url`，服务端 AEAD 加密保存，API 不回显。
- 控制面经可选的 `api_proxy_url`（出口代理）拉取该链接，用订阅解析器解析返回的
  VLESS/Trojan WebSocket 分享 URI 生成住宅节点；canonical 配置透传给 Mihomo 编译器，
  控制面不实现任何代理协议。
- `session_ttl_seconds` 强制为 0：用户不主动 `next` 就不自动刷新；每次 `next` 才重新
  拉取面板链接并轮换 Cloudflare 出口地址。
- 供应商测试连接只验证面板可拉取、节点可解析与首个端点 TCP 可达；真实住宅出口 IP
  由渠道数据面验证。
- 兼容性修正：BPB `/sub/raw` 的分享 URI 携带标量 `alpn=http/1.1`（非 TLS 端口为
  `security=none` 且不带 sni/fp/alpn）。Mihomo 编译器现在把 `alpn` 统一规范化为
  切片（单值、逗号分隔或已是切片），并在安装了 Mihomo 的机器上用 `mihomo -t`
  回归验证 BPB VLESS/Trojan WS 节点形状。

> 来源与差异：本模式的协议语义（`/sub/raw` 返回 Base64 编码的 VLESS/Trojan 分享 URI、
> 每次请求重新解析 Cloudflare 地址与随机 WS 路径）借鉴自研究仓库
> [`ref/BPB-Worker-Panel`](https://github.com/bia-pain-bache/BPB-Worker-Panel)。
> HX-Pxy 只复用其面板链接的订阅输出约定，住宅集成模型（Provider/Channel/Session、
> 声明节点、control token、`dialer-proxy`、AEAD 加密信封）仍为本项目自身架构，未复制其源码。

### 2.6 并发集成标准：独占租约 + 分配版本护栏

- Schema v32 为 `residential_client_sessions` 增加 `lease_id`、`lease_holder`、
  `lease_expires_at`、`alloc_version` 四列；`alloc_version` 在轮换、路由切换、
  重新分配与空闲释放时单调 +1。
- 新增 `/ctl/<token>/nodes/<index>/claim|heartbeat|release`：服务器端**独占租约**
  原语，解决多个服务同时对接时"同一 IP 窗口被多个服务共享"的问题；被他人持有返回
  `409 lease_held`，租约失效返回 `409 lease_expired`。
- `next`/`route` 接受可选 `lease_id` + `expected_alloc_version` 护栏：版本过期返回
  `409 alloc_version_changed`，防止"对同一 IP 窗口的重复轮换"；不带护栏的旧客户端
  在空闲窗口上行为不变，兼容 OutlookRegister。
- `GET /ctl/<token>/nodes` 返回每节点 `alloc_version` 与 `lease`（holder/expires_at，
  不含能力令牌 `lease_id`）；管理端渠道视图同样暴露租约状态（不含 lease_id）。
- 空闲释放跳过有活跃租约的节点：租约即活跃信号，避免误伤在用窗口。
- 完整标准与集成验收清单见
  [`RESIDENTIAL_INTEGRATION_STANDARD.md`](RESIDENTIAL_INTEGRATION_STANDARD.md)。

## 3. 已验证

- `go test ./...`：全量通过（30 packages），覆盖三种住宅 WS 协议、渠道订阅、控制端点、
  旧全局 API 的 404 行为，BPB VLESS/Trojan WS 节点在真实 `mihomo -t` 下的编译兼容性，
  以及租约 claim/heartbeat/release、`alloc_version` CAS 护栏、空闲释放跳过租约节点
  等统一窗口并发语义。
- `go vet ./...`、前端 TypeScript 检查和生产构建通过。
- OutlookRegister 全量测试：100 passed；覆盖三种 URI、本地 Mihomo 生命周期、端点选择与失败清理。
- 住宅渠道 Playwright E2E 通过：真实创建 VMess 渠道，校验代理服务页两个复制动作及剪贴板 URL，
  并保存桌面和移动端截图。

## 4. 仍需真实环境验收

- 使用真实住宅供应商完成：建渠道 -> 拉渠道订阅 -> 真实代理请求 -> `next` -> 再次请求 ->
  核对渠道流量。
- 分别验证 CF 橙云 + 雷池 443 的 VLESS、VMess、Trojan WS 链路；一种协议成功不能外推为三种
  全部成功，也不能外推到公网 HTTP CONNECT/SOCKS。
- 使用真实控制 URL 验证 OutlookRegister 本地 Mihomo 的出口探测与完整注册 flow。只读
  `GET /nodes` 可用于诊断；未经授权不得调用会消耗供应商配额的 `POST /next`。

未完成上述真实边界验收前，不宣称住宅供应商兼容性、精确计费、端到端吞吐或无中断升级。
