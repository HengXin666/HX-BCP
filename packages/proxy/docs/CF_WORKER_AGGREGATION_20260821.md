# CF Worker 聚合链路 + 会话懒分配（2026-08-21，v0.7.0）

> 本文记录 v0.7.0 的两项核心变更：
> **① CF Worker（BPB/cfnew）代理聚合链路打通**——用户核心诉求，CF 代理与住宅
> 身份统一由 HX-Pxy 聚合管理；
> **② 会话懒分配**——创建渠道不再预占出口 IP，首个客户端请求才分配。
>
> 相关提交：`c4ad3e1`（CF 转发三处根因修复）、`1b1f460`（懒分配）。

---

## 一、背景：为什么要改

用户的代理架构是分层聚合的：

```text
客户端（指纹浏览器/注册机等）
    │  统一订阅 /rot /ctl（换 IP 只调一次）
    ▼
HX-Pxy（控制面 + Mihomo 数据面）  ← 聚合层
    ├── 住宅身份（HX-Pxy 住宅渠道，session-template / api-list）
    └── CF 身份（cf-worker：Cloudflare Worker VLESS WS 订阅）  ← 本次打通
```

用户明确要求：**CF 代理也必须经过 HX-Pxy 聚合**，与住宅身份一样由上游
统一管理换 IP；后续新增服务只需对接 HX-Pxy 一个控制层。此前 cf-worker
模式虽然能拉订阅，但**数据面转发全部失败**（`connection reset by peer`），
表现为「支持 BPB 但没什么用」。

## 二、三处根因修复（c4ad3e1）

### 1. normalizeWorkerURL：cfnew 订阅路径被错误改写

**现象**：cf-worker provider 创建成功，但 channel 创建时
`cf-worker endpoint returned status 404`。

**根因**：`normalizeWorkerURL` 把 `/{securePath}/sub?app=xray` 强制改写为
`/{securePath}/sub/raw?app=xray`。标准 BPB-Worker-Panel 需要 `/sub/raw` 才返回
节点；但 **cfnew（BPB fork）的 `/{path}/sub?app=xray` 直接返回 base64 节点**
（实测 HTTP 200, 29988B），改写后变成 404。

**修复**：`sub` 分支保留原路径，仅 panel/login 分支才重写为 `/sub/raw`；
统一 pin `?app=xray`。标准 BPB 的 raw 链接（`/sub/raw?app=xray`）走 default
分支原样保留，兼容不受影响。

### 2. applyTransport：canonical 传输类型未提升 + 早数据未解析

**现象**：mihomo 编译出的节点有 `ws-opts` 但**没有 `network: ws`**，转发时
`failed to dial WebSocket: connection reset by peer`。

**根因（双重）**：
- `workerSessions` 路径的 canonical 把传输类型放在 `query.type`（如 `"ws"`），
  没有像单节点解析那样提升到顶层 `network`；`applyTransport` 只查
  `config["network"]` / `config["net"]`，找不到 → ws-opts 存在却不启用 WS。
- cfnew 节点的 path 形如 `/?ed=2048`——`ed` 是 **WebSocket 早数据（max early
  data）** 参数。mihomo 需要显式 `ws-opts.max-early-data` +
  `early-data-header-name: Sec-WebSocket-Protocol`，path 只保留 `/`；
  否则 mihomo 把 `?ed=2048` 当作普通 path 发送，worker 端
  `Sec-WebSocket-Protocol` 握手不匹配 → reset。

**修复**：`applyTransport` 增加 query 回退（network/host/path 从
`config["query"]` 读取）；新增 `splitEarlyData` 解析 path 的 `?ed=N` →
`max-early-data` + `early-data-header-name: Sec-WebSocket-Protocol`。

编译后节点形态（与独立 mihomo 配置一致，实测可转发）：

```yaml
proxies:
  - name: hx-node-xxx
    type: vless
    server: cloudflare.182682.xyz   # 优选域名
    port: 443
    uuid: <session uuid>
    tls: true
    servername: <worker>.alexrennie293.workers.dev
    network: ws
    client-fingerprint: randomized
    ws-opts:
      path: /
      headers: { Host: <worker>.alexrennie293.workers.dev }
      max-early-data: 2048
      early-data-header-name: Sec-WebSocket-Protocol
```

### 3. ResolveEgressInterface：Wi-Fi 接口强制绑定导致全部 reset

