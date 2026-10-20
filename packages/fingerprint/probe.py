#!/usr/bin/env python3
"""指纹度量回路 -- 协议层 (TLS/HTTP2) 与浏览器层并排测, 同一出口下比对。

为什么需要它
------------
本工作区栽过两次「以为换了, 其实没换」:
  · 2026-09-14 换 seed 没换指纹 (platform/UA/cores/screen/WebGL 6/7 字段恒定)
  · 2026-09-17 impersonate=chrome146 与浏览器真身不同 (ja3 6fb2220e vs 4bbab334)
两次的共同形态: 没有度量回路, 伪装是否生效靠猜。

本工具把两个独立变量拆开测:
  · 伪装 (客户端声称自己是谁) -> ja4 / ja3n / peetprint / akamai h2
  · 出口 (请求从哪台机器出去) -> ip / ASN / colo / 时区

纪律 (硬实测依据, 2026-09-22 本机 tls.peet.ws)
----------------------------------------------
1. **ja3_hash 不是身份, 且这不是 curl_cffi 的缺陷**: 同一 impersonate="chrome" 连续 3 次,
   ja3_hash 三次全不同 (GREASE 每次随机); 本机真 Chrome 151 headless **同样如此**
   (e9a8910b / d5c476f4), 而两者的 ja4 各自恒定。-> **ja4 才是身份**。
   去 GREASE 只是必要条件, 不够 —— Chrome 还会随机化部分扩展顺序, 所以 ja3n 也不稳定。
2. **只有同出口的比对才有效**: 出口 IP 不同则指纹对照无意义。
   -> 报告按出口 IP 分组, 跨出口的组不下结论。
3. **浏览器层不可省**: 协议层只覆盖 L2/L3。L4/L5 (字体数、webdriver、时区)
   只能从真浏览器取, 而它们往往才是决定性信号。

用法
----
    uv run probe.py protocol
    uv run probe.py protocol --impersonate chrome,firefox,safari,native
    uv run probe.py browser --endpoint http://127.0.0.1:43020
    uv run probe.py compare

退出码: 0=全绿 1=有红线 2=材料不足
"""

# Agent Note: .agents/notes/implemented/architecture/2026-09-22-fingerprint-metric-loop.md
from __future__ import annotations

import argparse
import glob
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.request
from typing import Any

GREASE = {(n << 8) | n for n in range(0x0A, 0x100, 0x10)}
PEET = "https://tls.peet.ws/api/all"
TRACE = "https://www.cloudflare.com/cdn-cgi/trace"
GEO = "http://ip-api.com/json/{ip}?fields=status,timezone,as,country"
HERE = os.path.dirname(os.path.abspath(__file__))


def _seg(seg: str) -> list[int]:
    out = []
    for x in seg.split("-"):
        x = x.strip()
        if not x.isdigit():
            continue
        v = int(x)
        if v in GREASE:
            continue
        out.append(v)
    return out


def normalize_ja3(ja3: str) -> str:
    parts = ja3.split(",")
    if len(parts) < 3:
        return ja3
    return ",".join(
        "-".join(map(str, _seg(parts[i] if i < len(parts) else "")))
        for i in range(5)
    )


def ja3n_hash(ja3: str) -> str | None:
    if not ja3:
        return None
    return hashlib.md5(normalize_ja3(ja3).encode()).hexdigest()


def ua_platform(ua: str | None) -> str:
    u = (ua or "").lower()
    if "windows" in u:
        return "windows"
    if "macintosh" in u or "mac os x" in u:
        return "macos"
    if "android" in u:
        return "android"
    if "iphone" in u or "ipad" in u:
        return "ios"
    if "linux" in u or "x11" in u:
        return "linux"
    return "unknown"


def _import_curl():
    try:
        from curl_cffi import requests as cr
        return cr
    except ImportError:
        sys.exit("[!] 缺 curl_cffi: 用 uv run probe.py 跑 (pyproject 已声明依赖)")


