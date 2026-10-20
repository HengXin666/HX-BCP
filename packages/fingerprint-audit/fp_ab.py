#!/usr/bin/env python3
"""fp-ab — 两路指纹对测: 同一浏览器, 唯一变量是**走不走代理**。

要回答的问题只有一个, 而且必须是二值的:
    「加了这层代理之后, 站方看到的握手身份还是不是同一个客户端?」

为什么必须 A/B 而不是"看代理日志":
  代理自己报告"我用了 chrome 指纹"没有信息量 —— 那是**实现声明**, 不是站方读数。
  只有拿掉代理前后在同一台机器、同一个浏览器、同一个 profile 上取站方读数,
  差异才唯一归因于代理。

三路定义 (2026-09-17 起):
  direct   直连 (对照组, 即"真身")
  proxy    经代理 **但不终结 TLS**的域 (peek/隧道档) —— 理论应 = direct
  mitm     经代理 **且终结 TLS** 的域 (MITM 档)        —— 实测差异在这里

判定:
  · 先查**同路自稳性** (samples>=2): 同一路自己都不稳, 跨路比对无意义 → 直接判 invalid;
  · 再比**顺序无关量** (ja3n_hash / ja4_r / akamai_hash): 全等 = identical;
  · 顺序敏感量 (ja3_hash/ja4) 只记录不判等 —— Chrome 逐连接随机置换扩展顺序,
    连自己都不等于自己 (见 probes/tls.py docstring)。

用法:
    .venv/bin/python fp_ab.py --port 43320 --proxy-port 8899 --samples 3 --label demo
输出:
    reports/ab-<时间戳>-<标签>.json  (机器可读, 供回归)
    evidence/ab-<时间戳>-<标签>.txt  (原始读数)
"""

# Agent Note: .agents/notes/implemented/testing/2026-09-17-fingerprint-ab-measurement-methodology.md
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from probes.cdp import Browser            # noqa: E402
from probes.tls import STABLE_KEYS, VOLATILE_KEYS, probe_tls  # noqa: E402
from probes.exitinfo import exit_ip       # noqa: E402

CHROME_CANDIDATES = ("google-chrome-stable", "google-chrome", "chromium")


def find_chrome() -> str:
    for c in CHROME_CANDIDATES:
        if subprocess.run(["which", c], capture_output=True).returncode == 0:
            return c
    raise RuntimeError("找不到 chrome/chromium")


def launch(port: int, profile: str, proxy: str = "", extra: list[str] | None = None) -> subprocess.Popen:
    """拉起被测环境。--remote-allow-origins=* 是必需的 (否则 websocket 升级被拒)。"""
    os.makedirs(profile, exist_ok=True)
    args = [find_chrome(), "--headless=new", "--no-sandbox", "--disable-gpu",
            "--remote-debugging-port=" + str(port), "--remote-allow-origins=*",
            "--user-data-dir=" + profile]
    if proxy:
        args.append("--proxy-server=" + proxy)
    args += (extra or [])
    args.append("about:blank")
    p = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                         start_new_session=True)
    for _ in range(60):
        try:
            urllib.request.urlopen("http://127.0.0.1:%d/json/version" % port, timeout=1).read()
            return p
        except Exception:
            time.sleep(0.4)
    raise RuntimeError("环境未在预期时间内起来 (port %d)" % port)


def stop(p: subprocess.Popen, profile: str) -> None:
    """用官方入口/精确匹配停止, 并**确认端口空了** (K1 教训: terminate 杀不掉守护进程)。"""
    p.terminate()
    time.sleep(0.8)
    try:
        p.kill()
    except Exception:
        pass
    subprocess.run(["pkill", "-f", "--user-data-dir=" + profile],
                   capture_output=True)
    time.sleep(0.5)
    subprocess.run(["rm", "-rf", profile], capture_output=True)


def measure(port: int, samples: int, wait: int) -> dict:
    b = Browser("http://127.0.0.1:%d" % port).connect()
    try:
        return probe_tls(b, wait_s=wait, samples=samples)
    finally:
        b.close()


