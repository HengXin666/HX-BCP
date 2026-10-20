"""红线规则引擎 — 把四个站的原始读数翻译成 红/黄/绿 + 可执行修复建议。

设计要点:
  1. **红线必须是"有区分力的信号"**。已实测反例 (pixelscan RESEARCH-2026-09-12.md):
     "Masking detected" 对干净系统 Chrome 也报 → 列入 NOISE, 永不参与判定。
     把它写成显式常量而不是"不写就行", 是为了挡住日后有人"顺手"加回来。
  2. 探针 **unknown 不等于通过** —— 抓不到就给 unknown 级, 需要人看, 绝不静默放行。
  3. 每条规则带 fix (修复动作), 让报告可直接当操作清单用, 而不只是"你挂了"。
"""
from __future__ import annotations

# ── 常量 ──────────────────────────────────────────────────

# 无区分力信号: 干净环境也会报, 纳入判定会造成"狼来了" (实测依据见 docstring)
NOISE_SIGNALS = {
    "masking_detected": "干净系统 Chrome 也报 (2026-09-12 实测 B 组), 无区分力",
    "uniqueness_pct": "人人几乎唯一, 是隐私指标不是限流指标",
}

LEVELS = {"red": 3, "yellow": 2, "green": 1, "unknown": 0}


def _mk(level: str, rule: str, detail: str, fix: str = "") -> dict:
    return {"level": level, "rule": rule, "detail": detail, "fix": fix}


def _js_tz(sig: dict) -> str:
    return str(sig.get("js_timezone") or "").strip()