def egress(cr, timeout: float = 15.0) -> dict:
    out: dict[str, Any] = {"ip": None, "colo": None, "timezone": None, "asn": None, "country": None}
    try:
        txt = cr.get(TRACE, timeout=timeout, impersonate="chrome").text
        kv = dict(l.split("=", 1) for l in txt.strip().splitlines() if "=" in l)
        out["ip"] = kv.get("ip")
        out["colo"] = kv.get("colo")
    except Exception as exc:
        out["trace_error"] = type(exc).__name__
    if out["ip"]:
        try:
            j = cr.get(GEO.format(ip=out["ip"]), timeout=timeout, impersonate="chrome").json()
            if j.get("status") == "success":
                out["timezone"] = j.get("timezone")
                out["asn"] = j.get("as")
                out["country"] = j.get("country")
        except Exception as exc:
            out["geo_error"] = type(exc).__name__
    return out


def probe_protocol(cr, impersonate: str, url: str = PEET, timeout: float = 30.0) -> dict:
    kwargs: dict[str, Any] = {"timeout": timeout}
    if impersonate != "native":
        kwargs["impersonate"] = impersonate
    try:
        d = cr.get(url, **kwargs).json()
    except Exception as exc:
        return {"impersonate": impersonate, "error": type(exc).__name__ + ": " + str(exc)}
    t = d.get("tls") or {}
    h2 = d.get("http2") or {}
    ja3 = t.get("ja3") or ""
    return {
        "impersonate": impersonate,
        "ip": d.get("ip"),
        "http_version": d.get("http_version"),
        "ua": d.get("user_agent"),
        "ua_platform": ua_platform(d.get("user_agent")),
        "tls_negotiated": (t.get("tls_version_negotiated") / 256)
        if isinstance(t.get("tls_version_negotiated"), int) else None,
        "ja3": ja3,
        "ja3_hash": t.get("ja3_hash"),
        "ja3n_hash": ja3n_hash(ja3),
        "ja4": t.get("ja4"),
        "peetprint_hash": t.get("peetprint_hash"),
        "akamai_h2_hash": h2.get("akamai_fingerprint_hash"),
        "ja4_head": "_".join((t.get("ja4") or "").split("_")[:2]),
        "ciphers": len(t.get("ciphers") or []),
        "extensions": [e.get("name") for e in (t.get("extensions") or [])],
    }


FP_JS = r"""(() => {
  const o = {};
  o.userAgent = navigator.userAgent;
  o.platform = navigator.platform;
  o.uaPlatform = (navigator.userAgentData && navigator.userAgentData.platform) || null;
  o.languages = navigator.languages;
  o.hardwareConcurrency = navigator.hardwareConcurrency;
  o.deviceMemory = navigator.deviceMemory ?? null;
  o.webdriver = navigator.webdriver;
  o.timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  o.tzOffsetMin = new Date().getTimezoneOffset();
  o.screen = [screen.width, screen.height, screen.colorDepth];
  o.devicePixelRatio = devicePixelRatio;
  try {
    const c = document.createElement('canvas');
    const gl = c.getContext('webgl');
    const dbg = gl && gl.getExtension('WEBGL_debug_renderer_info');
    o.webglVendor = dbg ? gl.getParameter(dbg.UNMASKED_VENDOR_WEBGL) : null;
    o.webglRenderer = dbg ? gl.getParameter(dbg.UNMASKED_RENDERER_WEBGL) : null;
  } catch (e) { o.webglError = String(e); }
  try { o.fonts = document.fonts ? document.fonts.size : null; } catch (e) { o.fonts = null; }
  return JSON.stringify(o);
})()"""


def _http_json(url: str, timeout: float = 10.0) -> Any:
    with urllib.request.urlopen(url, timeout=timeout) as fh:
        return json.loads(fh.read().decode("utf-8", "replace"))


def _pick_page(endpoint: str) -> str:
    base = endpoint.rstrip("/")
    try:
        targets = _http_json(base + "/json/list")
    except Exception as exc:
        sys.exit("[!] " + base + "/json/list 不可达 (" + type(exc).__name__ + "): "
                 "确认这是真 CDP (能响应 /json/version) 而不是厂商 Local API")
    for t in targets:
        if t.get("type") == "page" and t.get("webSocketDebuggerUrl"):
            return t["webSocketDebuggerUrl"]
    try:
        t = _http_json(base + "/json/new?about:blank")
        return t["webSocketDebuggerUrl"]
    except Exception:
        sys.exit("[!] 没有可用 page target, 且 /json/new 未开启 "
                 "(新版 Chrome 需 --remote-allow-origins=*)")


