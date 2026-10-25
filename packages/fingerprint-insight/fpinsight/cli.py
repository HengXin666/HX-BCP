"""fp-insight CLI。

两种用法:
- fp-insight serve            常驻服务, 面板在 http://127.0.0.1:28360/
- fp-insight run --url URL    一条命令跑完: 起服务 -> 开会话 -> 导航 -> 观测 -> 出画像 (无需常驻)

run 模式在**同一进程内**同时跑 uvicorn 与 CDP 会话: 探针要连回 WS 端口才能回传,
所以"不启服务"这条路走不通; 但用户不必自己管理两个进程。
"""
from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import os
import sys
import time
from pathlib import Path

BARS = "▁▂▃▄▅▆▇█"


def _fmt_ms(v: float | None) -> str:
    if v is None:
        return "-"
    return f"{v:.2f}ms"


def _bar(n: int, mx: int, width: int = 18) -> str:
    if mx <= 0:
        return ""
    k = max(1, round(n / mx * width))
    return "█" * k


async def _serve(args: argparse.Namespace) -> int:
    import uvicorn

    os.environ["FPI_PORT"] = str(args.port)
    from .app import _APP

    cfg = uvicorn.Config(_APP, host=args.host, port=args.port, log_level="warning")
    await uvicorn.Server(cfg).serve()
    return 0


def _free_port(preferred: int) -> int:
    """run 模式要自己起一个内嵌服务, 若首选端口被常驻 serve 占着就必须换一个,
    否则一条本来只读的命令会以"地址已被占用"崩掉 (实测踩过)。"""
    import socket

    for p in [preferred] + list(range(preferred + 1, preferred + 24)) + [0]:
        s = socket.socket()
        try:
            s.bind(("127.0.0.1", p))
            return s.getsockname()[1]
        except OSError:
            continue
        finally:
            s.close()
    return 0


async def _run(args: argparse.Namespace) -> int:
    import uvicorn

    args.port = _free_port(args.port)
    os.environ["FPI_PORT"] = str(args.port)
    from .app import HUB, SESSIONS, _APP
    from .session import FpSession, LaunchConfig

    log = (lambda *a: None) if args.quiet else (lambda *a: print(*a, file=sys.stderr))
    cfg = uvicorn.Config(_APP, host="127.0.0.1", port=args.port, log_level="error")
    server = uvicorn.Server(cfg)
    task = asyncio.create_task(server.serve())
    for _ in range(200):
        if getattr(server, "started", False):
            break
        await asyncio.sleep(0.05)
    if not getattr(server, "started", False):
        print("服务未能启动 (端口占用?)", file=sys.stderr)
        return 2

    sid = args.sid or ("s" + format(int(time.time() * 1000) % 0xFFFFFFF, "x"))
    workdir = Path(__import__("fpinsight.app", fromlist=["DATA"]).DATA) / "sessions" / sid
    workdir.mkdir(parents=True, exist_ok=True)
    screen = None
    if args.screen:
        a, b = args.screen.lower().split("x")
        screen = (int(a), int(b))
    lc = LaunchConfig(headless=not args.headed, proxy=args.proxy, width=args.width, height=args.height,
                      screen=screen, dsf=args.dsf, timezone=args.tz, locale=args.locale,
                      user_agent=args.ua, keep_profile=args.keep_profile)
    s = FpSession(sid, HUB, workdir, lc, label=args.label or "", target=args.url, mode="cdp")
    log(f"[fp-insight] 启动 Chromium (headless={not args.headed}) …")
    await s.start()
    SESSIONS[sid] = s
    exe = lc.chromium or "auto"
    log(f"[fp-insight] 会话 {sid} 就绪, 导航 -> {args.url}")
    await s.navigate(args.url, args.wait_ms)
    await asyncio.sleep(1.0)
    dg = await s.diag()
    if not dg:
        log("[fp-insight] 注入自检: 失败 —— 页面里找不到探针, 本次结果不可信")
    else:
        log(f"[fp-insight] 注入自检: 钩子 {dg.get('hooked')} 个 (失败 {dg.get('hookfail')}) · "
            f"已记录 {dg.get('total')} 次调用 · 回传通道 {'就绪' if dg.get('ws') == 1 else '未就绪'}")
    await asyncio.sleep(max(0.0, args.seconds - args.wait_ms / 1000 - 1.0))
    if args.shot:
        p = await s.save_shot()
        log(f"[fp-insight] 截图 {p}")
    if args.dom:
        d = await s.dom(200)
        dp = workdir / "dom.json"
        dp.write_text(json.dumps(d, ensure_ascii=False, indent=2), encoding="utf-8")
        log(f"[fp-insight] DOM 快照 {dp} ({len(d['elements'])} 个可交互节点)")
    await s.commit_counts()
    await s.mark("final", "run 结束")
    # 证据可见性边界: 探针是异步回传的, 不等落盘就查库会读到空快照。
    if not HUB.drain(8.0):
        log("[fp-insight] 警告: 事件队列未在 8s 内排空, 画像可能不完整")
    report = _build(sid)
    if args.out:
        Path(args.out).write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        log(f"[fp-insight] 画像已写入 {args.out}")
    if not args.quiet:
        _print_report(report, top=args.top)
    await s.stop()
    SESSIONS.pop(sid, None)
    server.should_exit = True
    with contextlib.suppress(Exception):
        await asyncio.wait_for(task, timeout=8)
    return 0


