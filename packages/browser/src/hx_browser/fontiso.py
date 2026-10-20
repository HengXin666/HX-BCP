"""副本级系统字体隔离 (所有内核) —— 网页只能枚举到白名单里的字体。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

## 两层

1. fontconfig 层 (process 与 sandbox 都生效): 为副本生成私有 fonts.conf, 只含一个目录
   ``<profile>/.hx-fonts/files`` —— 里面是白名单字体文件的软链; 浏览器以
   FONTCONFIG_FILE 启动, 枚举不到其他字体。用户家目录字体、conf.d 里的额外目录一概不读。
2. 文件系统层 (仅 sandbox): 系统字体目录被 tmpfs 遮蔽, 只把白名单文件按原路径只读
   绑回去 —— 即使兼容 fontconfig 直接读目录也看不到其他字体文件。

## 选哪些字体

* ``baseline``: 常见 Linux 发行版都有的拉丁基础族 + 电脑上**所有能显示中文的字体**
  (用户明确要求保留中文字体; 去掉它们中文页面会变方块, 本身就是异常信号)。
* ``custom``: 用户给的族名列表 (仍会自动补上中文字体, 理由同上)。
* ``inherit``: 不隔离 (= 改动前行为)。

Firefox 的 font.system.whitelist (firefox_prefs) 继续保留, 与本层叠加。
"""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

TOOL_FC_LIST = "fc-list"
FONT_DIRNAME = ".hx-fonts"
FILES_DIRNAME = "files"
CONF_NAME = "fonts.conf"
ENV_FONTCONFIG_FILE = "FONTCONFIG_FILE"
# 主流发行版默认装的拉丁/符号基础族 (按族名前缀匹配, 大小写不敏感)
BASELINE_FAMILIES = (
    "DejaVu Sans",
    "DejaVu Serif",
    "DejaVu Sans Mono",
    "Liberation Sans",
    "Liberation Serif",
    "Liberation Mono",
    "Noto Sans",
    "Noto Serif",
    "Noto Mono",
    "Noto Color Emoji",
)
FONT_ROOT_NAMES = ("fonts", ".fonts")  # 字体根目录名: 从实际字体文件路径向上找
GENERIC = {"sans-serif": "sans", "serif": "serif", "monospace": "mono"}


def available() -> bool:
    return bool(shutil.which(TOOL_FC_LIST))


def _fc_list(extra: list[str]) -> list[tuple[str, str]]:
    out = subprocess.run(
        [TOOL_FC_LIST, *extra, "--format", "%{family[0]}\t%{file}\n"],
        capture_output=True,
        text=True,
        timeout=20,
        check=False,
    ).stdout
    rows = []
    for line in out.splitlines():
        fam, _, path = line.partition("\t")
        if fam and path:
            rows.append((fam.strip(), path.strip()))
    return rows


def system_fonts() -> list[tuple[str, str]]:
    return _fc_list([])


def cjk_fonts() -> list[tuple[str, str]]:
    return _fc_list([":lang=zh"])


def select(mode: str, families: list[str]) -> list[tuple[str, str]]:
    """按模式选出 (族名, 文件) 列表; 中文字体总是保留。"""
    wanted = [
        f.lower() for f in (BASELINE_FAMILIES if mode == "baseline" else families)
    ]
    chosen = {
        path: fam
        for fam, path in system_fonts()
        if any(fam.lower() == w or fam.lower().startswith(w + " ") for w in wanted)
    }
    for fam, path in cjk_fonts():
        chosen.setdefault(path, fam)
    return sorted(((fam, path) for path, fam in chosen.items()), key=lambda x: x[1])


def _xml(text: str) -> str:
    return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def _conf(files_dir: Path, cache_dir: Path, fonts: list[tuple[str, str]]) -> str:
    fams = list(dict.fromkeys(f for f, _ in fonts))
    aliases = []
    for generic, hint in GENERIC.items():
        pref = [f for f in fams if hint in f.lower()] or fams[:1]
        if pref:
            fam_xml = "".join("<family>%s</family>" % _xml(f) for f in pref[:4])
            aliases.append(
                "  <alias><family>%s</family><prefer>%s</prefer></alias>"
                % (generic, fam_xml)
            )
    return "\n".join(
        [
            '<?xml version="1.0"?>',
            '<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">',
            "<!-- 由 hx-browser 生成: 本副本只可见下列目录中的字体 -->",
            "<fontconfig>",
            "  <dir>%s</dir>" % _xml(str(files_dir)),
            "  <cachedir>%s</cachedir>" % _xml(str(cache_dir)),
            *aliases,
            "</fontconfig>",
            "",
        ]
    )


def prepare(profile_dir: Path, mode: str, families: list[str]) -> dict:
    """生成私有字体目录与 fonts.conf。返回 {conf, files, families}; inherit 返回空。"""
    if mode == "inherit":
        return {}
    if not available():
        raise RuntimeError("字体隔离需要 fontconfig (fc-list); 已拒绝启动, 未降级")
    root = profile_dir / FONT_DIRNAME
    files_dir, cache_dir = root / FILES_DIRNAME, root / "cache"
    shutil.rmtree(files_dir, ignore_errors=True)
    files_dir.mkdir(parents=True, exist_ok=True)
    cache_dir.mkdir(parents=True, exist_ok=True)
    fonts = select(mode, families)
    used: set[str] = set()
    for i, (_fam, path) in enumerate(fonts):
        name = Path(path).name
        if name in used:
            name = "%d-%s" % (i, name)
        used.add(name)
        os.symlink(path, files_dir / name)
    conf = root / CONF_NAME
    conf.write_text(_conf(files_dir, cache_dir, fonts), encoding="utf-8")
    return {
        "conf": str(conf),
        "files": [p for _f, p in fonts],
        "families": sorted({f for f, _ in fonts}),
    }


def font_roots(files: list[str]) -> list[str]:
    """系统字体所在的根目录 (如 .../share/fonts)。由 fontconfig 实际报告的文件推出,
    而不是写死目录表 —— 发行版/用户自装位置各不相同, 漏一个就是漏一批字体。"""
    roots = set()
    for f in files:
        for parent in Path(f).parents:
            if parent.name in FONT_ROOT_NAMES:
                roots.add(str(parent))
                break
    return sorted(roots)


def sandbox_args(font_files: list[str]) -> list[str]:
    """bwrap 参数: 遮蔽所有系统字体根目录, 只把白名单文件按原路径只读绑回。"""
    out: list[str] = []
    for d in font_roots([p for _f, p in system_fonts()]):
        if Path(d).is_dir():
            out += ["--tmpfs", d]
    for f in font_files:
        out += ["--ro-bind-try", f, f]
    return out