def probe_browser(endpoint: str, timeout: float = 30.0) -> dict:
    import websocket

    ws_url = _pick_page(endpoint)
    ws = websocket.create_connection(ws_url, timeout=timeout)
    try:
        ws.send(json.dumps({"id": 1, "method": "Runtime.evaluate",
                            "params": {"expression": FP_JS, "returnByValue": True}}))
        deadline = time.time() + timeout
        while time.time() < deadline:
            msg = json.loads(ws.recv())
            if msg.get("id") != 1:
                continue
            if "error" in msg:
                return {"endpoint": endpoint, "error": str(msg["error"])}
            raw = (msg.get("result") or {}).get("result", {}).get("value")
            if raw is None:
                return {"endpoint": endpoint, "error": "Runtime.evaluate 无返回值"}
            data = json.loads(raw)
            data["endpoint"] = endpoint
            data["ua_platform"] = ua_platform(data.get("userAgent"))
            return data
    finally:
        try:
            ws.close()
        except Exception:
            pass
    return {"endpoint": endpoint, "error": "timeout"}


def build_checks(proto: dict, browser: dict) -> list[dict]:
    ck: list[dict] = []

    def add(cid: str, level: str, name: str, detail: str = "", fix: str = "",
            warn: str = "") -> None:
        ck.append({"id": cid, "level": level, "name": name, "detail": detail,
                   "fix": fix, "warn": warn})

    wd = browser.get("webdriver")
    if wd is None:
        add("C1", "gray", "webdriver 未取到", "不等于通过", "确认 evaluate 跑在 page target 上")
    elif wd:
        add("C1", "red", "webdriver=true", "自动化特征直接暴露",
            "去掉自动化 launch flag (Patchright/CloakBrowser)")
    else:
        add("C1", "green", "webdriver=false")

    fonts = browser.get("fonts")
    if fonts is None:
        add("C2", "gray", "字体列表未取到", "无法判定", "在真实页面上测")
    elif fonts == 0:
        add("C2", "red", "document.fonts.size=0", "伪造信号的教科书样例",
            "UA/平台画像必须与宿主同源, 禁用跨平台 UA 覆盖")
    else:
        add("C2", "green", "fonts=" + str(fonts))

    pp, bp = proto.get("ua_platform"), browser.get("ua_platform")
    if not pp or not bp or "unknown" in (pp, bp):
        add("C3", "gray", "UA 平台无法解析", "proto=" + str(pp) + " browser=" + str(bp))
    elif pp != bp:
        add("C3", "red", "协议层与浏览器层平台不同族",
            "proto UA=" + pp + " / browser UA=" + bp,
            "不要混用不同平台的客户端 (curl_cffi chrome 档固定吐 macOS UA)")
    else:
        add("C3", "green", "UA 平台一致 (" + pp + ")")

    etz = (proto.get("egress") or {}).get("timezone")
    btz = browser.get("timezone")
    if not etz or not btz:
        add("C4", "gray", "时区材料不足", "egress=" + str(etz) + " browser=" + str(btz),
            "出口时区取不到时不能跳过校验, 跳过=静默泄露")
    elif etz != btz:
        add("C4", "red", "浏览器时区与出口时区不一致",
            "出口=" + etz + " 浏览器=" + btz,
            "--fingerprint-timezone 与 TZ 环境变量双保险")
    else:
        add("C4", "green", "时区一致 (" + etz + ")")

    def _bare(v: str | None) -> str | None:
        """去掉端口。IPv4 是 a.b.c.d:port; IPv6 若带端口写作 [v6]:port (裸 v6 无端口)。"""
        if not v:
            return v
        if v.startswith("["):
            return v.split("]")[0].lstrip("[")
        return v.split(":")[0] if v.count(":") == 1 else v

    pip, bip = _bare(proto.get("ip")), _bare(browser.get("ip"))
    if pip and bip and pip != bip:
        add("C5", "yellow", "两层不在同一出口", "proto=" + pip + " browser=" + bip,
            "指纹对照只在同出口下成立; 本次比对结论降级")
    else:
        add("C5", "green", "同出口 (" + str(pip or bip or "n/a") + ")")

    ja4 = proto.get("ja4") or ""
    # JA4 结构: <t|q><版本2位><d|i><cipher数2位><扩展数2位><ALPN>_<cipher哈希>_<扩展哈希>
    # 注意第 3 字符是字母 (d=SNI 域名 / i=IP), 曾写成 \d 导致恒判缺失。
    # 且**不能**由第一段推断浏览器族 (实测 Chrome 与 Firefox 同为 t13d... 开头),
    # 族标识在第二段 —— 所以这里只报事实, 家族判断交给 C8 与真身比对。
    m = re.match(r"^([tq])(\d\d)([di])(\d\d)(\d\d)(\w\w)_", ja4)
    if not m:
        add("C6", "gray", "ja4 缺失或格式不符", "无法判定客户端族")
    else:
        ver = "TLS1.3" if m.group(2) == "13" else ("TLS1.2" if m.group(2) == "12" else "TLS" + m.group(2))
        sni = "SNI=域名" if m.group(3) == "d" else "SNI=IP(无域名)"
        add("C6", "green", "ja4=" + ja4,
            ver + " / " + sni + " / cipher数=" + m.group(4)
            + " / 扩展数=" + m.group(5) + " / ALPN=" + m.group(6))

    dm = browser.get("deviceMemory")
    if dm is None:
        add("C7", "yellow", "navigator.deviceMemory 为空", "本机已知缺陷",
            "查 --fingerprint-device-memory 是否真透传到 Blink")
    else:
        add("C7", "green", "deviceMemory=" + str(dm))

    # C8: 与「同机同出口的真浏览器」直接比对 -- 这是伪装是否说的通的终审。
    # 没有这一项就只剩「像不像」的自我判断 (本仓库栽过: 3 个 seed 其实同一套指纹)。
    ref = proto.get("reference")
    if not ref or not ref.get("ja4"):
        add("C8", "gray", "无参考真身",
            "未提供 --reference, 无法判断协议层是否在说真话",
            "uv run probe.py protocol --reference '<本机 Chrome 的 ja4>'")
    else:
        same_family = (proto.get("ja4_head") == ref.get("ja4_head"))
        same_ua = (proto.get("ua_platform") == ref.get("ua_platform"))
        if not same_family or not same_ua:
            add("C8", "yellow", "协议层与参考真身不同源",
                "客户端=" + str(proto.get("ja4_head")) + "/" + str(proto.get("ua_platform"))
                + "  真身=" + str(ref.get("ja4_head")) + "/" + str(ref.get("ua_platform")),
                "协议客户端与本机浏览器指纹不同族 = 静态可识别; 若不是有意为之需对齐版本",
                warn="警告: 不一致本身不阻塞 (真身是另一台机器时属正常), 但它使 C3 的平台"
                     "一致性结论失去意义 —— 因为比的是两个不同的客户端")
        else:
            add("C8", "green", "协议层与参考真身同族同平台")

    return ck


