# Agent Note: HX-Browser 增加 WebKit 族与 safari 内核档, 落到 WebKitGTK MiniBrowser

Status: implemented

## Problem

HX-Browser 的内核适配层 (`kernels.py`) 只有两族: blink (Chromium 系) 与 gecko (Firefox 系)。
用户要求再加一档 Safari。难点不在 UI, 而在三件事:

1. **Linux 上没有 Safari。** 能拿到的等价物是 WebKitGTK 自带的 MiniBrowser
   (本机 webkit2gtk-4.1 2.54.1, `/usr/lib/webkit2gtk-4.1/MiniBrowser`)。
   它的开关只有 `--user-agent=` / `--profile-dir=` / `--proxy=` / `--ignore-host=` /
   `--user-script-at-start=` / `--user-script-at-end=` / `--private` / `--automation` /
   `--enable-itp`, **没有 `--headless`, 没有 `--lang`, 不说 CDP**。
2. **既有启动路径默认「非 Firefox 即 CDP」。** `browser.py` 原先按
   `fingerprint.browser == "flower"` 判族, 加第三族后 Safari 会被误判成 Chromium:
   启动后去做 cookie 注入与暗色 CDP, stop 时调 `cdp.close_browser`, 全部会失败或挂起。
3. **显示后端。** 不钉 `GDK_BACKEND=x11` 时 GTK 在有 `WAYLAND_DISPLAY` 的环境里
   直接挂掉: `Gdk-Message: Error 71 (协议错误) dispatching to Wayland display.`
   而且窗口落在合成器里, 无法用 X11 (xdotool) 断言可见。

约束: `browser.py` 已 522 行 (超 300 行规矩), 净增不超过 8 行且不重排既有行;
旧环境 (`kernel` 为空) 与 Chromium/Firefox 分支行为必须逐字节不变; 不加新依赖。

## Decision

**新增 webkit 族, 内核 id 为 `safari`, 启动逻辑全部放进独立模块 `webkit.py`,
`browser.py` 只做按族分派。**

- `kernels.py`: `KERNELS` 加一行 `Kernel("safari", "Safari (WebKit)", WEBKIT, ...)`,
  可执行文件探测覆盖 MiniBrowser 的绝对路径。新增两个纯函数:
  - `speaks_cdp(env)`: 只看族, 仅 webkit 返回 False。这是本次唯一的「判族点」,
    `browser.py` 里所有 CDP 相关路径都改由它守卫, 不再各自比较 `browser == "flower"`。
  - `legacy_browser_for(kernel_id)`: kernel -> 既有 browser 字段值,
    webkit 返回 `safari`, gecko 仍返回 `flower` (`librewolf` 的既有断言不变), 其余 `sun`。
- `webkit.py` (纯函数为主, 便于单测):
  - `build_argv(env, profile_dir, port, user_script="")`: `--profile-dir`, `--user-agent`
    (环境配了就用, 没配兜底 macOS Safari UA, 绝不兜底成 Chrome UA),
    配了代理才加 `--proxy=http://127.0.0.1:<relay_port>`, 再加 `--user-script-at-start=`,
    最后追加 `launch.urls`。
    代理判据与 Chromium 相同 (`proxy_link.upstream_of`), 凭据只在本地 relay,
    不进命令行。配了代理却拿不到 relay 端口时**抛异常**, 不静默直连 (会暴露真实出口)。
  - `child_env(base, plan)`: 复制 base, 叠加 `plan.env`, 删 `plan.unset`,
    再删 `WAYLAND_DISPLAY` 并把 `GDK_BACKEND` 钉成 `x11`。顺序保证 plan 也改不回 Wayland。
  - `ensure_not_headless(env)`: `fingerprint.headless` 为真直接抛 `WebKitLaunchError`,
    文案说明 Safari 档没有 headless。
  - `resolve_executable(settings)`: `settings.kernel_paths["safari"]` (路径存在才用) >
    `kernels.detect("safari")` > 抛异常。
  - `async launch(env, profile_dir, port, plan, spawn)`: relay 端口取
    `launch_hooks.prep_of(env).relay_port`, 串起上面几步后交给 `BrowserManager._spawn`。
