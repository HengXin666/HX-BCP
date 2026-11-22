# Agent Note: 防泄漏是创建环境时的默认不变量

Status: implemented

## Problem

用户需求 (2026-11-15, plan.md 红线一第 1 条):

> 把"防泄漏"变成创建环境时的默认不变量, 而不是事后检查。
> 新建环境默认 `webrtc_mode=proxy` + sandbox(有 bwrap 时) + relay, 三者同开。UI 上把"直连宿主回环"做成需要显式确认的危险选项。

改动前: `Fingerprint.webrtc_mode` 默认 `real`, `prefs.isolation` 默认 `process`, 新建环境天然带两个泄漏面, 只有 doctor 体检 (事后) 会提示, 且 `process` 在 doctor 里还报 HEALTHY。新建有 6 条入口 (POST /environments, 批量新建, 模板应用, 复制, 账号包导入, userdir 导入) 加 MCP create_profile (走同一 HTTP 路由), 各自拼 Environment, 没有共同的判据。

## Decision

### 一、判据只在 `hx_browser/leakproof.py`

- 泄漏面只有两个名字: `webrtc_real` (WebRTC 未限制, 网页经 UDP 拿真实出口) 与 `host_loopback` (未开沙箱, 网页可达宿主 127.0.0.1 且有绕过代理的直连路径)。
- relay 不单列: 沙箱档出网唯一路径就是宿主 relay; process 档自定义上游本来就一律经 relay (proxy_link.py)。所以 "三者同开" = WebRTC 守住 + 沙箱。
- `webrtc_guarded()` 按内核族如实判断, 不是 "传了参数":
  - Blink: 只有主世界 RTCPeerConnection 钩子真生效 (2026-10-10 note: Chrome 154 上 `--force-webrtc-ip-handling-policy` 零效果)。钩子开关原来只认 `fp.webrtc_mode == "proxy"`, `disable` 与 `prefs.webrtc=proxy|disable` 在未打补丁 Chrome 上其实什么都没拦, 而 doctor 却报 "已限制"。现在 `guard._fp_json_for` 的 `hide_webrtc` 改读 `leakproof.blink_hook()`, 两个字段任一守住就注钩子。
  - Gecko: 只读 user.js。原来 `fp.webrtc_mode=proxy` 在 Firefox 上不落任何偏好。现在 `launch_hooks.write_firefox` 用 `leakproof.effective_webrtc()` (prefs 优先, inherit 时取守住的 fp 值) 写 `media.peerconnection.ice.proxy_only` 等。
- 沙箱能不能开 (`sandbox_blocker`) 与 launch_hooks / display_iso 的拒绝启动条件一一对应: 缺 bwrap; 有可见桌面 + nested 却缺 Xephyr; 选了 Wayland 却没 socket。headless (环境级或全局) 不需要显示层。桌面探测是子进程, 缓存 30 s。

### 二、新建 = 默认守住, 放开 = 显式确认

- `apply_defaults(env, body)`: 请求体没写的字段才填 (没写 `webrtc_mode` 且 `prefs.webrtc` 为 inherit -> `proxy`; 没写 `prefs.isolation` -> 能开就 `sandbox`, 开不了 `process`)。请求体写了的值原样保留, 不偷偷改。
- `check(env, body, before)`: 当前每个泄漏面, 若不在 `leak_ack` (请求体) ∪ `env.leak_ack` (已存) 里, 也不是本机做不到的, 且相对 `before` 是新出现的 -> 报错拒绝。报错文案直接告诉调用方带 `leak_ack: [名字]` 或用默认值。通过后 `env.leak_ack` 只保留仍然存在的确认项。
- 本机做不到 (缺 bwrap / Xephyr) 的 `host_loopback` 不拦: 拦了等于这台机器建不了环境; 改为 `status()` 里 `unavoidable` 带原因, 列表与 doctor 如实显示。
- 入口落点:
  - POST /environments, 批量新建 (顺带补上了 base.prefs 与 apply_extras), 模板应用 (`?leak_ack=` 查询参数, 模板只能 human 通道调): apply_defaults + check(before=None)。
  - 复制: 副本带着源的 `leak_ack`, 再过一次 check; 源是没确认过的老环境时要在 body 里补 `leak_ack`。
  - 账号包导入 / userdir 导入: 配置来自文件不是人选的, 用 `force_safe` 把未确认的泄漏面拉回守住并记日志 `env.leak-guard`, 不拒绝 (老账号包里 `webrtc_mode` 恒为 real, 拒绝会让所有旧包导不进来)。导入到已有环境时只拉回新出现的泄漏面。
  - 编辑 (PUT /environments/{id}, PUT /prefs, 修复记录恢复): check(before=改之前的泄漏面)。老环境本来就有的泄漏面不追溯拦截, 改名改标签照常; 新打开的才要确认。
