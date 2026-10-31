"""在 CI 里拉起一个真实 hx-browser 副本, 给评分脚本一个 --cdp 端点 (而不是普通 Playwright Chromium)。

up:   (--spawn 时) 启动 hx-browser API (python run.py serve), 建/按名复用 ci-fingerprint-score 环境,
      headless 启动, 打印 CDP=<url> 并写状态文件; 在 GitHub Actions 里同时写 $GITHUB_OUTPUT 的 cdp=...
down: 停掉该环境; up 时由本脚本拉起的 API 一并结束。

环境配置对齐 NodeCloak 复检截图里的「干净」副本: 英文、UTC、strict 字体隔离、按 seed 噪声。

注意: hx-browser 的数据目录固定在 packages/browser/data。对本机正在用的服务跑 up (不带
--spawn) 会在你的环境列表里多一个 ci-fingerprint-score (按名复用, 不会越建越多)。

退出码: 0 成功; 3 拉不起真实副本 (CI 记为「未评分」, 不是指纹红)。
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

PKG = Path(__file__).resolve().parent.parent
ENV_NAME = "ci-fingerprint-score"
WEBKIT_KERNELS = ("safari",)  # browser-gate: allow-legacy kernel id, webkit family, see hx_browser kernels on the PR 18 line
DEFAULT_PORT = 8325
TMP = Path(os.environ.get("RUNNER_TEMP", "/tmp"))
STATE = TMP / "hx-real-profile.json"
UNSCORED = 3
NOT_INSTALLED = 4
NO_CDP = 5
STATUS = {0: "ok", UNSCORED: "unscored", NOT_INSTALLED: "not_installed", NO_CDP: "no_cdp"}
FINGERPRINT = {
    "headless": True,
    "language": "en-US,en",
    "timezone": "UTC",
    "canvas_noise": True,
    "webgl_noise": True,
    "audio_noise": True,
    "webrtc_mode": "proxy",  # hide_webrtc: no STUN, no srflx leak
}
PREFS = {
    "font_isolation": "strict",
    # inherit 只传 --lang, Linux 上的 Chrome 不认, 网页仍读到电脑的 zh-CN (实测 +24 分);
    # custom 才会同步 LANG/LANGUAGE 与 intl.accept_languages, 见 launch_hooks.process_env
    "language_mode": "custom",
    "languages": ["en-US", "en"],
}
UA_TEMPLATE = (
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) "
    "Chrome/%s.0.0.0 Safari/537.36"
)
CHROME_FALLBACK = ("google-chrome-stable", "google-chrome", "chromium", "chromium-browser")


def local(port: int) -> str:
    return "%s://127.0.0.1:%d" % ("http", port)


def chrome_ua(version_text: str) -> str:
    """由 chrome --version 拼一个非 Headless 的 UA; 认不出主版本就返回空串, 不瞎编。"""
    m = re.search(r"(\d+)\.\d+\.\d+\.\d+", version_text or "")
    return UA_TEMPLATE % m.group(1) if m else ""


def detect_ua(api: str, exe: str = "") -> str:
    """headless 默认 UA 带 HeadlessChrome, Sannysoft / CreepJS 一眼认出; 换成同版本的正常 UA。"""
    try:
        settings = call(api, "GET", "/settings") or {}
        exe = exe or settings.get("chromium_path") or (settings.get("settings") or {}).get("chromium_path") or ""
    except (OSError, ValueError, RuntimeError, AttributeError):
        pass
    exe = exe or next((p for p in map(shutil.which, CHROME_FALLBACK) if p), "")
    if not exe:
        return ""
    try:
        res = subprocess.run([exe, "--version"], capture_output=True, text=True, timeout=20, check=False)
    except (OSError, subprocess.SubprocessError):
        return ""
    return chrome_ua(res.stdout)


def profile_fingerprint(api: str, kernel: dict | None = None) -> dict:
    """Fingerprint body for the env, a kernel row sets kernel and browser, only blink kernels get the Chrome UA override"""
    fp = dict(FINGERPRINT)
    family = (kernel or {}).get("family", "blink")
    if kernel:
        fp["kernel"] = kernel["id"]
        fp["browser"] = "flower" if family == "gecko" else "sun"
    ua = detect_ua(api, (kernel or {}).get("path", "")) if family == "blink" else ""
    if ua:
        fp["user_agent"] = ua
    return fp


def env_name(kernel: str) -> str:
    """Env name for a kernel, no kernel keeps the old single env name"""
    return "%s-%s" % (ENV_NAME, kernel) if kernel else ENV_NAME


def kernel_row(api: str, kernel: str) -> dict | None:
    """The kernel as the running API lists it, id, family, path and available, None when it is not listed"""
    rows = (call(api, "GET", "/kernels") or {}).get("kernels", [])
    return next((r for r in rows or [] if isinstance(r, dict) and r.get("id") == kernel), None)


def emit(code: int, kernel: str, family: str, cdp: str = "") -> int:
    """Prints and writes to GITHUB_OUTPUT the status, kernel and family of this up, returns code"""
    print("STATUS=%s KERNEL=%s FAMILY=%s" % (STATUS[code], kernel or "-", family or "-"))
    gh = os.environ.get("GITHUB_OUTPUT")
    if gh:
        with open(gh, "a", encoding="utf-8") as fh:
            fh.write("status=%s\nkernel=%s\nfamily=%s\n" % (STATUS[code], kernel, family))
            if cdp:
                fh.write("cdp=%s\n" % cdp)
    return code


def stop_group(pid: int, grace: float = 10) -> None:
    """SIGTERM 后等进程组退干净, 超时再 SIGKILL (uvicorn 优雅退出可能卡在浏览器子进程上)。"""
    try:
        os.killpg(pid, signal.SIGTERM)
    except OSError:
        return
    end = time.time() + grace
    while time.time() < end:
        try:
            os.killpg(pid, 0)
        except OSError:
            return
        time.sleep(0.5)
    try:
        os.killpg(pid, signal.SIGKILL)
    except OSError:
        pass


def call(api: str, method: str, path: str, body: dict | None = None, timeout: float = 120):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        api + "/api/v1" + path, data=data, method=method, headers={"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        out = json.loads(resp.read().decode() or "{}")
    if isinstance(out, dict) and out.get("ok") is False:
        raise RuntimeError(out.get("error") or out.get("msg") or json.dumps(out, ensure_ascii=False))
    return out.get("data", out) if isinstance(out, dict) else out


def wait_api(api: str, seconds: float, proc: subprocess.Popen | None = None) -> bool:
    """等 API 就绪; 子进程已经退出就立刻返回, 不要空等满 wait 秒."""
    end = time.time() + seconds
    while time.time() < end:
        try:
            call(api, "GET", "/status", timeout=5)
            return True
        except (OSError, ValueError, RuntimeError):
            if proc is not None and proc.poll() is not None:
                return False
            time.sleep(1)
    return False


def wait_cdp(cdp: str, seconds: float) -> bool:
    """start 返回时 DevTools 端口可能还没就绪; 探针立刻连会卡在 ws preparing (实测)。"""
    end = time.time() + seconds
    while time.time() < end:
        try:
            with urllib.request.urlopen(cdp + "/json/version", timeout=3) as resp:
                if json.loads(resp.read().decode() or "{}").get("webSocketDebuggerUrl"):
                    return True
        except (OSError, ValueError):
            pass
        time.sleep(0.5)
    return False


def find_env(api: str, name: str = ENV_NAME) -> str | None:
    envs = call(api, "GET", "/environments")
    if isinstance(envs, dict):
        envs = envs.get("items") or envs.get("environments") or []
    for env in envs or []:
        if isinstance(env, dict) and env.get("name") == name:
            return env.get("id")
    return None


def save(**state) -> None:
    STATE.write_text(json.dumps(state), encoding="utf-8")


def spawn_command(port: int) -> list[str]:
    """拉起 API 的命令行; run.py 现在是 hxb 的壳, 少了 serve 会被 argparse 判成非法参数."""
    return [sys.executable, "run.py", "serve", "--host", "127.0.0.1", "--port", str(port)]


def up(args: argparse.Namespace) -> int:
    pid = 0
    proc = None
    api = args.api or local(DEFAULT_PORT)
    if args.spawn:
        api = local(args.port)
        log = open(TMP / "hx-browser-api.log", "w", encoding="utf-8")  # noqa: SIM115 子进程持有
        proc = subprocess.Popen(
            spawn_command(args.port),
            cwd=PKG,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        pid = proc.pid
    save(api=api, pid=pid)
    kernel = args.kernel.strip().lower()
    family = "webkit" if kernel in WEBKIT_KERNELS else ""
    if not wait_api(api, args.wait, proc):
        print("hx-browser API 没有起来: %s" % api, file=sys.stderr)
        if proc is not None and proc.poll() is not None:
            print("API 进程已退出 (exit %s), 见 hx-browser-api.log" % proc.returncode, file=sys.stderr)
        return emit(UNSCORED, kernel, family)
    row = None
    if kernel:
        try:
            row = kernel_row(api, kernel)
        except (OSError, ValueError, RuntimeError) as exc:
            print("读内核列表失败: %s" % exc, file=sys.stderr)
            return emit(UNSCORED, kernel, family)
        family = (row or {}).get("family") or family
        if family == "webkit":
            print("内核 %s 属 webkit 族, 不说 CDP, 不起副本" % kernel, file=sys.stderr)
            return emit(NO_CDP, kernel, family)
        if row is None or not row.get("available"):
            print("内核 %s 未安装: %s" % (kernel, json.dumps(row, ensure_ascii=False)), file=sys.stderr)
            return emit(NOT_INSTALLED, kernel, family)
    name = env_name(kernel)
    try:
        body = {"name": name, "fingerprint": profile_fingerprint(api, row), "prefs": PREFS}
        env_id = find_env(api, name)
        if env_id:
            try:  # 按名复用时把配置刷成本脚本的版本, 免得旧副本带旧设置被评分
                call(api, "PUT", "/environments/%s" % env_id, body)
            except (OSError, ValueError, RuntimeError) as exc:
                print("刷新已有副本配置失败, 沿用旧配置: %s" % exc, file=sys.stderr)
        else:
            env_id = call(api, "POST", "/environments", body)["id"]
        save(api=api, pid=pid, env=env_id)
        print("ENV=%s NAME=%s" % (env_id, name))
        started = call(api, "POST", "/environments/%s/start" % env_id, timeout=180)
    except (OSError, ValueError, RuntimeError, KeyError, TypeError) as exc:
        print("真实副本启动失败: %s" % exc, file=sys.stderr)
        return emit(UNSCORED, kernel, family)
    rt = started.get("runtime") or {}
    cdp = rt.get("cdp_http") or (local(rt["debug_port"]) if rt.get("debug_port") else "")
    save(api=api, pid=pid, env=env_id, cdp=cdp)
    if not cdp:
        print("启动返回里没有 CDP 端点: %s" % json.dumps(rt, ensure_ascii=False), file=sys.stderr)
        return emit(UNSCORED, kernel, family)
    if not wait_cdp(cdp, 30):
        print("CDP 端点未就绪: %s" % cdp, file=sys.stderr)
        return emit(UNSCORED, kernel, family)
    print("CDP=" + cdp)
    return emit(0, kernel, family, cdp)


def down(_args: argparse.Namespace) -> int:
    if not STATE.exists():
        return 0
    st = json.loads(STATE.read_text(encoding="utf-8"))
    if st.get("env"):
        try:
            call(st["api"], "POST", "/environments/%s/stop" % st["env"], timeout=30)
        except (OSError, ValueError, RuntimeError) as exc:
            print("停止副本失败: %s" % exc, file=sys.stderr)
    if st.get("pid"):
        stop_group(st["pid"])
    STATE.unlink(missing_ok=True)
    return 0


def main() -> None:
    parser = argparse.ArgumentParser(description="拉起/关闭用于评分的真实 hx-browser 副本")
    sub = parser.add_subparsers(dest="cmd", required=True)
    p_up = sub.add_parser("up", help="启动副本并输出 CDP 端点")
    p_up.add_argument("--spawn", action="store_true", help="由本脚本拉起 hx-browser API (CI 用)")
    p_up.add_argument("--api", default="", help="已在跑的 API 地址, 默认本机 %d 端口" % DEFAULT_PORT)
    p_up.add_argument("--port", type=int, default=DEFAULT_PORT, help="--spawn 时的 API 端口")
    p_up.add_argument("--wait", type=float, default=60, help="等 API 就绪的秒数")
    p_up.add_argument("--kernel", default="", help="内核 id, 取自 API 的 /kernels, 空则沿用旧的单一环境")
    sub.add_parser("down", help="停止副本与由 up 拉起的 API")
    args = parser.parse_args()
    sys.exit(up(args) if args.cmd == "up" else down(args))


if __name__ == "__main__":
    main()
