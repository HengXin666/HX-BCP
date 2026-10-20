"""指纹多样性守卫 — 自动看住 2026-09-14 那个「换 seed 没换指纹」的缺陷。

为什么需要它 (真实事故):
  用户要求「每次都换完整的指纹浏览器吗?」然后实测发现: 3 个不同 seed 的
  `platform / UA / cores / screen / WebGL` **6/7 字段完全相同** —— 根因是启动器
  硬编码了 `--fingerprint-platform=windows`, `--fingerprint=<seed>` 只改 canvas
  与 WebGL renderer。于是"每轮换新指纹"名不副实。

本模块的职责只有一个: **证明两个不同 seed 的环境在可观测维度上确实不同**。
它不判断"这指纹好不好"(那是 rules.py 的事), 只回答"它到底换没换"。

★ 能力边界 (设计上的诚实, 不是缺陷):
  只能验证**本工具采集到的维度**。fp-audit 通过 CDP 在页面里读, 所以看得到
  navigator/screen/WebGL/时区等几百个字段, 但**看不到** TLS 指纹、HTTP/2 帧序、
  字体列表的逐字节顺序等非 JS 维度。要想覆盖那些, 需要换用抓包/端侧方案。
  所以"本守卫通过" ≠ "指纹完全隔离", 只等于"JS 可见维度上确实换了"。
"""

# Agent Note: .agents/notes/implemented/feature/2026-09-16-fingerprint-diversity-guard.md
from __future__ import annotations

import json
import os
from datetime import datetime

HERE = os.path.dirname(os.path.abspath(__file__))
BASELINE = os.path.join(os.path.dirname(HERE), "reports", "BASELINE.json")

# 采集哪些维度 (与 cloak_launch.derive_fingerprint 派生的维度对齐, 但**在读侧重新
# 从浏览器实际取值** —— 不信任启动参数。"传了参数" ≠ "生效了"。)
PROBE_JS = r"""
(() => {
  const g = (f) => { try { return f() } catch (e) { return null } };
  let gl = null, gpu = {}, webgl = null;
  try {
    const c = document.createElement("canvas");
    gl = c.getContext("webgl") || c.getContext("experimental-webgl");
    if (gl) {
      const d = gl.getExtension("WEBGL_debug_renderer_info");
      gpu = {
        vendor: d ? gl.getParameter(d.UNMASKED_VENDOR_WEBGL) : gl.getParameter(gl.VENDOR),
        renderer: d ? gl.getParameter(d.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER),
      };
      webgl = gl.getParameter(gl.VERSION);
    }
  } catch (e) {}
  return {
    ua: navigator.userAgent,
    platform: navigator.platform,
    cores: navigator.hardwareConcurrency,
    mem: navigator.deviceMemory === undefined ? null : navigator.deviceMemory,
    languages: (navigator.languages || []).join(","),
    lang: navigator.language,
    timezone: g(() => Intl.DateTimeFormat().resolvedOptions().timeZone),
    screen: [screen.width, screen.height].join("x"),
    avail: [screen.availWidth, screen.availHeight].join("x"),
    colorDepth: screen.colorDepth,
    pixelRatio: window.devicePixelRatio,
    gpuVendor: gpu.vendor || null,
    gpuRenderer: gpu.renderer || null,
    webglVersion: webgl,
    maxTouchPoints: navigator.maxTouchPoints,
    cookieEnabled: navigator.cookieEnabled,
    doNotTrack: navigator.doNotTrack,
    webdriver: navigator.webdriver === true,
  };
})()
"""


def sample(endpoint: str) -> dict:
    """采集一个环境当前**实际**报出的指纹维度。"""
    from .cdp import Browser
    b = Browser(endpoint).connect()
    try:
        # 必须在真实 http(s) 页面上取 (about:blank 下部分 API 不可用)
        if not str(b.url() or "").startswith("http"):
            b.goto("https://example.com/", wait_s=4)
        val = b.js(PROBE_JS)
        return val if isinstance(val, dict) else {}
    finally:
        b.close()


def compare(a: dict, b: dict) -> dict:
    """比较两次采样, 给出"哪些维度真的变了"。"""
    keys = sorted(set(a) | set(b))
    same, diff, missing = [], [], []
    for k in keys:
        va, vb = a.get(k), b.get(k)
        if va is None or vb is None:
            missing.append(k)
        elif str(va) == str(vb):
            same.append(k)
        else:
            diff.append(k)
    total_real = len(same) + len(diff)
    return {
        "changed": diff,
        "unchanged": same,
        "unavailable": missing,
        "changed_n": len(diff),
        "total_n": total_real,
        "ratio": (len(diff) / total_real) if total_real else 0.0,
    }


