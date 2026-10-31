"""浏览器进程管理（对应 PRD F1/F6）：隔离启动、可见窗口、优雅停止。

# Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

职责切分 (每文件 <=300 行; 本文件与 api.py 是既有存量, 棘轮登记在册只许降不许涨):

    runtime.py   运行态账本 / 孤儿进程 / 僵尸回收 / 落盘接管
    ports.py     CDP 端口分配与预留
    cookies.py   cookie 解析与注入
    desktop.py   用户真实桌面在哪 (**唯一判据**)
    xvfb.py      探不到桌面时的虚拟屏回落
    window.py    窗口断言 + 窗口位置错开
    本文件       怎么起/怎么停一个浏览器, 以及把上面几件事接起来

★ 可见窗口 (2026-10-13, 用户要求「人可以直接在他们的 web ui 上操作」):

    fingerprint.headless=false -> 探到真实桌面就开**可见**窗口; 探不到回落 Xvfb
    并在响应里**明说** visible=false + reason。

**绝不假装窗口可见**: visible 的唯一真值来源是 window.window_verdict 的 xdotool
断言, 且必须**同时**满足「窗口映射了」与「那个 X 是用户桌面」—— 在 Xvfb 上枚举到
窗口**不算**(那是屏幕外, 用户看不见)。headless 分支连 X 都没有, 不做断言。
"""

from __future__ import annotations

import asyncio
import logging
import os
import signal
import subprocess
import time
from pathlib import Path

from . import cdp, desktop, guard, kernels, launch_hooks, relay, window, xvfb

# parse_cookies 是 api.py 的既有入口, 在这里再导出保持导入路径不变
from .cookies import _inject_firefox_cookies, parse_cookies  # noqa: F401
from .fingerprint import build_fingerprint_extension, build_firefox_userjs
from .models import Environment, RuntimeInfo, Settings, now_ms
from .ports import clear_reserved, next_port, release_port

# 运行态账本与孤儿管理已拆到 runtime.py; 这里再导出, 让 api.py / tests 的既有
# 导入路径 (from .browser import RUNTIME, scan_orphans, ...) 原样可用。
from .runtime import (  # noqa: F401
    _PROCS,
    RUNTIME,
    cleanup_orphans,
    get_runtime,
    load_persisted_runtime,
    pid_alive,
    reap_finished,
    refresh_runtime,
    runtime_path,
    save_runtime,
    scan_orphans,
    start_reaper,
)

# 旧名别名: runtime.py 里是公开名 (pid_alive/save_runtime/start_reaper), 而
# browser.py 与 tests/test_lifecycle.py 的既有调用点用的是下划线名。
# 保留别名而不改调用点, 是为了让「搬家」不混进「改名」—— 后者会让 diff 淹没真正的改动。
_pid_alive = pid_alive
_save_runtime = save_runtime
_start_reaper = start_reaper

log = logging.getLogger("hx_browser.browser")

# 窗口断言任务 (env_id -> Task)。为什么单独记一份: start() 的 HTTP 响应要等
# **真值**才能说 visible, 而断言是异步的 —— 没有这个句柄就只能靠 sleep 猜,
# 那是"传了参数就以为生效"的另一种形态。
_WINDOW_TASKS: dict[str, "asyncio.Task[None]"] = {}


async def wait_window_verdict(env_id: str, timeout: float = 25.0) -> bool:
    """等该环境的窗口断言出结果。返回是否在超时前拿到。

    超时**不报错**: 拿不到真值时 visible 保持 false + reason 说明 ——
    「没断言到」与「断言为不可见」都该如实报不可见, 绝不乐观默认为可见。
    """
    task = _WINDOW_TASKS.get(env_id)
    if task is None:
        return False
    try:
        await asyncio.wait_for(asyncio.shield(task), timeout=timeout)
        return True
    except Exception:  # noqa: BLE001 — 超时与断言异常都按"没拿到真值"处理
        return False


