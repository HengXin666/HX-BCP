"""副本级系统字体隔离 (所有内核) —— 网页只能枚举到白名单里的字体。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

## 两层

1. fontconfig 层 (process 与 sandbox 都生效): 为副本生成私有 fonts.conf, 只含一个目录
   ``<profile>/.hx-fonts/files`` —— 里面是白名单字体文件的软链; 浏览器以
   FONTCONFIG_FILE 启动, 枚举不到其他字体。用户家目录字体/conf.d 里的额外目录一概不读。
2. 文件系统层 (仅 sandbox): 系统字体目录被 tmpfs 遮蔽, 只把白名单文件按原路径只读
   绑回去 —— 即使兼容 fontconfig 直接读目录也看不到其他字体文件。

## 选哪些字体

* ``baseline``: 常见 Linux 发行版都有的拉丁基础族 + 电脑上**所有能显示中文的字体**
  (用户明确要求保留中文字体; 去掉它们中文页面会变方块, 本身就是异常信号)。
* ``custom``: 用户给的族名列表 (仍会自动补上中文字体, 理由同上)。
* ``strict``: 只留拉丁基础族与 Emoji, 去掉所有能显示中文的字体。对齐 NodeCloak 的「限制字体」:
  需用户主动选择, 不进一键修复; 中文页面可能显示为方块。
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
# 按 os 的字体族集合。字体是操作系统自带的, 三个不同 os 的副本读回同一份字体列表
# 就是最强的关联特征之一。只列该 os 出厂自带的族名, 宿主上没有的由 select_os 自然跳过。
# macOS 出处: Apple "Fonts included with macOS Sonoma" https://support.apple.com/en-us/103203
#   (不含 Segoe UI / Calibri / Roboto: 那是 Windows / Android 的字体, 出现在 mac 画像里就是破绽)
# Windows 出处: Microsoft "Windows 10 font list" https://learn.microsoft.com/typography/fonts/windows_10_font_list
#   (不含 Andale Mono / Helvetica / Menlo: macOS 独有)
FAMILIES_BY_OS = {
    "macOS": (
        "Andale Mono", "Arial", "Arial Black", "Comic Sans MS", "Courier", "Courier New", "Georgia",
        "Helvetica", "Helvetica Neue", "Impact", "Lucida Grande", "Menlo", "Monaco", "Tahoma",
        "Times", "Times New Roman", "Trebuchet MS", "Verdana", "Webdings",
    ),
    "Windows": (
        "Arial", "Arial Black", "Calibri", "Cambria", "Candara", "Comic Sans MS", "Consolas",
        "Courier New", "Georgia", "Impact", "Lucida Console", "Segoe UI", "Tahoma",
        "Times New Roman", "Trebuchet MS", "Verdana", "Webdings",
    ),
    # Linux: 主流发行版默认装的族, 与 BASELINE_FAMILIES 同源
    "Linux": BASELINE_FAMILIES,
}
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
    """按模式选出 (族名, 文件) 列表; 除 strict 外中文字体总是保留。

    族名按**完整族名**匹配, 不做前缀展开: 前缀匹配会把 Noto Sans 展开成
    Noto Sans CJK KR / JP / SC 几十个变体, 实测 2230 个文件全被选中, 隔离等于没做。
    同一个族的 Bold/Italic 是 fontconfig 的 style, _fc_list 拿到的族名已经归一。
    """
    base = BASELINE_FAMILIES if mode in ("baseline", "strict") else families
    wanted = {f.strip().lower() for f in base if f and f.strip()}
    chosen = {
        path: fam
        for fam, path in system_fonts()
        if fam.lower() in wanted
    }
    if mode == "strict":
        cjk = {path for _fam, path in cjk_fonts()}
        chosen = {path: fam for path, fam in chosen.items() if path not in cjk}
    else:
        for fam, path in cjk_fonts():
            chosen.setdefault(path, fam)
    return sorted(((fam, path) for path, fam in chosen.items()), key=lambda x: x[1])


# 中文族: mac / Windows 出处同 FAMILIES_BY_OS; Linux 取 Debian/Ubuntu 的 fonts-noto-cjk 与 fonts-wqy-zenhei
CJK_BY_OS = {
    "macOS": ("PingFang SC", "Hiragino Sans", "Songti SC", "Heiti SC"),
    "Windows": ("Microsoft YaHei", "SimSun", "SimHei", "Microsoft JhengHei"),
    "Linux": ("Noto Sans CJK SC", "Noto Serif CJK SC", "WenQuanYi Zen Hei"),
}


def select_os(os_name: str) -> list[tuple[str, str]]:
    """按 os 选出自己那套字体, 含中文子集。

    不能走 select 的 custom 分支: 那里把系统里全部中文字体无条件塞进来,
    三个 os 的副本会因此共享同一份中文族, 隔离被稀释成只在拉丁部分有差别。
    """
    families = {f.strip().lower() for f in FAMILIES_BY_OS.get(os_name, ())}
    cjk = {f.strip().lower() for f in CJK_BY_OS.get(os_name, ())}
    pool = system_fonts() + cjk_fonts()
    chosen: dict[str, str] = {}
    for fam, path in pool:
        low = fam.lower()
        if low in families or low in cjk:
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


def prepare(
    profile_dir: Path, mode: str, families: list[str], os_name: str = ""
) -> dict:
    """生成私有字体目录与 fonts.conf。返回 {conf, files, families}; inherit 返回空。

    strict 用固定白名单, 不同 os 的副本会读到同一份字体, 因此给了 os_name 且
    没有显式指定 families 时, 改按该 os 的默认族集合隔离。
    """
    if mode == "inherit":
        return {}
    if not families and os_name in FAMILIES_BY_OS:
        return _prepare_with(profile_dir, select_os(os_name))
    return _prepare_with(profile_dir, select(mode, families))


def _prepare_with(profile_dir: Path, fonts: list[tuple[str, str]]) -> dict:
    """把选定字体按软链摆进私有目录, 写 fonts.conf; 返回 conf/files/families。"""
    if not available():
        raise RuntimeError("字体隔离需要 fontconfig (fc-list); 已拒绝启动, 未降级")
    root = profile_dir / FONT_DIRNAME
    files_dir, cache_dir = root / FILES_DIRNAME, root / "cache"
    shutil.rmtree(files_dir, ignore_errors=True)
    files_dir.mkdir(parents=True, exist_ok=True)
    cache_dir.mkdir(parents=True, exist_ok=True)
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