**现象**：独立 mihomo 配置相同节点可转发（出口美国 IP），HX-Pxy 托管的
mihomo 却全部 `connection reset by peer`。

**根因**：HX-Pxy 默认 `auto` 解析 Linux 主路由表得到默认出口网卡并注入
`interface-name: wlo1`（Wi-Fi，处于省电 DORMANT 模式）。强制绑定该网卡后，
mihomo 出站 WebSocket 被内核重置。

**修复**：`ResolveEgressInterface` 检测到接口为 Wi-Fi
（`/sys/class/net/<name>/wireless` 目录存在，比 IFF_DORMANT 更可靠——sysfs
flags 文件不含 dormant 位）时返回空串，不注入 `interface-name`，交给系统
路由决策。有线网卡场景不受影响。

**实测结果**：修复后 `interface-name` 从配置移除，mihomo 日志 **0 个
connection reset**，`curl -x` 出口为 `104.28.152.127`（美国，CF worker 出口）。

## 三、会话懒分配（1b1f460）

### 背景

用户反馈：创建渠道时「分配 50 个 IP 直接现场分配」，导致创建阻塞卡顿。
正确做法：**先不分配，等客户端请求来了再分配**。

### 机制

- 新增渠道级字段 `preallocate`（默认 **false**）：
  - `false`（默认）：创建/更新渠道时**只建会话凭据**（订阅仍发布所有逻辑节点，
    `allocated: false`），**首个客户端请求**（`EnsureClientSession` /
    `/rot/<token>/sessions/<id>/next`）才触发 `replaceClientSessionAllocation`
    分配出口 IP；
  - `true`：保留旧的立即预分配行为（配合 `idle_release_seconds=0` 永驻 /
    `>0` 用后释放）。
- **cf-worker 特例**：懒分配时创建渠道仍做**一次订阅可达性校验**
  （`verifyProviderReachableLocked`，只 fetch 1 个节点不分配），让死订阅在
  创建时就暴露，而不是等首个请求。
- migration v29：`ALTER TABLE residential_channels ADD COLUMN preallocate ...`。

### API / UI

- `POST /api/v1/residential/channels` 请求体新增 `preallocate?: boolean`；
  `UpdateChannelRequest` 新增 `preallocate?: boolean`；`Channel` 视图新增
  `preallocate`。
- 渠道表单新增复选框「创建时立即预分配全部出口 IP」，默认关闭（懒分配）。
- 会话视图 `allocated` 字段区分是否已分配；未分配节点仍出现在订阅里
  （节点名/凭据稳定，客户端可随时连接）。

### 行为对比

| 场景 | 旧行为（preallocate 隐式） | 新行为（默认懒分配） |
| --- | --- | --- |
| 创建 session_count=50 的渠道 | 立即向 provider 分配 50 个 IP，可能阻塞/超限 | 秒建，0 分配 |
| 首个客户端请求 s01 | — | 触发分配，返回 `allocated_at` |
| 订阅发布 | 节点带真实 IP | 节点先发布（allocated=false），请求后补 IP |
| cf-worker 订阅挂了 | 创建时报错 | 创建时报错（保留可达性校验） |

## 四、端到端验证记录（临时实例实测）

```text
1. POST /api/v1/residential/providers
   { vendor: "bpb-panel", protocol: "vless",
     worker_url: "https://falcon63.../dd0d75a47fda/sub?app=xray",
     rotation_mode: "cf-worker" }
   → worker_url_configured: true

2. POST /api/v1/residential/channels
   { mode: "sticky", session_count: 3, preallocate: false }
   → 创建成功，5 个 session 全部 allocated: false（零预分配）

3. POST /rot/<token>/sessions/s01/next
   → allocated_at 出现，mihomo 编译出 CF 节点（network: ws + 早数据）

4. 数据面验证
   → active.yaml 无 interface-name；mihomo 日志 0 reset；
     独立 HTTP 代理出口 104.28.152.127（美国，CF worker）
```

## 五、相关文档

- `docs/CLOUDFLARE.md`：公网拓扑（橙云 + 雷池）与协议边界
- `docs/V1_CORE.md`：v1 核心链路（数据面固定 Mihomo 的约束）
- `docs/RESIDENTIAL_SESSION_API.md`：`/rot` `/ctl` 消费者 API
- 上游 CF Worker 部署/混淆工具链：**HX-CF-Tunnel** 项目
  （`/home/hx/Loli/code/AI-Code/HX-CF-Tunnel`，私有仓库）
