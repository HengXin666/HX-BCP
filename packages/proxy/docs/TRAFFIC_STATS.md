# 流量统计

HX-Pxy 从 Mihomo External Controller 的连接快照采集统计。普通入口的协议和目标连接
由 Mihomo 处理；Cloudflare / 雷池拓扑中的 Edge Relay 只转发 WebSocket 连接，不解析协议，
因此流量统计仍以 Mihomo 入站和代理链为准。

## 统计维度

- `listener`：自定义代理服务入口。
- `proxy_group`：入口绑定的代理组及连接链中出现的嵌套组。
- `node`：订阅节点；节点退出活动快照后仍保留历史总计和 30 天趋势。
- `residential_channel`：住宅渠道；托管 WS 入口和历史兼容入口归因到稳定渠道 ID。

每条连接按 Mihomo 的稳定入站名和代理链同时归属到上述维度。累计上传、下载和连接数保存在
`traffic_totals`，直到对应 Listener、Proxy Group 或 Node 在本地被真正删除。订阅中消失的节点只会进入
`retired` 状态，不会因此丢失累计统计。

住宅供应商节点和真实出口 IP 会在 TTL 到期或 `next` 后替换，不能作为稳定的统计身份。
采集器因此同时把住宅渠道拥有的所有 Listener 映射到 `residential_channel:<channel-id>`；轮换、
空闲释放和重新分配都继续累计到同一渠道。删除渠道时才清理对应总计和趋势。

## 聚合与保留

- 每秒读取一次连接快照并在内存计算字节增量。
- 后台采集任务独立于总览页面常驻运行；总览 SSE 只订阅采集器的有界历史和实时样本，不会因为浏览器关闭而停止采集。
- 每分钟用一个 SQLite 短事务批量写入，不逐数据包、逐字节或逐连接写库。
- 近 24 小时保留 1 分钟粒度，2 到 7 天压缩为 5 分钟，8 到 30 天压缩为 1 小时。
- 趋势数据超过 30 天后清除；不受 30 天趋势保留影响的累计值会保留到本地实体删除。
- 进程退出前进行最长 5 秒的有界 Flush。

## API

累计统计列表：

```text
GET /api/v1/traffic?resource_type=node&limit=100&offset=0
GET /api/v1/traffic?resource_type=residential_channel&limit=100&offset=0
```

按时间范围汇总入口、代理组、节点或住宅渠道：

```text
GET /api/v1/traffic?resource_type=listener&from=<RFC3339>&to=<RFC3339>&limit=1000&offset=0
```

该接口返回范围内上传、下载、连接数和峰值活动连接；范围必须同时提供 `from`/`to`，最长 30 天，分页最多 1000 条（与节点列表上限一致），前端默认单次取满，避免多次往返。总览使用它读取每个入口的历史累计和当天汇总。

单个资源的趋势与累计值：

```text
GET /api/v1/traffic?resource_type=listener&resource_id=<id>&from=<RFC3339>&to=<RFC3339>&max_points=240
GET /api/v1/traffic?resource_type=residential_channel&resource_id=<channel-id>&from=<RFC3339>&to=<RFC3339>&max_points=240
```

查询范围最多 30 天，返回点数最多 500。后端根据时间范围和点数预算自动选择 1 分钟、5 分钟或
1 小时分辨率。

## 当前精度边界

Mihomo 当前公开的是连接累计字节快照，而不是带资源归属的结算事件。采集器以 1 秒频率计算活跃连接
增量，因此生命周期短于采样间隔、且两次采样之间完成的连接可能无法计入；连接在最后一次采样后到关闭
之间产生的尾部字节也可能少计。该统计适合面板趋势和使用量观察，不应作为计费账单。控制面或 Mihomo
重启期间不补造数据，也不会声称精确到每个数据包。

拨号超时、TLS 握手失败和被拒绝连接可能产生实时日志，但在进入 Mihomo 活跃连接快照前不会形成可归属
流量。因此“日志有失败记录、流量仍为零”不代表采集器停止工作；这类失败应通过实时日志和节点检测结果
诊断，流量页面只表达已建立且被采样到的连接。
