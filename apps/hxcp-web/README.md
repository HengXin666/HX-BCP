# hxcp 控制台

> 路径: `apps/hxcp-web/` · 类型: 应用

## 这是什么

缓存代理 hxcp 的控制台, React + TypeScript + @hx/ui, 与 `apps/console-web` 同一套组件, 设计令牌与字号刻度.
五个页签: 总览 / 抓包 / 缓存 / 主机与 TLS / 接入. 设计见 `.agents/notes/implemented/feature/2026-11-22-hxcp-universal-proxy.md`.

## 入口

- `src/main.tsx` -> `src/App.tsx` (顶栏 + NavTabs + 当前页, hash 路由 `#/flows?host=...`)
- `src/shell/Topbar.tsx`: 运行状态点, 缓存规模, 累计已省, 主题切换
- `src/pages/OverviewPage.tsx`: StatCard, 柱图 (最省流量的站点), 环图 (判定分布), 客户端表
- `src/pages/FlowsPage.tsx` + `FlowDetail.tsx` + `Spark.tsx` + `flowView.ts`: 抓包表, 详情, 时序密度条, 复制为 curl / fetch
- `src/pages/CachePage.tsx`: 缓存条目表 + 抽屉预览
- `src/pages/HostsPage.tsx`: 每个 host 的 TLS 档位切换
- `src/pages/AccessPage.tsx`: 入口, 配置片段, CA, Agent
- `src/pages/Snippet.tsx`: 等宽片段 + 复制. 不用 @hx/ui 的 CodeBlock, 它带 shiki 全部语法, 产物会多 10 MB
- `src/lib/api.ts`: 接口与类型, 全部相对路径; 401 时问一次 Bearer 令牌, 只存本标签页
- `src/lib/hooks.ts`: 轮询; `src/lib/route.ts`: hash 路由; `src/lib/format.ts`: 字节 / 时间 / 复制

## 页面与后端契约

只读写 `hxcp.api` (默认 8898) 的接口, 无改写:

| 页面 | 请求 |
|---|---|
| 顶栏 / 总览 | GET stats, hosts, clients |
| 抓包 | GET flows?host=&status=&q=&client=&kind=&via=&days=&since=&limit=&offset=, flow/body, flows.har |
| 缓存 | GET entries?q=&order=, body?url=; DELETE entry?url=; POST purge |
| 主机与 TLS | GET hosts, rules; PUT rules; POST rules/host {host, mode} |
| 接入 | GET access, ca.crt, proxy.pac |

## 开发与构建

```bash
pnpm install
pnpm dev        # http://127.0.0.1:8941, API 代理到 8898 (HXCP_API 可改)
pnpm typecheck
pnpm build      # 写进 packages/cache-proxy/src/hxcp/web/ 并先清空
```

改了源码就要重新 build 并提交产物: hxcp.api 只托管产物, 运行时不需要 node.
