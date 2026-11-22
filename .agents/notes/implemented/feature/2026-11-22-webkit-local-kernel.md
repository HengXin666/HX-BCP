# 本机 WebKit 内核 (Linux, Playwright 构建)

日期: 2026-11-22

## 背景
用户问 Safarier 为什么离不开 macOS, 要一个能在本机 (Linux) 跑的方案.

## 结论
- Safarier 依赖 macOS 不是因为硬件指纹, 而是它嵌的是苹果系统框架 WKWebView, 这个框架只在苹果系统上有.
- WebKit 引擎本身开源. Playwright 维护一份 Linux 上的 WebKit 构建 (GTK/WPE 移植), `playwright install --with-deps webkit` 即可装.
- 新增内核 `webkit` (webkit 族, mode=runner), 和 `safarier` 并列. 默认内核逻辑不变 (仍优先 cloak).

## 做法
- `webkit_local.py`: 一个环境一个 runner 进程 (`python -m hx_browser.webkit_local`), 里面是一个持久化 context, profile 目录就是环境目录, 重启不丢.
- 代理走 relay.py 的本地无凭据入口, runner 命令行里没有密码.
- 语言与时区用 profile_rules 的生效值, 和其他内核同一个输入.
- 停止: 对 runner 进程组发 SIGTERM, runner 关 context, WebKit 随之退出.
- `external.py`: browser.py 只调 external.start / stop, 由 kernel id 分到 safarier 或 webkit_local.
- `Kernel` 加 `features` 和 `how` 两个可选字段, 同族内核能力不同时覆盖族默认 (webkit 没有 agent_cli).

## 边界
- 没有 CDP: 实时预览, Cookie 注入, CDP 体检, 沙箱都不支持, 内核页如实置灰.
- 页面看到的是 Linux 上的 WebKit, 不是 macOS Safari. 要 Safari 的真实平台特征只能用 Safarier + Mac.
- playwright 目前在 dev 依赖组, 另加 optional `webkit` 组.

## 验证
- 沙箱装了 webkit-2388 构建, runner 能起能停, start_runtime 端到端通过.
- tests/test_webkit_local.py 5 条.
