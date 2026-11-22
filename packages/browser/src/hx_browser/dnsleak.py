"""启动后 DNS 泄漏探针 (dnsleaktest 类): 挂了代理的副本, 解析服务器必须与出口同国家或同 ASN.

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-dns-leak-probe.md
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import re
import time

from . import cdp, kernels, netcheck
from .proxy_link import system_proxy_link, upstream_of

log = logging.getLogger("hx_browser.dnsleak")

CHECK_ID = "dns_leak"
PROBE_DOMAIN = "bash.ws"
ID_URL = "https://%s/id" % PROBE_DOMAIN
TRIGGERS = 10
TRIGGER_MS = 8000
SETTLE_MS = 2000
LOAD_TIMEOUT_S = 20.0
PROBE_TIMEOUT_S = 60.0
HEALTHY, LEAK, UNKNOWN = "healthy", "error", netcheck.STATUS_UNKNOWN
_ASN_RE = re.compile(r"AS\d+", re.IGNORECASE)
_READY_JS = "location.host + ' ' + document.readyState"
_PROBE_JS = """(async (dom, n, ms, settle) => {
  const id = ((document.body && document.body.innerText) || "").trim();
  if (!/^[0-9a-zA-Z]{6,64}$/.test(id)) return {error: "probe id unreadable: " + id.slice(0, 60)};
  const opts = {mode: "no-cors", cache: "no-store"};
  const one = (i) => fetch("https://" + i + "." + id + "." + dom + "/", opts).then(() => 1, () => 1);
  const all = Promise.all(Array.from({length: n}, (_, i) => one(i + 1)));
  await Promise.race([all, new Promise((r) => setTimeout(r, ms))]);
  await new Promise((r) => setTimeout(r, settle));
  const r = await fetch("/dnsleak/test/" + id + "?json", {cache: "no-store"});
  if (!r.ok) return {error: "probe result HTTP " + r.status};
  return {id: id, rows: await r.json()};
})(%s, %d, %d, %d)"""


def applies(env, environ: dict | None = None) -> bool:
    """挂了代理才探测: 自定义上游 (http/https/socks5/hxpxy), 或 system 模式且服务进程带代理环境变量."""
    if upstream_of(env.proxy) is not None:
        return True
    environ = os.environ if environ is None else environ
    return getattr(env.proxy, "type", "") == "system" and bool(system_proxy_link(environ))


def asn_of(text: str) -> str:
    """'AS16509 Amazon.com Inc.' -> 'AS16509', 读不出返回空串."""
    m = _ASN_RE.search(text or "")
    return m.group(0).upper() if m else ""


def result(status: str, value: str, detail: str = "", **extra) -> dict:
    """与 doctor 检查项同形状, 另带 exit/resolvers/checked_at 等证据字段."""
    out = {"id": CHECK_ID, "status": status, "value": value, "detail": detail, "fixable": False, "fix": {}}
    out.update(extra)
    out.setdefault("checked_at", int(time.time() * 1000))
    return out


def _host(row: dict) -> dict:
    return {
        "ip": str(row.get("ip") or ""),
        "country": str(row.get("country") or "").upper(),
        "asn": asn_of(str(row.get("asn") or "")),
        "org": str(row.get("org") or "")[:100],
    }


def _label(h: dict) -> str:
    return " ".join(x for x in (h["ip"], h["country"], h["asn"], h.get("org", "")) if x)


def assess(rows: list) -> dict:
    """纯函数: 探针结果行 -> 检查项. 任一解析服务器既不同国家也不同 ASN 即标红, 证据不足一律 unknown."""
    rows = [r for r in rows or [] if isinstance(r, dict)]
    exits = [_host(r) for r in rows if r.get("type") == "ip"]
    found = [_host(r) for r in rows if r.get("type") == "dns"]
    if not exits or not exits[0]["country"]:
        return result(UNKNOWN, "未取到出口 IP 的国家", "探针没有返回出口, 无法比对")
    exit_ = exits[0]
    if not found:
        return result(UNKNOWN, "探针没有观测到任何解析服务器", "浏览器内的解析请求未到达探针域, 无法判定", exit=exit_)
    for h in found:
        same_asn = bool(h["asn"]) and h["asn"] == exit_["asn"]
        h["match"] = "asn" if same_asn else ("country" if h["country"] == exit_["country"] else "")
    bad = [h for h in found if not h["match"]]
    ev = {"exit": exit_, "resolvers": found}
    if bad:
        value = "%d/%d 个解析服务器不在出口国家或 ASN" % (len(bad), len(found))
        detail = "出口 %s, 泄漏: %s" % (_label(exit_), ", ".join(_label(h) for h in bad[:5]))
        return result(LEAK, value, detail, **ev)
    value = "%d 个解析服务器均与出口同国家或同 ASN" % len(found)
    return result(HEALTHY, value, "出口 " + _label(exit_), **ev)


def rows_of(value) -> tuple[list, str]:
    """纯函数: 页面内探针的返回值 -> (结果行, 错误). 形状不对一律当错误, 交给上层判 unknown."""
    if not isinstance(value, dict):
        return [], "probe returned %s" % type(value).__name__
    if value.get("error"):
        return [], str(value["error"])[:200]
    rows = value.get("rows")
    if not isinstance(rows, list):
        return [], "probe rows missing"
    return rows, ""


async def _page_ws(debug_port: int, target_id: str) -> str:
    for _ in range(20):
        rows = await cdp._get_json_any(debug_port, "/json/list")
        ws = next((t.get("webSocketDebuggerUrl", "") for t in rows or [] if t.get("id") == target_id), "")
        if ws:
            return ws
        await asyncio.sleep(0.25)
    raise cdp.CdpError("probe page target not found")


async def _wait_loaded(pconn) -> None:
    """等探针页落在探针域且加载完成; 代理不通时会停在错误页, 超时抛错."""
    deadline = cdp.loop_now() + LOAD_TIMEOUT_S
    seen = ""
    while cdp.loop_now() < deadline:
        try:
            res = await pconn.send("Runtime.evaluate", {"expression": _READY_JS, "returnByValue": True}, timeout=5)
            seen = str(res.get("result", {}).get("value") or "")
        except cdp.ConnectionClosed:
            raise
        except cdp.CdpError as e:  # 导航中执行上下文被替换, 下一轮再问
            seen = str(e)[:80]
        if seen == PROBE_DOMAIN + " complete":
            return
        await asyncio.sleep(0.3)
    raise cdp.CdpError("probe page not loaded: " + (seen or "no answer"))


async def run_in_browser(debug_port: int) -> dict:
    """在副本浏览器里开后台页打开探针域, 取 id, 触发解析, 同源取结果, 然后关页. 返回页面内结果."""
    browser_ws = await cdp.get_browser_ws(debug_port, timeout=10)
    bconn = await cdp.CdpConnection(browser_ws).connect(timeout=8)
    target_id = ""
    try:
        made = await bconn.send("Target.createTarget", {"url": ID_URL, "background": True}, timeout=10)
        target_id = made.get("targetId", "")
        pconn = await cdp.CdpConnection(await _page_ws(debug_port, target_id)).connect(timeout=8)
        try:
            await _wait_loaded(pconn)
            expr = _PROBE_JS % (json.dumps(PROBE_DOMAIN), TRIGGERS, TRIGGER_MS, SETTLE_MS)
            args = {"expression": expr, "awaitPromise": True, "returnByValue": True}
            res = await pconn.send("Runtime.evaluate", args, timeout=(TRIGGER_MS + SETTLE_MS) / 1000 + 15)
            if res.get("exceptionDetails"):
                return {"error": str(res["exceptionDetails"].get("text") or "probe script failed")[:200]}
            return res.get("result", {}).get("value")
        finally:
            await pconn.close()
    finally:
        if target_id:
            try:
                await bconn.send("Target.closeTarget", {"targetId": target_id}, timeout=5)
            except Exception:  # noqa: BLE001
                pass
        await bconn.close()


async def probe(env, debug_port: int) -> dict:
    """跑一次探针: 取 id, 触发解析, 取结果三步全在副本浏览器里做, 结果里的出口就是浏览器自己的出口."""
    if kernels.family_of(env) == kernels.GECKO:
        return result(UNKNOWN, "Firefox 内核未在浏览器内探测", "探针经 CDP 在页面里发解析请求, Gecko 内核未接入")
    try:
        value = await run_in_browser(debug_port)
    except Exception as e:  # noqa: BLE001
        return result(UNKNOWN, "浏览器内探针失败", "%s: %s" % (type(e).__name__, e))
    rows, err = rows_of(value)
    if err:
        return result(UNKNOWN, "探针结果不可用", err)
    return assess(rows)


async def run_after_start(env, info) -> None:
    """启动后台任务: 挂了代理才跑, 结果写进 RuntimeInfo.dns_leak 并落盘."""
    if not applies(env):
        return
    from .runtime import save_runtime

    info.dns_leak = result(UNKNOWN, "探测中", "启动后 DNS 泄漏探针运行中")
    try:
        await cdp.reachable_base(info.debug_port)
        info.dns_leak = await asyncio.wait_for(probe(env, info.debug_port), PROBE_TIMEOUT_S)
    except Exception as e:  # noqa: BLE001
        info.dns_leak = result(UNKNOWN, "探针未完成", "%s: %s" % (type(e).__name__, e))
    level = logging.WARNING if info.dns_leak["status"] == LEAK else logging.INFO
    log.log(level, "env=%s dns leak probe: %s %s", env.id, info.dns_leak["status"], info.dns_leak["value"])
    save_runtime()


def doctor_checks(env) -> list[dict]:
    """体检项: 只列挂了代理的副本, 读启动后探针的结果, 不在体检时重跑."""
    if not applies(env):
        return []
    from .runtime import get_runtime

    rt = get_runtime(env.id)
    if rt is None:
        return [result(UNKNOWN, "环境未运行", "DNS 泄漏探针在每次启动后自动运行", checked_at=0)]
    if not rt.dns_leak:
        return [result(UNKNOWN, "本次启动未探测", "重启环境后自动探测", checked_at=0)]
    return [dict(rt.dns_leak)]
