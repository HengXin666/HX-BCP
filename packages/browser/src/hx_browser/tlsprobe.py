"""出口侧握手探针: relay 旁路记录每条隧道的真实 ClientHello, 与宣称内核比对.

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-egress-tls-probe.md
"""

from __future__ import annotations

import asyncio
import collections
import json
import logging
import re
import time

from . import cdp, kernels, tlshello

log = logging.getLogger("hx_browser.tlsprobe")

CHECK_ID = "tls_handshake"
KEEP = 32
MIN_HELLOS = 3
TAP_LIMIT = tlshello.MAX_HELLO + 1024
TRIGGER_MS = 8000
TRIGGER_URLS = (
    "https://www.gstatic.com/generate_204",
    "https://www.cloudflare.com/cdn-cgi/trace",
    "https://www.bing.com/favicon.ico",
    "https://example.com/",
)
HEALTHY, WARNING, ERROR, UNKNOWN = "healthy", "warning", "error", "unknown"
H2_NOTE = "H2 SETTINGS 在 TLS 隧道内加密, 不终结 TLS 的出口侧只读探针看不到, 本项未覆盖 H2"
_UA_RE = re.compile(r"(Firefox|Chrome|Chromium|CriOS)/(\d+)")
_PRODUCT_RE = re.compile(r"/(\d+)\.")
# (特征, 类型, 码点, 早于此版本出现即红, 从此版本起缺失即黄)
CHROME_FEATURES = (
    ("ECH GREASE", "ext", tlshello.EXT_ECH, 117, 119),
    ("X25519Kyber768", "group", tlshello.GROUP_KYBER, 115, None),
    ("X25519MLKEM768", "group", tlshello.GROUP_MLKEM, 130, 132),
    ("ALPS 新码点 17613", "ext", tlshello.EXT_ALPS_NEW, 131, 134),
)
_GONE_FROM = (("X25519Kyber768", "group", tlshello.GROUP_KYBER, 132), ("ALPS 旧码点 17513", "ext", tlshello.EXT_ALPS_OLD, 135))
_SEEN: dict[str, collections.deque] = {}


class Tap:
    """喂入一条 CONNECT 隧道客户端到上游的字节段, 不再需要字节时返回 True."""

    def __init__(self, env_id: str, target: str):
        self.env_id, self.target, self.buf = env_id, target, b""

    def __call__(self, chunk: bytes) -> bool:
        try:
            self.buf += chunk
            got = tlshello.parse(self.buf)
            if got == tlshello.NEED_MORE and len(self.buf) < TAP_LIMIT:
                return False
            if isinstance(got, dict):
                rec = tlshello.fingerprint(got)
                rec.update(target=self.target, ts=int(time.time() * 1000))
                _SEEN.setdefault(self.env_id, collections.deque(maxlen=KEEP)).append(rec)
        except Exception as e:  # noqa: BLE001
            log.debug("env=%s tap failed: %s", self.env_id, e)
        return True


def tap_for(env_id: str, target: str) -> Tap | None:
    return Tap(env_id, target) if env_id else None


def seen(env_id: str) -> list[dict]:
    return list(_SEEN.get(env_id, ()))


def forget(env_id: str) -> None:
    _SEEN.pop(env_id, None)


def result(status: str, value: str, detail: str = "", **extra) -> dict:
    """与 doctor 检查项同形状, 另带证据字段."""
    out = {"id": CHECK_ID, "status": status, "value": value, "detail": detail, "fixable": False, "fix": {}}
    out.update(extra)
    out.setdefault("checked_at", int(time.time() * 1000))
    return out


def claimed(ua: str) -> tuple[str, int]:
    """UA -> (族, 主版本), Chrome 系为 blink, Firefox 为 gecko, 读不出为 ('', 0)."""
    m = _UA_RE.search(ua or "")
    if not m:
        return "", 0
    return (kernels.GECKO if m.group(1) == "Firefox" else kernels.BLINK), int(m.group(2))


