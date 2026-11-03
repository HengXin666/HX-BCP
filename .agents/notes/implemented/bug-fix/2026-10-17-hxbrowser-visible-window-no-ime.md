# Agent Note: HX-Browser 可见窗口打不出中文 (输入法环境缺失) + 全局关窗开关是死字段

Status: implemented

(2026-10-17)

## Problem

用户 2026-10-17 报: 「hark 还是启动 X11 窗口, 导致我连输入法都不能使用」。

拆开是**两件事**, 而它们各自都不会让任何现有检查变红:

1. **窗口是 X11 (Xwayland) 的, 而且这是刻意的** —— 不是 bug。
   `browser.py` 在 `plan.headless=false` 时 `pop WAYLAND_DISPLAY` 并钉
   `--ozone-platform=x11`。理由见
   `implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md`:
   窗口进 KWin 合成器后 `xdotool` **查不到**, 「窗口真的存在」就无法断言, 而本仓
   禁止拿「进程活着」冒充「窗口可见」。窗口本身也是用户要的 (hark 的「打开窗口」
   `visible=true` 是默认值, 见 `bridge/gwwin.py::hark_open`)。
2. **真正坏的是那个窗口收不到任何输入法** —— 一个字都打不出来。

## Evidence

### 一、服务自己的环境里没有输入法变量

面板用 `nsenter + setpriv ... env HOME=... PATH=...` 拉起 hx-browser, 于是:

    $ tr '\0' '\n' < /proc/<hx-browser 服务 pid>/environ | grep -E 'DISPLAY|XMODIFIERS|IM_MODULE|DBUS'
    (空 —— 只有 HOME / PATH / LANG 等 21 个变量)
    $ tr '\0' '\n' < /proc/<plasmashell pid>/environ | grep -E 'DISPLAY|XMODIFIERS|IM_MODULE|DBUS'
    DISPLAY=:1  XAUTHORITY=/run/user/1000/xauth_stJfap  XMODIFIERS=@im=fcitx
    GTK_IM_MODULE=fcitx  QT_IM_MODULE=fcitx  SDL_IM_MODULE=fcitx
    INPUT_METHOD=fcitx  DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus
    XDG_RUNTIME_DIR=/run/user/1000

而 `browser.py::_spawn` 是 `launch_env = dict(os.environ)` + `plan.env` - `WAYLAND_DISPLAY`
—— 浏览器继承的正是**服务**那份。改动前 `plan.env` 只有 `DISPLAY`/`XAUTHORITY`:
窗口能开、进程活着、CDP 连得上, 但 `XMODIFIERS` 缺失 ⇒ XIM 客户端根本不去连
fcitx5 (`xprop -root XIM_SERVERS` = `@server=ibus, @server=fcitx`: 服务器在, 只是没人告诉它)。

### 二、隔离实测: 只差这几个变量, 打字结果不同

隔离环境 (Xvfb :99 + kwin_x11 + 私有 dbus + 私有 fcitx5 + 本机 Chromium,
`--ozone-platform=x11`, 页面 `data:text/html,<input autofocus>`), 用 `xdotool type nihao`
+ `space`, 经 CDP 读回输入框的值:

| 浏览器进程的环境 | 输入框的值 | fcitx5 侧状态 |
|---|---|---|
| 只有 `HOME/PATH/DISPLAY/XAUTHORITY/LANG` | `"nihao "` | state=0 (**从未连接**) |
| 加上 `XMODIFIERS` / `*_IM_MODULE` / `DBUS_SESSION_BUS_ADDRESS` / `XDG_RUNTIME_DIR` | `"你好"` | state=2, 当前输入法 `pinyin` |

（`fcitx5-remote` 的 0/1/2 = 关闭/未激活/已激活; 只有带上这些变量时 fcitx5 才认为
「有一个客户端」并把它当成当前输入上下文。）

### 三、`headless_default` 是一条死字段

`grep -rn headless_default` 改动前只命中 `models.py`(声明) 与 `api.py`(接收 PUT):
**没有任何读取点**。用户在设置里打开「默认 headless」, 环境照样开可见窗口 ——
这正是「他还是启动窗口」里那个「还是」。

## Decision

1. `xenv.SESSION_KEYS` + `xenv.session_env()`: 从**桌面进程的 environ** 里按白名单取
   输入法/D-Bus 变量, 由 `desktop.resolve_display` 放进 `DisplayPlan.env` (仅 desktop 分支)。
   白名单而非整份转发: 桌面 environ 里还有代理、凭据、PATH, 照抄等于放大注入面。
   `_from_desktop_procs` 因此要把整份 environ 带回来; ②③ 两条探测路径没有 environ,
   故 `_desktop_session_environ()` 会再扫一次桌面进程 (「能不能打字」与「display 怎么
   探到的」无关)。
2. `headless_default` 真正接线: 与 `fingerprint.headless` **取或**, 判据仍只在
   `desktop.resolve_display` 一处; `DisplayPlan.headless` 成为**唯一可见性决策**,
   启动参数 (`--headless=new` vs `--ozone-platform=x11`) 跟着它走 —— 改前
   `_launch_chromium`/`_launch_firefox` 各判一次 `fingerprint.headless`, 两处不同源
   就会起一个「plan 说 headless、参数却钉 x11」的连不上 X 的 Chromium。
