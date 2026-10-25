# Agent Note: HX-Browser 多副本实时预览 + 远程操作 (CDP screencast, 兼容 #98 严格隔离)

Status: implemented

## Problem

用户 2026-10-17 原话: "我要一个兼容 #98 的浏览器的实时预览: 我可以切换多个浏览器,
能在一个前端显示所有浏览器页面, 并且操作浏览器"。

既有能力只覆盖一部分: console/hark 的「浏览器窗口」页 (T4) 管开/关窗与绑定闭环,
截图只有 gh-pool 接管会话的一张 PNG (`windows.snapUrl`), 看不到 hx-browser 的任意副本,
也不能操作。#98 之后副本的显示层可能是 Xvfb 专属 socket、Xephyr 嵌套或**原生 Wayland**
(窗口无法被外部定位, focus 接口如实返回失败), 沙箱副本还有独立网络命名空间 ——
任何"抓 X11 屏幕 / xdotool 送键"的方案在这几档里至少废掉一半。

## Decision

### 一、帧与输入全部走 CDP (`live_view.py`)
每个预览连接对副本的 page target 开 `Page.startScreencast` (jpeg), 收到
`Page.screencastFrame` 立刻回 `Page.screencastFrameAck` 再转发给客户端 (Chromium 按
未确认帧数限流, 不 ack 就停推)。输入走 `Input.dispatchMouseEvent / dispatchKeyEvent /
insertText`。CDP 端口就是 RuntimeInfo.debug_port: process 副本直连, 沙箱副本经宿主
bridge -> cdp.sock (#98 保证端口与用法不变), 所以对 headless / Xvfb / Xephyr / Wayland
一视同仁, 不需要任何显示层特例。`cdp.CdpConnection` 增加 `on_event` 回调 (默认 None,
既有调用方行为不变), 用 create_task 调度以免回调里 send 等自己的响应造成死锁。

### 二、协议层是纯函数 (`live_protocol.py`)
客户端只给"图像内像素 + 图像显示宽度", 换算到页面 CSS 像素**只认宽度比**并夹回页面
范围 —— 判据只此一处, 前端不算第二份。按键带 CDP 修饰位 (alt=1 ctrl=2 meta=4 shift=8),
可打印键与 Enter/Tab 在 keyDown 上带 text (不带 text 的 Enter 在 textarea 里不换行)。
滚轮 delta 不经缩放。协议错误回一条非致命 error 事件, 不断开连接。

### 三、路由单独成模块 (`api_live.py`, `live_nav.py`)
`GET /api/v1/live/envs` 列在跑副本 (内核族/隔离档/窗口系统/显示来源/是否支持, 值全部来自
env_dict 与 RuntimeInfo); `WS /api/v1/environments/{id}/live?preset=grid|focus` 是会话。
api.py 是行数棘轮存量, 只加一行 include_router 并压缩一行注释, 行数不涨。
http 中间件不经过 websocket scope, 所以 WS 自己按同一口径校验 api_key (header 或 `?key=`)。
拒绝用 4400 (不支持) / 4401 (鉴权) / 4404 (不存在/未运行) / 4502 (连 CDP 失败) 关闭,
先发一条 fatal error 事件带中文原因 (关闭原因字段 123 字节放不下)。

### 四、Firefox 显式不支持
gecko 族没有 CDP `Page.startScreencast` (其远程协议是 WebDriver BiDi)。不做"定时整页截图"
的降级假装 —— 每秒一帧且点不动的"实时预览"会让用户分不清是网卡还是功能不存在。
`live_view.unsupported_reason` 是唯一判据, 列表与 WS 都用它。

### 五、前端组件在 HX-UI, `console/hark` 只接线
用户 2026-10-17 追加: "预览浏览器应该是 hx-ui 提供的, 针对 hx-浏览器的一个组件, 这样下游
依赖浏览器的, 也可以在他们的前端直接复用组件"。所以协议客户端 (消息类型 / 关闭码 / 重连) 与
平铺墙 / 聚焦视图组件都在 HX-UI `src/hxbrowser/` (`BrowserLiveWall` / `BrowserLiveView` /
`BrowserLiveTile` / `useHxBrowserLive` ...), 本仓 `LivePreviewPage.tsx` 只传 `baseUrl` 与文案。
平铺墙每块一条 grid 档低画质流, 滚出视口 / 页面切后台即 pause; 聚焦视图只剩一条 focus 档流,
页面切后台同样 pause; 鼠标 / 滚轮 / 键盘 (需"捕获键盘") / 地址栏 / 前进后退刷新 / 标签条 /
"发送文本" (中文输入法与粘贴)。聚焦中的副本从列表消失时回到平铺并提示, **不**自动换成另一个
副本 (远程输入安全)。前端经 Vite 代理 `/hxbr` (ws: true) 直连 8325: 网关 hark2api 是
BaseHTTPRequestHandler, 不支持 WebSocket 升级。hx-browser 设了 api_key 时由 Vite 从环境变量
HXBR_API_KEY 注入头, key 不进前端代码; 代理在转发前校验 Origin (回环 / 同 host /
HXBR_TRUSTED_ORIGINS), 防跨站页面借本机 Vite 拿到带鉴权的连接。

### 六、评审 (PR #99) 后的健壮性约束
- 跨站 WS: 带 `Origin` 的握手只接受回环来源、同 host 或 `HXBR_LIVE_ORIGINS`; 不带 Origin 的
  脚本客户端仍按 api_key 判 (`api_live.origin_allowed` 是唯一判据)。
- 帧合并 (`live_frames.FrameRelay`): 客户端慢时只留最新一帧 + 单个发送任务, 不按帧堆任务与 JPEG。
- 帧回调绑定来源连接: 切标签后旧连接排队的帧不 ack 到新 target、不发给客户端。
- 上游断开 (`CdpConnection.on_close`, 或元数据连续 3 次读失败) -> 发一次 fatal 并以 4502 关 WS,
  客户端退避重连, 不会停在最后一帧却显示「实时」。
- 坏输入 (非数字 / NaN / 无穷) 是 ProtocolError, /json 读失败是 CdpError, 都只回非致命 error 事件。
- 启动阶段任何异常都发 fatal + 4502 并清理会话。

## Alternatives considered

- **什么都不做, 用 T4 窗口页 + 让用户自己看桌面** — 最强理由: 零新代码。否决: 沙箱 headless /
  Xvfb 副本用户根本看不见, Wayland 副本无法被外部定位; 需求本身就是"一个前端看全部并操作"。
- **复用 `windows.snapUrl` 的截图轮询** — 最强理由: 已有端点与面板。否决: 它只覆盖 gh-pool
  接管会话、是一张 PNG 轮询、不能送输入; 扩成实时流等于重写, 且要把 hx-browser 塞进 gh-pool。
- **抓 X11/Xvfb 屏幕 (ffmpeg x11grab / xwd) + xdotool 送输入** — 最强理由: 能看到浏览器 UI
  外壳 (地址栏/弹窗)。否决: #98 的原生 Wayland 档无法外部抓取与定位, Xephyr 嵌套要额外穿透,
  headless 档没有屏幕; 而 CDP 在所有档位都可达。代价是看不到浏览器原生 UI (见 Consequences)。
- **noVNC / x11vnc 远程桌面** — 最强理由: 成熟、全桌面可操作。否决: 同上依赖 X 显示, 且每副本
  多一个常驻进程与端口, 与 #98 "沙箱除代理与 CDP 外无路可走" 的收口相悖。
- **经网关 hark2api 转发** — 最强理由: 前端只连网关是既有纪律。否决: 网关不支持 WebSocket
  升级, 改成长轮询会让输入延迟到秒级; Vite 代理同样只暴露相对路径, 真实地址只在
  vite.config.ts 出现一次, 纪律的实质 (前端不拼绝对地址、凭据不进前端) 保留。
- **Firefox 用 BiDi `browsingContext.captureScreenshot` 轮询** — 本轮不做: 帧率低、无增量,
  输入要另走 BiDi `input.performActions`, 等于第二套实现; 先显式报不支持。
- **组件留在 console/hark** (第一版的做法) — 最强理由: 只有一个使用方, 少一次跨仓发布。否决
  (2026-10-17 用户追加要求): 下游依赖 hx-browser 的前端要直接复用; 且评审指出的前端问题全在
  协议细节上, 留在应用里每个使用方都要各修一遍。代价: HX-UI 合并前本仓依赖它的 PR 分支。

## Consequences

- 预览的是**页面视口**, 不含浏览器原生 UI (标签条/地址栏/扩展弹窗/原生对话框);
  地址栏与标签条由本页自己提供。`alert/confirm` 等 JS 对话框会阻塞页面, 本轮未接管。
- 输入是 CDP 合成事件, 对页面而言 `isTrusted=true` (Input 域派发), 但鼠标轨迹由用户真实
  操作产生, 不额外伪造。
- `/json/activate` 让浏览器把切到的标签放到前台, 可见窗口里用户看到的与预览一致。
- 经 HX-WebX 子路径 (`/proxy/hark-console`) 访问时, WebSocket 需要 HX-WebX 的 HTTP 服务器处理
  `upgrade` 事件 (评审指出它目前只在 Express 代理上写了 ws: true), 这是 HX-WebX 仓的改动, 本仓未做;
  直连 Vite 8930 可用。
- `@hx/ui` 以 `file:` 链到 HXLoLis 的 components/HX-UI 子模块, 本仓不锁版本: 用到本页需要
  HX-UI 含 `src/hxbrowser/` 的版本 (HX-UI PR 合并后 HXLoLis 子模块指针要跟着升)。

## Testing

- `uv run pytest tests/test_live_protocol.py tests/test_live_view.py tests/test_live_api.py tests/test_live_review.py`
  — 纯函数换算 / 假 CDP 驱动的会话 (ack、缩放、导航、暂停幂等、切标签保持暂停态) /
  WS 端点 (gecko 4400、不存在、未运行、api_key、跨站 Origin、在跑副本推流) / 评审回归
  (ack 先于转发、旧标签帧丢弃、慢客户端帧合并、上游断开 fatal、坏数值与 /json 失败不断连)。
  CI 不需要真浏览器。前端协议与输入逻辑的单测在 HX-UI (scripts/tests/hxbrowser)。
- 本机冒烟 (不入库): 假 CDP 服务 + 真 hx-browser (uvicorn) + 真 Vite dev 代理, Node 客户端
  经 `/hxbr` 走 WS: 收到 19 帧、点击 (320,180)@640 -> CDP (640,360)、按键/中文文本/导航、
  pause -> stopScreencast、切到标签 B 后在新 target 上重开 screencast、gecko 副本 4400。

## Code

- services/hx-browser/src/hx_browser/live_view.py
- services/hx-browser/tests/test_live_view.py
- console/hark/src/LivePreviewPage.tsx
- console/hark/src/constants/liveView.ts
- console/hark/vite.config.ts
