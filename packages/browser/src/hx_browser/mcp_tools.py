"""MCP 工具表 —— 每个工具 = 一个既有 REST 端点 (stdio 与 HTTP 两种传输共用)。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 为什么 MCP 只是 REST 的一层薄壳

判据 (删除要先进回收站、偏好互斥、代理不回落直连...) 只在 REST 背后实现一次;
MCP 工具若直接调内部函数, 就会出现两套入口两套判据。所以这里只做:
参数 -> HTTP 请求 -> 结果脱敏 -> MCP content。

## 脱敏

返回给 AI 的 JSON 里 password / two_factor / cookies 一律替换为 "***" (非空时)。
REST 本身仍返回明文 (管理台编辑表单依赖它, 改了会破坏既有前端)。

## 与 CDP 的关系

start_profile 返回 runtime.cdp_http —— 交给 Playwright MCP / chrome-devtools-mcp
(``--browserUrl``) 或任何 CDP 客户端即可接管页面。本服务不重复实现页面操作工具。
"""

from __future__ import annotations

import json

import httpx

SECRET_KEYS = {"password", "two_factor", "cookies"}
MASK = "***"
_STR = {"type": "string"}
_BOOL = {"type": "boolean"}
ENV_ID = {"env_id": {**_STR, "description": "副本 id (env_xxx)"}}
_PROFILE_FIELDS = {
    "name": _STR,
    "tags": {"type": "array", "items": _STR},
    "notes": _STR,
    "kernel": {
        **_STR,
        "description": "cloak/chrome/chromium/edge/brave/firefox/librewolf",
    },
    "proxy_link": {
        **_STR,
        "description": "socks5://user:pass@host:port / hxpxy://身份 / direct / system",
    },
    "prefs": {
        "type": "object",
        "description": "副本偏好, 字段见 GET /api/v1/environments/{id}/prefs",
    },
}

# name -> (描述, 额外参数 schema, 必填参数, HTTP 方法, 路径模板)
TOOLS: dict[str, tuple[str, dict, list[str], str, str]] = {
    "list_profiles": ("列出副本 (搜索名称/备注/标签, 按标签筛选, deleted=true 看最近删除)",
                      {"search": _STR, "tag": _STR, "deleted": _BOOL}, [], "GET", "/profiles"),
    "get_profile": ("读取副本详情 (含运行态与 CDP 端点)", ENV_ID, ["env_id"], "GET", "/environments/{env_id}"),
    "create_profile": ("新建副本", _PROFILE_FIELDS, ["name"], "POST", "/environments"),
    "update_profile": ("修改副本 (运行中修改代理/偏好会显示待重启)", {**ENV_ID, **_PROFILE_FIELDS}, ["env_id"],
                       "PUT", "/environments/{env_id}"),
    "start_profile": ("启动副本, 返回 runtime.cdp_http 供 CDP 客户端接管", ENV_ID, ["env_id"], "POST",
                      "/environments/{env_id}/start"),
    "stop_profile": ("关闭副本", ENV_ID, ["env_id"], "POST", "/environments/{env_id}/stop"),
    "restart_profile": ("重启副本 (应用待重启的设置)", ENV_ID, ["env_id"], "POST", "/environments/{env_id}/restart"),
    "focus_window": ("定位副本窗口 (激活并置前)", ENV_ID, ["env_id"], "POST", "/environments/{env_id}/window/focus"),
    "check_profile": ("检测网络/语言/时区/时钟/WebRTC/DNS/字体等", {**ENV_ID, "network": _BOOL},
                      ["env_id"], "GET", "/environments/{env_id}/doctor"),
    "repair_profile": ("按项或一键修复 (修改前自动备份, 可按记录恢复); items 为空 = 全部可修复项",
                       {**ENV_ID, "items": {"type": "array", "items": _STR}}, ["env_id"], "POST",
                       "/environments/{env_id}/doctor/fix"),
    "list_repairs": ("修复记录", {"env_id": _STR}, [], "GET", "/repairs"),
    "restore_repair": ("按修复记录恢复 (副本需已关闭)", {"record_id": _STR}, ["record_id"], "POST",
                       "/repairs/{record_id}/restore"),
    "fingerprint_check": ("在运行中的副本里读取网页实际看到的指纹值", ENV_ID, ["env_id"], "GET",
                          "/environments/{env_id}/fingerprint/check"),
    "check_clock": ("比较电脑时间与 HTTPS 服务端时间 (容差 2 分钟)", ENV_ID, ["env_id"], "GET", "/clock"),
    "delete_profile": ("移至最近删除 (可恢复)", ENV_ID, ["env_id"], "DELETE", "/environments/{env_id}"),
    "restore_profile": ("从最近删除恢复", ENV_ID, ["env_id"], "POST", "/environments/{env_id}/restore"),
    "purge_profile": ("彻底删除 (不可恢复, 清理数据与代理凭据); 需 confirm=true 且副本已在最近删除",
                      {**ENV_ID, "confirm": _BOOL}, ["env_id", "confirm"], "POST",
                      "/environments/{env_id}/purge"),
    "list_kernels": ("列出可用浏览器内核与沙箱能力", {}, [], "GET", "/kernels"),
    "parse_proxy": ("解析代理链接 (兼容 upstream-proxy), 不回显密码", {"link": _STR}, ["link"], "POST", "/proxies/parse"),
    "start_hxcp": ("为运行中的副本挂载 hxcp 缓存代理", ENV_ID, ["env_id"], "POST", "/environments/{env_id}/hxcp/start"),
    "stop_hxcp": ("停止副本的 hxcp 缓存代理", ENV_ID, ["env_id"], "POST", "/environments/{env_id}/hxcp/stop"),
    "hxcp_status": ("查询副本 hxcp 状态", ENV_ID, ["env_id"], "GET", "/environments/{env_id}/hxcp/status"),
}  # fmt: skip


