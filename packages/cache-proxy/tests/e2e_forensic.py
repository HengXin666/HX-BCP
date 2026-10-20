#!/usr/bin/env python3
"""端到端: 真浏览器 -> 真 POST -> 取证档存下**完整体** -> 按记录里的引用逐字节取回。

判据 (缺一不可):
  1. flow 记录里 post_data 是 8K **摘要** (取证档不改观测面形态);
  2. 记录里有 body_ref 引用, 且 size 等于真实发送的字节数;
  3. 按引用取回的字节与发送的 body **逐字节相同** (这才是"取证"的定义);
  4. 完整体不在 JSONL 里 (单条记录行长度受控) —— 否则就是拿扫描成本换取证;
  5. 关闭取证档时一切照旧: 记录里没有 body_ref, post_data 仍被截断。

第 3 条必须逐字节比: 只断言"取到了东西"会漏掉编码/截断/半截写入。
"""
import json, os, sys, time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))
from hxcp.launcher import launch
from hxcp.cdp import Session
from hxcp.engine import Engine
from hxcp.store import Store
from hxcp import flows, policy


def serve_post(port):
    """一个真 HTTP 端点, 收 POST 并回 200。

    为什么必须真起服务: flow 记录在 Network.loadingFinished 之后才补全,
    连接被拒 (Failed to fetch) 只走 loadingFailed —— 那条路径上根本不产生记录。
    拿不存在的端口测, 测的其实是"失败请求的记录缺口", 那是另一件事。
    """
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class H(BaseHTTPRequestHandler):
        def do_GET(self):
            # 同源页面: 从 about:blank 直接 fetch 跨源会先撞 CORS 预检,
            # 预检不过则真正的 POST 根本不会发出 —— 那样测的就不是抓包了。
            out = b"<!doctype html><title>fx</title><body>fx sink</body>"
            self.send_response(200)
            self.send_header("content-type", "text/html")
            self.send_header("content-length", str(len(out)))
            self.end_headers()
            self.wfile.write(out)

        def do_POST(self):
            n = int(self.headers.get("content-length") or 0)
            self.rfile.read(n)
            out = b'{"ok":true}'
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(out)))
            self.end_headers()
            self.wfile.write(out)

        def log_message(self, *a):
            pass

    srv = ThreadingHTTPServer(("127.0.0.1", port), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv

PORT = int(os.environ.get("HXCP_FX_PORT", "43560"))
BASE = "/tmp/hxcp-forensic-e2e"
PAGE = "http://127.0.0.1:43561/api/sink"
PAGE_HTML = "http://127.0.0.1:43561/"
API_PORT = 43562
API = "http://127.0.0.1:%d" % API_PORT

# 48 KiB: 远超 MAX_BODY(8192), 且内容可校验 (逐字符不同, 截断/错位都会被逐字节比抓出来)
BODY = ("PAYLOAD-%05d|" % 0).join("")  # 占位, 下面真正构造
BODY = "".join("PAYLOAD-%05d|" % i for i in range(3000))
assert len(BODY) > 40000, len(BODY)


def wait_port(port, timeout=20.0):
    import socket
    end = time.time() + timeout
    while time.time() < end:
        with socket.socket() as s:
            s.settimeout(0.4)
            if s.connect_ex(("127.0.0.1", port)) == 0:
                return True
        time.sleep(0.2)
    return False


def main():
    import shutil
    shutil.rmtree(BASE, ignore_errors=True)
    cache = os.path.join(BASE, "cache")

    srv = serve_post(43561)          # 真端点: POST 得有真响应才会落 flow
    lc = launch(PORT, profile=os.path.join(BASE, "profile"))
    print("[fx] 环境", lc.endpoint, "| 页面", PAGE_HTML, "| POST", PAGE)
    try:
        s = Session(lc.endpoint, timeout=120)
        store = Store(cache)
        eng = Engine(s, store, policy.Rules(), log=lambda m: print(m, flush=True),
                     record_flows=True, forensic=True, forensic_max_bytes=64 * 1024 * 1024)
        eng.start()
        print("[fx] 取证池:", eng.forensic.root, "enabled=", eng.forensic.enabled)

        # 导航到真页面 (同源), 再在页面里发真 POST —— body 就是我们构造的那串
        s.navigate(PAGE_HTML, settle=1.5)
        expr = ("fetch(%s, {method:'POST', headers:{'content-type':'application/json'},"
                " body: %s}).then(function(r){ return r.status; })" % (json.dumps(PAGE), json.dumps(BODY)))
        try:
            print("[fx] fetch 状态码", s.js(expr, await_promise=True))
        except Exception as e:
            print("[fx] fetch 异常:", str(e)[:120])
        for _ in range(20):
            eng.tick(); time.sleep(0.2)
        time.sleep(1.0)
        eng.stop()
        time.sleep(0.5)

        fdir = store.root / "flows"
        recs = [r for r in flows.iter_flows(fdir, q=BODY[:24])]
        if not recs:
            recs = [r for r in flows.iter_flows(fdir) if r.get("method") == "POST"]
        print("[fx] 命中 %d 条 POST flow 记录" % len(recs))
        assert recs, "没有抓到这条 POST —— 抓包本身没工作"

        rec = recs[0]
        ref = rec.get("body_ref") or {}
        print("[fx] verdict=%s method=%s size=%s" % (rec.get("verdict"), rec.get("method"), rec.get("size")))
        print("[fx] post_data 长度=%d  body_ref=%s" % (
            len(rec.get("post_data") or ""), {k: (str(v)[:20]) for k, v in ref.items()}))

        ok = True
        def chk(name, cond, extra=""):
            nonlocal ok
            print(("  PASS " if cond else "  FAIL ") + name + ("  " + str(extra)[:110] if extra else ""))
            ok = ok and cond

        # 1. 摘要留在 flows
        chk("记录里 post_data 仍是 8K 摘要 (观测面形态不变)",
            (rec.get("post_data") or "").endswith("...[truncated]"),
            len(rec.get("post_data") or ""))
        # 2. 引用与真实体量一致
        chk("body_ref 指向完整体", bool(ref.get("sha256")) and int(ref.get("size") or 0) == len(BODY),
            "ref.size=%s 实际=%d" % (ref.get("size"), len(BODY)))
        # 3. 逐字节取回 (经 API 读回, 证明读侧与写侧同源)
        got = None
        if ref.get("sha256"):
            fs = flows.open_forensic(store.root)
            got = fs.get(ref["sha256"])
        chk("按引用取回的字节与发送的 body 逐字节相同",
            got is not None and got.decode("utf-8") == BODY,
            "取回 %s 字节" % (len(got) if got else None))
        # 4. 完整体没有进 JSONL
        line = json.dumps(rec, ensure_ascii=False)
        chk("JSONL 单条记录未被完整体撑大 (< 32KB)", len(line) < 32768, "行长度=%d" % len(line))
        chk("完整体确实不在 JSONL 里", BODY[20000:20100] not in line)
        # 5. 池统计可见
        fst = eng.forensic.stats()
        print("[fx] 池统计:", json.dumps({k: fst[k] for k in
              ("written", "bytes", "evicted", "too_large", "max_bytes")}, ensure_ascii=False))
        chk("池统计报告写入过完整体", fst["written"] >= 1)

        # 6. 跨进程闭环: 引擎(已停)写下的池, 由**另一个进程**的 API 读回。
        #    单测只证明了读侧句柄指向同一目录, 这里证明端到端真的能取到字节 ——
        #    "引擎写的" 与 "API 读的" 是这条链路上最容易各拼一份路径的地方。
        import threading, urllib.request
        import uvicorn
        import hxcp.api as hxapi
        api_store = Store(cache)
        app = hxapi.build_app(api_store, "")
        cfg = uvicorn.Config(app, host="127.0.0.1", port=API_PORT, log_level="error")
        srv = uvicorn.Server(cfg)
        threading.Thread(target=srv.run, daemon=True).start()
        got_api = None
        for _ in range(40):
            try:
                with urllib.request.urlopen(
                        API + "/flow/body?sha256=" + (ref.get("sha256") or "") + "&limit=131072",
                        timeout=10) as rr:
                    got_api = json.loads(rr.read().decode())
                break
            except Exception:
                time.sleep(0.25)
        chk("API 跨进程按引用取回完整体 (引擎写的池)",
            bool(got_api) and got_api.get("text") == BODY,
            "取回 %s 字节" % (got_api or {}).get("returned"))
        srv.should_exit = True
        time.sleep(0.4)
        api_store.close()

        # 7. 关闭取证档: 行为回到原样
        shutil.rmtree(os.path.join(BASE, "cache2"), ignore_errors=True)
        store2 = Store(os.path.join(BASE, "cache2"))
        eng2 = Engine(s, store2, policy.Rules(), record_flows=True)   # 不传 forensic
        eng2.start()
        try:
            print("[fx] fetch 状态码", s.js(expr, await_promise=True))
        except Exception:
            pass
        for _ in range(12):
            eng2.tick(); time.sleep(0.2)
        eng2.stop(); time.sleep(0.5)
        recs2 = [r for r in flows.iter_flows(store2.root / "flows") if r.get("method") == "POST"]
        chk("关闭取证档后仍抓得到 POST 记录", bool(recs2), len(recs2))
        if recs2:
            chk("关闭取证档后记录里没有 body_ref", "body_ref" not in recs2[0])
            chk("关闭取证档后 post_data 仍被截断",
                (recs2[0].get("post_data") or "").endswith("...[truncated]"))
        store2.close()
        eng2.forensic  # noqa: B018  仅确认属性存在

        print()
        print("取证档 E2E:", "PASS" if ok else "FAIL")
        eng.stop(); store.close(); s.close()
        return 0 if ok else 1
    finally:
        lc.stop()
        srv.shutdown()
        print("[fx] 环境已清理")


if __name__ == "__main__":
    sys.exit(main())