- `browser.py` 净增 5 行: `_spawn` 加可选 `extra_env` (唯一一处改既有签名, 在
  `plan.unset` 循环之后 update); `start()` 在 GECKO 分支前加 webkit 分派, 并用
  `speaks_cdp` 决定 `RuntimeInfo.cdp_http` 为空串; `_post_start` 开头与 stop 里
  `close_browser` 前加同一守卫。
- 前端: `main.jsx` 增加 Safari 浏览器类型与 WebKitGTK 内核选项, 选 Safari 写
  `fingerprint.kernel="safari"`, 切回 sun/flower 清成空串 (空串 = 旧环境语义);
  `localenv-shim.js` 的 `toUiProfile` / `fromUiProfile` 补 kernel 双向映射,
  否则前端写的 kernel 会被白名单吃掉, 后端收到空 kernel 仍走老路径。

### 这一档在 Linux 上是什么

safarier 是 macOS 的 Swift 应用, 靠 WebKit 私有 SPI 在原生层改设备: `DeviceSimulation.swift` 经
`SPIRuntimeBridge.installDisplayOverride` 改屏幕尺寸、P3 色域、HDR, 经 `SPIRuntimeBridge.setDouble`
调混淆存放的 `_setOverrideDeviceScaleFactor:` 改 devicePixelRatio; `FontAllowlistPlanner` 算字体白名单。
这些在 Linux 上一条都拿不到。本仓的 Safari 档是 **UA + 脚本注入的降级形态**,
跑的是本机 WebKitGTK MiniBrowser, 引擎同源但不是 Safari。

### 判族只有一处

`kernels.family_of`: `browser="safari"` 且 `kernel` 为空时等价 `kernel="safari"` (webkit),
sun / flower 旧判定不变。`speaks_cdp` 由它派生, 只有 webkit 族为 False。其它地方不许再各自判族。

### 指纹自洽: 两条容易漏的规则

- `navigator.hardwareConcurrency` 在真 WebKit 只报 4 或 8 (safarier 引
  WebKit `Source/WebCore/page/NavigatorBase.cpp`)。`mac_models.web_cpu_cores` 按
  核数 <8 报 4, 否则 8; `cpu_cores` 保留芯片规格, 只给 UI 看, 不进网页。
- WebGL `UNMASKED_RENDERER_WEBGL` 在 WebKit 写死 `"Apple GPU"`,
  `UNMASKED_VENDOR_WEBGL` 写死 `"Apple Inc."` (`WebGLRenderingContextBase.cpp`),
  所有 Mac 一样, 不按机型随机。
- 机型表 `mac_models.py` 15 个 (MacBook Pro 16 / 14、Air、iMac 24、Mac Studio、Mac mini),
  数值抄自 safarier `MacDeviceModel.swift` 的 `presets`; UA 常量的出处写在常量旁注释里。

### 注入

- MiniBrowser 的 `--user-script-at-start=PATH`, document start 执行。脚本逐字节取自
  `fingerprint.build_fingerprint_extension` 生成的 `content.js`, 同源, 不另写一份。
- 参数必须排在 URL 之前; 排在后面时进程起不来, stderr 也没有输出, 很难查。
- 生成失败、`content.js` 缺失或为空、写盘失败, 一律抛 `WebKitLaunchError`, 绝不静默起无指纹窗口。
- 已知泄漏: 脚本源自 Chromium 档, 会造出真 Safari 没有的 `window.chrome`; iframe / Worker 未实测。

### kernel_paths 语义

`Settings.kernel_paths` 是 `{内核 id: 路径}`。值为空或键不存在 = 跟随自动探测;
配了但路径不存在也回落探测。后端 `PUT /settings` 对该字段整体覆盖, 所以前端
shim 每次保存都带上完整 dict, 只改 `safari`, 清空时删键而不是写空串。

### WebKit 档没有 CDP, 也没有 headless

这是能力边界, 不是待办:

- **没有 CDP。** MiniBrowser 不实现 Chrome DevTools Protocol。启动后不做 cookie 注入
  与暗色 CDP, stop 不调 `cdp.close_browser`, `cdp_http` 为空, 依赖 CDP 的功能
  对该档不可用, 面板上应如实显示。