def render(checks: list[dict]) -> tuple[str, int]:
    icon = {"red": "[X]", "yellow": "[!]", "green": "[v]", "gray": "[?]"}
    lines = []
    for c in checks:
        lines.append(icon[c["level"]] + " " + c["id"] + " " + c["name"]
                     + (" -- " + c["detail"] if c["detail"] else ""))
        if c["fix"] and c["level"] in ("red", "yellow"):
            lines.append("        fix: " + c["fix"])
    reds = sum(1 for c in checks if c["level"] == "red")
    grays = sum(1 for c in checks if c["level"] == "gray")
    # 只有「拿到读数」的检查项才说明环境达标; gray 是「没读到」, 不是通过。
    # 全部 gray = 设备根本没测出来 = 材料不足 (2), 不算绿。
    greens = sum(1 for c in checks if c["level"] == "green")
    code = 1 if reds else (2 if greens == 0 else 0)
    lines.append("")
    lines.append("红=" + str(reds) + " 黄=" + str(sum(1 for c in checks if c["level"] == "yellow"))
                 + " 绿=" + str(sum(1 for c in checks if c["level"] == "green"))
                 + " 灰=" + str(grays) + " -> 退出码 " + str(code))
    return "\n".join(lines), code


def _latest(pattern: str) -> str | None:
    hits = sorted(glob.glob(pattern))
    return hits[-1] if hits else None


