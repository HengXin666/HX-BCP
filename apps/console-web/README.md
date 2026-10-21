# 控制台

> 路径: `apps/console-web/` · 类型: 应用

## 这是什么

HX-BCP 的只读控制台, React + TypeScript + @hx/ui. 四个页签: 浏览器环境 / 缓存代理 / 指纹评分 / 通道说明.

## 入口

- `src/main.tsx` -> `src/App.tsx` (AppFrame + Tabs 外壳)
- `src/lib/api.ts`: 后端访问与类型
- `src/lib/hooks.ts`: 5 秒轮询
- `src/styles.css`: 只 import @hx/ui 的 tokens.css 与 styles.css

## 页面与后端契约

| 页面 | 请求 | 后端 | 关键字段 |
|---|---|---|---|
| EnvironmentsPage | GET /api/browser/environments | 8325 | data.list[].id/name/group/platform/proxy/fingerprint.browser/runtime/visible/visible_reason/open_count |
| CacheProxyPage | GET /api/cache/stats | 8898 | engine.hits/engine.misses/engine_state, store.entries/store.bytes/store.bytes_served, flows.records |
| FingerprintPage | GET /api/browser/fingerprint/verdict | 8325 | verdict/checked/at/evidence |
| LanesPage | 无 | - | 静态判据表 |

两个后端的响应形状不同, 不能共用一个解包函数:

- 8325 全部包信封 `{code,msg,data}`, 前缀 `/api/v1`, code 不为 0 表示业务失败
- 8898 是裸 JSON, 无前缀

## 已知坑

1. vite 代理的 rewrite 必须保留 `/api/v1`. 写成去前缀会打到 `8325/environments`, 实测 404.
2. 环境对象没有 `seed` 也没有 `running`. 运行态看 `runtime` 是否为 null, 指纹看 `fingerprint.browser`.
3. `store.bytes_served` 是缓存累计返回给浏览器的字节数, 不是节省字节, 页面上必须写清.
4. 服务未启动时接口返回 ok=false 而不是抛异常, 页面显示"未连接".
