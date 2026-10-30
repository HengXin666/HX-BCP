"""WebKit 档 (safari 内核) 启动逻辑, Linux 上落到 WebKitGTK MiniBrowser.

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 职责

    launch              browser.start 的 WebKit 分支入口, 串起下面三个并交给 spawn
    user_script_path    注入准备: 复用 fingerprint.build_fingerprint_extension 的 content.js, 落成 user script
    build_argv          纯函数, 环境 -> MiniBrowser 参数 (不含可执行文件本身)
    child_env           纯函数, 基础环境 + DisplayPlan -> 子进程环境
    resolve_executable  settings.kernel_paths["safari"] > kernels.detect("safari") > 抛异常

## 为什么与 Chromium/Firefox 分开

WebKit 族不说 CDP (kernels.speaks_cdp 返回 False), 也没有 headless 模式,
参数风格与 Chromium 完全不同. 放在独立模块里, browser.py 只按族分派, 不在
Chromium 启动路径里塞 if/else.

## 代理

与 Chromium 同一判据: proxy_link.upstream_of(env.proxy) 非 None 才算配了代理,
此时一律指向本地 relay (http://127.0.0.1:<relay_port>), 凭据不进命令行.
配了代理却拿不到 relay 端口时直接抛异常, 不静默直连 (那会泄露真实出口 IP).

## browser-gate

本仓门禁只允许 Chromix/CloakBrowser. 用户 2026-10-16 明确要求兼容多种内核,
故涉及 safari 内核 id 与 Safari UA 的行逐行带 allow-legacy 标注, 可审计.
"""

from __future__ import annotations

import os
from pathlib import Path

from . import fingerprint, kernels, launch_hooks
from .proxy_link import upstream_of

K_GDK_BACKEND = "GDK_BACKEND"
K_WAYLAND = "WAYLAND_DISPLAY"
RELAY_HOST = "127.0.0.1"
USER_SCRIPT_NAME = "hx-fingerprint.user.js"

# fmt: off
KERNEL_ID = "safari"  # browser-gate: allow-legacy 内核 id
# 环境没配 UA 时的兜底: macOS Safari 17 的真实 UA
DEFAULT_SAFARI_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15"  # browser-gate: allow-legacy 兜底 UA
# fmt: on


class WebKitLaunchError(RuntimeError):
    """WebKit 档无法启动 (headless 请求, 找不到可执行文件, 代理没有 relay 端口)."""


def ensure_not_headless(env) -> None:
    """fingerprint.headless 为真时抛异常. WebKitGTK MiniBrowser 没有 headless 模式."""
    if bool(getattr(env.fingerprint, "headless", False)):
        raise WebKitLaunchError(
            "Safari 档没有 headless 模式, 请关闭该环境的 fingerprint.headless 后再启动"
        )


def user_agent_of(env) -> str:
    """环境配了 UA 就用, 没配兜底 Safari UA."""
    ua = (getattr(env.fingerprint, "user_agent", "") or "").strip()
    return ua or DEFAULT_SAFARI_UA


def proxy_url(env, port: int) -> str:
    """配了代理返回本地 relay 地址, 没配返回空串.

    配了代理但 port 无效时抛异常, 宁可起不来也不直连.
    """
    if upstream_of(getattr(env, "proxy", None)) is None:
        return ""
    if not port or int(port) <= 0:
        raise WebKitLaunchError(
            "环境配了代理, 但本地 relay 端口无效 (port=%r), 拒绝直连启动" % (port,)
        )
    return "http://%s:%d" % (RELAY_HOST, int(port))


