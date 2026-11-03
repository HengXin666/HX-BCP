"""启动前的副本准备 —— browser.py 只调这里, 不直接知道 prefs/relay/sandbox 的细节。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 「启动视图」(launch view)

prepare() 返回环境的一份**深拷贝**, 其 fingerprint.language / timezone 已替换成
本次实际生效值 (custom / ip / system / inherit 推导见 profile_rules)。
browser.py 既有的每条路径 (--lang/TZ 环境变量/CloakBrowser --fingerprint-*/
指纹扩展/Firefox user.js) 都读这两个字段 —— 于是新模式自动贯穿到所有内核,
且 inherit 时拷贝与原环境逐字段相同 (= 改动前行为)。

持久化的环境**不被改写**: IP 匹配结果只记在 env.last_region, 下次换出口重新匹配。
"""

from __future__ import annotations

import hashlib
import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from . import (
    bridge,
    chromium_prefs,
    display_iso,
    firefox_prefs,
    fontiso,
    kernels,
    netcheck,
    relay,
    sandbox,
)
from .profile_rules import effective_languages, effective_timezone, validate_prefs
from .proxy_link import (
    chromium_proxy_args,
    egress_upstream,
    firefox_proxy_prefs,
    upstream_of,
)

PREP_KEY = "prep"
WEBRTC_STRICT_FLAG = "--force-webrtc-ip-handling-policy=disable_non_proxied_udp"


@dataclass
class LaunchPrep:
    env: Any  # 启动视图 (Environment 深拷贝)
    profile_dir: Path
    relay_port: int = 0
    languages: str = ""
    timezone: str = ""
    region: dict = field(default_factory=dict)
    warnings: list[str] = field(default_factory=list)
    config_hash: str = ""
    plan: Any = None  # desktop.DisplayPlan (沙箱据此决定显示层)
    cdp_port: int = 0
    run_dir: Path | None = None  # 沙箱副本的宿主运行目录 (Unix socket / shim)
    fonts: dict = field(default_factory=dict)  # fontiso.prepare 的结果


class _SandboxEgress:
    """沙箱副本在浏览器眼里的代理: 永远是沙箱内 shim 的无凭据入口。"""

    type, host, port, username, password = "http", "127.0.0.1", 0, "", ""


def config_hash(env) -> str:
    """影响启动结果的配置摘要。只改名称/标签/备注不变 -> 不提示重启 (与 nodecloak 一致)。"""
    blob = {
        "proxy": env.proxy.model_dump(),
        "fingerprint": env.fingerprint.model_dump(),
        "launch": env.launch.model_dump(),
        "prefs": env.prefs.model_dump(),
        "location_mode": env.location_mode,
    }
    return hashlib.sha256(
        json.dumps(blob, sort_keys=True, ensure_ascii=False).encode()
    ).hexdigest()[:16]


def needs_ip_region(env) -> bool:
    return env.prefs.language_mode == "ip" or env.prefs.timezone_mode == "ip"


def is_sandbox(env) -> bool:
    return env.prefs.isolation == "sandbox"


async def prepare(env, profile_dir: Path, plan=None, cdp_port: int = 0) -> LaunchPrep:
    errors = validate_prefs(env)
    if errors:
        raise ValueError("；".join(errors))
    if env.prefs.isolation == "sandbox" and not sandbox.available():
        raise sandbox.SandboxUnavailable(
            "副本要求 sandbox 隔离, 但本机未安装 bubblewrap (bwrap); 已拒绝启动, 未降级"
        )
    region: dict = {}
    if needs_ip_region(env):
        # 匹配失败直接拒绝启动: 带着错的语言/时区启动比不启动更糟 (用户以为对齐了)
        region = await netcheck.lookup_region(env)
        env.last_region = region
    view = env.model_copy(deep=True)
    view.fingerprint.language = effective_languages(env, region.get("languages", ""))
    view.fingerprint.timezone = effective_timezone(env, region.get("timezone", ""))
    prep = LaunchPrep(
        env=view,
        profile_dir=profile_dir,
        languages=view.fingerprint.language,
        timezone=view.fingerprint.timezone,
        region=region,
        config_hash=config_hash(env),
        plan=plan,
        cdp_port=cdp_port,
    )
    prep.fonts = fontiso.prepare(
        profile_dir, env.prefs.font_isolation, env.prefs.fonts, env.fingerprint.os
    )
    if plan is not None and display_iso.wants_wayland(env):
        display_iso.use_wayland(env.id, plan)
    if is_sandbox(env):
        await _prepare_sandbox(env, prep)
    else:
        up = upstream_of(env.proxy)
        if up is not None:
            prep.relay_port = await relay.ensure(env.id, up)
    # 两处都存: 启动链用的是 prep.env (深拷贝副本), 后续 API 调用拿到的
    # 是重新构造的 env, 只存一处总有一边 prep_of 取不到
    view.runtime[PREP_KEY] = prep
    env.runtime[PREP_KEY] = prep
    return prep


