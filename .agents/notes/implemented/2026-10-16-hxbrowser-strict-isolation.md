# Agent Note: HX-Browser 沙箱补齐网络 / 显示 / 字体 / seccomp 隔离, 支持 Wayland

Status: implemented

## Problem

上一轮 (2026-10-16-hxbrowser-profile-ops-nodecloak) 的 bwrap 沙箱如实写明「网络与 X11
共享、内核共享」。用户 2026-10-16 明确要求: 网络与 X11 也要隔离, 系统字体要隔离,
检查窗口系统 (应支持 Wayland), 并用仓库自带的 bot / 指纹检测工具 (toolbox/fp-audit)
跑出实测值。

共享的代价是具体的: 共享网络命名空间时, 副本里的网页能访问宿主 127.0.0.1 上的任何
服务, WebRTC/UDP 与不走代理的连接仍有直连路径; 共享 X server 时, X11 协议允许任意
客户端读取他人窗口与全局键盘; 字体目录全可见时, 字体枚举就是一项稳定的设备指纹。

## Decision

### 一、网络: 独立网络命名空间 + 两条 Unix socket (`sandbox.py`/`sandbox_shim.py`/`bridge.py`/`relay.py`)
bwrap 加 `--unshare-net`, 沙箱里只有回环口、没有任何网卡。出入口只剩两条:

    浏览器 -> 沙箱内 shim 127.0.0.1:3128 -> proxy.sock -> 宿主 relay -> 上游
    宿主 CDP 客户端 -> 宿主 bridge 127.0.0.1:<cdp> -> cdp.sock -> shim -> 沙箱内 CDP

shim 只用标准库, 以宿主基础解释器 `-S -E` 运行 (沙箱内看不到 venv; 复制到运行目录而
非绑包目录, 因为包里的 locale.py 会遮住标准库)。direct / system 副本也由宿主 relay
代为出网 (relay 新增 `direct` 上游), 行为与不开沙箱一致。bridge 与 relay 同生共死,
挂在 relay.stop 上, 所有既有停止路径无需改动; RuntimeInfo.cdp_http 端口号不变,
MCP 与既有 CDP 客户端无感知。沙箱 relay 只开仅属主可连的 Unix socket, 宿主上没有
可借用的 TCP 入口。

### 二、显示: 只给本副本专属的那一个 server (`display_iso.py`)
不再绑整个 `/tmp/.X11-unix`: headless 不给显示; Xvfb 只绑它那一个 socket 文件; 有桌面
时默认 Xephyr 嵌套 (`sandbox_x11=nested`), 用户显式选 `shared` 才共享桌面 X 并在
doctor 里标出。X11 抽象 socket 属于网络命名空间, unshare-net 后天然不可达。

### 三、Wayland (`prefs.window_system`)
默认 auto = 改动前行为 (有 X/XWayland 就钉 x11, 因窗口断言依赖 xdotool)。选 wayland:
Chromium `--ozone-platform=wayland`、Firefox `MOZ_ENABLE_WAYLAND=1`, process 模式传
compositor socket 绝对路径, 沙箱只绑这一个 socket 到私有 XDG_RUNTIME_DIR。代价如实:
原生 Wayland 窗口不能被外部定位, focus 接口明确返回失败而不是假装成功。

### 四、字体 (`fontiso.py`, 所有内核)
`font_isolation=baseline|custom` 时生成私有 fonts.conf (只含一个软链目录) 并以
FONTCONFIG_FILE 启动; 沙箱里再用 tmpfs 遮蔽系统字体目录, 只把白名单文件按原路径绑回。
中文字体总是保留 (用户原话要求; 缺了中文变方块本身就是异常信号)。

### 五、seccomp 拒绝表 (`seccomp_bpf.py`)
shim 在 exec 浏览器前装 BPF 过滤器: 挂载、内核模块、bpf、userfaultfd、io_uring、keyctl、
ptrace、kexec 等返回 EPERM, 架构不符 / x32 ABI 直接杀进程。x86_64 与 aarch64 共用一张
名字表。浏览器自身的渲染进程沙箱 (需要 user namespace) 不受影响。

## Alternatives considered

- **slirp4netns / pasta 给沙箱一张用户态网卡** — 否决: 那仍是一条通用出网路径, 只是换了
  形态; 我们要的是「除代理外无路可走」。Unix socket 只能连到 relay。
- **seccomp 白名单** — 否决: 要随浏览器版本追系统调用, 一漏就是启动即崩; 浏览器自己已
  对渲染进程做白名单, 外层做拒绝表补「不该出现」的那批。
- **bwrap --disable-userns** — 否决: 会迫使 Chromium 关掉自己的渲染进程沙箱
  (--no-sandbox), 等于拆掉一层更强的隔离去换一层更弱的。
- **缺 Xephyr / Wayland socket 时退回共享桌面** — 否决: 用户会以为显示层已隔离。拒绝启动。
- **用 gVisor / microVM 防内核级逃逸** — 本轮不做: 依赖重、与 GPU/显示集成复杂;
  文档如实写明 seccomp 只缩小内核攻击面, 不消灭它。

## Consequences

新增 6 个模块 (均 <300 行) 与 2 个测试文件; browser.py 只改一行 (prepare 多传显示方案
与 CDP 端口)。真浏览器实测 (Chromium 155 + Firefox ESR 153, aarch64): 沙箱里浏览器的
net 命名空间与宿主不同、访问宿主回环服务失败而经代理成功、只看得见本副本 X socket、
白名单外字体测不到而中文字体在、/proc/<pid>/status 的 Seccomp=2; Wayland 用无头
weston 验证 process 与 sandbox 两档。

fp-audit 跑通过程中抓到一个真缺陷: shim 的 unix->TCP 转发沿用了 create_connection 的
5 秒超时作为读超时, 空闲的 CDP WebSocket 5 秒即被断开 (creepjs 等待期间报
"Connection to remote host was lost")。已修并加回归用例。

PR 自审补一条: 运行目录在共享的 /tmp 下, 若其他用户抢先建同名目录或软链, 就能预置
socket 劫持 CDP 或代理入口。现在要求该目录不是软链且属于当前用户, 否则拒绝启动。

仍然共享: 宿主内核 (被允许的系统调用里的内核漏洞仍可能逃逸), 以及默认可见的 GPU 设备
(prefs.sandbox_gpu=false 可拿掉, 代价是 WebGL 指纹变成软件渲染)。

## Testing

- `uv run pytest tests/` — 新增 test_isolation.py (seccomp 程序形状与子进程实测 EPERM、
  bwrap 参数、显示/Wayland/字体/出口/relay unix+direct/shim 转发与空闲回归)。
- `HXBR_E2E_CHROMIUM=/usr/bin/chromium HXBR_E2E_SANDBOX=1 uv run pytest tests/test_e2e_isolation.py tests/test_e2e_browser.py`
  — 网络/显示/字体/seccomp/Wayland 实测; 对照组 (process 模式) 同样探针可达宿主服务、
  可测到全部字体, 证明断言有区分力。
- `toolbox/fp-audit/audit.py --endpoint <副本 CDP> --expect-tz auto` — process 与 sandbox
  两档对比, 结果写入 PR。
