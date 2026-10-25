# Agent Note: HX-Browser 副本运维能力 (多内核 / 代理链 / 区域 / 检测修复 / 回收站 / 沙箱 / MCP)

Status: implemented

## Problem

用户 2026-10-16 要求 HX-Browser 在**保留全部现有功能与 CDP/MCP 调用方式**的前提下,
对齐 nodecloak 的指纹浏览器能力清单: 完全隔离 (绝对防逃逸)、副本管理 (标签筛选/
定位窗口/重启)、代理 (直连/系统/HTTP/HTTPS/SOCKS5 + 账号密码 + 代理链接, 兼容 hx-pxy)、
语言跟随出口 IP、时区 (系统/IP/自定义, Firefox 严格保护统一 UTC)、系统时钟比对、
定位权限、网站设置 (WebRTC/DNS/DNT-GPC/通知/摄像头/麦克风/图片)、Firefox 字体限制与
严格指纹保护、检测与修复 (改前备份)、修复记录恢复、删除 (最近删除/恢复/彻底删除清凭据),
并兼容多种浏览器内核。

改动前的 HX-Browser: 只有 Chromium(sun)/Firefox(flower) 两档; 代理只有 no_proxy/
http/https/socks5 且**把凭据内嵌进 `--proxy-server`** (Chromium 不解析 -> 407/弹框,
Firefox 根本没接代理); 无区域/网站/字体/时钟/定位设置; 删除是一步硬删, 无回收站、
无修复记录; 无 MCP 接口。

## Decision

全部新增能力落成**独立模块**, 对两个行数棘轮存量文件 (api.py / browser.py) 只做
最小接线, 判据不写进它们:

### 一、内核适配层 (`kernels.py`)
族 = blink / gecko; 内核 = cloak/chrome/chromium/edge/brave/firefox/librewolf。
`family_of` 是「是不是 Firefox」的**唯一判据**; `resolve_executable` 在 kernel 为空时
退回 `settings.chromium_path/firefox_path` —— 旧环境逐字节同样启动。门禁只许 Chromix,
故其他内核的可执行名逐行 `# browser-gate: allow-legacy` 标注 (可审计)。

### 二、代理链 (`proxy_link.py` + `relay.py`)
`ProxyConfig.type` 加 direct/system; 自定义上游一律经**每副本一个本地 relay**
(只绑 127.0.0.1, 注入 `Proxy-Authorization` / SOCKS5 RFC1929), 浏览器命令行只见
`http://127.0.0.1:<port>`, 凭据不进 ps、不进 profile。上游失败回 502 **绝不回落直连**
(回落 = 用真实出口 IP, 隔离失效)。这同时修好了本仓 2026-10-06 记过的两个坑。

### 三、区域与网站设置 (`prefs.py`/`profile_rules.py`/`chromium_prefs.py`/`firefox_prefs.py`/`netcheck.py`)
新模型 `ProfilePrefs` 所有字段默认 inherit -> 旧 environments.json 反序列化后行为不变。
`launch_hooks.prepare` 产出「启动视图」: 把 language/timezone 换成实际生效值后交给
**既有**启动路径, 于是新模式自动贯穿所有内核。语言跟随 IP 时同步进程 LANG (Intl locale
与 navigator.language 对齐)。Chromium 偏好**合并写**、文件损坏拒绝覆盖; Firefox 写
user.js 末尾的受管块 (后写覆盖, 幂等)。严格指纹保护与「语言跟随 IP」互斥, 时区恒 UTC。

### 四、检测与修复 (`doctor.py`/`repair_ops.py`/`repairs.py`)
11 项检测给出 status + fixable + fix; 修复前备份偏好旧值**与配置文件副本** (只回滚字段
不够: inherit 的键启动时不再写, 残值留在文件里), 记入 data/repairs.json, 可按记录恢复
(字段被后续改过则跳过并报冲突)。系统时钟比对走 nodecloak clock_probe 同一判据 (ts= +
2 分钟容差, 取不到参考 = 尚未确认)。

### 五、回收站与隔离 (`profile_ops.py`/`sandbox.py`)
删除两步: 移至最近删除 (deleted_at, 数据不动) -> 彻底删除 (清 profile/记录含凭据/relay/
修复备份; env_id 做越界校验; mode=reference 导入的软链只断引用)。`?purge=true` 保留旧语义。
隔离第二层 = bubblewrap 命名空间沙箱 (PID/IPC/UTS/家目录); **缺 bwrap 拒绝启动不降级**;
网络/X11/内核共享如实写入 status, 不宣称「绝对防逃逸」。

### 六、MCP (`mcp_tools.py`/`mcp_http.py`/`mcp_server.py`)
19 个工具, 每个 = 一个既有 REST 端点的薄壳 (判据只实现一次); 返回给 AI 的密码类字段
脱敏; 彻底删除需 confirm=true。HTTP 端点经 ASGITransport 回调同一 app (走鉴权中间件,
不占端口); stdio 版连运行中的服务。页面操作仍交给 CDP (start 返回 cdp_http)。

### 七、PR 自审修正
本机 CDP (`cdp.http_json` 与 websockets 连接) 显式不走环境 HTTP(S)_PROXY —— 回环口请求被
送去代理会让启动判定「CDP 未就绪」; websockets 下限因 `proxy=None` 参数升到 15。
启停钩子由已弃用的 `on_event` 迁到 FastAPI lifespan。

路由挂在新 router (`api_profiles.py`), create_app 只多两行 include。

## Alternatives considered

- **凭据内嵌 `--proxy-server=http://user:pass@...`** — 看起来最省事, 但 Chromium 不解析
  (本仓 2026-09-26/10-06 两次实测 407/弹框), 且密码进 ps。否决, 改本地 relay。
- **上游失败回落直连** — 否决: 副本会悄悄用真实出口 IP, 与隔离初衷相反; 回 502。
- **把新路由与逻辑加进 api.py/browser.py** — 否决: 二者是行数棘轮存量 (只许降不许涨),
  且判据集中进大文件会让「改一处看两处」重现。拆成独立模块, 大文件只最小接线。
- **沙箱缺 bwrap 时降级为 process** — 否决: 用户会以为自己在沙箱里。缺则拒绝启动。
- **MCP 工具直接调内部函数** — 否决: 会出现两套入口两套判据。只做 REST 薄壳。

## Consequences

新增 ~20 个 <300 行模块 + 3 个测试文件 (45 个新用例, 另有真实 headless Chromium 的
opt-in 端到端, process 与 bwrap 两档均通过)。api.py 759→(接线后仍 759)、browser.py
659→519 (拆出 launch_hooks 后反而变小), 两处棘轮基线相应缩小重录。16 项质量门禁全绿。

已知限制 (写入 PR): 代理密码仍明文存 environments.json (0600; nodecloak 用系统钥匙串);
Chromium 无原生 GPC; 沙箱不隔离网络与 X11; Firefox 因运行库缺失仅单测覆盖, 未端到端。

## Testing

- `uv run pytest tests/` — 80 passed (改动前 35); 3 个 smoke 需服务在线, 起服务后通过。
- `HXBR_E2E_CHROMIUM=<headless_shell> [HXBR_E2E_SANDBOX=1] uv run pytest tests/test_e2e_browser.py`
  — 真浏览器经 relay->需认证上游取回页面、命令行无密码、navigator 语言生效、停止后 relay 端口关闭。
- `node .agents/quality/run.mjs --all --check` — 16 项门禁全过。
