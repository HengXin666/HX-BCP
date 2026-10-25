# Agent Note: HX-Browser 可见窗口与多实例并发

Status: implemented

> ⚠️ 本文由 Lead 代 T1 (win-visible) 补写 —— T1 在完成验收前失败, 代码已落盘且
> 验收由 Lead 代跑通过 (见 `services/hx-browser/REPORT-T1-LEAD-VERIFIED.md`)。
> 因此本文记录的是**代码里实际存在的**决策, 不是 T1 的自述。

## Problem

用户 2026-10-13 选定方案甲, 并把核心定义为「**人可以直接在他们的 web ui 上操作**」,
且明确要求「注意考虑**多浏览器窗口**, 实现**并发**」。

而 HX-Browser (:8325) 当时的启动路径只有 `--headless=new` 一种可见性, 且:

1. **服务由 HX-WebX 以 `nsenter` + `setpriv` 拉起**, 环境里**只有 `HOME` 与 `PATH`** ——
   没有 `DISPLAY`, 也没有 `XAUTHORITY`。于是任何 `headless=false` 的启动都是
   「起了一个连不上 X 的 Chromium」, 表现为进程起完即死、`runtime` 被清空、
   后续 `POST /cookies` 抛 `CdpError(no page target)` (**T2 实测踩到**, 见其 REPORT §7.1)。
2. **宿主是 KDE/Wayland**, 用户可见的桌面在 **Xwayland :1**。若只设 `DISPLAY` 不设 `XAUTHORITY`,
   连接会被拒 (`Authorization required, but no authorization protocol specified`) ——
   `gh-pool` 的 `desktop.py` 已实测过这个反例。
3. **`XAUTHORITY` 是每会话随机的** (`/run/user/1000/xauth_<随机后缀>`), 写死必然失效。

## Decision

### 一、桌面探测只有一条可靠路径: 从 `/proc/<pid>/environ` 反推

不猜 `:0/:1/:2`, 而是读 **Xwayland / plasmashell 自己的 environ** —— 那是它们正在服务的
那个 display。判据是**连通性** (`xdpyinfo` 成功), 不是 socket 文件存在
(socket 会在 Xvfb 被 SIGKILL 后残留)。

### 二、必须 pop `WAYLAND_DISPLAY` + 钉死 `--ozone-platform=x11`

宿主是 Wayland: 只要环境里有 `WAYLAND_DISPLAY`, Chromium 就走 Wayland, 窗口进 KWin 合成器,
**X 工具完全看不到** —— 于是「窗口存在」无法断言 (本仓对 gmail-login 记过同类教训)。
钉死 x11 后窗口是可枚举的 X11 窗口, `xdotool` 才能成为判据。

### 三、探测不到就**明说**, 不假装可见

探不到桌面时回落 Xvfb, 并在响应里给 `visible=false` + `reason`。
这与 `gh-pool` takeover 的口径一致: 用户看到 `visible=false` 才知道「你看不见这个窗口」。

### 四、多实例并发: 端口预留 + 窗口错开

- CDP 端口由 `ports.py` **预留**, 不是「扫到空就用」(并发启动时有竞态);
- 窗口位置按 `serial_no` 错开 (`--window-position=80+40*i,60+40*i`), 否则多窗口完全重叠,
  「多窗口」在用户眼里等于一个窗口;
- `runtime.py` 把运行态账本从 `browser.py` 拆出 —— 它要能被重启后找回, 也要能被独立测试。

### 五、字段暴露到 API 层

`visible` / `display` / `display_source` / `window_title` 暴露在 `start` 响应与
`GET /api/v1/environments` 顶层。**判定「窗口开在真实桌面还是 Xvfb」的唯一可判读信号**
是 `display_source`, 上层靠它决定要不要提示用户「你看不见」。

## Consequences

- `headless=false` 现在真的会开可见窗口 (实测 `xdotool` 可见窗口计数 2, `display=:1`,
  `display_source=desktop`);