- `Environment.leak_ack: list[str]` 新字段, 默认空, 老记录反序列化无感。
- MCP create_profile/update 的 schema 加 `leak_ack` (其余参数原样透传到 body)。

### 三、可见性

- `env_dict` 加 `leak: {exposures, acked, unavoidable, guarded}`。
- doctor: `webrtc` 项改用 leakproof 判据; `isolation` 的 process 档从 HEALTHY 改为 WARNING, 能开沙箱时给一键修复 `{"isolation": "sandbox"}`。
- 控制台: 新建/编辑对话框 WebRTC 自动档改为 "只走代理", "真实" 标危险; 高级页加 "隔离" (自动 / 沙箱 / 直连宿主回环 (危险)); 选了危险值出现红框 "我确认放开防泄漏", 不勾不能提交, 勾上请求体带 `leak_ack`。环境列表状态格: 没全守住时出盾牌图标, 未确认的老环境红色, 已确认或本机做不到的黄色, title 写每一项与原因。

## Alternatives considered

- **直接改模型默认值** (`webrtc_mode="proxy"`, `isolation="sandbox"`): 否决。老环境里缺键的记录会被静默改行为; 而且 sandbox 能不能开取决于本机 (bwrap/Xephyr/桌面), 模型默认值表达不了; 也挡不住显式传 real。
- **把判断放进 store.add_env**: 否决。add_env 只知道成品 Environment, 分不清 "调用方没写" 与 "调用方写了 real", 就没法做到 "没写才填, 写了要确认"。
- **缺 Xephyr 时默认 `sandbox_x11=shared`**: 否决。2026-10-16 note 定死 shared 只能用户显式选; 静默共享桌面 X 等于把显示层隔离悄悄拆掉。
- **导入路径也要求确认**: 否决, 理由见上 (旧包恒 real)。

## Consequences

- 有 bwrap 的机器上新建环境默认进沙箱; 有可见桌面时需要 Xephyr, 否则自动回落 process 并在列表与 doctor 标黄说明原因。
- 显式传 `webrtc_mode=real` 或 `prefs.isolation=process` 而不带 `leak_ack` 的调用方 (脚本 / Agent) 会收到 code=-1。仓库内的 e2e 用例 (process 对照组) 已补 `leak_ack`。
- 老环境: 行为不变, 但列表会出红色盾牌, doctor 的 isolation 由绿变黄。这是有意的: 它们确实开着泄漏面。
- Blink 上 `webrtc_mode=disable` 或 `prefs.webrtc=proxy|disable` 的老环境, 下次启动起会真的注入 WebRTC 钩子 (此前是空转)。
- 未做 (plan.md 同条第二点): 出口绑定时在被测浏览器内部探测地理并回填 timezone/locale/geolocation 等人确认。现状仍是启动时按出口 IP 对齐 (match_ip / prefs ip 模式), 走的是宿主侧 relay 探测而非浏览器内部。

## Verification

- tests/test_leakproof.py: 默认守住; 显式放开需确认; 缺 bwrap 回落并说明; 有桌面缺 Xephyr 回落, headless 仍进沙箱; 编辑新打开需确认且确认可保留; 批量 / 模板 / 复制 / 账号包导入各入口; Gecko 有效策略; Blink 钩子跟随 prefs。
- tests/test_profile_api.py 的 doctor 修复用例改为显式放开 WebRTC 后再修复 (默认已守住就没得修)。
- 本地只跑了不依赖浏览器与 hxcp 的用例 (197 passed) 与 `tsc --noEmit`; 模拟 "bwrap 可用" 跑同一批用例也全部通过。e2e (真浏览器 + 沙箱) 未在本机跑。
