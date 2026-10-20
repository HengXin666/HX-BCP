"""MCP 的 JSON-RPC 处理 + Streamable HTTP 传输 (POST /mcp)。stdio 传输见 mcp_server.py。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 为什么 HTTP 端点不经网络回调自己

/mcp 收到 tools/call 后用 httpx.ASGITransport 直接把请求交给**同一个 app** ——
走完整的路由与中间件 (含 x-api-key 鉴权), 但不占端口、不受监听地址影响。
客户端带来的 x-api-key 原样转给内部请求, 鉴权语义与直接调 REST 相同。

## 协议范围

无状态 Streamable HTTP: 每个 POST 是一条 JSON-RPC 请求 (或批量), 响应
application/json; 通知 (无 id) 回 202。不开 SSE 流 —— 本服务没有服务端主动推送的工具。
"""

from __future__ import annotations

import httpx
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, Response

from . import mcp_tools

SUPPORTED_VERSIONS = ("2025-06-18", "2025-03-26", "2024-11-05")
SERVER_INFO = {"name": "hx-browser", "version": "0.2.0"}
API_KEY_HEADER = "x-api-key"
JSONRPC = "2.0"


def _error(req_id, code: int, message: str) -> dict:
    return {
        "jsonrpc": JSONRPC,
        "id": req_id,
        "error": {"code": code, "message": message},
    }


async def handle(msg: dict, client: httpx.AsyncClient) -> dict | None:
    """处理一条 JSON-RPC 消息; 通知返回 None。"""
    if (
        not isinstance(msg, dict)
        or msg.get("jsonrpc") != JSONRPC
        or "method" not in msg
    ):
        return _error(
            msg.get("id") if isinstance(msg, dict) else None, -32600, "Invalid Request"
        )
    req_id, method, params = msg.get("id"), msg["method"], msg.get("params") or {}
    if req_id is None:
        return None  # notifications/initialized 等通知: 无需响应
    if method == "initialize":
        asked = params.get("protocolVersion", "")
        result = {
            "protocolVersion": asked
            if asked in SUPPORTED_VERSIONS
            else SUPPORTED_VERSIONS[0],
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": SERVER_INFO,
            "instructions": "管理 HX-Browser 指纹浏览器副本。start_profile 返回的 cdp_http 可交给 CDP 客户端接管页面。",
        }
    elif method == "ping":
        result = {}
    elif method == "tools/list":
        result = {"tools": mcp_tools.tool_list()}
    elif method == "tools/call":
        args = params.get("arguments") or {}
        if not isinstance(args, dict):
            return _error(req_id, -32602, "arguments must be an object")
        result = await mcp_tools.call_tool(client, str(params.get("name", "")), args)
    else:
        return _error(req_id, -32601, "Method not found: %s" % method)
    return {"jsonrpc": JSONRPC, "id": req_id, "result": result}


def mount(app: FastAPI) -> None:
    @app.post("/mcp")
    async def mcp_endpoint(request: Request):
        try:
            payload = await request.json()
        except ValueError:
            return JSONResponse(_error(None, -32700, "Parse error"), status_code=400)
        headers = {API_KEY_HEADER: request.headers.get(API_KEY_HEADER, "")}
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(
            transport=transport,
            base_url="http://hx-browser",
            headers=headers,
            timeout=120,
        ) as client:
            if isinstance(payload, list):
                out = [
                    r
                    for r in [await handle(m, client) for m in payload]
                    if r is not None
                ]
                return JSONResponse(out) if out else Response(status_code=202)
            res = await handle(payload, client)
        return JSONResponse(res) if res is not None else Response(status_code=202)

    @app.get("/mcp")
    async def mcp_get():
        return Response(status_code=405, headers={"allow": "POST"})
