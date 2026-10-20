#!/usr/bin/env python3
"""前端端到端: 真 uvicorn + 真 HTTP 取页面与数据。

判据 (缺一不可):
  1. GET / 返回真的 HTML 页面 (不是 404, 也不是 JSON);
  2. 页面里引用的数据端点在**同一进程**上可用 (/flows /stats), 否则页面打开就是空壳;
  3. /flow/body 能按引用取回完整体 (前端"取回完整体"按钮走的就是它);
  4. 未知路径 404 (StaticFiles(html=True) 只兜目录, 不该吞掉 API 的 404 语义);
  5. 带 token 时数据端点 401, 但页面本身仍 200 —— 浏览器发不出 Bearer,
     若连 HTML 都要 token, 用户只会看到白屏且不知道原因。
"""
import json, os, shutil, sys, threading, time, urllib.error, urllib.request

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

from hxcp.store import Store
from hxcp import flows
import hxcp.api as api
import uvicorn

CACHE = "/tmp/hxcp-web-e2e"
PORT = 43570
TOKEN = "web-token"
BODY = json.dumps({"user": "alice", "sig": "deadbeef" * 400})   # ~5.4KB, 会超过 8K? 不, 单独构造
BIG = "x" * 30000


def seed():
    shutil.rmtree(CACHE, ignore_errors=True)
    store = Store(CACHE)
    store.put("https://cdn.test/app.js", "GET", 200,
              [["content-type", "text/javascript"]], b"console.log('hi')", ttl=3600)
    store.close()

    fs = flows.ForensicStore(flows.forensic_path(CACHE), enabled=True)
    fl = flows.FlowLog(os.path.join(CACHE, "flows"), enabled=True, forensic=fs)
    now = time.time()
    fl.write({"kind": "flow", "verdict": "skip:no_store", "url": "https://api.test/v1/login",
              "status": 200, "method": "POST", "post_data": BIG,
              "req_headers": {"cookie": "s=abc", "authorization": "Bearer zz"},
              "resp_headers": {"set-cookie": "sid=1"}, "at": now - 5})
    fl.write({"kind": "hit", "verdict": "hit", "url": "https://cdn.test/app.js",
              "status": 200, "method": "GET", "size": 1234, "at": now - 3})
    fl.write({"kind": "flow", "verdict": "skip:sensitive_path", "url": "https://api.test/v1/orders",
              "status": 404, "method": "GET", "at": now - 1})
    fl.close()
    return fs


def get(path, token=TOKEN, want_json=True):
    req = urllib.request.Request("http://127.0.0.1:%d%s" % (PORT, path))
    if token:
        req.add_header("authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            raw = r.read().decode("utf-8", "replace")
            return r.status, r.headers.get("content-type", ""), (json.loads(raw) if want_json else raw)
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", "replace")
        try:
            return e.code, e.headers.get("content-type", ""), json.loads(raw)
        except Exception:
            return e.code, e.headers.get("content-type", ""), raw


def main():
    fs = seed()
    store = Store(CACHE)
    app = api.build_app(store, TOKEN)
    cfg = uvicorn.Config(app, host="127.0.0.1", port=PORT, log_level="error")
    srv = uvicorn.Server(cfg)
    threading.Thread(target=srv.run, daemon=True).start()
    for _ in range(60):
        try:
            urllib.request.urlopen("http://127.0.0.1:%d/health" % PORT, timeout=1).read()
            break
        except Exception:
            time.sleep(0.2)

    ok = True
    def chk(name, cond, extra=""):
        nonlocal ok
        print(("  PASS " if cond else "  FAIL ") + name + ("  " + str(extra)[:110] if extra else ""))
        ok = ok and cond

    # 1. 页面
    st, ct, body = get("/", want_json=False)
    chk("GET / 返回 HTML 页面", st == 200 and "text/html" in ct, "%s %s" % (st, ct))
    chk("页面是自己那份前端 (含抓包流标记)",
        isinstance(body, str) and "HX-Capture-Proxy" in body and "/flows?" in body,
        "长度=%d" % (len(body) if isinstance(body, str) else -1))
    chk("页面引用的 API 路径都带挂载前缀处理", "basePrefix" in body)

    # 2. 页面所依赖的数据端点
    st, ct, d = get("/flows?limit=10")
    chk("GET /flows 有数据", st == 200 and d.get("count") == 3, d.get("count"))
    chk("flows 含 host 过滤所需字段", all("url" in r and "at" in r for r in d["flows"]))
    st, ct, d = get("/flows?host=api.test")
    chk("host 过滤生效", d.get("count") == 2, d.get("count"))
    st, ct, d = get("/flows?status=404")
    chk("状态码过滤生效", d.get("count") == 1, d.get("count"))
    st, ct, d = get("/flows?q=sensitive")
    chk("关键字过滤生效", d.get("count") == 1, d.get("count"))
    st, ct, d = get("/flows?limit=1&offset=1")
    chk("分页 offset 生效 (前端「加载更多」走它)", d.get("count") == 1 and d.get("offset") == 1)
    st, ct, d = get("/stats")
    chk("GET /stats 带 flow 与取证池段", st == 200 and "flows" in d and "forensic" in (d.get("flows") or {}))

    # 3. 完整体取回 (页面上的"取回完整体")
    st, ct, rows = get("/flows?q=no_store")
    ref = rows["flows"][0].get("body_ref") or {}
    chk("记录带完整体引用", bool(ref.get("sha256")), ref)
    st, ct, d = get("/flow/body?sha256=" + ref.get("sha256", "") + "&limit=131072")
    chk("GET /flow/body 取回完整体", st == 200 and d.get("text") == BIG, "size=%s" % d.get("size"))
    chk("完整体标记 eof", d.get("eof") is True)
    st, ct, d = get("/flow/body?sha256=" + ref.get("sha256", "") + "&offset=29990&limit=100")
    chk("分段续读正确 (offset/limit)", d.get("text") == BIG[29990:30090], d.get("returned"))
    st, ct, d = get("/flow/body?sha256=deadbeef")
    chk("非法/不存在的引用 404", st == 404, st)

    # 4. 未知路径
    st, ct, d = get("/definitely-not-a-route")
    chk("未知路径 404 (静态兜底没有吞掉 API 语义)", st == 404, st)

    # 5. token 语义
    st, ct, body2 = get("/", token="", want_json=False)
    chk("有 token 时页面仍可打开 (否则用户只看到白屏)", st == 200)
    st, ct, d = get("/flows?limit=1", token="")
    chk("无 token 时数据端点 401", st == 401, st)

    print()
    print("前端 E2E:", "PASS" if ok else "FAIL")
    srv.should_exit = True
    time.sleep(0.5)
    store.close()
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