# 这几个维度若**完全相同**, 是"没换指纹"的强信号 (来自 09-14 事故的实测字段)。
# 之所以单列: 它们是最容易硬编码/最容易被站点当画像的项。
CRITICAL = ["platform", "ua", "cores", "screen", "gpuVendor", "gpuRenderer", "timezone"]


def judge(cmp_result: dict, max_ratio: float = 0.0) -> dict:
    """把比较结果翻译成通过/不通过。

    max_ratio: 允许"关键维度全同"的比例阈值。默认 0.0 = 关键维度**任何一项相同
               都可以接受, 但**全部相同**就判失败 (与 09-14 事故的判据一致)。
    """
    crit_same = [k for k in CRITICAL if k in cmp_result["unchanged"]]
    crit_changed = [k for k in CRITICAL if k in cmp_result["changed"]]
    # 采集不到的关键维度 —— 必须显式统计。
    # 实测 (2026-09-16): headless 下 WebGL 不可用, gpuVendor/gpuRenderer 恒为 None。
    # 而 GPU 恰恰是 09-14 事故中**会变**的字段之一 → Headless 下守卫看不到它。
    # 若此时还报"关键维度确实变化", 就是在**假装验证过没验证的东西** ——
    # 与本工具"探针 unknown 不等于通过"的原则直接冲突。必须降级并说清。
    crit_unavail = [k for k in CRITICAL if k in cmp_result["unavailable"]]
    crit_seen = len(crit_changed) + len(crit_same)
    all_same = crit_seen > 0 and len(crit_changed) == 0
    findings = []
    if all_same:
        findings.append({
            "level": "red", "rule": "D1 指纹未随 seed 变化",
            "detail": ("两个不同 seed 的关键维度**全部相同**: " + ", ".join(crit_same) +
                       " —— 说明启动器没有真的换指纹 (2026-09-14 事故同型)"),
            "fix": ("检查启动参数是否硬编码了 platform/UA 等; 应为每个 seed 派生整套 "
                    "(参考 cloak_launch.derive_fingerprint)"),
        })
    else:
        if crit_changed:
            # 有维度没采到 → 只能给 yellow: "看到的确实变了, 但没看全"。
            # 绝不因为在场维度都变了就宣称"指纹隔离已验证"。
            lvl = "yellow" if crit_unavail else "green"
            detail = "关键维度变化: " + ", ".join(crit_changed)
            if crit_unavail:
                detail += (" | ⚠️ 但有 %d 个关键维度**未能采集** (%s), 验证不完整"
                           % (len(crit_unavail), ", ".join(crit_unavail)))
            findings.append({
                "level": lvl, "rule": "D1 指纹确实随 seed 变化",
                "detail": detail,
                "fix": ("改用 xvfb/headed 模式以采集 GPU 等维度" if crit_unavail else ""),
            })
    if cmp_result["ratio"] < 0.2 and not all_same:
        findings.append({
            "level": "yellow", "rule": "D2 变化面偏窄",
            "detail": ("整体仅 %d/%d 个维度变化 (%.0f%%) —— 可能只有少数项在变"
                       % (cmp_result["changed_n"], cmp_result["total_n"],
                          cmp_result["ratio"] * 100)),
            "fix": "检查派生覆盖的维度是否足够 (platform/UA/GPU/屏幕/核心数/内存)",
        })
    level = "red" if all_same else ("yellow" if any(x["level"] == "yellow" for x in findings)
                                    else "green")
    return {"level": level, "findings": findings, "compare": cmp_result,
            "critical_unavailable": crit_unavail}


def load_baseline() -> dict | None:
    try:
        with open(BASELINE, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return None


def save_baseline(rec: dict) -> str:
    """把一个采样记为基线 (供日后比对)。"""
    os.makedirs(os.path.dirname(BASELINE), exist_ok=True)
    data = load_baseline() or {"created": None, "samples": {}}
    data.setdefault("samples", {})
    data["samples"][rec["label"]] = {"at": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
                                     "endpoint": rec.get("endpoint", ""),
                                     "fingerprint": rec["fingerprint"]}
    if not data.get("created"):
        data["created"] = data["samples"][rec["label"]]["at"]
    with open(BASELINE, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
    return BASELINE