def ua_conflicts(ua: str, kernel_family: str, real_major: int) -> list[str]:
    """宣称 UA 与内核真实握手撑不起的矛盾项, 空 UA 返回空列表, 族或真实版本为空时不判该项.

    Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-ua-kernel-coherence.md
    """
    if not ua:
        return []
    fam, major = claimed(ua)
    if not fam:
        return ["UA 不属于 Chrome 或 Firefox 系, 本仓任何内核的握手都对不上: %r" % ua[:120]]
    out = []
    if kernel_family and kernel_family != fam:
        out.append("UA 宣称 %s, 环境内核为 %s 族" % (fam, kernel_family))
    if real_major and real_major != major:
        out.append("UA 主版本 %d != 真实内核主版本 %d" % (major, real_major))
    return out


def hello_family(rec: dict) -> str:
    """握手所属族: 有 GREASE 为 Chromium 栈, 无 GREASE 且带 record_size_limit 为 NSS 栈."""
    if rec["grease"]:
        return kernels.BLINK
    if tlshello.EXT_RECORD_LIMIT in rec["ext_set"]:
        return kernels.GECKO
    return ""


def _has(rec: dict, kind: str, value: int) -> bool:
    return value in (rec["ext_set"] if kind == "ext" else rec["group_set"])


def _chrome_findings(recs: list[dict], major: int) -> tuple[list[str], list[str]]:
    errors, warns = [], []
    for name, kind, value, absent_below, expected_from in CHROME_FEATURES:
        anyhas = any(_has(r, kind, value) for r in recs)
        if anyhas and major < absent_below:
            errors.append("握手含 %s, Chrome %d 不会发" % (name, major))
        if expected_from and major >= expected_from and not anyhas:
            warns.append("Chrome %d 应发 %s, 握手里没有 (可能被策略关闭, 或握手来自更旧内核)" % (major, name))
    for name, kind, value, gone_from in _GONE_FROM:
        if major >= gone_from and any(_has(r, kind, value) for r in recs):
            errors.append("握手含 %s, Chrome %d 已不再发" % (name, major))
    orders = {r["ext_order"] for r in recs}
    if major >= 110 and len(recs) >= MIN_HELLOS and len(orders) == 1:
        errors.append("%d 个握手扩展顺序完全相同, Chrome 110 起逐连接随机置换" % len(recs))
    return errors, warns


def assess(recs: list[dict], ua: str, product: str = "", kernel_family: str = "") -> dict:
    """纯函数判定: 捕获握手 + 宣称 UA + 真实 product -> 检查项, 证据不足一律 unknown."""
    recs = [r for r in recs or [] if isinstance(r, dict)]
    ev = {"ua": ua, "product": product, "hellos": [_brief(r) for r in recs[-8:]], "h2": {"observable": False, "note": H2_NOTE}}
    if not recs:
        return result(UNKNOWN, "出口侧还没有捕获到 ClientHello", "没有 TLS 连接经过本副本的 relay. " + H2_NOTE, **ev)
    fam, major = claimed(ua)
    if not fam:
        return result(UNKNOWN, "读不到宣称的 UA 主版本", "UA=%r. %s" % (ua, H2_NOTE), **ev)
    errors, warns = [], []
    fams = {hello_family(r) for r in recs} - {""}
    if len(fams) > 1:
        errors.append("同一副本出现两族握手 %s, 有连接未走浏览器自身协议栈" % sorted(fams))
    elif fams and fams != {fam}:
        errors.append("UA 宣称 %s, 握手却是 %s 族" % (fam, fams.pop()))
    real = int(_PRODUCT_RE.search(product).group(1)) if _PRODUCT_RE.search(product or "") else 0
    errors += ua_conflicts(ua, kernel_family, real)
    if fam == kernels.BLINK and not errors:
        e2, warns = _chrome_findings(recs, major)
        errors += e2
    label = "%s %d" % ("Chrome" if fam == kernels.BLINK else "Firefox", major)
    n = len(recs)
    if errors:
        return result(ERROR, "%d 个握手与宣称的 %s 矛盾" % (n, label), "; ".join(errors) + ". " + H2_NOTE, **ev)
    if warns:
        return result(WARNING, "%d 个握手基本符合 %s, 有缺项" % (n, label), "; ".join(warns) + ". " + H2_NOTE, **ev)
    if not real:
        return result(UNKNOWN, "握手符合 %s, 但没读到真实内核版本" % label, H2_NOTE, **ev)
    return result(HEALTHY, "%d 个握手与 %s 一致 (H2 未覆盖)" % (n, label), H2_NOTE, **ev)


