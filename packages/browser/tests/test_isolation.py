"""沙箱隔离各层的单元测试 (不起浏览器): seccomp / bwrap 参数 / 显示 / 字体 / relay 出口。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md
"""

from __future__ import annotations

import ctypes
import os
import socket as so
import struct
import subprocess
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest
from hx_browser import (
    display_iso,
    fontiso,
    sandbox,
    seccomp_bpf,
    xenv,
)
from hx_browser.models import Environment
from hx_browser.profile_rules import validate_prefs

import hxbr_testenv  # noqa: F401  (把 src 放进 sys.path)

BWRAP = "/usr/bin/bwrap"


# ---------- seccomp ----------
@pytest.mark.parametrize("arch", sorted(seccomp_bpf.ARCHES))
def test_seccomp_program_shape(arch):
    prog = seccomp_bpf.build(arch)
    ins = [struct.unpack("=HBBI", prog[i : i + 8]) for i in range(0, len(prog), 8)]
    assert ins[0] == (seccomp_bpf.LD_ABS_W, 0, 0, 4)  # 先验架构
    assert ins[1][3] == seccomp_bpf.ARCHES[arch][0]
    assert 1 + ins[1][2] + 1 == len(ins) - 1  # 架构不符 -> 最后一条 KILL
    assert ins[-3][3] == seccomp_bpf.RET_ALLOW and ins[-1][3] == seccomp_bpf.RET_KILL
    # 每条 JEQ 都跳到 EPERM 那条返回
    for i, (code, jt, _jf, _k) in enumerate(ins):
        if code == seccomp_bpf.JEQ_K and i > 1:
            assert ins[i + jt + 1][3] == seccomp_bpf.RET_EPERM
    assert len(seccomp_bpf.denied_numbers(arch)) >= 35


@pytest.mark.skipif(seccomp_bpf.machine() not in seccomp_bpf.ARCHES, reason="arch")
def test_seccomp_denies_in_child():
    nr = seccomp_bpf.ARCHES[seccomp_bpf.machine()][1]["keyctl"]
    code = (
        "import ctypes,sys;sys.path.insert(0,%r);import seccomp_bpf as s;s.apply();"
        "l=ctypes.CDLL(None,use_errno=True);r=l.syscall(%d,0,0,0,0,0);"
        "print(r,ctypes.get_errno())" % (str(Path(seccomp_bpf.__file__).parent), nr)
    )
    out = subprocess.run(
        [sys.executable, "-S", "-c", code], capture_output=True, text=True
    )
    assert out.stdout.split() == ["-1", "1"], out.stderr  # EPERM, 进程未被杀


def test_seccomp_unknown_arch():
    with pytest.raises(seccomp_bpf.SeccompUnsupported):
        seccomp_bpf.build("mips")
    assert ctypes.sizeof(seccomp_bpf._SockFprog) == 16


# ---------- bwrap 参数 ----------
def _spec(tmp_path, **kw):
    profile = tmp_path / "profiles" / "env_x"
    profile.mkdir(parents=True)
    rd = tmp_path / "run"
    (rd / "sock").mkdir(parents=True)
    (rd / "shim").mkdir()
    return sandbox.SandboxSpec(profile_dir=profile, run_dir=rd, cdp_port=45555, **kw)


def test_sandbox_wrap_isolates_network_and_display(tmp_path, monkeypatch):
    monkeypatch.setattr(sandbox.shutil, "which", lambda name: BWRAP)
    spec = _spec(
        tmp_path, display_args=["--ro-bind", "/tmp/.X11-unix/X71", "/tmp/.X11-unix/X71"]
    )
    home = str(tmp_path / "home")
    argv = sandbox.wrap(["/b/browser", "--x"], spec, env={"HOME": home})
    assert argv[0] == BWRAP and argv[-3:] == ["--", "/b/browser", "--x"]
    for flag in (
        "--unshare-net",
        "--unshare-pid",
        "--unshare-ipc",
        "--die-with-parent",
    ):
        assert flag in argv
    assert argv[argv.index("--hostname") + 1] == "localhost"
    assert "/tmp/.X11-unix" not in argv  # 绝不绑整个 X socket 目录
    i = argv.index("--tmpfs", argv.index("/tmp") + 1)
    assert argv[i + 1] == home
    real = str(spec.profile_dir.resolve())
    assert ["--bind", real, real] == argv[
        argv.index("--bind") : argv.index("--bind") + 3
    ]
    shim = argv.index(sandbox.SHIM_DIR + "/sandbox_shim.py")
    tail = argv[shim:]
    assert tail[tail.index("--cdp-port") + 1] == "45555" and "--seccomp" in tail
    assert "/dev/dri" in argv  # GPU 默认保留 (WebGL 指纹真实)


def test_sandbox_wrap_options(tmp_path, monkeypatch):
    monkeypatch.setattr(sandbox.shutil, "which", lambda name: BWRAP)
    argv = sandbox.wrap(["/b"], _spec(tmp_path, gpu=False, seccomp=False))
    assert "/dev/dri" not in argv and "--seccomp" not in argv


def test_sandbox_missing_refuses(monkeypatch, tmp_path):
    monkeypatch.setattr(sandbox.shutil, "which", lambda name: None)
    with pytest.raises(sandbox.SandboxUnavailable):
        sandbox.wrap(["/x"], _spec(tmp_path))