def proxy_args(env: Environment) -> list[str]:
    """构造 --proxy-server 参数 (旧接口, 仅为外部 import 兼容保留)。

    启动路径已改走 launch_hooks.chromium_args + 本地 relay: Chromium 不解析这里
    内嵌的 user:pass (见 proxy_link.py 模块头), 带凭据的代理用它必然 407。
    """
    p = env.proxy
    if not p or p.type == "no_proxy" or not p.host:
        return []
    scheme = (
        "socks5" if p.type == "socks5" else ("https" if p.type == "https" else "http")
    )
    if p.username:
        url = f"{scheme}://{p.username}:{p.password}@{p.host}:{p.port}"
    else:
        url = f"{scheme}://{p.host}:{p.port}"
    return ["--proxy-server=" + url]


class BrowserManager:
    def __init__(self, settings: Settings, profiles_dir: Path):
        self.settings = settings
        self.profiles_dir = profiles_dir

    def env_profile_dir(self, env: Environment) -> Path:
        return self.profiles_dir / env.id

    def _dark_enabled(self, env: Environment) -> bool:
        """暗色主题：环境级覆盖全局。"""
        if env.fingerprint.dark_mode is not None:
            return bool(env.fingerprint.dark_mode)
        return bool(self.settings.dark_mode)

    # ---------- 启动 ----------
    async def start(self, env: Environment) -> RuntimeInfo:
        refresh_runtime()
        if env.id in RUNTIME:
            raise RuntimeError(f"环境 {env.name} 已在运行")
        index = max(env.serial_no - 1, 0)
        port = next_port(self.settings.cdp_base_port, index)
        profile_dir = self.env_profile_dir(env)
        profile_dir.mkdir(parents=True, exist_ok=True)

        # 显示方案**只在这里拍板一次** (desktop.resolve_display 是唯一判据, 含全局关窗开关)。
        plan = desktop.resolve_display(
            env, headless_default=self.settings.headless_default)

        try:
            # 启动视图: 语言/时区已替换为实际生效值, 代理 relay 已就绪 (见 launch_hooks)
            prep = await launch_hooks.prepare(env, profile_dir, plan, port)
            launch = prep.env
            if kernels.family_of(env) == kernels.GECKO:
                pid, executable = await self._launch_firefox(launch, port, profile_dir, plan)
            else:
                pid, executable = await self._launch_chromium(launch, port, profile_dir, plan)
        except Exception:
            release_port(port)
            xvfb.release(env.id)
            await relay.stop(env.id)
            raise

        info = RuntimeInfo(
            pid=pid,
            debug_port=port,
            profile_dir=str(profile_dir),
            executable=executable,
            started_at=now_ms(),
            cdp_http=f"http://127.0.0.1:{port}",
            visible=plan.visible,
            display=plan.display,
            xauth=plan.xauth,
            display_source=plan.source,
            display_source_detail=plan.source_detail,
            reason=plan.reason,
            kernel=launch_hooks.kernel_id(env),
            relay_port=prep.relay_port,
            isolation=env.prefs.isolation,
            config_hash=prep.config_hash,
        )
        RUNTIME[env.id] = info
        _save_runtime()  # 启动即落盘: 服务重启才能找回这条账

        # 后台：窗口断言 + 等 CDP 就绪 + Cookie 注入 + 登录态检查
        _WINDOW_TASKS[env.id] = asyncio.create_task(self._assert_window_safe(env, info))
        asyncio.create_task(self._post_start(env, info))
        return info

    async def _assert_window_safe(self, env: Environment, info: RuntimeInfo) -> None:
        """窗口断言的**兜底包装**: 断言本身炸了也只让 visible=false + reason。

        不加这层的话异常会逃进 asyncio 的「Task exception was never retrieved」,
        而响应里 visible 还是 plan 的**意图值** —— 那就等于假装窗口可见。
        """
        try:
            await self._assert_window(env, info)
        except Exception as e:  # noqa: BLE001
            info.visible = False
            info.reason = info.reason or ("窗口断言异常: %s" % e)
            log.warning("window assert for %s failed: %s", env.id, e)
        # ★ 断言结果**必须落盘** (2026-10-13 实测缺口): 落盘发生在 start() 里,
        #   那时 visible 还是 plan 的**意图值** (True)。断言把它改成真值后若不重写,
        #   runtime.json 会留着一个假的 visible=True —— 服务一重启接管, 假值就复活,
        #   而这次没有任何窗口断言会再纠正它。实测: 第二个环境的 window_title 与
        #   visible 在 runtime.json 里与 API 返回**不一致**。
        _save_runtime()

    async def _assert_window(self, env: Environment, info: RuntimeInfo) -> None:
        """窗口断言 —— visible 字段的**唯一真值来源** (2026-10-13)。

        为什么不能拿 plan.visible 当结论: 那是**意图** (探到了桌面), 不是事实。
        gh-pool 2026-10-02 实测过二者不一致 —— 进程活着 / 截图正常 / 登录态也对,
        桌上就是没窗口 (Wayland 后端那次)。故必须 xdotool 真枚举一次。

        ★ 判据是**合取**, 两个条件都必须成立 (缺一就变成假装可见):

            ① 窗口真映射在 X 上   xdotool --onlyvisible --pid <主进程> 有非空标题
            ② 那个 X 是用户桌面   display_source == "desktop"

        为什么 ② 不能省: 回落到 Xvfb 时, 在 **Xvfb 那个屏**上跑 xdotool 照样能
        枚举到窗口 —— 只看 ① 就会把「屏幕外的窗口」报成 visible=true, 正是
        本任务明令禁止的「假装窗口可见」。Xvfb 分支保留 window_title 作证据
        (证明窗口确实存在), 但 visible 恒为 False。

        headless 分支连 X 都没有, 不做断言, reason 由 resolve_display 给出。
        """
        if not info.display:
            if not info.reason:
                info.reason = "本次启动没有 X display, 无法断言窗口可见性"
            return
        mapped, title, why, win_id = await asyncio.to_thread(
            window.window_verdict, info.pid, info.display, info.xauth
        )
        info.window_title = title
        info.window_id = win_id
        # 强制摆位: --window-position 在 KDE/KWin 下**传了不生效** (实测三个环境
        # 请求不同坐标却都落到合成器的智能摆放位)。窗口映射后补一次 windowmove,
        # 多实例才真的错开 —— 否则"错开"只是 cmdline 里的一个字符串。
        if win_id and info.display_source == "desktop":
            wx, wy = window.window_position(env.serial_no)
            moved = await asyncio.to_thread(
                window.move_window, win_id, wx, wy + window.TITLEBAR_H,
                info.display, info.xauth,
            )
            got = await asyncio.to_thread(
                window.window_geometry, win_id, info.display, info.xauth
            )
            info.window_pos = list(got) if got else []
            log.info("env=%s window move to (%s,%s) ok=%s actual=%s",
                     env.id, wx, wy + window.TITLEBAR_H, moved, got)
        if info.display_source == "desktop":
            info.visible = bool(mapped)
            info.reason = "" if mapped else (why or "窗口未映射到用户桌面")
        else:
            # Xvfb: 窗口可能真的存在, 但**用户看不见** —— visible 必须为 False
            info.visible = False
            tail = (
                "窗口已映射到 %s (存在性成立), 但那是虚拟屏, 用户看不见" % info.display
                if mapped
                else (why or "窗口未映射")
            )
            info.reason = ("%s; %s" % (info.reason, tail)) if info.reason else tail
        log.info(
            "env=%s window verdict visible=%s mapped=%s title=%r display=%s",
            env.id,
            info.visible,
            mapped,
            title,
            info.display,
        )

    async def _post_start(self, env: Environment, info: RuntimeInfo) -> None:
        try:
            if env.fingerprint.browser == "flower":
                # Firefox CDP 兼容性弱：Cookie 走 sqlite（启动前已注入，见 _inject_firefox_cookies）
                pass
            else:
                if self._dark_enabled(env):
                    await self._apply_dark_mode_cdp(info.debug_port)
                # Chrome 136+ 收紧、154 移除 --load-extension, 扩展里的伪装全空转,
                # 指纹只能走 CDP 主世界注入这条官方通道
                await guard.inject_fingerprint(
                    env, info.debug_port, dark=self._dark_enabled(env), profile_dir=self.env_profile_dir(env)
                )
                cookies = parse_cookies(env.account.cookies)
                if cookies:
                    n = await cdp.set_cookies(info.debug_port, cookies)
                    log.info("env=%s injected %d cookies", env.id, n)
                # 登录态检查
                try:
                    all_cookies = await cdp.get_cookies(info.debug_port, timeout=15)
                    if all_cookies:
                        now = time.time()
                        expired = any(
                            c.get("expires", 0) and 0 < c["expires"] < now
                            for c in all_cookies
                        )
                        info.login_state = "expired" if expired else "ok"
                    else:
                        info.login_state = "empty"
                except Exception:
                    info.login_state = "unknown"
        except Exception as e:  # noqa: BLE001
            log.warning("post-start for %s failed: %s", env.id, e)
            info.login_state = "unknown"

    async def _apply_dark_mode_cdp(self, debug_port: int) -> None:
        """CDP 模拟 prefers-color-scheme: dark + 强制网页暗色渲染。"""
        try:
            ws_url = await cdp.get_browser_ws(debug_port, timeout=6)
            conn = await cdp.CdpConnection(ws_url).connect(timeout=8)
            try:
                # 找到首个 page target 做 emulation（Emulation 是 session 级，作用于 page）
                page_ws = await cdp.find_page_ws(debug_port, timeout=10)
                conn2 = await cdp.CdpConnection(page_ws).connect(timeout=8)
                try:
                    await conn2.send(
                        "Emulation.setEmulatedMedia",
                        {
                            "features": [
                                {"name": "prefers-color-scheme", "value": "dark"}
                            ]
                        },
                    )
                    await conn2.send(
                        "Emulation.setAutoDarkModeOverride", {"enabled": True}
                    )
                finally:
                    await conn2.close()
            finally:
                await conn.close()
            log.info("env dark mode applied on port %s", debug_port)
        except Exception as e:  # noqa: BLE001
            log.warning("apply dark mode cdp failed: %s", e)

    async def _launch_chromium(
        self, env: Environment, port: int, profile_dir: Path, plan: desktop.DisplayPlan
    ) -> tuple[int, str]:
        exe = kernels.resolve_executable(env, self.settings)
        if not exe or not Path(exe).exists():
            raise RuntimeError(
                f"Chromium 可执行文件不存在: {exe or '(未配置)'}，请在设置中配置"
            )
        fp = env.fingerprint
        args = [
            f"--user-data-dir={profile_dir}",
            f"--remote-debugging-port={port}",
            # Chrome 默认只监听 [::1]; 本服务与 cdp.py 都按 127.0.0.1 连, 不钉住地址会
            # 出现 "no page target" (实测 2026-10-17)。钉 IPv4 与 cdp.py 的判据一致。
            "--remote-debugging-address=127.0.0.1",
            # 必须加: 不加则 CDP 的 WebSocket 升级带 origin 校验, 会以
            # 403 Forbidden 拒绝所有非 Chrome 自带客户端 (实测 hxcp 直连 43042 就是这样),
            # 而本服务只绑 127.0.0.1, 放开 origin 不引入新的可达面。
            "--remote-allow-origins=*",
            "--no-first-run",
            "--no-default-browser-check",
            "--disable-sync",
            "--disable-background-mode",
            "--disable-blink-features=AutomationControlled",
            "--disable-features=Translate,OptimizationGuideOnDeviceModel",
        ]
        # 可见性只认 plan (desktop.resolve_display 是唯一判据, 见 DisplayPlan.headless)。
        if plan.headless:
            args.append("--headless=new")
        else:
            # ★ 有 X 时必须钉死 x11 后端 (2026-10-02 gh-pool 实测): 留着 WAYLAND_DISPLAY
            #   Chromium 就走 Wayland, xdotool 查不到窗口 -> 「窗口真的存在」无法断言。
            args.append("--ozone-platform=x11")
            wx, wy = window.window_position(env.serial_no)
            # 位置错开: 多实例并发时窗口不重叠, 否则用户只看得见最上面那个
            args.append("--window-position=%d,%d" % (wx, wy))
        if self._dark_enabled(env):
            # 浏览器 UI + 网页内容强制暗色（配合启动后 CDP Emulation 双保险）
            args.append("--force-dark-mode")
        res = fp.resolution or "1280x820"
        args.append(f"--window-size={res}")
        ua = guard.effective_user_agent(env, exe)  # 不填时按内核版本补正常 UA, 否则无头默认带 HeadlessChrome
        if ua:
            args.append(f"--user-agent={ua}")
        if fp.language:
            args.append(f"--lang={fp.language.split(',')[0]}")
        if fp.webrtc_mode in ("proxy", "disable"):
            args.append("--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
        args.extend(launch_hooks.chromium_args(env))  # 代理走本地 relay, 凭据不进命令行
        launch_hooks.write_chromium(env, profile_dir)
        # 指纹扩展
        ext_dir = build_fingerprint_extension(
            env, profile_dir, dark=self._dark_enabled(env)
        )
        args.append(f"--load-extension={ext_dir}")
        for ext in env.launch.extensions:
            if Path(ext).exists():
                args.append(f"--load-extension={ext}")
        args.extend(env.launch.extra_args or [])
        args.extend(env.launch.urls or [])
        return self._spawn(exe, args, env, plan)

    async def _launch_firefox(
        self, env: Environment, port: int, profile_dir: Path, plan: desktop.DisplayPlan
    ) -> tuple[int, str]:
        from .account_pack import effective_profile_dir

        exe = kernels.resolve_executable(env, self.settings)
        if not exe or not Path(exe).exists():
            raise RuntimeError(
                f"Firefox 可执行文件不存在: {exe or '(未配置)'}，请在设置中配置"
            )
        # 导入的 profile（register 机 userdir）可能挂在子目录/软链，取实际位置
        profile_dir = effective_profile_dir(profile_dir)
        profile_dir.mkdir(parents=True, exist_ok=True)
        build_firefox_userjs(env, profile_dir, dark=self._dark_enabled(env))
        launch_hooks.write_firefox(env, profile_dir)  # 托管块追加在既有 user.js 之后
        _inject_firefox_cookies(env, profile_dir)
        args = [
            "-no-remote",
            "-profile",
            str(profile_dir),
            "--width",
            str(1280),
            "--height",
            str(820),
            "--remote-debugging-port",
            str(port),
        ]
        if plan.headless:
            args.append("--headless")
        args.extend(env.launch.extra_args or [])
        args.extend(env.launch.urls or [])
        return self._spawn(exe, args, env, plan)

    def _spawn(
        self, exe: str, args: list[str], env: Environment, plan: desktop.DisplayPlan
    ) -> tuple[int, str]:
        """起进程。显示环境由 plan **显式钉死** —— 不依赖继承来的环境。

        两件事都是硬要求 (缺任一条都会让「窗口可见」变成猜测):
          · 注入 plan.env (DISPLAY / XAUTHORITY) —— 服务进程自己没有这些;
          · pop plan.unset (WAYLAND_DISPLAY) —— 留着它 Chromium 就走 Wayland 后端。
        """
        launch_env = dict(os.environ)
        for k, v in plan.env.items():
            launch_env[k] = v
        for k in plan.unset:
            launch_env.pop(k, None)
        if env.fingerprint.timezone:
            launch_env["TZ"] = env.fingerprint.timezone
        launch_env.update(launch_hooks.process_env(env))  # 托管语言时 LANG 与之对齐 (Intl locale)
        proc = subprocess.Popen(
            launch_hooks.wrap_argv(env, [exe, *args], launch_env),
            env=launch_env,
            start_new_session=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            stdin=subprocess.DEVNULL,
        )
        # 留着 Popen 引用 + 起回收线程, 否则进程退出后没人 wait() -> 僵尸。
        # start_new_session=True 让子进程自成进程组, stop() 才能 killpg 整组。
        _PROCS[env.id] = proc
        _start_reaper()
        return proc.pid, exe

    # ---------- 停止 ----------
    async def stop(self, env: Environment, timeout: float = 10.0) -> bool:
        try:
            return await self._stop(env, timeout)
        finally:
            await relay.stop(env.id)  # 副本停了, 它的本地代理入口也必须关 (不留可借用的凭据)

    async def _stop(self, env: Environment, timeout: float = 10.0) -> bool:
        info = RUNTIME.get(env.id)
        proc = _PROCS.get(env.id)
        # 已退出/僵尸的进程: 直接 reap 并清账, 不做无谓的等待与 kill。
        # (原实现这里判活为真 -> 空等满 10s -> killpg 对僵尸无效 -> 永远显示运行中)
        if info and info.pid and not _pid_alive(info.pid):
            if proc is not None:
                try:
                    proc.wait(timeout=1)
                except Exception:  # noqa: BLE001
                    pass
            _PROCS.pop(env.id, None)
            RUNTIME.pop(env.id, None)
            release_port(info.debug_port)
            xvfb.release(env.id)  # 不留屏幕外的垃圾 Xvfb
            _save_runtime()
            return True
        if not info or not info.pid:
            RUNTIME.pop(env.id, None)
            _save_runtime()
            return False
        # 优先 CDP 优雅关闭
        closed = await cdp.close_browser(info.debug_port, timeout=4)
        deadline = time.time() + timeout
        while time.time() < deadline:
            if not _pid_alive(info.pid):
                if proc is not None:
                    try:
                        proc.wait(timeout=1)  # reap, 否则留僵尸
                    except Exception:  # noqa: BLE001
                        pass
                _PROCS.pop(env.id, None)
                RUNTIME.pop(env.id, None)
                release_port(info.debug_port)
                xvfb.release(env.id)
                _save_runtime()
                return True
            await asyncio.sleep(0.3)
        # 进程组 SIGTERM 兜底
        try:
            os.killpg(os.getpgid(info.pid), signal.SIGTERM)
        except ProcessLookupError:
            pass
        RUNTIME.pop(env.id, None)
        release_port(info.debug_port)
        xvfb.release(env.id)
        _save_runtime()
        return True

    def stop_all(self) -> None:
        for env_id in list(RUNTIME.keys()):
            info = RUNTIME[env_id]
            proc = _PROCS.get(env_id)
            try:
                if _pid_alive(info.pid):
                    os.killpg(os.getpgid(info.pid), signal.SIGTERM)
            except Exception:  # noqa: BLE001
                pass
            # 无论 kill 是否成功都要 reap: 已经是僵尸的进程 kill 不掉,
            # 但 wait() 能回收它, 否则关服务后留下一堆 defunct。
            if proc is not None:
                try:
                    proc.wait(timeout=2)
                except Exception:  # noqa: BLE001
                    pass
        _PROCS.clear()
        RUNTIME.clear()
        clear_reserved()
        for env_id in list(xvfb.PROCS):
            xvfb.release(env_id)
        _save_runtime()