def cmd_protocol(args) -> int:
    cr = _import_curl()
    eg = egress(cr)
    imps = [s.strip() for s in args.impersonate.split(",") if s.strip()]
    rows = []
    for imp in imps:
        row = probe_protocol(cr, imp, args.url, args.timeout)
        row["egress"] = eg
        rows.append(row)
        if "error" in row:
            print("  " + imp.ljust(12) + " ERROR " + row["error"])
        else:
            print("  " + imp.ljust(12) + " ja4=" + str(row["ja4"])
                  + "  ja3n=" + str(row["ja3n_hash"])[:12]
                  + "  peet=" + str(row["peetprint_hash"])[:12]
                  + "  h2=" + str(row["akamai_h2_hash"])[:12])

    stability = None
    if args.repeat > 1:
        seen = []
        for _ in range(args.repeat):
            r = probe_protocol(cr, imps[0], args.url, args.timeout)
            seen.append((r.get("ja3_hash"), r.get("ja4"), r.get("ja3n_hash")))
        stability = {
            "impersonate": imps[0],
            "samples": len(seen),
            "ja3_hash_stable": len({s[0] for s in seen}) == 1,
            "ja4_stable": len({s[1] for s in seen}) == 1,
            "ja3n_hash_stable": len({s[2] for s in seen}) == 1,
        }
        print("\n  稳定性 (" + imps[0] + " x" + str(args.repeat) + "): ja4 恒定="
              + str(stability["ja4_stable"]) + "  <- 唯一判据")
        print("  ja3_hash 恒定=" + str(stability["ja3_hash_stable"])
              + " / ja3n 恒定=" + str(stability["ja3n_hash_stable"])
              + "  <- 期望为 False: Chrome 自 110+ 起随机化 ClientHello, 真 Chrome 同样如此")

    reference = None
    if not args.no_detect_chrome:
        if args.reference:
            reference = {"ja4": args.reference, "exe": "cli",
                         "ja4_head": "_".join(args.reference.split("_")[:2]),
                         "ua_platform": "unknown"}
        else:
            reference = detect_chrome()
        if reference:
            print("\n  本机真身基准: ja4=" + str(reference.get("ja4"))
                  + "  (" + str(reference.get("exe")) + ")")
        else:
            print("\n  本机真身基准: 未取到 (无 Chrome 或采集失败) -- 将记为灰项, 不等于通过")

    out = {"kind": "protocol", "ts": time.time(), "egress": eg,
           "rows": rows, "stability": stability, "reference": reference}
    path = args.json or os.path.join(HERE, "reports", "protocol-" + str(int(time.time())) + ".json")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(out, fh, ensure_ascii=False, indent=2)
    print("\n出口: " + str(eg.get("ip")) + " (" + str(eg.get("asn")) + ") colo="
          + str(eg.get("colo")) + " tz=" + str(eg.get("timezone")))
    print("报告: " + path)
    return 0


def cmd_browser(args) -> int:
    data = probe_browser(args.endpoint, args.timeout)
    if "error" in data:
        print("[X] " + data["error"])
        return 1
    tz_out, tz_src = None, None
    if args.egress or args.expect_tz:
        cr = _import_curl()
        eg = egress(cr)
        data["ip"] = eg.get("ip")
        data["egress_timezone"] = eg.get("timezone")
        data["expected_timezone"] = args.expect_tz
        if eg.get("timezone"):
            tz_out, tz_src = eg.get("timezone"), "出口"
        elif args.expect_tz:
            # 出口时区取不到, 退回人工期望值。**必须标注来源** —— 人工值与出口真值
            # 不是一回事, 混用会得出「一致」的假绿 (本仓库栽过: 填东京而出口在新加坡)。
            tz_out, tz_src = args.expect_tz, "人工指定(出口探测失败)"
        if not eg.get("timezone"):
            data["egress_probe_failed"] = True
    for k in ("endpoint", "userAgent", "platform", "uaPlatform", "languages",
              "hardwareConcurrency", "deviceMemory", "webdriver", "timezone",
              "screen", "devicePixelRatio", "webglVendor", "webglRenderer", "fonts"):
        print("  " + k.ljust(20) + " " + str(data.get(k)))
    if tz_out:
        print("  " + ("出口时区" if tz_src == "出口" else "期望时区").ljust(18) + " "
              + tz_out + " [" + str(tz_src) + "]  -> "
              + ("一致" if tz_out == data.get("timezone") else "不一致"))
        if tz_src != "出口":
            print("  [!] 出口时区未取到, 上面用的是人工值 —— 结果是灰项而非绿项")
    path = args.json or os.path.join(HERE, "reports", "browser-" + str(int(time.time())) + ".json")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump({"kind": "browser", "ts": time.time(), **data}, fh, ensure_ascii=False, indent=2)
    print("\n报告: " + path)
    return 0


