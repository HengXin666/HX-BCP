# Agent Note: hxcp 加标准代理入口, 所有浏览器共用一份缓存

Status: implemented

## Problem

hxcp 只有 CDP 档 (Fetch 拦截), 只有 Chromium 系能接; Firefox / WebKit / Safari / curl / 外部 Agent 都用不上.
缓存目录又是按环境分开的 (`data_dir/hxcp/<env_id>`), 两个浏览器访问同一个 CDN 也各拉一遍.
维护者实测浏览器自带缓存约 80% 可缓存内容不会命中, 这部分流量应该由代理兜住.

## Decision

1. **加 `hxcp proxy`**: 基于 mitmproxy 的标准 HTTP / SOCKS5 代理. 任何能配代理的客户端都能接.
   - `--client name=port[,upstream]`: 每个浏览器一个入口端口, 各自的上游出口代理, flow 按 client 打标.
     上游认证按入口注入 (`http_connect_upstream` / `requestheaders`), 不用全局 `upstream_auth`.
   - `connection_strategy=lazy`: 命中缓存时不建上游连接.
2. **HTTPS 默认隧道**: `tls_clienthello` 里对未列入解密的 host 设 `ignore_connection`, 浏览器原生握手逐字节透传,
   指纹不变. 只有 `proxy-rules.json` 里显式列出的 host 后缀 (或 `decrypt_all`) 才解密缓存.
   `never_decrypt` 优先级最高. 规则文件热加载 (mtime, 1 秒节流), 控制台 / CLI / MCP 改的是同一个文件.
3. **共享存储**: 浏览器包挂 CDP 档时传 `--store-dir data_dir/hxcp/_shared --client <env_id>`,
   per-env 目录只留日志 (且仍出现在命令行里, `_is_ours` 判据不变). Store 开 WAL + busy_timeout,
   FlowLog 改为 O_APPEND 单次 write, 多进程写同一天的文件不串行.
4. **运行态多实例**: 每个引擎 / 代理写 `runtime/<instance>.json`, `runtime.aggregate()` 汇总运行中的计数,
   `/stats` 的 engine 段保持扁平 (消费方契约不变). 旧的 `runtime.json` 仍写.
5. **共享缓存的正确性**: `Vary` 含 cookie / authorization / user-agent / sec-ch-ua* / `*` 不入库;
   带 Range 或 Authorization 的请求不读不写缓存.

6. **控制台改用 @hx/ui**: 源码 `apps/hxcp-web` (React + TS, 与 `apps/console-web` 同一套组件与令牌), 构建产物写进
   `hxcp/web` 由 hxcp.api 托管, CI 加 typecheck + build. 不用 CodeBlock: 它带 shiki 全部语法, 产物多 10 MB.

## Alternatives considered

- **自己写 asyncio MITM + uTLS 伪装上游握手** (最强理由: 不引入 mitmproxy). 否决: 本仓 2026-09-18 A/B 已证
  curl_cffi 7 个 impersonate 都少 GREASE 扩展, 伪装层本身就是泄漏; 而 mitmproxy 已覆盖 h2 / websocket / socks /
  upstream, 自写的协议面只会更差.
- **全部解密 + 尽量模仿浏览器握手**. 否决: 结构性做不到与真身相同, 默认就会改指纹. 只给按 host 显式开启.
- **每个浏览器一个代理进程**. 否决: 共享缓存要么跨进程同一个库 (可以做, CDP 档就是这样), 要么一个进程多入口;
  代理档选后者, 进程少且 hosts 统计集中.
- **控制台继续原生 JS** (最强理由: hxcp 是 uv 项目, 不需要 node). 否决: 维护者要求与主控制台同一套组件;
  运行时仍不需要 node, 只托管产物.
- **PAC 里按 host 分流 (解密的走代理, 其余 DIRECT)**. 否决: 浏览器出口就不再统一经过上游, 与出口代理冲突.

## Consequences

- 买到: 任意浏览器 / 程序可接; 多浏览器同站点资源复用; 抓包带 client / TLS 档 / 耗时, 可导出 HAR.
- 代价: 解密的 host 上游看到的是 mitmproxy (OpenSSL) 的握手, 控制台 主机与 TLS 页明说. 隧道 host 只记连接,
  看不到路径与字节数. 代理档依赖 `uv sync --extra proxy` (mitmproxy).
- 未做: 浏览器包启动非 Chromium 内核时自动把 `--proxy-server` 指向 hxcp 的入口并把原上游挪到 `--client`
  (现在要手动配置); 隧道字节计量; 响应体进取证池.
