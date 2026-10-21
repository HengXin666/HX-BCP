# fix(console): 修复控制台与 8325/8898 服务端的契约断裂

## 实现人
Hark 子 agent (j-20261021-023), 受 DSH 主 agent 派活

## 需求
控制台三个只读视图全部读不到数据: 环境列表 404, 缓存统计字段对不上, 指纹结论接口不存在。

## 变更实现
- vite.config.ts: /api/browser 改写为 /api/v1, 最终打到 8325/api/v1/environments; /api/cache 保持改写到根路径 /stats
- lib/api.ts: 新增 getEnvelope 统一拆 {code,msg,data}, code 非 0 返回 ok=false 与 msg; 环境列表读 data.list; 8898 无信封走 getRaw; Env/CacheStats/VerdictReport 类型按实测字段重写
- EnvironmentsPage.tsx: 运行态按 runtime 是否为 null, 显示 visible 与 visible_reason, 指纹显示 fingerprint.browser, 代理显示 type://host:port 或 直连, 补 group/platform/open_count
- CacheProxyPage.tsx: 命中率 = engine.hits / (engine.hits + engine.misses); 同时显示 store.bytes_served (累计服务字节, 页面注明不是节省字节) 与 store.bytes; 条目 = store.entries; 补 engine_state 与 flows.records
- FingerprintPage.tsx: 适配新返回形状, 补显示报告时间 at
- 新增 packages/browser/src/hx_browser/api_fingerprint.py: GET /api/v1/fingerprint/verdict, 读 packages/fingerprint-audit/reports/ 下按文件名排序最新的 json, 返回 {verdict, checked, at, evidence}; verdict = verdict.level, checked = results 站点数, evidence = verdict.findings; 目录空/读失败/解析失败一律返回 unknown + checked=0, 不 404 不 500; 支持 HX_FP_REPORTS_DIR 覆盖目录
- api.py: 只加 import 与一行 include_router (761 -> 762 行), 路由逻辑全部在新模块

## 高危变更
- api.py 净增 1 行 (include_router 挂载), 文件本身仍超 500 行上限, 未在本 PR 拆分
- vite 代理改写规则变化, 只影响 dev server 的 /api/browser 前缀

## 测试效果
- pnpm typecheck: tsc --noEmit 退出码 0
- verdict 端点 (独立 FastAPI 实例只挂 api_fingerprint.router, 临时报告目录):
  - 空目录: {"code":0,"msg":"success","data":{"verdict":"unknown","checked":0,"at":"","evidence":[]}}
  - 两份报告取最新: {"code":0,"msg":"success","data":{"verdict":"yellow","checked":3,"at":"2026-10-21 12:00:00","evidence":[{"level":"yellow","rule":"R3 trust score 偏低","detail":"creepjs trust score = 55 (30-70)","fix":""}]}}
  - 最新一份损坏: 返回 unknown, HTTP 200
- 8325/8898 实测返回: 待主 agent 在本机补贴 (子 agent 沙箱连不到 127.0.0.1:8325/8898)

## 浏览器预览
待主 agent 本机 pnpm dev (8940) 截图: 环境 / 缓存代理 / 指纹 三个页
