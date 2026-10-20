"""HX-Browser 冒烟测试：服务生命周期 + 环境隔离 + Cookie 注入 + 账号包闭环。"""

from __future__ import annotations

import json
import time
import urllib.request

import pytest

BASE = "http://127.0.0.1:8325/api/v1"


def http(method: str, path: str, body=None) -> dict:
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


def wait_status(timeout: float = 20.0) -> dict:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            return http("GET", "/status")
        except Exception:
            time.sleep(0.5)
    raise RuntimeError("service not ready")


@pytest.fixture(scope="module")
def srv():
    return wait_status()


def test_status(srv):
    assert srv["code"] == 0
    assert srv["data"]["status"] == "ok"


def test_env_crud(srv):
    d = http("POST", "/environments", {"name": "pytest-环境", "fingerprint": {"browser": "sun", "timezone": "Asia/Shanghai"}})
    assert d["code"] == 0
    env_id = d["data"]["id"]
    lst = http("GET", "/environments?search=pytest")
    assert any(e["id"] == env_id for e in lst["data"]["list"])
    # 更新
    u = http("PUT", f"/environments/{env_id}", {"name": "pytest-环境-改名"})
    assert u["data"]["name"] == "pytest-环境-改名"
    # 删除
    http("DELETE", f"/environments/{env_id}")
    lst2 = http("GET", "/environments?search=pytest")
    assert not any(e["id"] == env_id for e in lst2["data"]["list"])


def test_account_pack_circle(srv):
    """账号包闭环：注入 → 导出（域名过滤）→ 导入 → 验证。"""
    s = http("GET", "/settings")["data"]
    if not s.get("chromium_path"):
        pytest.skip("未配置 Chromium")
    d = http("POST", "/environments", {
        "name": "pytest-pack",
        "platform": "google",
        "account": {"cookies": json.dumps([
            {"name": "SID", "value": "FAKE_SID", "domain": ".google.com", "path": "/", "secure": True, "httpOnly": True},
            {"name": "NID", "value": "FAKE_NID", "domain": ".google.com", "path": "/"},
            {"name": "other", "value": "x", "domain": ".example.com", "path": "/"},
        ])},
        "fingerprint": {"browser": "sun", "headless": True},
    })
    env_id = d["data"]["id"]
    st = http("POST", f"/environments/{env_id}/start")
    assert st["code"] == 0, st
    time.sleep(3)
    # 导出账号包（只应含 google 域）
    pack = http("POST", f"/environments/{env_id}/account-pack/export")["data"]
    assert all("google.com" in c["domain"] for c in pack["cookies"]), "账号包应只含 google 域 Cookie"
    assert pack["meta"]["total_cookies"] >= 2
    http("POST", f"/environments/{env_id}/stop")
    # 导入 → 新环境
    imp = http("POST", "/account-packs/import", {"pack": pack, "name": "pytest-imported"})
    assert imp["code"] == 0
    new_id = imp["data"]["id"]
    new_env = http("GET", f"/environments/{new_id}")["data"]
    assert "SID" in new_env["account"]["cookies"]
    # 清理
    http("DELETE", f"/environments/{new_id}")
    http("DELETE", f"/environments/{env_id}")
