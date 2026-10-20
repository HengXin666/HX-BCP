#!/usr/bin/env python3
"""指纹多样性守卫 CLI — 证明「不同 seed 真的给出不同指纹」。

这是 2026-09-14 那次事故 (「换 seed 没换指纹」) 的自动守卫。
用法:
    # 用 3 个不同 seed 各起一个环境, 采样并互相比对 (headless, 不占桌面)
    uv run diversity.py --seeds 44444,55555,66666 --proxy http://127.0.0.1:7892

    # 与已存基线比对 (检查"某个账号的指纹是否被动过")
    uv run diversity.py --seeds 44444 --check-baseline

退出码: 0 = 关键维度确实变化; 1 = 未变化 (红线); 2 = 环境/端点问题。
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from probes import diversity                     # noqa: E402
from probes.cdp import cdp_alive                 # noqa: E402
from audit import launch_env                     # noqa: E402


def run_one(seed: str, port: int, mode: str, proxy: str, timeout_s: int = 420) -> dict:
    """起一个钉死 seed 的环境, 采样后停掉。"""
    profile = "/tmp/fp-diversity-" + seed
    subprocess.run(["rm", "-rf", profile], capture_output=True)
    os.makedirs(profile, exist_ok=True)
    # 关键: seed 写进 profile 的 .cloak-seed, 这是 cloak_launch 认的种子文件
    with open(os.path.join(profile, ".cloak-seed"), "w") as f:
        f.write(str(seed))
    proc = launch_env(mode, profile, port, proxy)
    ep = "http://127.0.0.1:" + str(port)
    rec = {"label": "seed-" + str(seed), "endpoint": ep, "seed": seed, "fingerprint": {}}
    try:
        for _ in range(80):
            if cdp_alive(ep):
                break
            time.sleep(0.5)
        else:
            rec["error"] = "CDP 未就绪"
            return rec
        rec["fingerprint"] = diversity.sample(ep)
    finally:
        # 用官方停止入口 (start_new_session 脱离父进程, terminate 杀不掉 — 见 KNOWN-ISSUES K1)
        from audit import CLOAK_LAUNCH
        subprocess.run([sys.executable, CLOAK_LAUNCH, "stop", str(port)],
                       capture_output=True, timeout=40)
        if proc:
            try:
                proc.terminate(); proc.wait(timeout=10)
            except Exception:
                pass
        subprocess.run(["rm", "-rf", profile], capture_output=True)
    return rec


def main() -> int:
    ap = argparse.ArgumentParser(description="指纹多样性守卫")
    ap.add_argument("--seeds", default="44444,55555,66666",
                    help="逗号分隔的 seed 列表 (至少 2 个才能比对)")
    ap.add_argument("--mode", default="headless", choices=["headless", "xvfb", "headed"])
    ap.add_argument("--proxy", default="")
    ap.add_argument("--base-port", type=int, default=43201)
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--save-baseline", action="store_true", help="把采样存入 BASELINE.json")
    a = ap.parse_args()

    seeds = [s.strip() for s in a.seeds.split(",") if s.strip()]
    if len(seeds) < 1:
        print("至少给一个 seed")
        return 2

    recs = []
    for i, seed in enumerate(seeds):
        print("[diversity] 采样 seed=" + seed + " …", flush=True)
        recs.append(run_one(seed, a.base_port + i, a.mode, a.proxy))

    out = {"at": time.strftime("%Y-%m-%d %H:%M:%S"), "mode": a.mode, "samples": recs}

    # 两两比对, 并把最差的一对作为判定依据
    comps = []
    for i in range(len(recs)):
        for j in range(i + 1, len(recs)):
            fa, fb = recs[i].get("fingerprint") or {}, recs[j].get("fingerprint") or {}
            if not fa or not fb:
                continue
            c = diversity.compare(fa, fb)
            v = diversity.judge(c)
            comps.append({"a": recs[i]["label"], "b": recs[j]["label"],
                          "verdict": v})
    out["comparisons"] = comps

    if a.save_baseline:
        for r in recs:
            if r.get("fingerprint"):
                diversity.save_baseline(r)
        print("[diversity] 已写入基线: " + diversity.BASELINE)

    if a.json:
        print(json.dumps(out, ensure_ascii=False, indent=2))
    else:
        ICO = {"red": "🔴", "yellow": "🟡", "green": "🟢"}
        print("")
        print("=" * 68)
        print("  指纹多样性守卫  " + out["at"] + "   mode=" + a.mode)
        print("=" * 68)
        for r in recs:
            fp = r.get("fingerprint") or {}
            if r.get("error"):
                print("  ⚪ " + r["label"] + ": " + r["error"])
                continue
            print("  " + r["label"] + ":")
            for k in ("platform", "cores", "screen", "timezone", "gpuRenderer"):
                print("      " + k.ljust(12) + " = " + str(fp.get(k)))
        print("")
        for c in comps:
            v = c["verdict"]
            print("  " + ICO.get(v["level"], "?") + " " + c["a"] + " vs " + c["b"] +
                  "  变化 %d/%d 维度" % (v["compare"]["changed_n"], v["compare"]["total_n"]))
            for f in v["findings"]:
                print("      " + f["rule"] + ": " + f["detail"])
                if f.get("fix"):
                    print("        → " + f["fix"])
        print("=" * 68)

    levels = [c["verdict"]["level"] for c in comps]
    if not comps:
        print("  结论: 无法比对 (采样失败或只给了一个 seed)")
        return 2
    if any(x == "red" for x in levels):
        print("  结论: 🔴 指纹没有随 seed 变化 —— 这是 2026-09-14 事故的同型缺陷")
        return 1
    if any(x == "yellow" for x in levels):
        # 与 judge() 的降级保持一致 —— 否则又变成"两处判据不同步"
        # (本仓库栽过的同型跟头: egress 的 cached() 与 verdict_of() 不一致)。
        print("  结论: 🟡 看到的关键维度确实变了, 但**验证不完整** (有维度未采集)")
        print("        → 要用 xvfb/headed 重跑才能宣称指纹隔离已验证")
        return 0
    print("  结论: 🟢 关键维度确实随 seed 变化 (且关键维度全部采集成功)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