def _brief(r: dict) -> dict:
    keys = ("target", "sni", "ja4", "ja4_r", "ja3n_hash", "alpn", "groups", "key_shares", "grease", "ts")
    return {k: r.get(k) for k in keys}


async def _sample(debug_port: int, need_traffic: bool) -> tuple[str, str]:
    """开后台空白页读 navigator.userAgent 与 product, 需要时顺带发起几条 TLS 连接, 返回 (ua, product)."""
    browser_ws = await cdp.get_browser_ws(debug_port, timeout=10)
    bconn = await cdp.CdpConnection(browser_ws).connect(timeout=8)
    target_id = ""
    try:
        product = str((await bconn.send("Browser.getVersion", {}, timeout=5)).get("product") or "")
        made = await bconn.send("Target.createTarget", {"url": "about:blank", "background": True}, timeout=10)
        target_id = made.get("targetId", "")
        page_ws = ""
        for _ in range(20):
            rows = await cdp._get_json_any(debug_port, "/json/list")
            page_ws = next((t.get("webSocketDebuggerUrl", "") for t in rows or [] if t.get("id") == target_id), "")
            if page_ws:
                break
            await asyncio.sleep(0.25)
        if not page_ws:
            raise cdp.CdpError("probe page target not found")
        pconn = await cdp.CdpConnection(page_ws).connect(timeout=8)
        try:
            urls = json.dumps(list(TRIGGER_URLS) if need_traffic else [])
            expr = (
                "(async (u, ms) => { const o = {mode: 'no-cors', cache: 'no-store'};"
                " await Promise.race([Promise.all(u.map((x) => fetch(x, o).then(() => 1, () => 1))),"
                " new Promise((r) => setTimeout(r, ms))]); return navigator.userAgent; })(%s, %d)" % (urls, TRIGGER_MS)
            )
            args = {"expression": expr, "awaitPromise": True, "returnByValue": True}
            res = await pconn.send("Runtime.evaluate", args, timeout=TRIGGER_MS / 1000 + 10)
            return str(res.get("result", {}).get("value") or ""), product
        finally:
            await pconn.close()
    finally:
        if target_id:
            try:
                await bconn.send("Target.closeTarget", {"targetId": target_id}, timeout=5)
            except Exception:  # noqa: BLE001
                pass
        await bconn.close()


async def doctor_checks(env) -> list[dict]:
    """体检项: 读被动捕获的握手, 样本不足时才在副本里补几条连接."""
    from . import relay
    from .runtime import get_runtime

    rt = get_runtime(env.id)
    if rt is None:
        return [result(UNKNOWN, "环境未运行", "出口侧握手探针只在运行中经 relay 捕获", checked_at=0)]
    if env.id not in relay.RELAYS:
        return [result(UNKNOWN, "本副本出口不经 relay", "直连副本没有出口侧观测点, 挂上游代理或用 sandbox 后可观测", checked_at=0)]
    fam = kernels.family_of(env)
    ua, product = env.fingerprint.user_agent or "", ""
    if fam == kernels.BLINK and rt.debug_port:
        try:
            ua, product = await _sample(rt.debug_port, len(seen(env.id)) < MIN_HELLOS)
        except Exception as e:  # noqa: BLE001
            log.info("env=%s tls probe sampling failed: %s", env.id, e)
    out = assess(seen(env.id), ua, product, fam)
    level = logging.WARNING if out["status"] == ERROR else logging.INFO
    log.log(level, "env=%s tls handshake probe: %s %s", env.id, out["status"], out["value"])
    return [out]