- **没有 headless。** MiniBrowser 没有 `--headless`。请求 headless 时拒绝启动并报错,
  不静默套一层 Xvfb 假装成功: 那样用户以为拿到了 headless, 实际行为却是有头窗口。
  需要无显示器运行时, 由调用方显式用 `xvfb-run` 之类提供 X 显示。
- 验收手法: `xvfb-run` 内钉 `GDK_BACKEND=x11` 启动, `xdotool search --pid <pid>`
  枚举到窗口; 用 `--user-script-at-start` 写 `document.title`, 从窗口标题读回 UA。

### 明确放弃的东西

1. HDR / P3 媒体查询 (`color-gamut`, `dynamic-range`): safarier 34 款机型的屏幕特性拿不到, 报宿主机真实值。
2. devicePixelRatio 原生覆盖: 没有, 只能靠注入脚本近似, 布局与图层仍按宿主机像素比。
3. 字体白名单隔离: 没有, 网页能枚举到宿主机字体。
4. sing-box 多协议代理: 没有, 本仓走自己的本地 relay, 只给 MiniBrowser 一个 HTTP 代理。
5. WebKitWebDriver 与 CDP 等价物: 没有, 见下文备选 2。

## Alternatives considered

1. **移植 safarier (https://github.com/Cd1s/safarier) 的 Swift 代码。**
   最强理由: 它已经是一个完整的「Safari 指纹浏览器」, 有 34 个 Mac 机型模拟与 sing-box
   代理集成, 看起来能省掉大量工作。
   **否决**: safarier 是 macOS 原生应用, 建立在 WKWebView + AppKit 之上, 机型模拟与
   网络栈依赖 Apple 的 SPI 私有接口, 这些在 Linux 上一律不存在, 没有可移植的层。
   真要移植等于重写一个 GTK 应用, 且要引入 Swift 工具链与 sing-box 两个新依赖,
   违反「不加新依赖」。本仓已有本地 relay 负责代理与凭据, sing-box 是重复建设。
   我们要的只是「用 WebKit 引擎带指定 UA / profile / 代理打开页面」,
   MiniBrowser 的现成开关已经覆盖。
2. **接 WebKitWebDriver (`--automation`) 做 CDP 的等价物。**
   最强理由: 能补回 cookie 注入与关闭浏览器的程序化控制。
   **否决**: WebDriver 与 CDP 是两套协议, 现有 `cdp.py` 与面板逻辑一行都复用不了,
   等于为一档再写一套驱动层; 本次范围明确不做。没有就是没有, 守卫跳过, 面板说清楚。
3. **把 WebKit 分支直接写进 `browser.py`。**
   最强理由: 少一个模块, 读启动流程时不用跳文件。
   **否决**: `browser.py` 已超 300 行规矩, 且 MiniBrowser 的参数风格与 Chromium
   完全不同, 塞进去会在 Chromium 启动路径里堆 if/else。独立模块让 `browser.py`
   只多 5 行分派, 纯函数也能脱离进程单测。
4. **继续按 `fingerprint.browser` 判族, 只给 safari 加一个特例。**
   最强理由: 不用新增 `speaks_cdp`, 改动最小。
   **否决**: 判族点已经散在 `start` / `_post_start` / `stop` 多处, 每处加特例就是三处
   各自判断, 日后再加一族又要全改一遍。集中到 `kernels.speaks_cdp` 一处,
   旧环境 (kernel 为空) 的判定结果与改动前完全相同。
5. **headless 请求时自动套 Xvfb。**
   最强理由: 对调用方透明, 「能跑起来」。
   **否决**: 静默改变语义比明确失败更糟, 调用方会基于错误前提做判断;
   拒绝并给出明确文案, 是否套 Xvfb 交给调用方显式决定。
6. **没配 UA 时不传 `--user-agent`, 用 MiniBrowser 自己的默认 UA。**
   **否决**: MiniBrowser 默认 UA 是 Linux WebKitGTK 形态, 与「Safari 档」的语义不符;
   兜底给 macOS Safari 形态 UA, 前端随机 UA 也只生成 Safari 形态。
