# 节点检测、站点别名与实时总览

本文记录节点检测、全局运行配置、站点别名、代理组独立路由和实时总览的当前实现边界。HX-Pxy 仍是控制面；所有代理转发、规则匹配和连接计数由 Mihomo 数据面完成。

## 1. 节点检测

### 1.1 交互语义

- 批量检测开始时，节点页先清空本轮全部检测结果。
- 后端使用有界 worker pool，默认并发数由全局配置控制，最大为 16。
- `POST /api/v1/nodes?stream=1` 返回 NDJSON；每完成一个节点立即返回一次进度。
- 前端逐个填充延迟、健康站点结果和错误状态，并按当前排序选项重新排序。
- 单击节点行只检测该节点；离开页面或开始新一轮批量检测会取消旧请求。
- 节点页的数值是经指定代理执行 HTTP 探测得到的 URL 延迟，单位为毫秒，不代表下载吞吐量，也不是 ICMP ping。
- 默认口径与 Clash Verge Rev 一致：`http://cp.cloudflare.com/generate_204`、10 秒超时。旧版未自定义的 Google 204/8 秒默认值会自动迁移。

复测响应中的 `sources` 和 `health_checks` 始终按空数组处理，避免空值导致页面崩溃。流量统计接口不可用时，节点列表仍可独立加载。

### 1.2 健康站点

全局配置页提供 ChatGPT、Claude、GitHub、Google、Telegram 默认目标，也允许新增自定义 HTTP/HTTPS URL。页面显示目标站点 favicon，并可手工对全部已启用站点执行节点 URL Test，汇总每个站点的成功节点数。附加站点探测只记录站点健康度，不改变节点的主生命周期结果。

为降低 SSRF 风险，配置拒绝指向字面量私网、环回、链路本地和其他特殊用途 IP 的探测地址。域名解析、重定向目标变化和 DNS Rebinding 仍需在后续版本中加入逐跳解析校验。

## 2. 全局配置

`GET /api/v1/settings` 和 `PUT /api/v1/settings` 管理以下配置：

- 节点检测 URL、周期、超时、批量大小、并发数和健康站点。
- Mihomo DNS 开关、IPv6、增强模式、Bootstrap DNS、主 DNS 和 Fallback DNS。
- TCP 并发建连、统一延迟、TCP Keepalive、进程识别模式和日志级别。

配置保存在 SQLite Desired State 中。更新时先校验完整候选配置，再调用 Mihomo Apply；应用失败会恢复旧设置并重新应用上一版。编译器使用结构化对象生成 YAML，不通过字符串拼接配置。

## 3. 站点别名与代理组路由

`GET /api/v1/routing-rules` 和 `PUT /api/v1/routing-rules` 管理可复用的全局站点别名。别名具有稳定 ID、优先级、启用状态和网页匹配项。

支持的规则类型：

- `domain`、`domain_suffix`、`domain_keyword`
- `ip_cidr`、`geoip`、`geosite`
- `process_name`、`network`、`dst_port`

动作不在全局页面配置。每个代理组的“路由策略”标签页独立决定是否引用某个站点别名，并选择 `REJECT`、`DIRECT` 或指定 Proxy Group。同一个别名可以在不同代理组使用不同动作。编译器根据该组 Listener 生成 Mihomo `IN-NAME` 条件；旧版全局动作配置仍可读取，并在首次从代理组保存时转换为逐组绑定。

保存流程会校验规则值、动作引用和代理组引用，生成稳定优先级顺序的完整候选配置，并在 Mihomo Apply 失败时回滚。最终兜底规则固定为 `MATCH,DIRECT`。

### 3.1 代理组成员保留（Retain）

**订阅来源**且带 `limit` 的代理组可设置 `source_spec.retain = {margin, min_dwell_seconds}`（住宅渠道组与无订阅来源的组会被拒绝），在 Sort 与 Limit 之间加入 Retain 阶段：上一次成功应用的成员只要仍满足全部筛选条件且未被隔离 / 停用 / 退役，就优先保留；新节点必须比最弱的可替换成员好出 `margin`（延迟排序单位为毫秒；自定义规则按评分排序时为分值；名称排序不替换），且该成员已在组内满 `min_dwell_seconds`，才会替换它。分桶上限在 Retain 内执行。成员记录在 `group_members`（按节点指纹），只在 Mihomo 接受新配置后写入，写入失败时保留在内存中继续作为下次编译的基线。成员保留减少的是由成员替换引起的配置变化与重载；节点配置本身变化仍会改变编译结果。

### 3.2 代理组类型

`GET /api/v1/proxy-groups` 的每个组都带 `type`：`residential`（由住宅渠道拥有，出口经 `/ctl/` 租约轮换）、`subscription`（从订阅节点中选择）、`custom`（显式节点、组合、DIRECT）。`?type=residential` 或逗号分隔的多个值可筛选，未知值返回 422。

## 4. 实时总览

总览页组合 Listener、Proxy Group、规则集和流量汇总 API，展示当前入口到代理组、策略、有效规则集以及入口累计/今日流量的关系。`GET /api/v1/overview/stream` 提供只读 SSE：

```text
event: history
data: [{"timestamp":"...","upload_bytes_per_second":0,"download_bytes_per_second":0,"active_connections":0,"running":true,"resources":[]}]

event: sample
data: {"timestamp":"...","upload_bytes_per_second":0,"download_bytes_per_second":0,"active_connections":0,"running":true,"resources":[]}
```

流量采集器由控制面后台任务常驻运行，与浏览器是否打开总览无关：每秒读取一次 Mihomo Unix Controller 的 `/connections`，在内存中计算非负字节差分，并通过有界历史缓冲和实时订阅提供给 SSE。计数器重置不会产生负速率；没有流量时仍发送零值样本，并保留当前活动入口的零速率资源指标。请求断开后订阅被取消，没有按节点或代理组创建永久 goroutine。`history` 事件用于页面首次连接时补齐最近采样，之后只发送 `sample`。

前端最多保留 120 个样本，可切换 30、60、120 秒滑动窗口。该实时曲线用于操作观察，不替代 SQLite 中的长期计费或审计统计；连接在两个采样点之间建立并结束时，尾部字节可能无法出现在实时曲线中。

图表显示上传/下载的传输量刻度、时间刻度和悬浮采样指示。路由拓扑中的每个入口同时显示当前速率、活动连接、历史累计流量、今日流量以及最近 120 秒的入口趋势图。今日统计按浏览器所在本地时区的当天起点请求，数据库仍以 UTC 时间保存。

界面验证截图：

- [桌面总览](screenshots/global-overview-desktop.png)
- [移动总览](screenshots/global-overview-mobile.png)
