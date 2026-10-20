"""四个质检站的探针 (creepjs / browserleaks / amiunique / pixelscan)。

设计原则 (来自已沉淀的 pixelscan RESEARCH-2026-09-12.md):
  1. **有区分力的信号才纳入红线** —— 该研究实测发现 "Masking detected" 对干净系统
     Chrome 也报, 属营销噪音, 无区分力; 而 "时区一致性" 是真风险。
  2. 每个探针返回结构化结果 + 原始证据, 由 rules.py 统一判红黄绿,
     探针本身**不下结论** (避免"判定逻辑散落各处"这个本仓库栽过的坑)。
  3. 站点改版是常态 → 探针必须**容忍解析失败**: 抓不到就给 status="unknown"
     并保留原文, 绝不把"没抓到"翻译成"通过"。
"""
from __future__ import annotations

import re
import time

from .cdp import Browser


def _clean(s: str, limit: int = 6000) -> str:
    return re.sub(r"\n{3,}", "\n\n", (s or "").strip())[:limit]


# ── creepjs ───────────────────────────────────────────────
def probe_creepjs(b: Browser, wait_s: int = 25) -> dict:
    """creepjs: 综合指纹 + 自动化/谎言检测 (浏览器指纹圈的事实标准)。

    官方站 abrahamjuliot.github.io/creepjs/ 与 creepjs.org/checker 都可。
    关键读数: trust score / bot 判定 / lies(伪装矛盾) / 是否 headless。
    """
    out = {"site": "creepjs", "url": "", "status": "unknown", "signals": {}, "raw": ""}
    b.goto("https://abrahamjuliot.github.io/creepjs/", wait_s=6)
    out["url"] = b.url()
    # 页面是异步算分, 等 trust score 出现
    b.wait_for("document.body.innerText.includes('trust score')", timeout_s=wait_s)
    txt = _clean(b.text())
    out["raw"] = txt[:4000]
    sig: dict = {}
    m = re.search(r"(\d{1,3})\s*%?\s*trust score", txt, re.I) or re.search(r"trust score[:\s]*(\d{1,3})", txt, re.I)
    if m:
        sig["trust_score"] = int(m.group(1))
    for key, pat in (
        ("bot", r"\b(bot|automation|headless|webdriver)\b"),
        ("lies", r"\blies?\b"),
        ("resistance", r"resistance"),
        ("headless", r"headless"),
    ):
        if re.search(pat, txt, re.I):
            sig[key] = True
    out["signals"] = sig
    out["status"] = "ok" if sig else "unknown"
    return out


# ── browserleaks ──────────────────────────────────────────
def probe_browserleaks(b: Browser, wait_s: int = 15) -> dict:
    """browserleaks: 分项泄漏检查 (Canvas/WebGL/WebRTC/TLS/字体/时区)。

    重点看**泄漏型**信号: WebRTC 真实内网 IP、DNS 泄漏、时区与 IP 不一致。
    """
    out = {"site": "browserleaks", "url": "", "status": "unknown", "signals": {}, "raw": ""}
    b.goto("https://browserleaks.com/", wait_s=5)
    out["url"] = b.url()
    txt = _clean(b.text())
    # 拉取两个最有区分力的子页: IP 与时区
    b.goto("https://browserleaks.com/ip", wait_s=6)
    ip_txt = _clean(b.text(), 3000)
    b.goto("https://browserleaks.com/javascript", wait_s=6)
    js_txt = _clean(b.text(), 3000)
    sig: dict = {}
    m = re.search(r"\b(\d{1,3}(?:\.\d{1,3}){3})\b", ip_txt)
    if m:
        sig["ip"] = m.group(1)
    m = re.search(r"Timezone[:\s]*([A-Za-z]+/[A-Za-z_]+)", js_txt)
    if m:
        sig["js_timezone"] = m.group(1)
    if re.search(r"WebRTC|local IP", txt, re.I):
        sig["has_webrtc_section"] = True
    out["signals"] = sig
    out["raw"] = _clean(ip_txt + "\n---\n" + js_txt, 4000)
    out["status"] = "ok" if sig else "unknown"
    return out


