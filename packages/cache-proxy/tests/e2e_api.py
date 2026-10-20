#!/usr/bin/env python3
"""API 端到端: 起真服务 -> 真 HTTP 请求 -> 断言响应。

为什么必须起真服务而不是用 TestClient 打桩:
  TestClient 兼容真实 socket/路由/序列化, 本仓库的铁律是"测试须走真实端到端路径"。
  这里用 uvicorn 真起在 loopback 上, 再用 urllib 真发请求。
"""
import json, os, sys, threading, time, urllib.request, urllib.error
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

from hxcp.store import Store
import hxcp.api as api
import uvicorn

CACHE = "/tmp/hxcp-api-e2e"
PORT = 28811
TOKEN = "smoke-token"

def get(path, token=TOKEN):
    req = urllib.request.Request("http://127.0.0.1:%d%s" % (PORT, path))
    if token:
        req.add_header("authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, {"detail": e.read().decode()[:120]}

def main():
    import shutil
    shutil.rmtree(CACHE, ignore_errors=True)
    store = Store(CACHE)
    store.put("https://cdn.test/app.js", "GET", 200,
              [["content-type", "text/javascript"]], b"console.log('hello-hxcp')",
              ttl=3600, etag='"v1"')
    store.put("https://cdn.test/style.css", "GET", 200,
              [["content-type", "text/css"]], b"body{margin:0}", ttl=3600)
    for _ in range(3):
        store.record_hit(store.lookup("https://cdn.test/app.js").key)

    app = api.build_app(store, TOKEN)
    cfg = uvicorn.Config(app, host="127.0.0.1", port=PORT, log_level="error")
    srv = uvicorn.Server(cfg)
    th = threading.Thread(target=srv.run, daemon=True)
    th.start()
    for _ in range(50):
        try:
            urllib.request.urlopen("http://127.0.0.1:%d/health" % PORT, timeout=1).read(); break
        except Exception:
            time.sleep(0.2)

    ok = True
    def chk(name, cond, extra=""):
        nonlocal ok
        print(("  PASS " if cond else "  FAIL ") + name + ("  " + str(extra)[:100] if extra else ""))
        ok = ok and cond

    st, body = get("/health")
    chk("GET /health 200", st == 200 and body.get("ok") is True, body)

    st, body = get("/stats")
    chk("GET /stats 200", st == 200, st)
    chk("stats 反映条目数", body.get("store", {}).get("entries") == 2, body.get("store"))

    st, body = get("/entries?limit=10")
    chk("GET /entries 200", st == 200, st)
    chk("entries 按命中排序 (app.js 在前)",
        bool(body.get("entries")) and "app.js" in body["entries"][0]["url"], body.get("count"))

    st, body = get("/lookup?url=https://cdn.test/app.js")
    chk("GET /lookup 命中", body.get("hit") is True and body.get("hit_count") == 3, body)

    st, body = get("/body?url=https://cdn.test/app.js")
    chk("GET /body 取回正文", st == 200 and "hello-hxcp" in body.get("preview", ""), body.get("size"))

    st, body = get("/lookup?url=https://cdn.test/nope.js")
    chk("GET /lookup 未命中返回 hit=false", body.get("hit") is False, body)

    st, body = get("/stats", token="")
    chk("无 token 被拒 401", st == 401, st)
    st, body = get("/stats", token="wrong")
    chk("错 token 被拒 401", st == 401, st)

    st, body = get("/body?url=https://cdn.test/nope.js")
    chk("GET /body 不存在返回 404", st == 404, st)

    print()
    print("API E2E:", "PASS" if ok else "FAIL")
    srv.should_exit = True
    time.sleep(0.5)
    store.close()
    return 0 if ok else 1

if __name__ == "__main__":
    sys.exit(main())
