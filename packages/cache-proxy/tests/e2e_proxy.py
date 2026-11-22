"""Real mitmproxy + real HTTP origin: two clients share one cache, flows tagged per client, API serves access."""
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(HERE, "..", "src")
NOPROXY = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def via(proxy_port: int, url: str) -> tuple[bytes, str]:
    op = urllib.request.build_opener(urllib.request.ProxyHandler({"http": "http://127.0.0.1:%d" % proxy_port}))
    with op.open(url, timeout=10) as r:
        return r.read(), r.headers.get("x-hx-cache") or ""


def main() -> int:
    tmp = tempfile.mkdtemp(prefix="hxcp-e2e-proxy-")
    www = os.path.join(tmp, "www")
    os.makedirs(www)
    with open(os.path.join(www, "app.js"), "w") as f:
        f.write("console.log(1)\n")
    env = dict(os.environ, PYTHONPATH=SRC)
    for k in list(env):
        if "proxy" in k.lower():
            env.pop(k)
    cache = os.path.join(tmp, "cache")
    procs = [
        subprocess.Popen([sys.executable, "-m", "http.server", "18180"], cwd=www,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL),
        subprocess.Popen([sys.executable, "-m", "hxcp.cli", "--cache-dir", cache, "proxy", "--port", "18199",
                          "--client", "envA=18201", "--client", "envB=18202"], env=env,
                         stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT),
        subprocess.Popen([sys.executable, "-m", "hxcp.api", "--cache-dir", cache, "--port", "18177"], env=env,
                         stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT),
    ]
    try:
        time.sleep(5)
        url = "http://127.0.0.1:18180/app.js"
        _, first = via(18201, url)
        body, second = via(18202, url)
        assert first == "" and second == "HIT", (first, second)
        assert body == b"console.log(1)\n"
        time.sleep(3)
        get = lambda p: json.loads(NOPROXY.open("http://127.0.0.1:18177" + p, timeout=10).read())  # noqa: E731
        flows = get("/flows?client=envB")["flows"]
        assert flows and flows[0]["kind"] == "hit" and flows[0]["via"] == "proxy", flows[:1]
        acc = get("/access")
        assert acc["running"] and {ln["name"] for ln in acc["listeners"]} >= {"envA", "envB"}
        assert get("/stats")["engine"]["hits"] >= 1
        print("e2e_proxy ok")
        return 0
    finally:
        for p in procs:
            p.terminate()


if __name__ == "__main__":
    sys.exit(main())