# ── amiunique ─────────────────────────────────────────────
def probe_amiunique(b: Browser, wait_s: int = 20) -> dict:
    """amiunique: 指纹**唯一性**统计 (你在多少访问者里是唯一的)。

    注意: 唯一性是隐私指标, **不是**限流指标 —— 每个真人也几乎唯一。
    这里只取它暴露的原始指纹字段做**自洽性**交叉验证 (与其它站比对)。
    """
    out = {"site": "amiunique", "url": "", "status": "unknown", "signals": {}, "raw": ""}
    b.goto("https://amiunique.org/", wait_s=6)
    out["url"] = b.url()
    # amiunique 是 SPA: 首屏只有导航壳, 真数据要等它自己算完 (实测 25s 时仍只有
    # "MY FINGERPRINT / MY EXTENSION" 两行) → 等**内容特征**出现, 而不是等长度。
    b.wait_for("document.body.innerText.length > 400", timeout_s=wait_s)
    b.wait_for("/^\\s*MY FINGERPRINT/im.test(document.body.innerText) "
               "&& document.body.innerText.length > 800", timeout_s=max(wait_s, 40))
    txt = _clean(b.text())
    out["raw"] = txt[:3000]
    sig: dict = {}
    m = re.search(r"([\d.]+)\s*%\s*(?:of|similar)", txt, re.I)
    if m:
        sig["uniqueness_pct"] = m.group(1)
    out["signals"] = sig
    out["status"] = "ok" if sig else "unknown"
    return out


# ── pixelscan ─────────────────────────────────────────────
def probe_pixelscan(b: Browser, wait_s: int = 30) -> dict:
    """pixelscan: **跨信号一致性**检查 (本仓库实测证明它最有区分力的就是这一条)。

    必须先点 "Scan My Browser Now" 才出报告 (在 /fingerprint-check)。
    已实测无区分力的项 (Masking detected) 也照收, 但由 rules.py 明确排除,
    不参与红线判定 —— 免得日后有人"顺手"把它加回去。
    """
    out = {"site": "pixelscan", "url": "", "status": "unknown", "signals": {}, "raw": ""}
    b.goto("https://pixelscan.net/", wait_s=6)
    out["url"] = b.url()
    # 点扫描按钮 (文案可能变, 多路兜底)
    clicked = b.js("""(() => {
      const cands = [...document.querySelectorAll('button, a, div[role=button]')];
      const t = cands.find(e => /scan my browser|scan now|start scan/i.test(e.innerText||''));
      if (t) { t.click(); return t.innerText.trim(); }
      return null;
    })()""", await_promise=False)
    out["signals"]["clicked"] = clicked
    if clicked:
        time.sleep(min(wait_s, 25))
    else:
        time.sleep(3)
    txt = _clean(b.text(), 5000)
    out["url"] = b.url()
    out["raw"] = txt[:4000]
    sig = dict(out["signals"])
    low = txt.lower()
    sig["consistent"] = bool(re.search(r"fingerprint is consistent", low))
    sig["inconsistent"] = bool(re.search(r"fingerprint is inconsistent", low))
    sig["timezone_spoofed"] = "timezone spoofed" in low or "timezone mismatch" in low
    sig["proxy_detected"] = "proxy detected" in low
    sig["masking_detected"] = "masking detected" in low
    sig["no_automation"] = "no automated behavior" in low
    # pixelscan 的报告是「标签 \n 取值」逐行排布 (不是 label: value), 故按行对解析。
    lines = [x.strip() for x in txt.split("\n")]
    lw = [x.lower() for x in lines]

    def after(label: str) -> str:
        """取 label 行之后的第一个非空行 (pixelscan 的值在下一行)。"""
        for i, x in enumerate(lw):
            if x == label or x.startswith(label):
                for j in range(i + 1, min(i + 4, len(lines))):
                    if lines[j].strip():
                        return lines[j].strip()
        return ""

    v = after("timezone from js")
    if v:
        sig["js_timezone"] = v
    v = after("time from ip")
    if v:
        sig["ip_time"] = v
    v = after("user type by ip")
    if v:
        sig["ip_user_type"] = v.lower()
    # pixelscan 报告的 `Timezone<TAB>Asia/Singapore (SGT)` 行 = **出口 IP 的时区**,
    # 这是最可靠的"期望时区"来源 (无需外部 geo API, 且走的就是被测环境的出口)。
    for i, x in enumerate(lines):
        if x.lower().startswith("timezone") and "\t" in x:
            parts = [p.strip() for p in x.split("\t") if p.strip()]
            if len(parts) >= 2 and "/" in parts[1]:
                sig["ip_timezone"] = parts[1].split(" ")[0]
                break
    if "ip_timezone" not in sig:
        v = after("timezone")
        if v and "/" in v:
            sig["ip_timezone"] = v.split(" ")[0]
    # 指纹字段缺失也是信号 (被测环境可能根本没提供 WebGL/Canvas)
    for label, key in (("webgl vendor", "webgl_vendor"), ("webgl renderer", "webgl_renderer")):
        v = after(label)
        if v:
            sig[key] = v
    out["signals"] = sig
    got = any(k in sig for k in ("consistent", "inconsistent", "timezone_spoofed"))
    out["status"] = "ok" if got else "unknown"
    return out


ALL_PROBES = {
    "creepjs": probe_creepjs,
    "browserleaks": probe_browserleaks,
    "amiunique": probe_amiunique,
    "pixelscan": probe_pixelscan,
}
