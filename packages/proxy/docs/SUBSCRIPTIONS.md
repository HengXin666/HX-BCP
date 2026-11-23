# 订阅管理与刷新

## 1. 当前实现范围

当前版本已完成订阅控制面的第一条纵向链路：

```text
创建订阅
 -> 加密保存来源配置
 -> 手动或定时加载来源
 -> 内容哈希与协议解析
 -> 节点规范化、稳定指纹去重与凭据加密
 -> 原子保存不可变原始快照
 -> 事务提交节点库存和活动快照关系
 -> 更新最近成功快照与下一次刷新时间
```

刷新成功后节点进入库存；只有节点被代理组和 Listener 引用并且完整候选配置通过当前 Mihomo 校验与应用后，才代表本机已开放对应代理端口。

## 2. 来源类型

### Remote

```json
{
  "name": "airport-a",
  "source_type": "remote",
  "source_config": {
    "url": "https://example.invalid/subscription",
    "headers": {
      "X-Example": "value"
    },
    "user_agent": "HX-Pxy/1",
    "timeout_seconds": 30,
    "allow_private": false
  },
  "refresh_interval_seconds": 3600
}
```

支持：

- HTTP / HTTPS。
- 自定义 Header、User-Agent 和超时。
- `ETag`、`If-None-Match`、`Last-Modified` 与 `If-Modified-Since`。
- 最多 5 次重定向。
- 最大 16 MiB 响应体。
- 默认不读取系统代理环境变量，避免控制面的订阅请求意外绕行。

### Inline

用于手工粘贴 URI 列表、Base64 文本或 Clash / Mihomo YAML。当前单条内联内容限制为 4 MiB。

**它同时是「HTTP/SOCKS 代理列表池」的入口**：一行一个端点，不需要任何容器头，因此粘贴的是
代理本身而不是订阅文档。两种行形态都接受：

```text
1.2.3.4:8080                     # 裸端点，无协议，按 HTTP 导入
1.2.3.4:8080:user:pass           # 带鉴权的裸端点（主机须为 IP 字面量）
http://user:pass@1.2.3.4:8080    # 带 scheme，按 scheme 决定协议
socks5://1.2.3.4:1080
socks5://1.2.3.4:4145#住宅-风险81% | AS22773   # 导出标注追加在 # 片段里
OK|http|http://user:pass@1.2.3.4:3129|1.2.3.4  # 检测器的多列输出
```

两条规则值得记住：

- **裸端点默认 HTTP**。「host:port」在 curl / HttpClient 语境里就是 HTTP 代理；按端口号猜
  SOCKS 会错得离谱，所以协议只能由 scheme 指定。要让整池走 SOCKS，每行都带 `socks5://`。
- **裸端点行占比不足一半的文档不被当作代理列表**，仍按「subscription format is not supported」
  报错 —— 免得一段散文被逐行报成失败。

兼容细节：同一份文档里带 scheme 的行与裸端点行各按自己的协议导入；带 scheme 的文档仍走原有的
`uri-list` 路径，只有**整份文档都是裸端点**时才报 `proxy-list` 格式。

代理列表池**没有独立实体**：它就是一条 Inline 订阅，因此去重、检测、Proxy Group、Listener、
`/sub/`、`/nodes/` 全部沿用，不存在第二套节点模型。决策与否定项见
[.agents/notes/implemented/feature/2026-09-26-proxy-list-pool-subscription-source.md](../.agents/notes/implemented/feature/2026-09-26-proxy-list-pool-subscription-source.md)。

### File

读取服务器上的绝对路径文件。文件必须是普通文件，不能是符号链接，最大 16 MiB。

## 3. SSRF 边界

Remote 来源默认拒绝解析或连接到：

- 环回地址。
- RFC1918 / 私网地址。
- 链路本地地址。
- 组播地址。
- 未指定地址。

如果订阅确实部署在本机或内网，管理员必须显式设置：

```json
{
  "allow_private": true
}
```

该选项是高级信任边界，不是普通默认值。重定向后的地址仍会重新经过 URL 与 IP 检查。

## 4. 敏感信息存储

订阅 URL、Header、内联内容和文件来源配置不会以明文写入 SQLite：

- 首次启动生成 32 字节本机主密钥。
- 主密钥文件权限为 `0600`。
- 来源配置使用 AES-256-GCM 加密。
- 来源密文绑定 `subscription:<id>` 作为 Associated Data，不能在订阅之间替换复用。
- 解析后的节点规范配置同样使用 AES-256-GCM 加密，并绑定 `node:<fingerprint>`。
- API 只返回来源是否已配置以及节点元数据，不回显订阅来源、节点密码、UUID 或完整规范配置。
- 主密钥不会进入普通非敏感 Backup。

生产默认路径：

```text
/var/lib/hx-pxy/master.key
```

主密钥丢失后，数据库中的来源配置无法恢复。因此完整跨服务器灾难恢复必须等待加密 Backup Wrapper 完成。

## 5. API

```text
POST   /api/v1/subscriptions
GET    /api/v1/subscriptions?limit=100&offset=0
GET    /api/v1/subscriptions/{id}
PUT    /api/v1/subscriptions/{id}
DELETE /api/v1/subscriptions/{id}?version={version}
POST   /api/v1/subscriptions/{id}/refresh
GET    /api/v1/nodes?search=&protocol=&state=&limit=&offset=
GET    /api/v1/nodes/{id}
```

