"""What fonts a page in one hx-browser environment should see, computed from that environment's own fonts.conf.

Used by independence.py to check font isolation end to end: the fonts the page detects must equal the fonts fc-list
reports under FONTCONFIG_FILE=<profile>/.hx-fonts/fonts.conf, once Chrome's own name matching is applied.

    python gate/fontcheck.py <profile_dir>      prints fc-list families and the expected page-visible probe fonts
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

# Fonts the page probes: OS markers from the three fontiso.FAMILIES_BY_OS sets plus fonts no profile should expose.
PROBE_FONTS = ["Andale Mono", "Arial", "Arial Black", "Calibri", "Cambria", "Comic Sans MS", "Consolas", "Courier New",
               "DejaVu Sans", "DejaVu Serif", "Georgia", "Helvetica", "Helvetica Neue", "Impact", "Liberation Mono",
               "Liberation Sans", "Lucida Grande", "Menlo", "Microsoft YaHei", "Monaco", "Noto Sans", "PingFang SC",
               "Roboto", "Segoe UI", "SF Pro Text", "Tahoma", "Times New Roman", "Trebuchet MS", "Ubuntu", "Verdana", "Webdings"]

# How Chrome decides a probed family exists (Linux): fontconfig's best match for the name must carry that name or a
# metric-compatible one (Skia kFontEquivMap, src/ports/SkFontConfigInterface_direct.cpp); if not, Blink retries once
# with an alternate name (AlternateFamilyName, third_party/blink/renderer/platform/fonts/alternate_font_family.h).
SKIA_EQUIV = [
    {"arial", "arimo", "liberation sans"},
    {"times new roman", "tinos", "liberation serif"},
    {"courier new", "cousine", "liberation mono"},
    {"cambria", "caladea"},
    {"calibri", "carlito"},
]
BLINK_ALT = {"arial": "helvetica", "helvetica": "arial", "courier": "courier new", "courier new": "courier",
             "times": "times new roman", "times new roman": "times"}


def conf_of(profile_dir: str) -> Path:
    return Path(profile_dir or "/nonexistent") / ".hx-fonts" / "fonts.conf"


def _fc(conf: Path, *args: str) -> str:
    return subprocess.run(["fc-list" if args[0] == ":" else "fc-match", *args], capture_output=True, text=True,
                          timeout=30, check=False, env={**os.environ, "FONTCONFIG_FILE": str(conf.resolve())}).stdout


def detectable(conf: Path, font: str) -> bool:
    """True when Chrome would report this family as installed under conf (rules above)."""
    def accepted(name: str) -> bool:
        got = {n.strip().lower() for n in _fc(conf, "-f", "%{family}", name).split(",") if n.strip()}
        low = name.lower()
        return low in got or any(low in group and group & got for group in SKIA_EQUIV)
    alt = BLINK_ALT.get(font.lower())
    return accepted(font) or bool(alt and accepted(alt))


def fc_fonts(profile_dir: str) -> tuple[list[str], list[str]] | None:
    """(PROBE_FONTS present in fc-list, PROBE_FONTS the page should detect); None when the profile has no fonts.conf."""
    conf = conf_of(profile_dir)
    if not conf.exists():
        return None
    names = {n.strip().lower() for line in _fc(conf, ":", "family").splitlines() for n in line.split(",") if n.strip()}
    return [f for f in PROBE_FONTS if f.lower() in names], [f for f in PROBE_FONTS if detectable(conf, f)]


if __name__ == "__main__":
    got = fc_fonts(sys.argv[1])
    if got is None:
        sys.exit("no fonts.conf under %s" % sys.argv[1])
    print("fc-list:  " + ",".join(got[0]))
    print("expected: " + ",".join(got[1]))
