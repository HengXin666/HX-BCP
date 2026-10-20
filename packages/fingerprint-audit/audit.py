#!/usr/bin/env python3
"""fp-audit — 指纹环境质检红线 (通用, 不绑定任何具体业务)。

把「这个浏览器环境能不能用」变成一条可重复执行、有明确红黄绿的红线, 而不是靠感觉。
对**任意** CDP 可达的环境都适用: HX-Browser / CloakBrowser / 系统 Chrome / 将来任何实现。

用法:
    # 质检一个已在运行的环境 (最常用)
    uv run audit.py --endpoint http://127.0.0.1:43020

    # 质检并顺带拉起一个环境 (headless, 不占桌面)
    uv run audit.py --launch headless --profile /tmp/fp-test

    # 指定期望时区 (通常按出口国家给)
    uv run audit.py --endpoint http://127.0.0.1:43020 --expect-tz Asia/Tokyo

输出:
    · 终端报告 (红/黄/绿 + 每条带修复动作)
    · reports/<时间戳>-<标签>.json  结构化结果 (供程序消费 / 回归比对)
    · evidence/<时间戳>-<标签>.txt  原始站点文本 (排查站点改版用)
"""

# Agent Note: .agents/notes/implemented/architecture/2026-09-14-fp-audit-standalone-quality-gate.md
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from probes import sites                      # noqa: E402
from probes.cdp import Browser, cdp_alive     # noqa: E402
import rules                                  # noqa: E402

CLOAK_LAUNCH = os.path.abspath(os.path.join(
    HERE, "..", "scripts", "freebuff-register", "cloak_launch.py"))


def launch_env(mode, profile, port, proxy="", timezone=""):
    """拉起一个被测环境 (可选用; 已有环境时不必用这个)。"""
    if not os.path.exists(CLOAK_LAUNCH):
        print("[fp-audit] 找不到启动脚本: " + CLOAK_LAUNCH)
        return None
    os.makedirs(profile, exist_ok=True)
    args = [sys.executable, CLOAK_LAUNCH, str(port), profile, "--" + mode]
    if proxy:
        args += ["--proxy", proxy]
    if timezone:
        # --fix-tz: 把期望时区**真的传给浏览器**。只比对不修复等于只报病不治病,
        # 而且时区一致性是实测最有区分力的一条 (见 README 铁律来源)。
        args += ["--timezone", timezone]
    print("[fp-audit] 拉起被测环境: mode=" + mode + " port=" + str(port) +
          " profile=" + profile + (" tz=" + timezone if timezone else ""))
    return subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def run(endpoint, expect_tz="", label="", only=None, wait=30):
    """跑全部探针并返回完整结果 (供 CLI 与程序调用)。"""
    results = {}
    names = only or list(sites.ALL_PROBES)
    for name in names:
        fn = sites.ALL_PROBES.get(name)
        if not fn:
            continue
        print("[fp-audit] 探测 " + name + " …", flush=True)
        try:
            b = Browser(endpoint).connect()
            try:
                results[name] = fn(b, wait_s=wait)
            finally:
                b.close()
        except Exception as e:  # noqa: BLE001
            results[name] = {"site": name, "status": "unknown", "signals": {},
                             "raw": "", "error": type(e).__name__ + ": " + str(e)}
            print("[fp-audit]   " + name + " 失败: " + str(e))
    verdict = rules.evaluate(results, expect_tz=expect_tz)
    return {"endpoint": endpoint, "expect_tz": expect_tz, "label": label,
            "at": time.strftime("%Y-%m-%d %H:%M:%S"), "results": results, "verdict": verdict}


ICON = {"red": "🔴", "yellow": "🟡", "green": "🟢", "unknown": "⚪"}


def render(rep):
    v = rep["verdict"]
    lines = []
    lines.append("")
    lines.append("═" * 68)
    lines.append("  指纹环境质检  " + ICON.get(v["level"], "?") + " " + v["level"].upper())
    lines.append("  端点 " + rep["endpoint"] + "   标签 " + (rep.get("label") or "-") + "   时间 " + rep["at"])
    lines.append("═" * 68)
    for lv in ("red", "yellow", "green", "unknown"):
        items = [x for x in v["findings"] if x["level"] == lv]
        if not items:
            continue
        lines.append("")
        lines.append("  " + ICON[lv] + " " + lv.upper() + "  (" + str(len(items)) + " 条)")
        for x in items:
            lines.append("    · " + x["rule"] + ": " + x["detail"])
            if x.get("fix"):
                lines.append("      → " + x["fix"])
    if v.get("signals"):
        lines.append("")
        lines.append("  关键读数:")
        for k, val in v["signals"].items():
            lines.append("    " + k + " = " + str(val))
    lines.append("")
    lines.append("─" * 68)
    reds = [x for x in v["findings"] if x["level"] == "red"]
    if reds:
        lines.append("  结论: 不可用 — " + str(len(reds)) + " 条红线必须先修")
    elif v["level"] == "unknown":
        lines.append("  结论: 未知 — 没有任何探针取到读数, 请先确认网络/站点可达")
    else:
        lines.append("  结论: 可用" + ("(有黄项待观察)" if v["level"] == "yellow" else ""))
    lines.append("─" * 68)
    return "\n".join(lines)


