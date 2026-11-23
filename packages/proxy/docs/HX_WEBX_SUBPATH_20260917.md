# HX-Webx 子路径访问 (/proxy/hx-pxy/ 白屏) — 诊断与落地

日期: 2026-09-17 · 状态: **已落地并端到端验证** (非侵入: 不改被代理服务源码, 不改 HX-Webx 既有语义)

**入口: `http://127.0.0.1:36000/proxy/hx-pxy/` (原 URL, 现已可用)**

## 1. 现象与根因

经 HX-Webx 访问原入口白屏; 直连 Vite 正常。用户在浏览器 Network 面板看到的关键一条:

```
GET http://127.0.0.1:36000/src/main.tsx     ->  200 text/html   (内容被"重定向"成 HTML)
```

这是根因的直接证据, 不是附带现象:

| 观测 | 结果 |
| --- | --- |
| `GET 36000/proxy/hx-pxy/` | 200, 与直连根**逐字节相同**的 index.html → 转发本身没坏 |
| 该 index.html 的资源引用 | `/@vite/client` `/@react-refresh` `/src/main.tsx` `/favicon.svg` —— **全是根绝对路径, 不带访问前缀** |
| `GET 36000/src/main.tsx` | 200 `text/html` —— 落到 **HX-Webx 面板自己的 SPA 兜底**, 不是 JS 模块 |
| `GET 36000/proxy/hx-pxy/src/main.tsx` | 200 `text/javascript` → **带前缀的转发完全正常** |
| `GET 36000/proxy/hx-email/` | 同样引用根绝对路径 → **同一故障, 非本服务独有** |

机制: HX-Webx 挂载顺序为 `/proxy` → 图标 → `express.static(web/dist)` → SPA 兜底
「非 /api /proxy 一律回面板 index.html」。**代理只重写 /proxy/<name>/ 内的路径,
不重写 HTML 里的资源引用**; 浏览器按 origin 解析 `/src/main.tsx`, 打到面板自己的
静态兜底 → 拿到 HTML 当 ESM `import` → MIME 报错 → 白屏。

**注入 `<base href>` 救不了** —— RFC 3986 规定以 `/` 开头的引用直接替换 path,
base 的 path 被丢弃。必须做**响应体路径重写**。

## 2. 落地拓扑

```
浏览器 → HX-Webx :36000 /proxy/hx-pxy/*
       → 适配反代 :28340   (剥前缀转发 + 响应体根绝对路径补前缀)
       → Vite dev :28341   (原样不动)
       → Go 控制面 :19090  (原样不动)
```

适配层脚本: `/home/hx/.local/bin/hx-subpath-adapter.mjs` (Node 内置模块, 零依赖)

1. **请求**: HX-Webx 已剥掉 `/proxy/<name>` 前缀, 适配层原样透传 (直连时也兼容带前缀)。
2. **响应**: 对 `text/html` / `javascript` / `css` 重写根绝对引用
   (`src|href="/…"`、`from "/…"`、`import("/…")`、`url(/…)`、`"/api/…"` 及模板串里的 `}/api/…`),
   补上前缀。二进制 / JSON / SSE 原样透传。
3. **前缀来源**: HX-Webx 在 `proxyReq` 设置的 `X-HX-Webx-Proxied-For: <name>`
   —— 头是权威来源, 任何注册名都能直接用, 不需要白名单。

**为什么把适配层放在 28340 (原端口)**: 一开始的方案是让真实栈留在 28340、适配层另开
28341 并注册成新名字 `hx-pxy-web`。用户随后按**原 URL** 复测, 原 URL 仍指向
28340 真实栈 → 白屏照旧。端口对调后原 URL 直接生效, 且完全不必碰 HX-Webx 的基址缓存。

## 3. 注册形态 (由面板托管, 随 pxy 组自启)

| 注册名 | 端口 | 角色 |
| --- | --- | --- |
| `hx-pxy` | 28340 | **入口** = 适配反代 → 28341 |
| `hx-pxy-stack` | 28341 | 真实栈 (Vite + Go 控制面 19090 + Mihomo), 前端/后端日志归它 |

服务组 `pxy` 顺序: `hx-pxy-stack` → `hx-pxy` (适配层依赖真实栈先就绪),
`autostart: true`。

## 4. 端到端验证 (全部实测)

| 检查 | 结果 |
| --- | --- |
| 入口 HTML 资源引用 | `/proxy/hx-pxy/@vite/client`、`/proxy/hx-pxy/src/main.tsx` ✓ |
| `36000/proxy/hx-pxy/src/main.tsx` | 200 `text/javascript` (**修复前: 200 text/html**) |
| 无头 Chromium 渲染修复前 | DOM **902 B, 可见文本为空** → 复现白屏 |
| 无头 Chromium 渲染修复后 | DOM **67.6 KB**, 可见 `HX-Pxy Control Plane · 管理员登录 · 用户名 · 密码 · 登录` |
| API 全链路 | `/proxy/hx-pxy/api/v1/auth/status` → 200 `{"authenticated":false,"configured":true}` |
| 静态资源 | `/proxy/hx-pxy/favicon.svg` → 200 `image/svg+xml` |

`GET 36000/src/main.tsx` (**不带前缀**) 仍返回面板自己的 HTML —— 这是**正确行为**,
它本就该由 HX-Webx 的面板处理; 适配后页面不再请求这条 URL。

## 5. 已知边界 (如实记录)

- **HX-Webx 的 `resolvedBase` 按服务名缓存基址**, 只在代理出错时才失效: 注册项改了
  host/port 后代理**仍打旧端口**, 而健康检查用新端口 → 会显示 `online=true` 却打不到。
  本次靠"端口对调"绕开, 缺陷未修。
- 适配层只覆盖 root-absolute 引用。**运行时拼接的绝对 URL** 在范围外 ——
  本项目 `terminalSocketURL()` 用 `window.location.host` 拼 ws, 终端页与三个 SSE
  (overview/logs/terminal-metrics) **尚未复测**, 需实际点开确认后再下结论。
- 适配层挂掉会让入口白屏; 它由 `pxy` 组托管, 但组只在 hx-webx 启动时拉一次。