def cmd_compare(args) -> int:
    p_path = args.protocol or _latest(os.path.join(HERE, "reports", "protocol-*.json"))
    b_path = args.browser or _latest(os.path.join(HERE, "reports", "browser-*.json"))
    if not p_path or not b_path:
        print("[?] 材料不足: 先跑 protocol 与 browser 两个子命令")
        return 2
    with open(p_path, encoding="utf-8") as fh:
        prow = json.load(fh)
    with open(b_path, encoding="utf-8") as fh:
        brow = json.load(fh)
    ok = [r for r in prow.get("rows", []) if "error" not in r]
    if not ok:
        print("[?] 协议层无有效样本")
        return 2
    proto = dict(ok[0])
    proto["egress"] = prow.get("egress") or {}
    proto["reference"] = prow.get("reference")   # 不传这一行 C8 会永远报"无参考真身"
    print("协议层: " + p_path + "\n浏览器层: " + b_path + "\n")
    checks = build_checks(proto, brow)
    text, code = render(checks)
    print(text)
    outp = os.path.join(HERE, "reports", "compare-" + str(int(time.time())) + ".json")
    with open(outp, "w", encoding="utf-8") as fh:
        json.dump({"proto": p_path, "browser": b_path, "checks": checks, "exit": code},
                  fh, ensure_ascii=False, indent=2)
    print("报告: " + outp)
    return code


def detect_chrome() -> dict | None:
    """用本机 Chrome 采一次真身指纹。用来给协议层一个「同机真话基准」。

    实测 (2026-09-22, Chrome 151 headless): ja4 = t13d1517h2_8daaf6152771_cb7bf5808d99
    —— 与 FoxIO 官方 JA4 文档里的 Chrome 示例**逐字节相同**, 可作家族基准。
    注意: 同一台 Chrome 连续两次 ja3_hash 也不同 (e110-GREASE), 所以基准只认 ja4。
    """
    exe = shutil.which("google-chrome-stable") or shutil.which("google-chrome") \
        or shutil.which("chromium") or shutil.which("chromium-browser")
    if not exe:
        return None
    try:
        out = subprocess.run(
            [exe, "--headless=new", "--no-sandbox", "--disable-gpu",
             "--virtual-time-budget=6000", "--dump-dom", PEET],
            capture_output=True, text=True, timeout=90).stdout
    except Exception:
        return None
    m = re.search(r"\{.*\}", out, re.S)
    if not m:
        return None
    try:
        d = json.loads(m.group(0))
    except json.JSONDecodeError:
        return None
    t = d.get("tls") or {}
    ja4 = t.get("ja4") or ""
    return {
        "exe": exe,
        "ja4": ja4,
        "ja4_head": "_".join(ja4.split("_")[:2]),
        "ja3_hash": t.get("ja3_hash"),
        "ua": d.get("user_agent"),
        "ua_platform": ua_platform(d.get("user_agent")),
    }


def main() -> int:
    ap = argparse.ArgumentParser(description="指纹度量回路 (协议层 / 浏览器层 / 比对)")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("protocol", help="协议层指纹 (TLS/HTTP2)")
    p.add_argument("--impersonate", default="chrome,chrome146,chrome150,firefox,safari,native")
    p.add_argument("--url", default=PEET)
    p.add_argument("--timeout", type=float, default=30.0)
    p.add_argument("--repeat", type=int, default=3)
    p.add_argument("--reference", help="本机真身 ja4 (省略则用本机 Chrome 自动采集)")
    p.add_argument("--no-detect-chrome", action="store_true", help="跳过本机 Chrome 真身采集")
    p.add_argument("--json")
    p.set_defaults(fn=cmd_protocol)

    b = sub.add_parser("browser", help="浏览器层指纹 (CDP)")
    b.add_argument("--endpoint", required=True)
    b.add_argument("--timeout", type=float, default=30.0)
    b.add_argument("--egress", action="store_true", help="顺带取出口时区做比对")
    b.add_argument("--expect-tz", help="人工期望时区 (仅当出口探测失败时兜底; 填错会自己制造不一致)")
    b.add_argument("--json")
    b.set_defaults(fn=cmd_browser)

    c = sub.add_parser("compare", help="跨层一致性比对")
    c.add_argument("--protocol")
    c.add_argument("--browser")
    c.set_defaults(fn=cmd_compare)

    args = ap.parse_args()
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main())
