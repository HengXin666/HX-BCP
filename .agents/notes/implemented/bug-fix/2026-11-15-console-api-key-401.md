# 控制台在设置了 api_key 时打不开 (401)

日期: 2026-11-15 · 类别: bug-fix · 对应: apps/console-web/README.md 已知坑 #7

## 现象

8325 设置了 `api_key` 后, `api.py` 的中间件对除 `/` 以外的所有路径都要求 `x-api-key`。
控制台的 js/css 静态资源和 `/api/v1/*` 请求都不带这个头, 全部回 401, 页面白屏。

## 根因

api_key 校验不区分静态资源和接口, 也不认人类通道已有的身份证明。控制台的身份证明本来就是
人类会话 cookie (由本机人类令牌换来, 见 2026-11-01-console-on-8325.md), 却被前置的 api_key 拦掉。

## 决策

校验逻辑移到 `hx_browser/api_key_gate.py`, `api.py` 只调用 `admitted()`:

1. 只守 `/api/*`, `/mcp*` 与 FastAPI 文档 `/docs*`, `/redoc`, `/openapi.json`。静态控制台文件不含令牌和任何密钥
   (README 约定令牌不进打包产物), 不需要守; 文档暴露全部接口结构, 保持旧行为继续守。
2. `/api/v1/lane/session` 放行: 它自己校验本机人类令牌且只接受同源页面, 控制台要靠它拿 cookie。
3. 带 `x-bcp-lane: human` 且被 `lane.decide` 以 `loopback`, `session` 或 `token` 放行的请求, 免 `x-api-key`。
   判据仍只在 `packages/lane`, 这里只读 `audit["by"]`。
4. `loopback` 也免 key: `lane.decide` 先判 loopback, 本机打开控制台时 `GET /lane/session` 即返回 human,
   页面不会再用令牌换 cookie, 不免则本机控制台仍 401 (首版遗漏, 同日自查发现)。理由同 `lane.loopback_admitted`:
   能连 127.0.0.1 的进程与 HX-BCP 同权限, 本就能读令牌; Host 校验挡住 DNS rebinding。
   前提: 8325 前面不加反向代理 (否则所有请求都成 loopback), 此前提对人类通道准入本就成立。
5. key 比较改为 `secrets.compare_digest`, 避免逐字节比较的时序差。

Agent 通道、MCP、脚本调用的行为不变: 设置了 api_key 就必须带 `x-api-key`。

## 验证 (交给 CI, 本地不跑测试)

- 设 api_key, 不带 key 访问 `/` 与 `/assets/*.js` → 200。
- 设 api_key, 非回环来源不带 key 访问 `/api/v1/environments` → 401 invalid api key。
- 设 api_key, 不带 key 访问 `/docs`, `/openapi.json` → 401。
- 设 api_key, 本机 127.0.0.1:8325 打开控制台 (带 `x-bcp-lane: human`, 无 cookie) → 接口不再 401。
- 设 api_key, 回环来源但不带 `x-bcp-lane: human` 的 Agent 请求不带 key → 401。
- 设 api_key, `POST /api/v1/lane/session` 带正确令牌且同源 → 200 并下发 cookie。
- 之后同源页面带 cookie 与 `x-bcp-lane: human` 访问 `/api/v1/environments` → 不再 401。
- 带正确 `x-api-key` 的 Agent 请求 → 行为不变。