def _q(sql: str, args: tuple = ()) -> list[dict]:
    r"""只读查询 —— **自建连接, 不依赖 Hub 已启动**。

    Bug (2026-09-22 实测): 原实现转调 app._q, 而它读 HUB._init_con —— 那个属性
    只在 HUB.start() 之后才存在 (Hub.start -> _conn_init 里赋值)。
    于是 fp-insight sessions / fp-insight report 在**服务没跑**时必然
    AttributeError: 'Hub' object has no attribute '_init_con'。
    修法: CLI 直接开自己的 sqlite 只读连接 (uri mode=ro),
          既不依赖 Hub 生命周期, 也不会写入。"""
    import sqlite3
    from pathlib import Path
    from .app import DATA
    db = Path(DATA) / 'fp-insight.db'
    if not db.exists():
        return []
    con = sqlite3.connect('file:' + str(db) + '?mode=ro', uri=True, timeout=10)
    con.row_factory = sqlite3.Row
    try:
        rows = [dict(r) for r in con.execute(sql, args).fetchall()]
    except Exception:
        rows = []
    finally:
        con.close()
    return rows


def _build(sid: str) -> dict:
    from .app import HUB

    total = _q("SELECT COUNT(*) AS n FROM calls WHERE sid = ?", (sid,))
    pages = _q("SELECT * FROM pages WHERE sid = ?", (sid,))
    apis = _q(
        "SELECT path, cat, surface, COUNT(*) AS events FROM calls WHERE sid = ?"
        " GROUP BY path, surface ORDER BY events DESC", (sid,))
    cnt = _q("SELECT path, surf, n FROM counters WHERE sid = ? ORDER BY n DESC", (sid,))
    cmap = {f"{r['path']}|{r['surf']}": r["n"] for r in cnt}
    for r in apis:
        r["reads"] = cmap.get(f"{r['path']}|{r['surface']}", r["events"])
    apis.sort(key=lambda r: -r["reads"])
    firsts = _q(
        "SELECT path, MIN(id) AS first_id, MAX(n) AS reads FROM calls WHERE sid = ?"
        " GROUP BY path ORDER BY first_id ASC LIMIT 40", (sid,))
    frames = _q(
        "SELECT top_url, COUNT(*) AS n FROM calls WHERE sid = ? AND top_url IS NOT NULL AND top_url != ''"
        " GROUP BY top_url ORDER BY n DESC LIMIT 30", (sid,))
    cats = _q("SELECT cat, COUNT(*) AS events FROM calls WHERE sid = ? GROUP BY cat ORDER BY events DESC",
              (sid,))
    inv = _q("SELECT * FROM invariants WHERE sid = ? ORDER BY stable DESC, n DESC", (sid,))
    sess = _q("SELECT * FROM sessions WHERE sid = ?", (sid,))
    return {
        "sid": sid,
        "session": sess[0] if sess else None,
        "page": pages[0] if pages else None,
        "total_events": total[0]["n"] if total else 0,
        "reads_total": sum(r["n"] for r in cnt),
        "apis": apis,
        "categories": cats,
        "first_reads": firsts,
        "frames": frames,
        "invariants_stable": [r for r in inv if r["stable"]],
        "invariants_varying": [r for r in inv if not r["stable"]],
        "stats": HUB.stats,
        "generated": time.time(),
    }


def _print_report(rep: dict, top: int = 40) -> None:
    page = rep.get("page") or {}
    sess = rep.get("session") or {}
    print()
    print(f"=== fp-insight 画像 · {rep['sid']} ===")
    print(f"站点      {page.get('url') or (sess.get('target') or '-')}")
    meta = sess.get("meta")
    if isinstance(meta, str):
        try:
            meta = json.loads(meta)
        except Exception:
            meta = {}
    headless = (meta or {}).get("headless")
    print(f"会话      {sess.get('mode','cdp')} · tz={page.get('tz') or '-'} · "
          f"headless={'是' if headless is True else ('否' if headless is False else '-')} · "
          f"UA={(page.get('ua') or '')[:90]}")
    print(f"规模      事件 {rep['total_events']} · 精确读取 {rep['reads_total']}")
    cats = rep.get("categories") or []
    if cats:
        print("指纹类别  " + " · ".join(f"{c['cat'] or '其他'}({c['events']})" for c in cats))
    apis = [a for a in rep.get("apis") or [] if a["reads"] > 0]
    if apis:
        mx = apis[0]["reads"]
        print()
        print("被读取的指纹面 (按精确读取次数):")
        for a in apis[:top]:
            print(f"  {a['reads']:>7}  {_bar(a['reads'], mx)}  {a['path']}")
        if len(apis) > top:
            print(f"  (其余 {len(apis) - top} 项省略)")
    fr = rep.get("frames") or []
    if fr:
        tot = sum(f["n"] for f in fr) or 1
        print()
        print("采集代码来源 (按事件占比):")
        for f in fr[:12]:
            print(f"  {f['n'] / tot * 100:5.1f}%  {f['n']:>6}  {(f['top_url'] or '')[:110]}")
    st = rep.get("invariants_stable") or []
    if st:
        print()
        print(f"变量纠察 · 运行期恒定 (反爬锚点) 共 {len(st)} 项:")
        for r in st[:30]:
            vals = r.get("vals") or "[]"
            print(f"  n={r.get('n'):<6} {r.get('path')} = {vals[:90]}")