# ── 规则 ──────────────────────────────────────────────────
def evaluate(results: dict, expect_tz: str = "") -> dict:
    """results: {site: probe_result}。expect_tz: 期望时区 (通常由出口国家推出)。

    返回 {level, findings[], signals{}}。level 取所有 finding 里最严重的 (unknown 不压红)。
    """
    f: list[dict] = []
    sig_all: dict = {}

    # R1 自动化痕迹 (creepjs) —— 被抓到基本等于"这环境不能用"
    cj = results.get("creepjs", {})
    cs = cj.get("signals", {}) or {}
    if cs.get("bot") or cs.get("headless") or cs.get("webdriver"):
        hits = [k for k in ("bot", "headless", "webdriver") if cs.get(k)]
        f.append(_mk("red", "R1 自动化痕迹",
                     f"creepjs 报出 {hits}: 环境被明确识别为自动化",
                     "改用 headed/xvfb 模式; 确认启动参数无 --headless、无 AutomationControlled 残留"))
    if cs.get("lies"):
        f.append(_mk("red", "R2 指纹自相矛盾",
                     "creepjs 报 lies: 伪装值互相打架 (如 platform 与 UA 不一致)",
                     "让所有指纹字段同源派生 (参考 cloak_launch.derive_fingerprint 的做法)"))
    ts = cs.get("trust_score")
    if isinstance(ts, int):
        if ts < 30:
            f.append(_mk("red", "R3 trust score 过低", f"creepjs trust score = {ts} (<30)",
                         "逐项修复本报告其它红线; trust 是综合分, 单项修复会带动它"))
        elif ts < 70:
            f.append(_mk("yellow", "R3 trust score 偏低", f"creepjs trust score = {ts} (30-70)",
                         "检查 canvas/WebGL/字体等分项是否有明显异常"))

    # R4 时区一致性 (pixelscan + browserleaks 双源) —— 本仓库实测最有区分力的一条
    ps = results.get("pixelscan", {})
    pss = ps.get("signals", {}) or {}
    if pss.get("timezone_spoofed"):
        f.append(_mk("red", "R4 时区与出口不一致",
                     "pixelscan 报 Timezone spoofed: JS 时区与出口 IP 时区对不上",
                     "给浏览器传 --fingerprint-timezone=<出口时区> 并同步 TZ 环境变量 "
                     "(只传 --fingerprint-timezone 不够, JS 仍读宿主 TZ)"))
    elif pss.get("inconsistent"):
        f.append(_mk("yellow", "R4 跨信号不一致",
                     "pixelscan 报 inconsistent (未必是时区, 需看 raw 定位)",
                     "对照 evidence 原始报告定位是哪一组信号冲突"))
    elif pss.get("consistent"):
        f.append(_mk("green", "R4 跨信号一致", "pixelscan 报 consistent", ""))
    jtz = _js_tz(pss) or _js_tz((results.get("browserleaks", {}) or {}).get("signals", {}) or {})
    # pixelscan 自己给出了出口时区 → 可**自洽**判定, 不需要外部 geo API
    # (实测: ip-api 走 http 被拦、ipapi.co 免费额度耗尽, 都不能依赖)
    ip_tz = str(pss.get("ip_timezone") or "").strip()
    if jtz and ip_tz and jtz.lower() != ip_tz.lower():
        f.append(_mk("red", "R4b 时区与出口自相矛盾",
                     f"JS 时区={jtz} 但出口时区={ip_tz} (pixelscan 自报, 无需外部查询)",
                     "给浏览器传 --fingerprint-timezone= 出口实际时区, 并同步 TZ 环境变量"))
    elif jtz and ip_tz:
        f.append(_mk("green", "R4b 时区与出口一致", f"JS={jtz} 出口={ip_tz}", ""))
    if jtz:
        sig_all["js_timezone"] = jtz
    if ip_tz:
        sig_all["ip_timezone"] = ip_tz
    if jtz:
        if expect_tz and jtz.lower() != expect_tz.lower():
            f.append(_mk("red", "R5 时区未对齐预期",
                         f"JS 时区={jtz}, 期望={expect_tz} (该出口应表现为此地用户)",
                         "按出口国家重设 --fingerprint-timezone (见 hx_proxy tz_for_country)"))
        elif expect_tz:
            f.append(_mk("green", "R5 时区已对齐", f"JS 时区={jtz} 与期望一致", ""))

    # R6 出口性质 (不是浏览器缺陷, 但决定限流待遇)
    ut = str(pss.get("ip_user_type") or "").lower()
    if ut in ("hosting", "datacenter"):
        f.append(_mk("yellow", "R6 出口被标为机房",
                     f"pixelscan: User Type by IP = {ut} (非住宅/ISP)",
                     "这是出口 ASN 属性, 与浏览器无关; 换出口才能改 (属出口体检维度)"))
    if pss.get("proxy_detected"):
        f.append(_mk("yellow", "R7 出口被标为代理",
                     "pixelscan 报 Proxy detected",
                     "同上, 属出口属性; 若目标站只卡代理可考虑其它出口"))

    # R10 指纹字段缺失 —— 实测撞到: WebGL Vendor/Renderer 都是 "-" (空值)。
    # 一个真实浏览器**必然**能报出 WebGL/Canvas; 打不开或报空, 说明环境被削弱
    # (无 GPU / 被 --disable-gpu / 代理拦了 webgl 探测), 这本身就是可疑信号。
    for key, label in (("webgl_vendor", "WebGL Vendor"), ("webgl_renderer", "WebGL Renderer")):
        val = str(pss.get(key) or "").strip()
        if val in ("", "-", "null", "undefined"):
            f.append(_mk("red", "R10 指纹字段缺失",
                         f"pixelscan: {label} = {val or '(空)'} —— 真实浏览器不应为空",
                         "确认 GPU 未被禁用 (--disable-gpu / 无头环境常导致); "
                         "WebGL 为空会同时拉低 creepjs trust 分"))

    # R8 行为检测 (这条好 = 说明自动化没被行为模型抓到)
    if pss.get("no_automation"):
        f.append(_mk("green", "R8 无自动化行为", "pixelscan: No automated behavior detected", ""))

    # R9 每个站的可用性: 抓不到 ≠ 通过
    for site, r in results.items():
        if (r or {}).get("status") != "ok":
            f.append(_mk("unknown", f"R9 {site} 未取到读数",
                         f"{site} 探针 status={ (r or {}).get('status') } (可能站点改版/超时)",
                         "人工查看 evidence 原始文本, 确认是站点问题还是环境问题"))

    # 汇总: unknown 不参与"最严重"计算, 但会被单独列出
    real = [_mk(x["level"], "", "") for x in f if x["level"] in LEVELS and x["level"] != "unknown"]
    level = "green"
    for x in real:
        if LEVELS[x["level"]] > LEVELS[level]:
            level = x["level"]
    if not real:
        level = "unknown"
    return {"level": level, "findings": f, "signals": sig_all,
            "unknowns": [x for x in f if x["level"] == "unknown"]}
