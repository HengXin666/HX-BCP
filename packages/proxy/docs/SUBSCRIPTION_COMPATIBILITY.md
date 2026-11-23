# 订阅与 Provider 兼容矩阵

## 1. 能力定义

HX-Pxy 只解析订阅并生成 Mihomo 候选配置，不实现代理协议。下表中的“解析支持”表示控制面可规范化该格式；最终可用性仍由关于页显示的当前 Mihomo 版本和 `mihomo -t` 校验决定。

## 2. 容器格式

| 输入 | 状态 | 说明 |
| --- | --- | --- |
| Clash / Mihomo YAML `proxies` | 支持 | 保留 Mihomo 原生节点字段，执行基本类型、地址和端口校验 |
| Mihomo Provider YAML `payload` | 支持 | 可作为订阅顶层内容或 Provider 展开结果 |
| `proxy-providers.<name>.payload` / `proxies` | 支持 | 在当前文档内直接展开 |
| `proxy-providers` HTTP Provider | 支持 | 继承订阅 SSRF、DNS、重定向、超时和体积限制；合并 Header，Provider User-Agent 优先 |
| `proxy-providers` File Provider | 支持 | 仅允许 File 订阅引用；相对路径以父文件目录解析，继续拒绝符号链接和非普通文件 |
| sing-box JSON `outbounds` | 部分支持 | 支持下表列出的常用代理出站；`direct`、`block` 等非代理出站记录为结构化失败 |
| URI 列表 | 支持 | 一行一个分享 URI，无法识别的协议记录结构化失败 |
| 带导出标注的 URI 列表 | 支持 | 每行仍是分享 URI，但尾部带导出标注（`#名称 | AS22773 ...`、`OK|http|URL|出口IP`）。标注让 `url.Parse` 整体失败，因此先按原样解析，仅对失败的行按 `|` 分列并剥离 `#` 片段重试；能独立解析的行不受影响（节点名保留） |
| 裸端点代理列表 | 支持 | 一行一个 `host:port` 或 `host:port:user:password`（主机须为 IP 字面量）；无 scheme 时按 HTTP 导入。**裸端点行占比需 ≥ 一半**才按代理列表识别，否则报不支持 |
| Base64 包装 URI 列表 | 支持 | 支持标准和 URL-safe、有/无 padding；编码嵌套最多 2 层 |

Provider 展开上限为 3 层、32 个 Provider、累计 32 MiB。单个 Provider 加载仍受其 Remote/File 来源的体积上限约束。达到上限或某个 Provider 失败时保留已成功节点并记录失败，不输出完整 URL 或凭据。

## 3. Mihomo YAML 出站类型

| 类型 | YAML | sing-box JSON 映射 | 分享 URI |
| --- | :---: | :---: | :---: |
| AnyTLS | 是 | 是 | `anytls://` |
| Hysteria | 是 | 是 | `hysteria://` |
| Hysteria2 | 是 | 是 | `hysteria2://`、`hy2://` |
| HTTP / HTTPS | 是 | HTTP | `http://`、`https://` |
| Mieru | 是 | 是 | 否 |
| ShadowTLS | 是 | 否 | 否 |
| Snell | 是 | 否 | 否 |
| SOCKS5 | 是 | 是 | `socks://`、`socks5://` |
| Shadowsocks | 是（`ss`） | 是 | `ss://` |
| SSH | 是 | 是 | `ssh://` |
| ShadowsocksR | 是（`ssr`） | 否 | 否 |
| Trojan | 是 | 是 | `trojan://` |
| TUIC | 是 | 是 | `tuic://` |
| VLESS | 是 | 是 | `vless://` |
| VMess | 是 | 是 | `vmess://` Base64 JSON |
| WireGuard | 是 | 是 | 否 |

矩阵不表示所有第三方客户端私有参数都已转换。Mihomo YAML 的未知原生字段会保留到候选配置；分享 URI 和 sing-box JSON 只转换当前显式覆盖的字段。插件协议、证书/私钥组合和机场私有变体仍需用脱敏样本增加回归测试。

## 4. 安全与失败语义

- Remote Provider 每次重定向和连接目标都重新执行 SSRF 检查；父订阅未显式 `allow_private` 时不能访问私网、环回、链路本地或云元数据目标。
- File Provider 不能从 Inline 或 Remote 订阅越权读取服务器文件。
- Provider URL、认证 Header、节点密码和完整规范配置不进入 API 响应或普通日志。
- 某次刷新没有任何有效节点时不切换活动快照；下载、解析或展开失败不会清空上一版节点。
- 节点类型最终由当前安装的 Mihomo 构建校验。项目不承诺未知协议或未来 Mihomo 版本的自动兼容。

协议与 Provider 矩阵由 `internal/nodeparse/parser_test.go`、`internal/subscription/parsed_refresh_test.go`、
`internal/nodeparse/proxylist_test.go` 与 `internal/subscription/proxylist_test.go` 持续回归。

### 代理列表池（HTTP/SOCKS 列表）

一份「很多 HTTP / SOCKS 代理」的清单**不是新的来源类型**：它就是一条 Inline 订阅。
粘贴到 `source_config.inline`，或让 `POST /api/v1/quickstart` 用 `subscription_inline` 一步建好。
这样去重、检测、Proxy Group、Listener 与 `/sub/`、`/nodes/` 导出全部自动复用。
