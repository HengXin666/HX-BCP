# 住宅代理并发集成标准（Unified Window Standard）

> 版本：v1 · 状态：已实现（schema v32）
> 本文是外部服务（OutlookRegister、爬虫集群、多租户自动化等）对接 HX-Pxy
> 住宅代理的**唯一并发契约**。协议与流量转发属于 Mihomo；本文件只定义控制面提供的
> 并发保证、统一窗口模型和自动化 API 标准。

## 1. 要解决的问题

旧模型下多个服务同时对接同一住宅渠道时，会出现四类并发问题：

| 问题 | 表现 | 后果 |
| --- | --- | --- |
| 窗口撞车 | 两个服务同时选中同一个声明节点 index | 同一住宅 IP 被多个服务共享，互相污染目标站会话 |
| 重复轮换 | 两个服务对同一节点先后调用 `next` | 同一 IP 窗口被轮换多次，消耗供应商配额且出口漂移 |
| 过期操作 | 某服务基于旧状态轮换，不知窗口已被别人换过 | 服务拿到的是"未知 IP"，无法复现请求 |
| 无法协调 | 客户端互斥只在进程内，跨进程/跨服务无效 | 无服务器端所有权，标准无法落地 |

本标准引入两个服务器端原语解决上述问题：

1. **节点租约（Lease）**：每个声明节点是一个**独占 IP 窗口**，同一时刻只能被一个
   持有者使用。持有者通过 `claim` 获得租约、`heartbeat` 续租、`release` 归还。
2. **分配版本（alloc_version）**：每次轮换/路由切换单调递增。持有者用
   `expected_alloc_version` 做 **CAS（Compare-And-Swap）**，版本过期即拒绝，
   杜绝"对同一窗口的重复轮换"。

## 2. 统一窗口模型

```text
渠道 (sticky, session_count=N)
  └── 声明节点 s01 ── 窗口 1  ── 租约 holder=A
  └── 声明节点 s02 ── 窗口 2  ── 租约 holder=B
  └── 声明节点 s03 ── 窗口 3  ── 空闲
  ...
  └── 声明节点 sNN ── 窗口 N
```

**核心规则：一个声明节点 = 一个互斥的 IP 窗口。**

- 窗口数量 = 渠道 `session_count`（1..64，同时受供应商并发上限约束）。
- 每个服务应**独占一个或多个窗口**，绝不同服务共享同一窗口。
- 服务只轮换**自己持有租约**的窗口；被他人租约占用的窗口操作一律返回 409。
- 客户端看到的节点名称（`residential-us-01`）和凭据在窗口生命周期内稳定，
  轮换只改变窗口背后的住宅出口，不改变节点身份。
- 供应商会话、网关节点和出口 IP 对客户端不可见；TTL、空闲释放和 `next`
  只改变服务器内部映射。

### 2.1 服务器保证（服务端强制）

| 保证 | 实现 | 违反时的响应 |
| --- | --- | --- |
| 同一窗口同时只被一个持有者使用 | 租约 + 全局互斥临界区 | `409 lease_held` |
| 窗口轮换是原子的 | `clientSessionMutex` 内检查-执行 | 无竞态窗口 |
| 基于过期状态的轮换被拒绝 | `alloc_version` CAS | `409 alloc_version_changed` |
| 轮换频率受限 | 每窗口 2 秒最小间隔 | `429 rotate_rate_limited` |
| 崩溃的持有者不永久占窗 | 租约 TTL + 过期可被他人认领 | 认领即成功 |
| 空闲释放不误伤在用窗口 | 有活跃租约的节点跳过空闲释放 | 无 |

### 2.2 客户端纪律（文档约定）

服务器保证之外，集成方还必须遵守：

1. **先认领，后使用**：任何轮换/路由操作前先 `claim` 自己的窗口。
2. **窗口固定**：同一服务在运行期内复用同一批窗口 index，不要每次重新挑选。
3. **定期心跳**：在租约过期前 `heartbeat`（建议 TTL 的 1/3 间隔）。
4. **用完归还**：服务退出时 `release`，让窗口可被其他服务复用。
5. **版本回传**：轮换时携带上次读取的 `alloc_version`；收到
   `409 alloc_version_changed` 时重新 `GET /ctl/.../nodes` 再重试。
6. **容量规划**：`session_count ≥ 并发服务数`；供应商并发上限
   `max_concurrent_sessions ≥ session_count`。

## 3. 自动化接口契约

所有接口位于 `/ctl/<control-token>/`，control token 是高权限凭据（可轮换、
消耗供应商配额、读取临时节点鉴权），按密码管理、独立轮换、不进日志。

### 3.1 节点池

```http
GET /ctl/<control-token>/nodes
```

返回本渠道全部声明节点。每个节点新增两个字段：

```json
{
  "channel": "住宅美国",
  "nodes": [
    {
      "index": 1,
      "node_name": "住宅美国-01",
      "endpoints": [ { "protocol": "vless", "transport": "ws", "uri": "vless://...", "browser_compatible": false } ],
      "proxy_url": null,
      "route_mode": "residential",
      "country_code": "US",
      "alloc_version": 7,
      "lease": { "holder": "service-a", "expires_at": "2026-08-23T10:05:00Z" }
    }
  ]
}
```