def user_script_path(env, profile_dir) -> str:
    """注入准备: 调 build_fingerprint_extension 生成 content.js, 原样写成 profile_dir 下的 user script.

    同一来源: 文本逐字节取自 Chromium 扩展的 content.js, 不在这里另写指纹脚本.
    生成抛异常 / 落盘失败 / 文件为空, 一律抛 WebKitLaunchError, 不起没有指纹的窗口.
    """
    dark = bool(getattr(env.fingerprint, "dark_mode", False))
    try:
        ext_dir = fingerprint.build_fingerprint_extension(env, Path(profile_dir), dark)
        data = (Path(ext_dir) / "content.js").read_bytes()
        out = Path(profile_dir) / USER_SCRIPT_NAME
        out.write_bytes(data)
        size = out.stat().st_size
    except Exception as exc:
        raise WebKitLaunchError("指纹注入脚本准备失败, 拒绝启动: %s" % exc) from exc
    if not data or size <= 0:
        raise WebKitLaunchError("指纹注入脚本为空 (%s), 拒绝启动" % out)
    return str(out)


def build_argv(env, profile_dir, port: int, user_script: str = "") -> list[str]:
    """纯函数: 环境 -> MiniBrowser 参数 (不含可执行文件本身).

    顺序: --profile-dir, --user-agent, [--proxy], [--user-script-at-start], launch.urls.
    port 是本地代理 relay 端口; 没配代理时忽略. user_script 是 user_script_path 的返回值.
    --user-script-at-start 必须排在 URL 之前: 排在后面时 MiniBrowser 起不来且 stderr 无输出.
    """
    ensure_not_headless(env)
    argv = [
        "--profile-dir=%s" % Path(profile_dir),
        "--user-agent=%s" % user_agent_of(env),
    ]
    url = proxy_url(env, port)
    if url:
        argv.append("--proxy=%s" % url)
    if user_script:
        argv.append("--user-script-at-start=%s" % user_script)
    argv.extend(list(getattr(env.launch, "urls", None) or []))
    return argv


def child_env(base, plan) -> dict[str, str]:
    """纯函数: 基础环境 + DisplayPlan -> 子进程环境. 不修改入参.

    与 browser._spawn 同一做法: 复制 base, 叠加 plan.env, 删 plan.unset.
    另外两条硬要求: 删 WAYLAND_DISPLAY, GDK_BACKEND 钉死 x11.
    留着 WAYLAND_DISPLAY 时 GTK 走 Wayland 后端, 窗口在合成器里, 无法用 X11 断言可见.
    """
    out = dict(base or {})
    out.update(dict(getattr(plan, "env", None) or {}))
    for key in tuple(getattr(plan, "unset", None) or ()):
        out.pop(key, None)
    out.pop(K_WAYLAND, None)
    out[K_GDK_BACKEND] = "x11"
    return out


def resolve_executable(settings) -> str:
    """WebKit 档可执行文件. 优先级: settings.kernel_paths["safari"] > 探测 > 抛异常.

    配置路径必须真实存在才采用; 不存在时回落探测, 与 kernels.resolve_executable 一致.
    """
    configured = (getattr(settings, "kernel_paths", None) or {}).get(KERNEL_ID, "")
    if configured and Path(configured).exists():
        return configured
    found = kernels.detect(KERNEL_ID)
    if found:
        return found
    raise WebKitLaunchError(
        "找不到 Safari 档可执行文件 (WebKitGTK MiniBrowser): "
        "请安装 webkit2gtk 或在设置 kernel_paths 里配置 %s 的路径" % KERNEL_ID
    )


async def launch(env, profile_dir, port: int, plan, spawn) -> tuple[int, str]:
    """browser.start 的 WebKit 分支. 返回 spawn 的 (pid, executable).

    env 是 launch_hooks.prepare 产出的启动视图, relay 端口从它的 prep 里取.
    port 是分配给该副本的调试端口, WebKit 不说 CDP, 这里不用它, 只为与其他族签名对齐.
    spawn 即 BrowserManager._spawn, 显示环境经 child_env 作为 extra_env 传入.
    """
    prep = launch_hooks.prep_of(env)
    relay_port = prep.relay_port if prep is not None else 0
    exe = resolve_executable(getattr(getattr(spawn, "__self__", None), "settings", None))
    argv = build_argv(env, profile_dir, relay_port, user_script_path(env, profile_dir))
    return spawn(exe, argv, env, plan, child_env(os.environ, plan))
