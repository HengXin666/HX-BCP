#!/usr/bin/env python3
"""真实浏览器 + 跨进程观测: 引擎跑在独立进程, 面板 API 读到真实计数。"""
import json, os, shutil, subprocess, sys, time, urllib.request
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))
from hxcp.launcher import launch

CACHE = "/tmp/hxcp-panel-e2e"
PORT = 43520
API = 28814
BASE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")

shutil.rmtree(CACHE, ignore_errors=True)
lc = launch(PORT, profile="/tmp/hxcp-panel-profile")
print("[panel] 环境", lc.endpoint)
try:
    env = dict(os.environ, PY=os.path.join(BASE, "src"), CACHE=CACHE, EP=lc.endpoint)
    child = r'''
import os, sys, time
sys.path.insert(0, os.environ["PY"])
from hxcp.cdp import Session
from hxcp.engine import Engine
from hxcp.store import Store
from hxcp import policy
s = Session(os.environ["EP"], timeout=120)
st = Store(os.environ["CACHE"])
eng = Engine(s, st, policy.Rules(), log=lambda m: print(m, flush=True))
eng.start()
s.navigate("https://github.com/", settle=6.0)
eng.tick()
s.navigate("https://github.com/", settle=6.0)
eng.tick()
for _ in range(20):
    eng.tick(); time.sleep(0.2)
eng.stop(); st.close()
'''
    proc = subprocess.Popen([sys.executable, "-c", child], env=env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    # 另起 API 进程 (与引擎不同进程)
    api_proc = subprocess.Popen(
        [sys.executable, "-m", "hxcp.api", "--host", "127.0.0.1", "--port", str(API),
         "--cache-dir", CACHE],
        env=dict(os.environ, PYTHONPATH=os.path.join(BASE, "src")),
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def get(p):
        with urllib.request.urlopen("http://127.0.0.1:%d%s" % (API, p), timeout=8) as r:
            return json.loads(r.read().decode())

    for _ in range(60):
        try:
            get("/health"); break
        except Exception:
            time.sleep(0.3)

    seen = []
    for _ in range(30):
        try:
            s = get("/stats")
            st_ = s.get("engine") or {}
            seen.append((s.get("engine_state"), st_.get("paused"), st_.get("hits"),
                         st_.get("filled"), st_.get("bytes_network")))
        except Exception:
            pass
        if proc.poll() is not None:
            break
        time.sleep(0.6)
    proc.wait(timeout=30)

    print("  面板观测采样 (state, paused, hits, filled, net_bytes):")
    for row in seen[:3] + (["..."] if len(seen) > 6 else []) + seen[-3:]:
        print("   ", row)
    peak = max((r[3] or 0) for r in seen if isinstance(r[0], str)) if seen else 0
    any_running = any(r[0] == "running" for r in seen if isinstance(r[0], str))
    hits_max = max((r[2] or 0) for r in seen if isinstance(r[0], str)) if seen else 0
    filled_max = max((r[3] or 0) for r in seen if isinstance(r[0], str)) if seen else 0
    net_max = max((r[4] or 0) for r in seen if isinstance(r[0], str)) if seen else 0
    # 引擎退出后应为 stopped
    time.sleep(1.0)
    final = get("/health")
    res = {"ok": True}
    def chk(n, c, e=""):
        print(("  PASS " if c else "  FAIL ") + n + ("  " + str(e)[:90] if e else ""))
        res["ok"] = res["ok"] and bool(c)
    chk("面板曾看到 running", any_running, seen[:2])
    chk("面板读到 filled>0 (真实填充)", filled_max > 0, filled_max)
    chk("面板读到 hits>0 (真实命中)", hits_max > 0, hits_max)
    chk("面板读到网络字节 (真实流量)", net_max > 0, net_max)
    chk("引擎退出后面板显示非 running", final.get("engine_state") != "running", final.get("engine_state"))
    print()
    print("面板跨进程观测:", "PASS" if res["ok"] else "FAIL")
    api_proc.terminate()
    sys.exit(0 if res["ok"] else 1)
finally:
    lc.stop()
    print("[panel] 环境已清理")