- 探测不到桌面时行为可解释 (回落 Xvfb + `reason`), 不再静默失败;
- 多实例并发启动端口不冲突、窗口不重叠 (Lead 实测 2 个环境: 43053/43054, X=300/X=360);
- 代价: 启动路径多了一次 `xdpyinfo` 探测与 `/proc` 读取 (毫秒级), 且新增 9 个模块
  (拆模块是为了守住单文件 300 行门禁)。

## Alternatives considered

**什么都不做, 继续只支持 headless** —— 最强理由: 零改动, 且本仓旧纪律本就要求
「验证一律 headless/xvfb」。**否决**: 用户 2026-10-13 明确要「人可以直接在他们的 web ui
上操作」+「多浏览器窗口」, 这是**交付形态**而不是验证形态。headless 窗口人看不见,
方案甲的核心诉求直接落空。

**写死 `DISPLAY=:1`** —— 最强理由: 本机实测就是 `:1`, 一行常量就够。**否决**: `XAUTHORITY`
是每会话随机的, 写死 `:1` 而不给正确的 `XAUTHORITY` 会得到「Authorization required」;
而且宿主重启后 display 号可能变。`gh-pool` 的 `desktop.py` 已经为这个坑写过完整实测。

**只设 `DISPLAY`/`XAUTHORITY`, 不管 `WAYLAND_DISPLAY`** —— 最强理由: 少处理一个变量。
**否决**: Wayland 后端下窗口在 KWin 里, `xdotool` 查不到 ⇒ **无法断言「窗口真的存在」**,
于是验收只能靠「进程活着」这种弱证据 —— 而本仓明文禁止弱证据充当判据。

**每个窗口沿用同一个 CDP 端口范围扫描 (`next_port`)** —— 最强理由: 已有实现, 零新代码。
**否决**: 并发启动时两个进程可能扫到同一个空端口 (check-then-act 竞态)。改由 `ports.py`
预留分配, 并把「归还端口」也做成显式动作。

**窗口位置不偏移, 让 WM 自己摆** —— 最强理由: 零代码。**否决**: KWin 会把新窗口层叠在
同一位置, 用户看到的是「只有一个窗口」, 与「多浏览器窗口」的诉求相悖。

**把 `window_title` 静默留空** —— 最强理由: 标题是锦上添花, 抓不到就算了。**否决**:
T4 面板要靠它显示「这个窗口是什么」, 空串会让面板显示空白且**无从知道是抓失败还是真没标题**。
现改为超时后如实留空**并在 `reason` 里说明**。

## Testing

`services/hx-browser/tests/test_visible_window.py` —— **24 passed** (Lead 实跑):
窗口错开/环绕/零 serial、不可达 display 被拒、缺 `xdpyinfo` 不算 ok、headless 报不可见+原因、
无桌面回落 Xvfb 并说明、Xvfb 启动失败被报、探到桌面要求 x11 后端、`display_source` 是「种类」
而非细节、无 pid/无 display 不予断言、有标题才算可见、超时报不可见+原因、并发分配端口互异、
释放后端口回池、未运行时字段仍在、`display_source` 暴露到顶层、runtime 携带契约、
几何解析含负坐标。

端到端 (Lead 亲跑, 采集实际值):

    2 个环境并发 start -> visible=True/True, display=:1/:1, CDP 43053/43054
    xdotool: win=48234500 X=300 · win=50331652 X=360 (两个独立可见窗口)
    清理后可见窗口计数 = 0

## 关联

- 契约: `workspaces/reverse/hark.com/BROWSER-INJECT-PLAN.md` §4.1/§4.2
- Lead 代跑验收: `services/hx-browser/REPORT-T1-LEAD-VERIFIED.md`
- 集成验收: `workspaces/reverse/hark.com/INTEGRATION-VERDICT-2026-10-13.md`
- 桌面探测的原始实测 (被复用): `workspaces/browser-auto/gh-pool/server/app/desktop.py`
- 代理必须走 relay: `.agents/notes/implemented/bug-fix/2026-10-04-relay-required-for-browser-egress.md`
