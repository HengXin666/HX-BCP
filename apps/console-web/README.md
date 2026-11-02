# 控制台

> 路径: `apps/console-web/` · 类型: 应用

## 这是什么

HX-BCP 的控制台, React + TypeScript + @hx/ui. 两个页签: 浏览器 / 状态.

- 浏览器: 左侧分组树, 右侧当前范围的浏览器表.
  - 人类组 `文件夹 / 浏览器`: 可创建, 改名, 移动, 删除, 启动, 停止浏览器, 可建删改文件夹.
  - API 组 `项目 / [文件夹] / 浏览器`: 由 API/MCP 创建, 控制台只读. 被占用的浏览器显示 "API 占用中"; 树上 API 组与项目节点带 "占用 N" 角标, 不展开也能看到.
- 状态: 缓存代理统计与指纹自检结论合并在一页.

通道判据只在 `packages/lane/`, 设计见 `.agents/notes/implemented/feature/2026-11-01-lane-folders.md`.

## 入口

- `src/main.tsx` -> `src/App.tsx` (AppFrame + Tabs 外壳, 挂 ConfirmDialogHost 与 Toaster)
- `src/pages/browsers/BrowsersPage.tsx`: 树 + 面包屑 + 表 + 人类组操作
- `src/pages/browsers/EnvTable.tsx`: DataTable, 状态点, 占用角标
- `src/pages/browsers/EnvDialogs.tsx`: 新建 / 改名 / 移动浏览器与文件夹的对话框
- `src/pages/system/SystemPage.tsx`: 状态 / 日志 / 孤儿进程 / 设置四个子页 (缓存代理 + 指纹在状态子页 StatusTab)
- `src/pages/HumanGate.tsx`: 人类通道门, 未解锁时弹令牌框, 解锁后才渲染页签
- `src/lib/api.ts`: 读接口与类型, 前缀 `API = /api/v1`, 每个请求带 `x-bcp-lane: human`
- `src/lib/session.ts`: `/api/v1/lane/session` 查询 / 解锁 / 退出
- `src/lib/api-write.ts`: 写接口
- `src/lib/tree.ts`: 分组树组装, 节点 id `h`, `h/<folder>`, `a`, `a/<project>`, `a/<project>/<folder>`
- `src/lib/hooks.ts`: 5 秒轮询, 带 `reload`
- `src/styles.css`: 只 import @hx/ui 的 tokens.css 与 styles.css

## 页面与后端契约

前端只发 `/api/v1/*`, 与 8325 原路径相同, 无改写. 生产: 8325 自己托管构建产物 (`pnpm build` 输出到 `packages/browser/src/hx_browser/web/`), 同源直发. 开发: vite 把 `/api/v1` 原样代理到 8325. 缓存统计改由 8325 的 `GET /api/v1/cache/stats` 只读转发 8898 `/stats`, 前端不再直连 8898.

| 页面 | 请求 | 后端 | 关键字段 |
|---|---|---|---|
| 浏览器 | GET /api/v1/environments?page_size=1000 | 8325 | data.list[].id/name/lane/project/folder/runtime/in_use/in_use_since/readonly/proxy/last_open_at |
| 浏览器 | GET /api/v1/folders | 8325 | data 为 `{分组键: [文件夹名]}`, 分组键 `human` 或 `agent:<project>` |
| 浏览器 (人类组) | POST /api/v1/environments | 8325 | body {name, folder, proxy?}; lane 与 project 由后端按请求打标, 不读 body |
| 浏览器 (人类组) | PUT / DELETE /api/v1/environments/{id} | 8325 | PUT {name} 改名, {folder} 移动; DELETE 移入最近删除 |
| 浏览器 (人类组) | POST /api/v1/environments/{id}/start, /stop | 8325 | 启动 / 停止 |
| 浏览器 (人类组) | POST /api/v1/folders, PUT / DELETE /api/v1/folders/{name} | 8325 | 只作用于人类组; 非空文件夹删除回 409 |
| 状态 | GET /api/v1/cache/stats | 8325 转 8898 | engine.hits/engine.misses/engine_state, store.entries/store.bytes/store.bytes_served, flows.records |
| 状态 | GET /api/v1/fingerprint/verdict | 8325 | verdict/checked/at/evidence |

响应形状不同, 不能共用一个解包函数:

- 8325 自己的接口全部包信封 `{code,msg,data}`, code 不为 0 表示业务失败
- `/api/v1/cache/stats` 原样返回 8898 的裸 JSON; 8898 不通时 502 `{code: cache_unreachable}`

通道相关的错误码 (8325):

| HTTP | code | 含义 |
|---|---|---|
| 400 | project_required | 非人类请求没带合法项目名 (API/MCP 必须带 `X-BCP-Project`) |
| 401 | human_token_required | 声明人类通道但没有或令牌不对 |
| 403 | lane_forbidden | 对不属于自己的环境做写操作 (如人类写 API 组) |

## 人类身份

判据只在 `packages/lane/lane.py` 的 `decide`, 人类通道认两种证明, 任一即可:

1. 请求头 `x-bcp-human-token` 等于本机令牌. 只有 vite dev 代理会注入 (Node 侧现读 `~/.bcp/human.token`, 可用 `BCP_HUMAN_TOKEN_FILE` 覆盖), 且只给本页面的请求: Origin 为 127.0.0.1:8940 / localhost:8940, 或无 Origin 且 `Sec-Fetch-Site: same-origin`.
2. 会话 cookie `bcp_human`, 且浏览器标记请求来自同源页面 (`Sec-Fetch-Site: same-origin`, 无该头时 Origin 等于 `http://<Host>`). 8325 托管的控制台走这条: 首次打开粘贴令牌, `POST /api/v1/lane/session` 校验后下发 HttpOnly, SameSite=Strict, Path=/api/v1, 12 小时的 cookie, 值是 `<时间戳>.<HMAC(令牌, 时间戳)>`, 不含令牌. `bcp-lane issue` 换令牌后旧会话全部失效.

令牌不进打包产物, 也不存在浏览器里. 不带 cookie 的 curl, 或别的 localhost 端口页面 (同站不同源) 带着 cookie 发来的请求, 都回 401 human_token_required. 设计见 `.agents/notes/implemented/feature/2026-11-01-console-on-8325.md`.

## 已知坑

1. 前端路径必须是 `/api/v1/*`, vite 代理不改写. 写成别的前缀在 8325 托管时会落到静态目录, 回 index.html 或 404.
2. 环境对象没有 `seed` 也没有 `running`. 运行态看 `runtime` 是否为 null; API 组占用看 `in_use`.
3. `store.bytes_served` 是缓存累计返回给浏览器的字节数, 不是节省字节, 页面上必须写清.
4. 服务未启动时接口返回 ok=false 而不是抛异常, 页面显示"未连接".
5. 不带 `x-bcp-lane: human` 的请求按 Agent 通道处理, 缺项目名回 400. 前端每个请求都带这个头 (`HUMAN_HEADERS`).
7. 8325 设了 `api_key` 时, `api.py` 的中间件除 `/` 外都要 `x-api-key`, 静态资源也会 401; 控制台不发这个头, 这种配置下打不开 (既有行为, 未改).
6. TreeNav (@hx/ui v0.1.2) 不支持拖拽, 移动浏览器用 "移动到文件夹" 对话框, 见 `.review/hx-ui-issue.md`.