def compare(runs: dict) -> dict:
    """runs: {arm_name: probe_result}。返回判定 + 逐量差异明细。"""
    findings = []
    # 关键: **取不到读数一律判红, 绝不静默跳过**。
    # 实测撞到过 (2026-09-17 三路首跑): mitm 路因代理自身崩溃返回 ERR_PROXY_CONNECTION_FAILED,
    # 旧逻辑把它从 arms 里 filter 掉, 于是三条 finding 全绿、裁决 GREEN ——
    # 一条**完全没被测试**的路被报成"与真身完全相同"。
    # 这正是本仓库铁律要防的「抓不到 ≠ 通过」, 所以先做完整性检查再谈比对。
    all_arms = list(runs.keys())
    arms = [k for k, v in runs.items() if (v or {}).get("status") == "ok"]
    for k, v in runs.items():
        if (v or {}).get("status") != "ok":
            why = str((v or {}).get("raw", ""))[:160].replace("\n", " ")
            findings.append({"level": "red", "rule": "S0 该路无读数",
                             "detail": k + " 没取到任何指纹读数 (端点不可达/代理未起来/被阻断): " + why,
                             "fix": "先让这一路能通再谈比对; 不得把无读数当作通过"})
    if len(arms) < 2:
        return {"verdict": "invalid", "base": None, "arms": all_arms,
                "findings": findings + [{"level": "red", "rule": "S0 有效路数不足",
                             "detail": "有效路数 < 2, 无法比对", "fix": "修通各路后重跑"}]}

    # 1) 同路自稳性: 不稳则无论差异多大都不可信
    unstable = [a for a in arms if not (runs[a].get("signals", {}) or {}).get("_self_stable", True)]
    if unstable:
        findings.append({"level": "red", "rule": "S0 同路自稳性",
                         "detail": "这些路自己在多次采样间就不稳定, 跨路比对无意义: " + ", ".join(unstable),
                         "fix": "提高 samples 并确认端点未被缓存/CDN 改写; 必要时换端点"})

    # 2) 顺序无关量逐量比对 (以 direct 为基准)
    base = "direct" if "direct" in arms else arms[0]
    b_sig = runs[base].get("signals", {}) or {}
    for a in arms:
        if a == base:
            continue
        a_sig = runs[a].get("signals", {}) or {}
        for k in STABLE_KEYS:
            bv, av = str(b_sig.get(k, "")), str(a_sig.get(k, ""))
            if not bv or not av:
                findings.append({"level": "yellow", "rule": "S1 读数缺失",
                                 "detail": f"{a} 的 {k} 取不到 (base {base} 有)", "fix": "检查端点可达性与探针解析"})
            elif bv == av:
                findings.append({"level": "green", "rule": "S2 指纹一致",
                                 "detail": f"{a} 的 {k} 与 {base} 完全相同", "fix": ""})
            else:
                findings.append({"level": "red", "rule": "S2 指纹不一致",
                                 "detail": f"{a} 的 {k} != {base}\n        {base}: {bv[:110]}\n        {a}: {av[:110]}",
                                 "fix": "该路改变了站方可见的握手身份 — 要么走不终结 TLS 的档, 要么放弃这一路"})
    lv = "green"
    for f in findings:
        if f["level"] == "red":
            lv = "red"
            break
        if f["level"] == "yellow":
            lv = "yellow"
    if unstable:
        return {"verdict": "invalid", "base": base, "arms": arms, "findings": findings +
                [{"level": "red", "rule": "S0 同路不稳", "detail": "同路自不稳, 无法跨路比对",
                  "fix": "见上条"}]}
    return {"verdict": lv, "base": base, "arms": arms, "findings": findings}


def render(res: dict, runs: dict) -> str:
    L = []
    L.append("")
    L.append("=" * 70)
    L.append("  两路指纹对测  " + res["verdict"].upper())
    L.append("=" * 70)
    for f in res["findings"]:
        icon = {"red": "🔴", "yellow": "🟡", "green": "🟢"}[f["level"]]
        L.append("  " + icon + " " + f["rule"] + ": " + f["detail"])
        if f.get("fix"):
            L.append("      → " + f["fix"])
    L.append("")
    L.append("  逐路读数 (顺序无关量):")
    for a, r in runs.items():
        s = (r or {}).get("signals", {}) or {}
        L.append("    [" + a + "] ja4_r=" + str(s.get("ja4_r", "-"))[:70])
        L.append("        ja3n_hash=" + str(s.get("ja3n_hash", "-")) +
                 "  akamai=" + str(s.get("akamai_hash", "-")))
    L.append("")
    L.append("  仅记录 (顺序敏感, Chrome 逐连接随机置换, 不参与判等):")
    for a, r in runs.items():
        s = (r or {}).get("signals", {}) or {}
        L.append("    [" + a + "] ja3_hash=" + str(s.get("ja3_hash", "-")))
    L.append("=" * 70)
    return "\n".join(L)