- `alloc_version`：本窗口的分配版本。读取后作为后续 `next`/`route` 的
  `expected_alloc_version`。
- `lease`：窗口所有权状态。**列表永不返回 `lease_id`**（能力令牌只发给持有者）。
  - `lease == null`：窗口空闲，可 `claim`。
  - `lease.holder == 自己的 holder`：窗口是"我的"，可直接操作（若丢失
    lease_id，重新 `claim` 同 holder 即刷新）。
  - `lease.holder == 其他服务`：窗口被占用，等待其过期或选择其他窗口。
- `GET nodes` 会更新节点最后使用时间，空闲释放依据它计算。

### 3.2 认领租约（独占窗口）

```http
POST /ctl/<control-token>/nodes/<index>/claim
Content-Type: application/json

{ "holder": "service-a", "ttl_seconds": 300 }
```

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `holder` | 是 | 1..64 字符，仅 `[A-Za-z0-9._:-]`；服务标识（如 `outlook-batch-1`） |
| `ttl_seconds` | 否 | 60..86400，缺省 300 |

响应 200（节点视图 + 租约）：

```json
{
  "index": 1,
  "alloc_version": 7,
  "lease": {
    "holder": "service-a",
    "lease_id": "3f9c...a1",        // 能力令牌，仅此处返回，务必保存
    "expires_at": "2026-08-23T10:05:00Z"
  }
}
```

语义：

- 窗口空闲（从未被租或租约已过期）→ 立即授予。
- 同一 `holder` 再次 `claim` → 刷新租约并**更换 lease_id**（旧 id 失效）。
- 窗口被其他 holder 持有且未过期 → `409 lease_held`，响应包含占用人与到期时间。
- `lease_id` 是后续 `heartbeat`/`release`/受保护轮换的唯一凭据；丢失后以同
  holder 重新 `claim` 即可。

### 3.3 续租

```http
POST /ctl/<control-token>/nodes/<index>/heartbeat
Content-Type: application/json

{ "lease_id": "3f9c...a1", "ttl_seconds": 300 }
```

- 正确 lease_id → 延长到期时间，响应返回新的 `expires_at`。
- lease_id 与占用者不匹配 → `409 lease_held`。
- 窗口无活跃租约 → `409 lease_expired`（需重新 claim）。
- 建议在租约到期前按 TTL/3 间隔心跳；心跳是廉价 DB 操作，但不要低于 60 秒。

### 3.4 归还租约

```http
POST /ctl/<control-token>/nodes/<index>/release
Content-Type: application/json

{ "lease_id": "3f9c...a1" }
```

- **幂等**：窗口已无活跃租约，或 lease_id 与当前租约不符时也返回成功
  （不删除他人的新租约）。服务退出路径无需区分"已归还/未归还"。
- 归还后窗口可被任何服务 `claim`。

### 3.5 轮换出口（受保护）

```http
POST /ctl/<control-token>/nodes/<index>/next
Content-Type: application/json
（可选 body）

{ "lease_id": "3f9c...a1", "expected_alloc_version": 7 }
```

| body 字段 | 作用 | 缺失时行为 |
| --- | --- | --- |
| `lease_id` | 证明窗口所有权 | 窗口被他人持有 → `409 lease_held`；窗口空闲 → 照常轮换（兼容旧客户端） |
| `expected_alloc_version` | CAS 护栏 | 不做版本校验（兼容旧客户端） |

响应 200：节点视图（含新的 `alloc_version`、`residential_endpoint`——仅 API 提取
供应商在已分配后返回）。

### 3.6 路由切换（受保护）

```http
POST /ctl/<control-token>/nodes/<index>/route
Content-Type: application/json

{ "route_mode": "residential|upstream|direct", "lease_id": "...", "expected_alloc_version": 7 }
```

护栏语义与 `next` 相同。路由切换同样使 `alloc_version` +1。

## 4. 稳定错误码

客户端**只依赖 HTTP 状态码与 `error.code`**，不得解析错误消息字符串。

| 状态码 | `error.code` | 含义 | 客户端动作 |
| --- | --- | --- | --- |
| 200 | — | 成功 | 处理响应 |
| 404 | `not_found` | token 未知/禁用/越界 index | 检查凭据 |
| 409 | `lease_held` | 窗口被其他持有者占用 | 读取 `lease.holder`，等待过期或换窗口 |
| 409 | `lease_expired` | 心跳/操作引用已失效租约 | 重新 `claim` |
| 409 | `alloc_version_changed` | 窗口已被他人轮换过 | 重新 `GET nodes` 拿新版本重试 |
| 409 | `conflict` | 供应商并发上限/资源冲突 | 扩容 `session_count` 或等窗口释放 |
| 422 | `validation_failed` | holder/ttl/route_mode 非法 | 修正请求 |
| 429 | `rotate_rate_limited` | 同一窗口轮换过快（2s 最小间隔） | 退避后重试 |
| 502 | `provider_unreachable` | 供应商上游不可达 | 稍后重试 |

