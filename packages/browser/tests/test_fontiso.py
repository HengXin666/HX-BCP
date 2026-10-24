"""fontiso.select: baseline 保留中文字体, strict 去掉所有中文字体 (对齐 NodeCloak 「限制字体」)。"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from hx_browser import fontiso  # noqa: E402

SYSTEM = [
    ("DejaVu Sans", "/f/DejaVuSans.ttf"),
    ("Noto Sans", "/f/NotoSans.ttf"),
    ("Noto Sans CJK SC", "/f/NotoSansCJK.ttc"),
    ("FZShuTi", "/f/FZSTK.TTF"),
    ("Comic Neue", "/f/ComicNeue.ttf"),
]
CJK = [("Noto Sans CJK SC", "/f/NotoSansCJK.ttc"), ("FZShuTi", "/f/FZSTK.TTF")]


def _patch(monkeypatch):
    monkeypatch.setattr(fontiso, "system_fonts", lambda: SYSTEM)
    monkeypatch.setattr(fontiso, "cjk_fonts", lambda: CJK)


def _families(mode, families=()):
    return {fam for fam, _path in fontiso.select(mode, list(families))}


def test_baseline_keeps_every_cjk_font(monkeypatch):
    _patch(monkeypatch)
    assert _families("baseline") == {"DejaVu Sans", "Noto Sans", "Noto Sans CJK SC", "FZShuTi"}


def test_strict_drops_cjk_even_when_prefix_matches(monkeypatch):
    _patch(monkeypatch)
    assert _families("strict") == {"DejaVu Sans", "Noto Sans"}


def test_custom_still_adds_cjk(monkeypatch):
    _patch(monkeypatch)
    assert _families("custom", ["Comic Neue"]) == {"Comic Neue", "Noto Sans CJK SC", "FZShuTi"}