更新时 `source_config` 可省略；此时服务端保留已加密的原来源，只修改名称、启用状态和刷新计划。
只有显式提交新的 `source_config` 才会替换来源并重新加密。由于来源明文永不回显，管理面默认采用该
保留语义，并要求用户主动勾选“替换加密来源”后才能修改 URL、Header、文件路径或内联内容。

管理面支持把浏览器本机的 `.yaml` / `.yml` Clash 文件读取为内联订阅；文件内容仍受 4 MiB 上限约束，
保存后需要执行刷新，且只有至少解析出一个有效节点时才会切换活动快照。

更新和删除使用乐观锁版本。客户端提交陈旧版本时返回：

```text
HTTP 409 subscription_conflict
```

## 6. 快照语义

每个成功拉取的不同内容保存为不可变快照：

```text
/var/lib/hx-pxy/snapshots/<subscription-id>/<snapshot-id>.source
```

保证：

- 内容使用 SHA-256 去重。
- 相同内容不会重复创建文件和数据库记录。
- 文件通过临时文件、`fsync` 和原子重命名发布。
- 快照文件权限为 `0600`，目录权限为 `0700`。
- 新快照提交成功后才替换 `last_success_snapshot_id`。
- 下载、解析初检或数据库提交失败时，最近成功快照保持不变。

当前解析器支持：

- Clash / Mihomo YAML 中的 `proxies` 列表。
- Mihomo Provider `payload`，以及配置中的内联 `proxy-providers.payload/proxies`。
- 展开 HTTP Provider，并复用父订阅的 SSRF、DNS、重定向、Header、User-Agent、超时和大小限制。
- 展开 File 订阅中的相对或绝对 File Provider，并继续拒绝符号链接和非普通文件。
- sing-box JSON 中的 `outbounds` 列表。
- VLESS、VMess、Trojan、Shadowsocks、HTTP / HTTPS、SOCKS / SOCKS5、Hysteria、Hysteria2 / Hy2、TUIC、AnyTLS 和 SSH 分享 URI。
- Base64 包裹的 URI 列表。
- 单节点解析失败结构化保存，不会静默丢弃。
- 按规范配置生成稳定 SHA-256 指纹，显示名称变化不会制造重复节点。
- 新快照至少包含一个有效节点才会激活；解析失败时继续保留最近成功快照。
- 相同内容和 HTTP 304 路径会重建节点关系，兼容解析器上线前保存的旧原始快照。

Provider 展开最多 3 层、32 个 Provider、累计 32 MiB。尚未覆盖全部机场私有变体和所有插件协议，准确边界见 [`SUBSCRIPTION_COMPATIBILITY.md`](SUBSCRIPTION_COMPATIBILITY.md)。最终协议可用性仍由关于页显示的当前 Mihomo 构建和候选配置校验决定。

## 7. 自动刷新调度

自动调度采用：

- SQLite 持久化 `next_refresh_at`。
- 数据库租约领取到期订阅。
- 固定大小 worker pool，默认 4 个 worker。
- 每 30 秒扫描一次，默认批量领取 100 条。
- 默认租约 5 分钟。
- 同一订阅在服务层再次串行化，避免手工刷新与定时刷新并发覆盖。
- 不为每个订阅创建永久 ticker 或 goroutine。

成功刷新后：

```text
next_refresh_at = now + refresh_interval
```

失败后使用指数退避：

```text
30s, 60s, 120s, 240s, ...
```

退避最多达到订阅刷新周期或 30 分钟中的较小值。服务器在任务执行中崩溃时，租约到期后任务会再次被领取。

## 8. 代理服务与住宅渠道订阅

来源订阅负责把机场节点导入库存；客户端订阅负责发布本机可以提供的入口，两者方向相反。
客户端订阅按代理服务或住宅渠道隔离，不存在跨 Listener、跨渠道的全局聚合订阅。普通 Listener
通过自己的管理接口轮换 share token；住宅渠道使用：

```text
GET  /api/v1/residential/channels/<channel-id>
POST /api/v1/residential/channels/<channel-id>/rotate-share
GET  /sub/<token>?format=clash|v2rayn|sing-box|uri
```

住宅渠道导出只包含本渠道声明节点，并排除渠道内部的 provisioning 凭据。普通 Listener 与住宅
节点共用同一渲染器；Clash/Mihomo、v2rayN、sing-box 和明文 URI 格式看到所属服务的同一节点
集合和稳定顺序。新 token 只有在完整候选订阅构建成功后才写入数据库，构建失败时旧链接继续有效。

住宅节点的显示名称和认证信息不会随出口 IP 轮换变化。调用 `/ctl/<token>/nodes/<index>/next`
后，客户端无需重新拉订阅。订阅和控制 URL 均在「代理服务」页面按渠道复制；`/sub/` token 可
读取客户端代理凭据，响应不缓存，日志隐藏完整路径。

## 9. 当前扩展项

- [x] 展开 Mihomo HTTP / File / Inline Provider，并建立协议兼容矩阵。
- [x] 支持批量手工刷新 API。
- [x] 支持 UTC 标准 5 字段 Cron 表达式。
- [ ] 请求代理配置；当前订阅 Fetcher 明确直连。
- [x] 失败退避加入有界随机抖动。
- [ ] 管理面刷新历史和实时 SSE 进度。