## 5. 参考集成流程

### 5.1 单服务标准流程

```text
启动：
  1. GET /ctl/<token>/nodes                 # 读取窗口池与 alloc_version
  2. 挑选空闲窗口：lease == null 或 lease.holder == 我
  3. POST /nodes/<i>/claim  {holder, ttl}   # 获得独占租约，保存 lease_id
运行：
  4. 定时 POST /nodes/<i>/heartbeat {lease_id}   # 每 TTL/3 续租
  5. 需要新 IP：POST /nodes/<i>/next
         {lease_id, expected_alloc_version: 上次读取的版本}
     - 409 alloc_version_changed → 回到步骤 1 重新读取再重试
     - 409 lease_held → 已被抢占（holder 冲突），重新 claim 或换窗口
  6. 从响应 endpoints[] / residential_endpoint 落地本地 Mihomo，探测出口后执行业务
退出：
  7. POST /nodes/<i>/release {lease_id}     # 归还窗口（幂等）
```

### 5.2 多服务共存示例

```text
服务 A（爬虫批量）   → claim s01, s02   → 每窗口独立 next，互不影响
服务 B（Outlook）    → claim s03        → 独立轮换
服务 C（监控）       → claim s04        → 独立轮换
```

- 各服务使用**不同 holder**，服务器保证互不抢占。
- 若 A 崩溃，其租约 TTL 到期后 B/C 可认领 s01/s02，不阻塞。
- `session_count` 至少等于服务数；瞬时峰值建议留 20% 余量。

### 5.3 curl 示例

```bash
CTL="https://proxy.example.com/ctl/<control-token>"

# 1. 读取窗口池
curl -s "$CTL/nodes" | jq '.nodes[] | {index, alloc_version, lease}'

# 2. 认领窗口 1
CLAIM=$(curl -s -X POST "$CTL/nodes/1/claim" -H 'Content-Type: application/json' \
  -d '{"holder":"batch-service","ttl_seconds":300}')
LEASE_ID=$(echo "$CLAIM" | jq -r '.lease.lease_id')
VERSION=$(echo "$CLAIM" | jq -r '.alloc_version')

# 3. 轮换（带 CAS 护栏）
curl -s -X POST "$CTL/nodes/1/next" -H 'Content-Type: application/json' \
  -d "{\"lease_id\":\"$LEASE_ID\",\"expected_alloc_version\":$VERSION}"

# 4. 心跳
curl -s -X POST "$CTL/nodes/1/heartbeat" -H 'Content-Type: application/json' \
  -d "{\"lease_id\":\"$LEASE_ID\",\"ttl_seconds\":300}"

# 5. 归还
curl -s -X POST "$CTL/nodes/1/release" -H 'Content-Type: application/json' \
  -d "{\"lease_id\":\"$LEASE_ID\"}"
```

## 6. 兼容性

- 旧客户端（不带 `lease_id` / `expected_alloc_version`）在**空闲窗口**上行为不变：
  直接 `next`/`route` 照常执行。
- 一旦某窗口被新协议 `claim`，旧客户端对该窗口的轮换会收到 `409 lease_held`，
  这正是标准要防住的"共享同一 IP 窗口"。
- `/rot/` 旧接口继续服务历史集成，但新代码必须使用 `/ctl/` + 租约模型。
- `lease_id` 与 control token 同级敏感：不得进入日志、截图或普通订阅。

## 7. 运维与容量

| 参数 | 建议 | 说明 |
| --- | --- | --- |
| `session_count` | ≥ 并发服务数 + 20% | 渠道窗口总量，1..64 |
| 供应商 `max_concurrent_sessions` | ≥ `session_count` | 供应商侧硬上限 |
| 租约 `ttl_seconds` | 300（默认） | 崩溃恢复窗口；越长越抗抖动，越短释放越快 |
| 心跳间隔 | ttl/3 | 例如 ttl=300 → 每 100s 一次 |
| `idle_release_seconds` | 按业务空闲容忍度 | 无活跃租约且空闲超时的窗口自动归还供应商 IP |

## 8. 集成验收清单

新服务接入前逐项核对：

- [ ] 使用 `/ctl/` 而非 `/rot/` 创建会话。
- [ ] 先 `claim` 再轮换；holder 全局唯一。
- [ ] `next` 总是携带 `lease_id` 与最近读取的 `expected_alloc_version`。
- [ ] 正确处理 `409 lease_held` / `409 alloc_version_changed`（重新读取再重试）。
- [ ] 定期 `heartbeat`，退出时 `release`。
- [ ] 按 `error.code` 分支，不解析错误消息。
- [ ] `session_count` 覆盖全部并发服务。
- [ ] 控制 token 与 lease_id 不进日志、不写死进公开配置。

## 9. 相关文档

- [客户端与自动化 API](RESIDENTIAL_SESSION_API.md) — 会话 API 全览
- [住宅渠道订阅与自动化契约](RESIDENTIAL_V2_CONTRACT.md) — 所有权模型与字段契约
- [住宅渠道发布与自动化进度](RESIDENTIAL_V2_PROGRESS.md) — 实现与验收状态