def test_run_dir_rejects_foreign_symlink(tmp_path, monkeypatch):
    monkeypatch.setattr(sandbox.tempfile, "gettempdir", lambda: str(tmp_path))
    (tmp_path / "elsewhere").mkdir()
    (tmp_path / ("hx-browser-%d" % os.getuid())).symlink_to(tmp_path / "elsewhere")
    with pytest.raises(sandbox.SandboxUnavailable):
        sandbox.run_dir("env_q")


def test_run_dir_private(tmp_path, monkeypatch):
    monkeypatch.setattr(sandbox.tempfile, "gettempdir", lambda: str(tmp_path))
    rd = sandbox.run_dir("env_q")
    assert oct(rd.parent.stat().st_mode & 0o777) == "0o700"
    assert sorted(p.name for p in (rd / "shim").iterdir()) == sorted(sandbox.SHIM_FILES)


# ---------- 显示 ----------
def test_display_headless_strips_all(tmp_path):
    env = {"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0", "XAUTHORITY": "/x"}
    assert display_iso.sandbox_args(SimpleNamespace(source="headless"), [], env) == []
    assert env == {}


def test_display_xvfb_binds_single_socket():
    plan = SimpleNamespace(source="xvfb", source_detail="", display=":73")
    env = {"DISPLAY": ":73", "WAYLAND_DISPLAY": "w"}
    args = display_iso.sandbox_args(plan, [], env)
    sock = str(xenv.socket_path(73))
    assert args == ["--ro-bind", sock, sock] and "WAYLAND_DISPLAY" not in env


def test_wayland_switch(tmp_path, monkeypatch):
    sock = tmp_path / "wayland-3"

    s = so.socket(so.AF_UNIX)
    s.bind(str(sock))
    monkeypatch.setenv("XDG_RUNTIME_DIR", str(tmp_path))
    monkeypatch.delenv("WAYLAND_DISPLAY", raising=False)
    assert display_iso.wayland_socket() == str(sock)
    plan = SimpleNamespace(source="xvfb", display=":70", env={}, unset=())
    display_iso.use_wayland("env_none", plan)
    assert plan.source_detail == "wayland:" + str(sock) and plan.display == ""
    argv = ["chromium", display_iso.OZONE_X11]  # browser-gate: allow-legacy argv 字面量
    env = {"DISPLAY": ":70"}
    args = display_iso.sandbox_args(plan, argv, env)
    assert argv[1] == display_iso.OZONE_WAYLAND and "DISPLAY" not in env
    assert env["WAYLAND_DISPLAY"] == "wayland-0" and args[-2:] == [
        str(sock),
        "/run/hx-wayland/wayland-0",
    ]
    s.close()


def test_wayland_missing_refuses(tmp_path, monkeypatch):
    monkeypatch.setenv("XDG_RUNTIME_DIR", str(tmp_path))
    monkeypatch.delenv("WAYLAND_DISPLAY", raising=False)
    with pytest.raises(display_iso.DisplayUnavailable):
        display_iso.use_wayland("e", SimpleNamespace(source="desktop"))


def test_nested_requires_xephyr(monkeypatch):
    monkeypatch.setattr(display_iso.shutil, "which", lambda name: None)
    with pytest.raises(display_iso.DisplayUnavailable):
        display_iso.start_nested("e", SimpleNamespace(env={}, display=":0"))


# ---------- 字体 ----------
FAKE = [
    ("DejaVu Sans", "/f/dv.ttf"),
    ("Nimbus Sans", "/f/nb.otf"),
    ("Noto Sans CJK SC", "/f/cjk.ttc"),
]


def test_font_isolation(tmp_path, monkeypatch):
    monkeypatch.setattr(fontiso, "available", lambda: True)
    monkeypatch.setattr(fontiso, "system_fonts", lambda: FAKE)
    monkeypatch.setattr(fontiso, "cjk_fonts", lambda: FAKE[2:])
    got = fontiso.prepare(tmp_path, "custom", ["dejavu sans"])
    assert got["families"] == ["DejaVu Sans", "Noto Sans CJK SC"]  # 中文字体总是保留
    links = sorted(os.readlink(p) for p in (tmp_path / ".hx-fonts" / "files").iterdir())
    assert links == ["/f/cjk.ttc", "/f/dv.ttf"]
    conf = Path(got["conf"]).read_text()
    assert (
        "<dir>%s</dir>" % (tmp_path / ".hx-fonts" / "files") in conf
        and "/usr/share" not in conf
    )
    assert fontiso.font_roots(["/x/share/fonts/truetype/a.ttf", "/h/.fonts/b.otf"]) == [
        "/h/.fonts",
        "/x/share/fonts",
    ]
    args = fontiso.sandbox_args(got["files"])
    assert args[args.index("--ro-bind-try") + 1] in got["files"]
    assert fontiso.prepare(tmp_path, "inherit", []) == {}
    assert "DejaVu Sans" in [f for f, _ in fontiso.select("baseline", [])]


def test_font_prefs_validation():
    env = Environment()
    env.prefs.font_isolation = "custom"
    assert any("字体" in e for e in validate_prefs(env))
    env.prefs.fonts = ["DejaVu Sans"]
    assert validate_prefs(env) == []
