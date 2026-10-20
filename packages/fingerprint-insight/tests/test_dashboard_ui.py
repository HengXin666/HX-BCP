"""面板的真实端到端验证: 用无头 Chromium 打开面板 -> 点「开始观测」-> 断言界面上真的出现数据。

为什么不只测 API: 内存纪律「测试须走真实端到端路径, 全绿不等于功能可用」。
API 全绿而面板白屏/不渲染, 用户拿到的东西就是坏的 —— 本仓库栽过这类跟头。
"""
from __future__ import annotations

import asyncio
import contextlib
import socket
import sqlite3
import sys
import threading
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from fpinsight.hub import Hub  # noqa: E402
from fpinsight.session import find_chromium  # noqa: E402
from test_e2e import FixtureServer, _free_port  # noqa: E402

pytestmark = pytest.mark.skipif(find_chromium() is None, reason="本机无 Chromium")
FIXTURE = Path(__file__).resolve().parent / "fixtures" / "fp-live.html"


async def _drive(tmp_path: Path):
    """起服务 -> 起浏览器 -> 打开面板 -> 点按钮 -> 读界面。全程同一事件循环。"""
    import uvicorn
    from playwright.async_api import async_playwright

    import fpinsight.app as appmod

    port = _free_port()
    hub = Hub(tmp_path / "ui.db", port=port)
    hub._conn_init()
    hub._writer = threading.Thread(target=hub._writer_loop, daemon=True)
    hub._writer.start()
    appmod.HUB = hub
    appmod.SESSIONS.clear()

    cfg = uvicorn.Config(appmod._APP, host="127.0.0.1", port=port, log_level="error")
    srv = uvicorn.Server(cfg)
    task = asyncio.create_task(srv.serve())
    for _ in range(240):
        if srv.started:
            break
        await asyncio.sleep(0.05)
    assert srv.started

    fixture = FixtureServer(FIXTURE.parent)
    out: dict = {"bad_requests": [], "errors": []}
    try:
        async with async_playwright() as pw:
            b = await pw.chromium.launch(headless=True, executable_path=find_chromium(),
                                         chromium_sandbox=False)
            pg = await b.new_page(viewport={"width": 1680, "height": 950})
            pg.on("console", lambda m: out["errors"].append(m.text) if m.type == "error" else None)
            pg.on("pageerror", lambda e: out["errors"].append(f"pageerror: {e}"))
            pg.on("response", lambda r: out["bad_requests"].append((r.status, r.url))
                  if r.status >= 400 else None)

            await pg.goto(f"http://127.0.0.1:{port}/", wait_until="networkidle")
            await pg.fill("#url", fixture.url)
            await pg.click("#launch")
            await pg.wait_for_timeout(9000)

            out["apis"] = await pg.locator("#apis .api").count()
            out["calls"] = await pg.locator("#calls tr").count()
            out["top_api"] = (await pg.locator("#apis .api .p").first.inner_text()
                              if out["apis"] else "")
            out["top_val"] = (await pg.locator("#apis .api .v").first.inner_text()
                              if out["apis"] else "")

            # 切到「恒定量」应看到纠察条目
            await pg.click('.tabs button[data-tab="inv"]')
            await pg.wait_for_timeout(2200)
            out["inv"] = await pg.locator("#invBody .iv").count()

            # 点一行 -> 详情面板应出现调用栈
            if out["calls"]:
                await pg.locator("#calls tr").first.click()
                await pg.wait_for_timeout(700)
                out["stack_frames"] = await pg.locator("#detail .fr").count()
                out["detail_head"] = await pg.locator("#detail .hd").inner_text()
            else:
                out["stack_frames"] = 0
                out["detail_head"] = ""

            await pg.screenshot(path=str(tmp_path / "dashboard.png"), full_page=False)
            await b.close()
        return out, hub
    finally:
        fixture.close()
        with contextlib.suppress(Exception):
            hub.stop()
        with contextlib.suppress(Exception):
            srv.should_exit = True
            await asyncio.wait_for(task, timeout=8)


def test_dashboard_renders_real_observation(tmp_path: Path) -> None:
    out, _ = asyncio.run(_drive(tmp_path))
    assert not out["errors"], f"面板有 JS 报错: {out['errors']}"
    assert out["apis"] > 5, f"技术画像没渲染出指纹面: {out}"
    assert out["calls"] > 5, f"调用流水没渲染出记录: {out}"
    assert out["detail_head"], "详情面板为空"
    assert out["stack_frames"] >= 1, "调用栈没渲染出来"


def test_dashboard_has_no_broken_assets(tmp_path: Path) -> None:
    """回归: 面板曾有一个 404 资源 (favicon), 会污染"零报错"判据。"""
    out, _ = asyncio.run(_drive(tmp_path))
    bad = [u for s, u in out["bad_requests"] if "favicon" not in u]
    assert not bad, f"面板请求了不存在的资源: {bad}"
