"""MCP stdio 服务器: ``uv run python -m hx_browser.mcp_server`` (连到运行中的 HX-Browser)。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

环境变量:
    HXBR_URL      HX-Browser 地址, 默认 http://127.0.0.1:8325
    HXBR_API_KEY  若服务设置了 api_key, 填同一个值

客户端配置示例 (Claude Desktop / Cursor / Codex 的 mcpServers):

    {"hx-browser": {"command": "uv", "args": ["--directory", "<hx-browser 目录>",
      "run", "python", "-m", "hx_browser.mcp_server"]}}

stdout 只写 JSON-RPC 消息 (每行一条), 日志一律走 stderr —— 往 stdout 打印任何别的东西
都会让客户端解析失败。
"""

from __future__ import annotations

import asyncio
import json
import os
import sys

import httpx

from .mcp_http import API_KEY_HEADER, _error, handle

DEFAULT_URL = "http://127.0.0.1:8325"


async def serve(stdin=None, stdout=None) -> None:
    stdin = stdin or sys.stdin
    stdout = stdout or sys.stdout
    headers = {API_KEY_HEADER: os.environ.get("HXBR_API_KEY", "")}
    base = os.environ.get("HXBR_URL", DEFAULT_URL)
    async with httpx.AsyncClient(
        base_url=base, headers=headers, timeout=120, trust_env=False
    ) as client:
        while True:
            line = await asyncio.to_thread(stdin.readline)
            if not line:
                return
            if not line.strip():
                continue
            try:
                msg = json.loads(line)
            except ValueError:
                res: dict | list | None = _error(None, -32700, "Parse error")
            else:
                if isinstance(msg, list):
                    res = [
                        r
                        for r in [await handle(m, client) for m in msg]
                        if r is not None
                    ] or None
                else:
                    res = await handle(msg, client)
            if res is not None:
                stdout.write(json.dumps(res, ensure_ascii=False) + "\n")
                stdout.flush()


def main() -> None:
    try:
        asyncio.run(serve())
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
