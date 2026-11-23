# 实时代理日志

实时日志通过 Mihomo External Controller 的 `/logs` WebSocket 读取，由控制面转换为只读 SSE。浏览器不直接访问 External Controller，也不会把事件写入 SQLite。

## 管理 API

认证后的管理端点：

```text
GET /api/v1/logs/stream
GET /api/v1/logs/stream?proxy_group_id=<id>
GET /api/v1/logs/stream?listener_id=<id>
GET /api/v1/logs/stream?node_id=<id>&level=info
```

`level` 可取 `debug`、`info`、`warning`、`error`，表示最低级别。`listener_id` 通过 Desired State 解析为其绑定的 Proxy Group；同时传入 `listener_id` 和 `proxy_group_id` 时，两者必须匹配。`node_id` 使用控制面的稳定节点 ID，Mihomo 内部代理名不会暴露给前端。

SSE 事件格式：

```text
event: log
data: {"timestamp":"...","level":"info","message":"...","proxy_group_id":"...","proxy_group":"Fast","node_id":"...","node":"Tokyo 01"}
```

## 资源与生命周期

- 默认最多允许 8 个并发管理日志流，超过限制返回 `429 log_stream_busy`。
- 每个流使用固定 64 条事件缓冲。慢客户端只丢弃最旧实时事件，不阻塞 Mihomo 转发或创建无界队列。
- 每个 SSE 请求最多对应一个读取任务，不按节点、Proxy Group 或 Listener 创建永久 goroutine。
- 浏览器断开或请求 Context 取消时立即关闭对应 Mihomo WebSocket 并释放并发槽位。
- 日志是瞬时观察数据；刷新页面不会恢复历史事件。长期流量趋势使用统计聚合，不依赖日志回放。

## 安全边界

控制面对 Mihomo 日志执行二次脱敏后才发送给浏览器：

- 连接源地址和目标地址替换为 `[source]` 与 `[destination]`。
- URL userinfo、Authorization、Cookie、密码、Token、API Key 和 Secret 字段替换为 `[redacted]`。
- 保留协议、Proxy Group 名和 Mihomo 节点名，供管理页面定位路由结果。
- 单条上游 WebSocket 帧限制为 32 KiB，输出消息限制为 2 KiB。

Mihomo External Controller 仍必须仅使用受控 Unix Socket 或环回地址，不能通过 Cloudflare、雷池或其他公网反向代理暴露。
