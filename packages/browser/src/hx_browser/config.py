"""全局设置与路径（对应 PRD F12）。"""

from __future__ import annotations

import json
import os
import shutil
from pathlib import Path

from .models import Settings

APP_DIR = Path(__file__).resolve().parent.parent.parent  # hx-browser/
DEFAULT_DATA_DIR = APP_DIR / "data"
DEFAULT_PROFILES_DIR = DEFAULT_DATA_DIR / "profiles"
DEFAULT_SETTINGS_PATH = DEFAULT_DATA_DIR / "settings.json"


def _find_executable(names: list[str]) -> str:
    for name in names:
        p = shutil.which(name)
        if p:
            return p
    return ""


def detect_chromium() -> str:
    return _find_executable(["google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "microsoft-edge"])


def detect_firefox() -> str:
    return _find_executable(["firefox", "firefox-esr", "firefox-developer-edition"])


class SettingsManager:
    def __init__(self, path: Path | None = None):
        self.path = path or DEFAULT_SETTINGS_PATH

    def load(self) -> Settings:
        data = {}
        if self.path.exists():
            try:
                data = json.loads(self.path.read_text("utf-8"))
            except Exception:
                data = {}
        s = Settings(**{k: v for k, v in data.items() if k in Settings.model_fields})
        if not s.chromium_path:
            s.chromium_path = detect_chromium()
        if not s.firefox_path:
            s.firefox_path = detect_firefox()
        if not s.data_dir:
            s.data_dir = str(DEFAULT_DATA_DIR)
        return s

    def save(self, s: Settings) -> Settings:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text(json.dumps(s.model_dump(), ensure_ascii=False, indent=2), "utf-8")
        return s

    def profiles_dir(self) -> Path:
        return Path(self.load().data_dir) / "profiles"


settings_manager = SettingsManager()