3. `api.manager()` 每次取用都刷新设置快照。改前它把 `BrowserManager.settings` 缓存在
   **启动时**那份上, 而 `PUT /settings` 只写盘 —— 于是「打开全局关窗开关」要重启服务
   才生效, 表现与死字段一模一样 (实测: 改完设置再起环境, 照样开窗口)。

## Consequences

- 桌面分支的窗口现在带 `XMODIFIERS=@im=fcitx` / `GTK_IM_MODULE=fcitx` / `QT_IM_MODULE=fcitx`
  / `SDL_IM_MODULE` / `INPUT_METHOD` / `DBUS_SESSION_BUS_ADDRESS` / `XDG_RUNTIME_DIR`;
  用户能在 hark 窗口里打中文 (需要重启由面板托管的那份服务才会生效)。
- 想**彻底不要窗口**: `settings.json` 里 `headless_default: true` (或
  `PUT /api/v1/settings`) —— 一键关掉所有环境的可见窗口, 不动每个环境的 fingerprint。
- headless / Xvfb 分支**不注入**这些变量 (屏幕外没人打字, 不放大注入面)。
- 代价: desktop 分支多一次 `/proc` 扫描 (毫秒级, 且只在没有 environ 的探测路径上)。

## Alternatives considered

**只设 `GTK_IM_MODULE=fcitx`** —— 最强理由: 一行搞定。**否决**: im module 要经
`DBUS_SESSION_BUS_ADDRESS` 才能找到 fcitx5; 只给模块名会在「进程起来了、输入法还是
连不上」上再踩一次, 且 XIM 路径 (`XMODIFIERS`) 仍缺。

**把桌面进程的 environ 整份转发** —— 最强理由: 零白名单维护。**否决**: 那份里含
代理/凭据/PATH 等与显示无关的东西, 等于把子进程的环境面偷偷放大; 且与本仓
「只搬目标站 cookie」那类最小化纪律相反。

**改成 Wayland 后端 (窗口原生, 走 text-input 协议)** —— 最强理由: 输入法由合成器负责,
不需要环境变量。**否决**: KWin 里的窗口 `xdotool` 看不到 ⇒ `visible` 无法断言,
而那正是 2026-10-13 那次改动的全部价值 (本仓禁止弱证据充当判据)。

**只做全局开关, 不修输入法** —— 最强理由: 用户抱怨的是「窗口」。**否决**: 「打开窗口」
是 hark 的既有能力 (手动接管 GitHub/Hark 登录要用), 关掉它等于砍功能; 用户要的是
窗口**能用**。

## Testing

- 新增 `services/hx-browser/tests/test_ime_env.py` (10 例): 白名单只挑会话变量、
  desktop 分支带上输入法变量、① 路径把 environ 带回来、③ 路径回落再扫一次、
  headless 分支不注入、全局开关强制 headless 计划 (reason 说明是哪个开关)、
  **启动参数跟着 plan 走** (`--headless=new` 与 `--ozone-platform=x11` 互斥)、
  以及 `PUT /settings` 后 `manager()` 取到的是新值。
  **fail-before 实测**: 把这份测试拿到 HEAD 的 worktree 上跑 -> 9 failed (缺
  `session_env` / `DisplayPlan.headless` / `_desktop_session_environ`, 全局开关无效)。
- 全量: `services/hx-browser` `pytest -q` → **93 passed, 2 skipped**。
- 端到端 (真起窗口, 面板那种环境: 只有 `HOME/PATH/LANG`):
  桌面分支 `visible=true`、`display=:1`、`display_source=desktop`, 子进程 env 实测含
  `XMODIFIERS=@im=fcitx` / `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus`;
  全局 `headless_default=true` 时同一路径 `visible=false`、`display_source=headless`、
  不开任何窗口。跑完即停, 不留窗口。
- 面板托管的那份服务重启后 (HX-WebX `POST /api/services/hx-browser/{stop,start}`):
  · `PUT /settings {headless_default:true}` -> 起环境得到 `display_source=headless`、
    无窗口 (证明新代码已生效, 且设置不再需要重启);
  · 起一个可见窗口后, 浏览器进程的 `/proc/<pid>/maps` 里出现
    `/usr/lib/gtk-3.0/3.0.0/immodules/im-fcitx5.so` —— GTK 只在 `GTK_IM_MODULE=fcitx`
    时才加载它, 这是「输入法环境真的进了那个窗口」的**现场证据**
    (该进程 `dumpable=0`, `/proc/<pid>/environ` 读出来是全 NUL, 不能用它取证)。

## 关联

- 可见窗口与多实例: `implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md`
- hark 侧开窗契约: `console/hark/src/windows.ts` / `bridge/gwwin.py::hark_open`
- 单服务单一归属 (面板托管 8325): `implemented/architecture/2026-10-11-single-owner-per-service.md`
