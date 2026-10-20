#!/usr/bin/env python3
"""跨进程运行态验证: 引擎在子进程跑, API 从另一进程读它的状态。

这是真实部署形态 (引擎挂在浏览器旁, API 是长驻服务), 必须证明:
  API 不依赖与引擎同进程也能报告 engine_state=running 与真实计数。
"""
import json, os, shutil, subprocess, sys, time, urllib.request, threading
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

CACHE = "/tmp/hxcp-xproc"
PORT = 28812
API = 28813

child = r'''
import sys, time, os
sys.path.insert(0, os.environ["PY"])
from hxcp.engine import Engine
from hxcp.store import Store
from hxcp import policy

class FakeSession:
    def __init__(self): self.on_event = None
    def send(self, *a, **k): return {}
    def enable_fetch(self, *a, **k): return None
    def disable_fetch(self): return None
    def continue_request(self, *a, **k): return None
    def fulfill(self, *a, **k): return None
    def close(self): return None

st = Store(os.environ["CACHE"])
eng = Engine(FakeSession(), st, policy.Rules())
eng.start()
eng.stats.hits = 7          # 造一个可辨认的读数
eng.stats.bytes_served = 4096
eng.tick()
time.sleep(6)
eng.stop()
'''
os.environ["PY"] = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src")
os.environ["CACHE"] = CACHE
shutil.rmtree(CACHE, ignore_errors=True)

p = subprocess.Popen([sys.executable, "-c", child], env=dict(os.environ),
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
rt = os.path.join(CACHE, "runtime.json")
for _ in range(60):
    if os.path.exists(rt):
        break
    time.sleep(0.2)

with open(rt, encoding="utf-8") as f:
    snap = json.load(f)
print("  落盘运行态:", {k: snap[k] for k in ("state", "pid")},
      "hits=", snap["stats"]["hits"], "bytes_served=", snap["stats"]["bytes_served"])

# 另起 API 进程读它
import hxcp.api as api
import uvicorn
store = api.Store(CACHE)
app = api.build_app(store, "")
cfg = uvicorn.Config(app, host="127.0.0.1", port=API, log_level="error")
srv = uvicorn.Server(cfg)
threading.Thread(target=srv.run, daemon=True).start()
for _ in range(50):
    try:
        urllib.request.urlopen("http://127.0.0.1:%d/health" % API, timeout=1).read(); break
    except Exception:
        time.sleep(0.2)

def get(p_):
    with urllib.request.urlopen("http://127.0.0.1:%d%s" % (API, p_), timeout=8) as r:
        return json.loads(r.read().decode())

h = get("/health")
s = get("/stats")
ok = True
def chk(name, cond, extra=""):
    global ok
    print(("  PASS " if cond else "  FAIL ") + name + ("  " + str(extra)[:90] if extra else ""))
    ok = ok and cond

chk("health 报 engine=True (跨进程)", h.get("engine") is True, h)
chk("health engine_state=running", h.get("engine_state") == "running", h.get("engine_state"))
chk("health schema_version=1 (不再是 None)", h.get("schema_version") == 1, h.get("schema_version"))
chk("stats 读到子进程的真实计数", (s.get("engine") or {}).get("hits") == 7, s.get("engine"))
chk("stats bytes_served 正确", (s.get("engine") or {}).get("bytes_served") == 4096)

p.wait(timeout=15)
time.sleep(0.5)
h2 = get("/health")
chk("引擎退出后 engine_state 不再 running", h2.get("engine_state") != "running", h2.get("engine_state"))

print()
print("跨进程运行态:", "PASS" if ok else "FAIL")
srv.should_exit = True
time.sleep(0.4)
store.close()
sys.exit(0 if ok else 1)
