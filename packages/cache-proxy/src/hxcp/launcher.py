"""浏览器启动器 — 起一个可被 CDP 驱动的环境 (本工具的被测对象)。

设计约束 (全部来自实测):
  · `--remote-allow-origins=*` **必须加**, 否则 websocket 升级被拒, CDP 连不上;
  · 停止要走**精确匹配** (`--user-data-dir`), 不能只 terminate 父进程 ——
    带 `start_new_session=True` 起的进程脱离父会话, terminate 杀不掉, 旧实例会占着 CDP 端口;
  · 用后即清: 临时 profile 默认删除 (本机磁盘上堆了几十个历史 profile, 是真实教训)。
"""
from __future__ import annotations

import os
import shutil
import subprocess
import time
import urllib.request
from dataclasses import dataclass, field

CANDIDATES = ("google-chrome-stable", "google-chrome", "chromium", "chromium-browser")


def find_chrome() -> str:
    for c in CANDIDATES:
        if shutil.which(c):
            return c
    raise RuntimeError("找不到 chrome/chromium; 请显式传 --chrome")


@dataclass
class Launched:
    proc: subprocess.Popen
    port: int
    profile: str
    endpoint: str
    keep_profile: bool = False

    def stop(self) -> None:
        try:
            self.proc.terminate()
            self.proc.wait(timeout=5)
        except Exception:
            try:
                self.proc.kill()
            except Exception:
                pass
        # 精确补杀: 脱离会话的子进程 terminate 追不到
        subprocess.run(["pkill", "-f", "--user-data-dir=" + self.profile],
                       capture_output=True)
        for _ in range(20):
            if not port_open(self.port):
                break
            time.sleep(0.2)
        if not self.keep_profile:
            shutil.rmtree(self.profile, ignore_errors=True)

    def alive(self) -> bool:
        return port_open(self.port)


def port_open(port: int, host: str = "127.0.0.1") -> bool:
    import socket
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex((host, port)) == 0


def launch(port: int = 9333, *, profile: str = "", proxy: str = "",
           headless: bool = True, extra: list[str] | None = None,
           chrome: str = "", timeout: float = 30.0,
           keylog: str = "", keep_profile: bool = False) -> Launched:
    profile = profile or ("/tmp/hxcp-profile-" + str(port))
    os.makedirs(profile, exist_ok=True)
    args = [chrome or find_chrome(),
            "--remote-debugging-port=" + str(port),
            "--remote-allow-origins=*",
            "--no-first-run", "--no-default-browser-check",
            "--user-data-dir=" + profile]
    if headless:
        args.append("--headless=new")
    if proxy:
        args.append("--proxy-server=" + proxy)
    if keylog:
        # Chromium 原生 flag: 导出 TLS 会话密钥, **不碰证书、不碰 profile**
        args.append("--ssl-key-log-file=" + keylog)
    args += (extra or [])
    args.append("about:blank")
    proc = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                            start_new_session=True)
    endpoint = "http://127.0.0.1:" + str(port)
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            urllib.request.urlopen(endpoint + "/json/version", timeout=1).read()
            return Launched(proc, port, profile, endpoint, keep_profile)
        except Exception:
            time.sleep(0.3)
    try:
        proc.kill()
    except Exception:
        pass
    raise RuntimeError("环境未在 " + str(timeout) + "s 内起来 (port " + str(port) + ")")
