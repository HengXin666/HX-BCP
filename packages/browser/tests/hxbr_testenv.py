"""测试用隔离数据目录: 每个测试一个临时 data_dir, 绝不碰 services/hx-browser/data。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md
"""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

from fastapi.testclient import TestClient  # noqa: E402

from hx_browser import api, config, store  # noqa: E402
from hx_browser.models import Settings  # noqa: E402


def isolate(tmp_path: Path) -> Path:
    """把全局 settings/store/manager 指到 tmp_path; 返回 data_dir。"""
    data = tmp_path / "data"
    data.mkdir(parents=True, exist_ok=True)
    config.settings_manager.path = data / "settings.json"
    config.settings_manager.save(
        Settings(data_dir=str(data), chromium_path="", firefox_path="")
    )
    store.store_singleton = None
    api._manager = None
    return data


def client(tmp_path: Path):
    isolate(tmp_path)
    return TestClient(api.create_app())
