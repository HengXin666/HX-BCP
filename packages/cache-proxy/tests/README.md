# 测试

> 路径: `toolbox/hx-capture-proxy/tests/` · 类型: Python

## 这是什么

测试。

## 从哪开始

- 入口: `e2e_api.py`

## 内容划分

文件: `e2e_api.py`, `e2e_mcp.py`, `e2e_panel.py`, `e2e_real.py`, `e2e_runtime.py`, `test_flows.py`, `test_policy.py`, `test_store.py`

## 约定与坑

- **分成两层, 别混**: `test_*.py` 是纯单测 (不碰网络/浏览器, 跑起来 1 秒内); `e2e_*.py` 是端到端,
  会真起 uvicorn / 真起 Chrome / 真起 stdio MCP。`pytest tests/` 只收前者。
- **端到端必须走真路径**, 不许用 TestClient / mock 打桩: 本仓栽过的坑 (两套 requestId 命名空间、
  reader 死锁、`/stats` 读同进程变量恒为 null) 全都是**打桩测不出来**的。
- **e2e 会起真浏览器, 跑完必须自己清理**: 用 `launcher.launch()` 拿到的句柄 `.stop()` 会精确
  匹配 `--user-data-dir` 补杀并删 profile; 直接 `terminate` 父进程杀不掉脱离会话的子进程。
- **POST 的 e2e 要起真 HTTP 端点**: flow 记录在 `Network.loadingFinished` 之后才补全,
  连接被拒只走 `loadingFailed`, 那条路径上根本不产生记录 —— 拿不存在的端口测, 测的是另一件事。
- **跨源 fetch 会先撞 CORS 预检**: 从 `about:blank` 直接 POST 别的源, 预检不过则真正的 POST 不发。
  要测"发出去的请求被抓到", 先导航到同源页面再发。
- 端口分配: 各 e2e 用互不相同的固定高位口 (28811 API / 43500 real / 43520 panel / 43560-43562 forensic
  / 43570 web)。改端口时同步改 `tests/README.md` 与 README 的测试段, 否则并行跑会互相撞。
- `e2e_panel.py` 依赖 8898 上的面板服务与 `hxcp` 缓存, 属于**联调**性质, 不在常规回归里。