def save(rep, label):
    ts = time.strftime("%Y%m%d-%H%M%S")
    tag = (ts + "-" + label) if label else ts
    rp = os.path.join(HERE, "reports", tag + ".json")
    ep = os.path.join(HERE, "evidence", tag + ".txt")
    with open(rp, "w", encoding="utf-8") as f:
        json.dump(rep, f, ensure_ascii=False, indent=2)
    with open(ep, "w", encoding="utf-8") as f:
        f.write("# fp-audit evidence  " + rep["at"] + "  endpoint=" + rep["endpoint"] + "\n\n")
        for name, r in rep["results"].items():
            r = r or {}
            f.write("\n" + "=" * 66 + "\n== " + name + "  status=" + str(r.get("status")) +
                    "  url=" + str(r.get("url")) + "\n" + "=" * 66 + "\n")
            f.write(str(r.get("raw") or r.get("error") or "")[:6000] + "\n")
    return rp, ep


def main():
    ap = argparse.ArgumentParser(description="指纹环境质检红线")
    ap.add_argument("--endpoint", default="http://127.0.0.1:43020", help="已有 CDP 端点")
    ap.add_argument("--launch", choices=["headless", "xvfb", "headed"], help="先拉起环境再质检")
    ap.add_argument("--profile", default="/tmp/fp-audit-profile", help="--launch 时的 profile")
    ap.add_argument("--port", type=int, default=43020, help="--launch 时的端口")
    ap.add_argument("--proxy", default="", help="--launch 时的代理 (为空则直连)")
    ap.add_argument("--expect-tz", default="",
                    help="期望时区, 如 Asia/Tokyo; 传 auto = 按出口 IP 自动推导 (推荐)")
    ap.add_argument("--fix-tz", action="store_true",
                    help="--launch 时把 --expect-tz 直接传给环境 (自动对齐, 而非只比对)")
    ap.add_argument("--label", default="", help="报告标签 (进入文件名)")
    ap.add_argument("--only", default="", help="只跑指定站 (逗号分隔)")
    ap.add_argument("--wait", type=int, default=30, help="每站最长等待秒数")
    ap.add_argument("--json", action="store_true", help="只输出 JSON")
    a = ap.parse_args()

    proc = None
    # --expect-tz auto: 从出口实况推导, 避免"人猜错时区反而自己制造不一致"
    #   (2026-09-14 实测踩到: 手工给 Asia/Tokyo 而出口在新加坡 → 自己造出 spoofed)。
    want_auto = a.expect_tz.strip().lower() == "auto"
    a.expect_tz = "" if want_auto else a.expect_tz

    if a.launch:
        proc = launch_env(a.launch, a.profile, a.port, a.proxy,
                          timezone=(a.expect_tz if a.fix_tz else ""))
        ep = "http://127.0.0.1:" + str(a.port)
        for _ in range(40):
            if cdp_alive(ep):
                break
            time.sleep(0.5)
        else:
            print("[fp-audit] 环境未就绪 (CDP 无响应)")
            return 2
        a.endpoint = ep

    # --expect-tz auto: 在**被测浏览器内**查出口地理, 拿到真实时区
    if want_auto:
        from probes import exitinfo
        geo = exitinfo.ip_geo(a.endpoint)
        a.expect_tz = geo["timezone"]
        print("[fp-audit] --expect-tz auto → 出口 " + (geo["ip"] or "?") +
              " (" + (geo["country"] or "?") + ") 时区=" + (a.expect_tz or "未知"))
        if not a.expect_tz:
            print("[fp-audit] 推导失败: 拿不到出口时区 → 时区一致性将无法判定 (不猜)")
        elif a.fix_tz and a.launch and proc:
            # 需要按正确时区重启环境才能真的对齐
            print("[fp-audit] 按推导结果对齐时区, 重启环境 …")
            # 坑 (2026-09-14 实测): cloak_launch 用 start_new_session=True **脱离父进程**,
            # 所以 proc.terminate() 杀不掉浏览器 —— 旧实例仍占着 CDP 端口, 新实例起不来,
            # 于是"重启后时区还是旧的"(实测: 传了 Asia/Singapore, JS 仍报 Asia/Shanghai)。
            # 必须用 cloak_launch 自带的 stop <port> (它按 --remote-debugging-port 精确定位)。
            try:
                subprocess.run([sys.executable, CLOAK_LAUNCH, "stop", str(a.port)],
                               capture_output=True, timeout=40)
            except Exception:
                pass
            try:
                proc.terminate(); proc.wait(timeout=10)
            except Exception:
                pass
            # 确认端口真的空了再起, 否则又是"起了个新的但连到旧的"
            for _ in range(20):
                if not cdp_alive(a.endpoint):
                    break
                time.sleep(0.5)
            time.sleep(1.0)
            proc = launch_env(a.launch, a.profile, a.port, a.proxy, timezone=a.expect_tz)
            for _ in range(40):
                if cdp_alive(a.endpoint):
                    break
                time.sleep(0.5)

    try:
        if not cdp_alive(a.endpoint):
            print("[fp-audit] CDP 端点无响应: " + a.endpoint)
            return 2
        rep = run(a.endpoint, expect_tz=a.expect_tz, label=a.label,
                  only=[x for x in a.only.split(",") if x] or None, wait=a.wait)
    finally:
        if proc:
            try:
                proc.terminate()
            except Exception:
                pass

    rp, ep = save(rep, a.label)
    if a.json:
        print(json.dumps(rep, ensure_ascii=False, indent=2))
    else:
        print(render(rep))
        print("  报告: " + rp)
        print("  证据: " + ep)
    return 0 if rep["verdict"]["level"] in ("green", "yellow") else 1


if __name__ == "__main__":
    raise SystemExit(main())
