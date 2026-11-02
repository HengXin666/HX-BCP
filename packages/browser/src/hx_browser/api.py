"""FastAPI app assembly: routers in their original registration order, middlewares, static page.

Agent Note: .agents/notes/implemented/architecture/2026-11-01-api-split.md
"""

from __future__ import annotations

import contextlib
import logging
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from . import (api_cache, api_env_ops, api_envs, api_fingerprint, api_folders, api_hxcp, api_lane, api_live, api_packs,
               api_profiles, api_resources, api_system, hxcp_link, lane_guard, mcp_http, profile_ops, relay)
from .api_common import fail, manager, ok, store  # noqa: F401  re-exported for existing callers
from .api_env_ops import start_env
from .browser import load_persisted_runtime
from .config import settings_manager
from .web_static import ConsoleStatic

log = logging.getLogger("hx_browser.api")


def create_app() -> FastAPI:
    @contextlib.asynccontextmanager
    async def _lifespan(_app: FastAPI):
        await profile_ops.resume_relays(store())  # 接管的在跑副本: 原端口重建 relay, 否则代理全断
        yield
        await hxcp_link.astop_all()
        manager().stop_all()
        await relay.stop_all()

    app = FastAPI(title="HX-Browser", version="0.1.0", lifespan=_lifespan)
    try:
        n = load_persisted_runtime()
        hxcp_link.adopt_from_runtime()
        if n:
            log.info("接管 %d 个在跑的浏览器环境 (runtime.json)", n)
    except Exception as e:  # noqa: BLE001
        log.warning("接管运行态失败: %s", e)
    app.include_router(api_envs.router)  # list and create, lane scoped
    app.include_router(api_folders.router)  # folders inside a lane group
    app.include_router(api_system.router)  # status, settings, orphans, logs, groups
    app.include_router(api_env_ops.router)  # single browser crud, start, stop
    app.include_router(api_packs.router)  # account packs, cookies, cache, fingerprint check
    app.include_router(api_resources.router)  # proxies, templates, extensions
    app.include_router(api_profiles.build_router(start_env, manager))  # 副本管理/检测修复/回收站
    app.include_router(api_live.build_router())  # 实时预览: 在跑副本列表 + WS 帧流/远程操作
    app.include_router(api_hxcp.router)  # hxcp 缓存代理挂载
    app.include_router(api_fingerprint.router)  # 指纹自检结论
    app.include_router(api_lane.router)  # console session cookie of the human lane
    app.include_router(api_cache.router)  # cache proxy stats for the console
    mcp_http.mount(app)  # MCP Streamable HTTP: POST /mcp (stdio 版见 mcp_server.py)

    app.middleware("http")(lane_guard.middleware)  # lane admission, runs inside the api key check

    @app.middleware("http")
    async def api_key_middleware(request: Request, call_next):
        s = settings_manager.load()
        if s.api_key:
            key = request.headers.get("x-api-key", "")
            if key != s.api_key and request.url.path != "/":
                return JSONResponse({"code": -1, "msg": "invalid api key"}, status_code=401)
        return await call_next(request)

    # console built from apps/console-web (vite outDir), see .review/serve-new-console-on-8325.md
    web_dir = Path(__file__).parent / "web"
    if web_dir.exists():
        app.mount("/", ConsoleStatic(directory=str(web_dir), html=True), name="web")

    return app