def tool_list() -> list[dict]:
    out = []
    for name, (desc, props, required, _, _) in TOOLS.items():
        schema = {
            "type": "object",
            "properties": props,
            "required": required,
            "additionalProperties": False,
        }
        out.append({"name": name, "description": desc, "inputSchema": schema})
    return out


def redact(value):
    if isinstance(value, dict):
        return {
            k: (MASK if k in SECRET_KEYS and v else redact(v)) for k, v in value.items()
        }
    if isinstance(value, list):
        return [redact(v) for v in value]
    return value


def _body(name: str, args: dict) -> dict:
    body = {
        k: v
        for k, v in args.items()
        if k not in ("env_id", "proxy_link", "kernel", "confirm")
    }
    if args.get("proxy_link"):
        body["proxy"] = {"link": args["proxy_link"]}
    if args.get("kernel"):
        body["fingerprint"] = {"kernel": args["kernel"]}
    return body


async def call_tool(client: httpx.AsyncClient, name: str, args: dict) -> dict:
    """执行工具, 返回 MCP CallToolResult。"""
    if name not in TOOLS:
        return _result({"error": "未知工具: %s" % name}, True)
    _, props, required, method, path = TOOLS[name]
    missing = [k for k in required if args.get(k) in (None, "")]
    unknown = [k for k in args if k not in props]
    if missing or unknown:
        return _result(
            {"error": "参数错误", "missing": missing, "unknown": unknown}, True
        )
    if name == "purge_profile" and args.get("confirm") is not True:
        return _result({"error": "彻底删除不可恢复, 需显式 confirm=true"}, True)
    url = "/api/v1" + path.format(**{k: str(v) for k, v in args.items()})
    params = {
        k: v
        for k, v in args.items()
        if k in ("search", "tag", "deleted", "network", "env_id")
    }
    if method == "GET":
        r = await client.get(
            url,
            params={
                k: str(v).lower() if isinstance(v, bool) else v
                for k, v in params.items()
            },
        )
    elif method == "DELETE":
        r = await client.delete(url)
    else:
        r = await client.request(method, url, json=_body(name, args))
    try:
        data = r.json()
    except ValueError:
        return _result({"error": "HTTP %d" % r.status_code, "body": r.text[:500]}, True)
    failed = r.status_code >= 400 or (
        isinstance(data, dict) and data.get("code") not in (0, None)
    )
    return _result(redact(data), failed)


def _result(payload, is_error: bool) -> dict:
    return {
        "content": [{"type": "text", "text": json.dumps(payload, ensure_ascii=False)}],
        "isError": is_error,
    }
