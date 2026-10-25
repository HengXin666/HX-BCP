"""HX-Browser 命令行入口: hxb serve."""

from __future__ import annotations

import argparse
import logging


def build_parser() -> argparse.ArgumentParser:
    """构造 hxb 的参数解析器, 无子命令时按 serve 处理."""
    p = argparse.ArgumentParser(prog="hxb", description="HX-Browser web service")
    p.set_defaults(host="0.0.0.0", port=None, reload=False)
    sub = p.add_subparsers(dest="cmd")
    s = sub.add_parser("serve", help="run the HX-Browser web service")
    s.add_argument("--host", default="0.0.0.0", help="listen host (default 0.0.0.0)")
    s.add_argument("--port", type=int, default=None, help="listen port (default from settings, 8325)")
    s.add_argument("--reload", action="store_true", help="dev auto-reload")
    return p


def serve(host: str, port: int | None, reload: bool) -> int:
    """在 host:port 上启动 HX-Browser 的 API 与管理台."""
    import uvicorn

    from .config import settings_manager

    port = port or settings_manager.load().api_port
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    print(f"\n  HX-Browser listening on http://{host}:{port}")
    print(f"  API:    http://{host}:{port}/api/v1/status")
    print(f"  Web UI: http://{host}:{port}/\n")
    uvicorn.run("hx_browser.api:create_app", host=host, port=port, reload=reload, factory=True)
    return 0


def main(argv: list[str] | None = None) -> int:
    """hxb 入口, 解析参数后启动服务."""
    args = build_parser().parse_args(argv)
    return serve(args.host, args.port, args.reload)