async def _tail(args: argparse.Namespace) -> int:
    import websockets

    url = f"ws://127.0.0.1:{args.port}/ws/ui"
    async with websockets.connect(url) as ws:
        while True:
            raw = await ws.recv()
            m = json.loads(raw)
            k = m.get("kind")
            if k == "ev":
                for e in (m.get("payload") or [])[:40]:
                    print(f"{e.get('t', 0):>7}ms n={e.get('n'):<4} {e.get('path'):<48} "
                          f"{(str(e.get('args'))[:70])}  <- {((e.get('top') or {}).get('url') or '')[:70]}")
            elif k == "inv":
                print(f"[inv] {len(m.get('payload') or [])} 项变量纠察")
            elif k in ("hello", "meta"):
                print(f"[{k}] {json.dumps(m.get('payload'), ensure_ascii=False)[:200]}")
    return 0


async def _report(args: argparse.Namespace) -> int:
    rep = _build(args.sid)
    if args.out:
        Path(args.out).write_text(json.dumps(rep, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"已写入 {args.out}")
    else:
        _print_report(rep, top=args.top)
    return 0


async def _sessions(args: argparse.Namespace) -> int:
    rows = _q("SELECT sid,label,target,status,started,ended FROM sessions ORDER BY started DESC LIMIT 100")
    for r in rows:
        flag = "●" if r["status"] == "running" else "○"
        print(f"{flag} {r['sid']:<14} {r['status']:<8} {(r['target'] or '')[:70]}")
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="fp-insight", description="fp-insight 浏览器指纹探测工具")
    p.add_argument("--port", type=int, default=int(os.environ.get("FPI_PORT", "28360")))
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("serve", help="常驻服务 + 面板")
    s.add_argument("--host", default="0.0.0.0")
    s.add_argument("--port", type=int, default=int(os.environ.get("FPI_PORT", "28360")))
    s.set_defaults(fn=_serve)

    r = sub.add_parser("run", help="一条命令跑完并出画像")
    r.add_argument("--url", required=True)
    r.add_argument("--sid")
    r.add_argument("--label", default="")
    r.add_argument("--headed", action="store_true", help="显示窗口 (默认无头)")
    r.add_argument("--tz", help="时区, 如 Asia/Shanghai")
    r.add_argument("--proxy", help="代理, 如 http://127.0.0.1:7892")
    r.add_argument("--ua")
    r.add_argument("--locale")
    r.add_argument("--width", type=int, default=1440)
    r.add_argument("--height", type=int, default=900)
    r.add_argument("--screen", help="WxH, 默认同窗口")
    r.add_argument("--dsf", type=float, default=1.0)
    r.add_argument("--seconds", type=float, default=12.0, help="总观测时长")
    r.add_argument("--wait-ms", type=int, default=3000, help="导航后等待")
    r.add_argument("--shot", action="store_true", help="结束时截图")
    r.add_argument("--dom", action="store_true", help="导出 DOM 快照")
    r.add_argument("--out", help="画像 JSON 输出路径")
    r.add_argument("--top", type=int, default=40)
    r.add_argument("--keep-profile", action="store_true")
    r.add_argument("--quiet", action="store_true")
    r.add_argument("--port", type=int, default=int(os.environ.get("FPI_PORT", "28360")))
    r.set_defaults(fn=_run)

    rp = sub.add_parser("report", help="重建某会话的技术画像")
    rp.add_argument("--sid", required=True)
    rp.add_argument("--out")
    rp.add_argument("--top", type=int, default=40)
    rp.set_defaults(fn=_report)

    t = sub.add_parser("tail", help="实时打印探针事件")
    t.add_argument("--port", type=int, default=int(os.environ.get("FPI_PORT", "28360")))
    t.set_defaults(fn=_tail)

    ls = sub.add_parser("sessions", help="列出历史会话")
    ls.set_defaults(fn=_sessions)
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        return asyncio.run(args.fn(args))
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