def main() -> int:
    ap = argparse.ArgumentParser(description="两路 TLS 指纹对测")
    ap.add_argument("--port", type=int, default=43320, help="被测浏览器 CDP 端口")
    ap.add_argument("--proxy", default="", help="被测路要走的代理 URL, 如 http://127.0.0.1:8899")
    ap.add_argument("--samples", type=int, default=3, help="每路采样次数 (>=2 才能查自稳性)")
    ap.add_argument("--wait", type=int, default=15, help="每次导航最长等待秒")
    ap.add_argument("--label", default="ab")
    ap.add_argument("--arms", default="direct,proxy", help="要跑的路, 逗号分隔: direct,proxy")
    a = ap.parse_args()

    # 路定义支持 "名字=代理URL", 以及给该路追加浏览器 flag 用 "|" 分隔:
    #   direct                                  直连 (对照组)
    #   proxy=http://127.0.0.1:2334             经上游代理, 不终结 TLS
    #   mitm=http://127.0.0.1:8899|ignorecert   经 MITM 代理 (需信任自签 CA)
    # 为什么用同一套 arms 而非写死两条: 判据要能复用到**将来任何**候选实现上,
    # 换路只改一行参数, 不改测具 (本仓库铁律: 能力抽象成端口, 换实现不动业务代码)。
    specs: list[tuple[str, str, list[str]]] = []
    for raw in [x.strip() for x in a.arms.split(",") if x.strip()]:
        name, eq, rest = raw.partition("=")
        url, _, flags = rest.partition("|")
        extra: list[str] = []
        for fl in flags.split("+"):
            fl = fl.strip()
            if fl == "ignorecert":
                extra.append("--ignore-certificate-errors")
            elif fl:
                extra.append(fl)
        if name == "direct":
            url = ""
        if not url and name != "direct" and a.proxy:
            url = a.proxy
        specs.append((name, url, extra))
    arms = [s[0] for s in specs]

    runs: dict = {}
    procs = []
    try:
        for i, (arm, proxy, extra) in enumerate(specs):
            port = a.port + i
            prof = "/tmp/fp-ab-%s-%d" % (arm, port)
            print("[fp-ab] 起路 " + arm + " (proxy=" + (proxy or "直连") + ", port=%d, extra=%s)" % (port, extra or "-"), flush=True)
            p = launch(port, prof, proxy, extra)
            procs.append((p, prof))
            time.sleep(1.0)
            print("[fp-ab]   采样 " + arm + " x" + str(a.samples) + " …", flush=True)
            runs[arm] = measure(port, a.samples, a.wait)
            stop(p, prof)
            procs.pop()
    finally:
        for p, prof in procs:
            stop(p, prof)

    res = compare(runs)
    rep = {"at": time.strftime("%Y-%m-%d %H:%M:%S"), "label": a.label,
           "host_exit_ip": exit_ip(), "samples": a.samples, "arms": arms,
           "arm_specs": [{"arm": n, "proxy": u, "extra_args": e} for n, u, e in specs],
           "runs": runs, "verdict": res}
    ts = time.strftime("%Y%m%d-%H%M%S")
    os.makedirs(os.path.join(HERE, "reports"), exist_ok=True)
    os.makedirs(os.path.join(HERE, "evidence"), exist_ok=True)
    rp = os.path.join(HERE, "reports", "ab-%s-%s.json" % (ts, a.label))
    ep = os.path.join(HERE, "evidence", "ab-%s-%s.txt" % (ts, a.label))
    with open(rp, "w", encoding="utf-8") as f:
        json.dump(rep, f, ensure_ascii=False, indent=2)
    with open(ep, "w", encoding="utf-8") as f:
        f.write(render(res, runs))
        for arm, r in runs.items():
            f.write("\n\n===== RAW " + arm + " =====\n" + str((r or {}).get("raw", ""))[:4000])
    print(render(res, runs))
    print("[fp-ab] 报告: " + rp)
    print("[fp-ab] 证据: " + ep)
    return 0 if res["verdict"] == "green" else 1


if __name__ == "__main__":
    sys.exit(main())