async def _prepare_sandbox(env, prep: LaunchPrep) -> None:
    """沙箱副本: 出口只经 Unix socket -> 宿主 relay; CDP 经宿主桥; 必要时起嵌套 X。"""
    rd = sandbox.run_dir(env.id)
    prep.run_dir = rd
    await relay.ensure(
        env.id,
        egress_upstream(env.proxy, dict(os.environ)),
        unix_path=sandbox.host_proxy_sock(rd),
    )
    prep.relay_port = sandbox.SANDBOX_PROXY_PORT
    if prep.cdp_port:
        await bridge.ensure(env.id, prep.cdp_port, sandbox.host_cdp_sock(rd))
    plan = prep.plan
    if (
        plan is not None
        and plan.source == "desktop"
        and not display_iso.wants_wayland(env)
        and env.prefs.sandbox_x11 == display_iso.NESTED
    ):
        display_iso.start_nested(env.id, plan)


def prep_of(env) -> LaunchPrep | None:
    return env.runtime.get(PREP_KEY) if isinstance(env.runtime, dict) else None


def chromium_args(env) -> list[str]:
    """代理 + WebRTC 启动参数 (替代旧 proxy_args: 凭据不再进命令行)。"""
    prep = prep_of(env)
    proxy = _SandboxEgress() if is_sandbox(env) else env.proxy
    out = chromium_proxy_args(proxy, prep.relay_port if prep else 0)
    if env.prefs.webrtc in ("proxy", "disable") and env.fingerprint.webrtc_mode not in (
        "proxy",
        "disable",
    ):
        out.append(WEBRTC_STRICT_FLAG)
    return out


def _managed_languages(env, prep: LaunchPrep | None) -> str:
    # inherit 时不写语言偏好: 沿用既有 --lang / 指纹扩展的行为, 不多改一个键
    return prep.languages if prep and env.prefs.language_mode != "inherit" else ""


def write_chromium(env, profile_dir: Path) -> list[str]:
    prep = prep_of(env)
    if prep is not None:
        prep.profile_dir = profile_dir
    warnings = chromium_prefs.write(
        profile_dir, env.prefs, _managed_languages(env, prep)
    )
    if prep is not None:
        prep.warnings.extend(warnings)
    return warnings


def write_firefox(env, profile_dir: Path) -> Path:
    prep = prep_of(env)
    if prep is not None:
        prep.profile_dir = (
            profile_dir  # 导入的 userdir 可能在子目录/软链: 以实际目录为准
        )
    proxy = _SandboxEgress() if is_sandbox(env) else env.proxy
    proxy_prefs = firefox_proxy_prefs(proxy, prep.relay_port if prep else 0)
    values = firefox_prefs.plan(env.prefs, _managed_languages(env, prep), proxy_prefs)
    return firefox_prefs.write(profile_dir, values)


def wrap_argv(env, argv: list[str], launch_env: dict) -> list[str]:
    """isolation=sandbox 时把 argv 包进 bwrap; 否则只处理 Wayland 后端切换。"""
    prep = prep_of(env)
    argv = list(argv)
    detail = getattr(prep.plan, "source_detail", "") if prep and prep.plan else ""
    if not is_sandbox(env) or prep is None or prep.run_dir is None:
        if detail.startswith("wayland:"):
            display_iso.apply_wayland(argv, launch_env, detail.split(":", 1)[1], False)
        return argv
    spec = sandbox.SandboxSpec(
        profile_dir=prep.profile_dir,
        run_dir=prep.run_dir,
        cdp_port=prep.cdp_port,
        extra_ro=[str(Path(argv[0]).resolve().parent), *env.launch.extensions],
        display_args=display_iso.sandbox_args(prep.plan, argv, launch_env),
        font_args=fontiso.sandbox_args(prep.fonts.get("files", []))
        if prep.fonts
        else [],
        gpu=env.prefs.sandbox_gpu,
        seccomp=env.prefs.sandbox_seccomp,
    )
    return sandbox.wrap(argv, spec, env=launch_env)


def kernel_id(env) -> str:
    k = kernels.get(env.fingerprint.kernel)
    if k is not None:
        return k.id
    return "legacy-" + kernels.family_of(env)


def process_env(env) -> dict[str, str]:
    """托管语言时同步进程 locale (LANG/LANGUAGE)。

    Linux 上 ICU 的默认 locale 取自 LANG: 只改 Accept-Language 而不改它, 网页里
    ``Intl.DateTimeFormat().resolvedOptions().locale`` 仍是电脑的语言 —— 与
    navigator.language 不一致本身就是常见检测点。精简版 headless_shell 甚至只认它。
    """
    prep = prep_of(env)
    out = (
        {fontiso.ENV_FONTCONFIG_FILE: prep.fonts["conf"]} if prep and prep.fonts else {}
    )
    langs = _managed_languages(env, prep)
    if not langs:
        return out
    first = langs.split(",")[0].strip()
    posix = first.replace("-", "_")
    if "_" not in posix:
        posix = {"en": "en_US", "zh": "zh_CN", "ja": "ja_JP", "de": "de_DE"}.get(
            posix, posix
        )
    return {
        **out,
        "LANG": posix + ".UTF-8",
        "LANGUAGE": ":".join(x.strip().replace("-", "_") for x in langs.split(",")),
    }
