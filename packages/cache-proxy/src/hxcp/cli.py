"""HX-Capture-Proxy 命令行入口。

一份 core, 三个入口: 本文件 (CLI) / api.py (HTTP) / mcp_server.py (MCP)。
三个入口都只调用 engine+store+cdp, 不各自实现逻辑 —— 否则三份判定必然不同步
(本仓库栽过: `cached()` 与 `verdict_of()` 的判据不一致, 闸门据此跳过探测判死整轮)。
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time

from .attach import open_fleet
from .launcher import launch, port_open
from . import policy
from .store import Store


# 取证档旗标集中在**一处**: 两个子命令 (run/launch) 必须给出同一组语义,
# 分别声明一次就是两处判据, 迟早不同步。
def add_forensic_args(p: argparse.ArgumentParser) -> None:
    p.add_argument("--forensic", action="store_true",
                   help="取证档: 请求体整份落 <cache-dir>/flows/payloads/ (引用写进 flow);"
                        " 需同时开 --record-flows")
    p.add_argument("--forensic-max-bytes", type=int, default=0,
                   help="取证池预算上限 (0=默认 1GiB); 超出按 mtime 从旧到新轮转删除")
    p.add_argument("--forensic-free-mb", type=int, default=0,
                   help="磁盘余量水位 (0=默认 512MiB); 低于它就继续删")


def _rules_from_args(a) -> policy.Rules:
    return policy.Rules(
        allow_hosts=frozenset(x.strip() for x in (a.allow_host or "").split(",") if x.strip()),
        deny_hosts=frozenset(x.strip() for x in (a.deny_host or "").split(",") if x.strip()),
        block_images=bool(a.block_images),
        max_bytes=int(a.max_bytes),
    )


def cmd_run(a) -> int:
    """对**已存在**的 CDP 环境挂载引擎 (最常用: 接管一个正在跑的浏览器)。"""
    store = Store(a.cache_dir, max_entries_bytes=int(a.max_bytes))
    if store.rebuilt:
        print("[hxcp] 索引版本不符, 已丢弃重建 (派生数据可全量重建)")
    if a.seed_dir and os.path.isdir(a.seed_dir):
        n = import_seed(store, a.seed_dir)
        print("[hxcp] 导入种子 " + str(n) + " 条 <- " + a.seed_dir)

    fleet = None
    try:
        fleet = open_fleet(
            a.endpoint, store, _rules_from_args(a),
            log=lambda m: print(m, flush=True),
            record_flows=bool(a.record_flows or a.verbose),
            flow_max_body=int(a.flow_max_body or 0),
            forensic=bool(a.forensic),
            forensic_max_bytes=int(a.forensic_max_bytes or 0),
            forensic_free_bytes=int(a.forensic_free_mb or 0) * 1024 * 1024,
            on_flow=(lambda f: print("[flow] " + json.dumps(f, ensure_ascii=False))
                     if a.verbose else None),
        )
        print("[hxcp] 已挂载 " + a.endpoint + "  cache=" + str(store.root), flush=True)
        while True:
            time.sleep(a.duration if a.duration else 3600)
            print("[hxcp] " + json.dumps(fleet.stats(), ensure_ascii=False))
            if a.duration:
                break
    except KeyboardInterrupt:
        pass
    finally:
        if fleet is not None:
            fleet.stop()
            print("[hxcp] 结束: " + json.dumps(fleet.stats(), ensure_ascii=False))
        print("[hxcp] 库: " + json.dumps(store.stats(), ensure_ascii=False))
        store.close()
    return 0


def cmd_launch(a) -> int:
    """起一个环境并挂引擎, 跑完自动收摊 (用后即清)。"""
    lc = launch(a.port, profile=a.profile, proxy=a.proxy, headless=not a.headed,
                extra=list(a.extra or []), keep_profile=a.keep_profile,
                keylog=a.keylog)
    print("[hxcp] 环境 " + lc.endpoint + " profile=" + lc.profile)
    try:
        a.endpoint = lc.endpoint
        return cmd_run(a)
    finally:
        lc.stop()
        print("[hxcp] 环境已停止并清理")
    return 0


def cmd_cache(a) -> int:
    store = Store(a.cache_dir, max_entries_bytes=int(a.max_bytes))
    if a.action == "stats":
        print(json.dumps(store.stats(), ensure_ascii=False, indent=2))
    elif a.action == "list":
        rows = store._conn.execute(
            "SELECT url,size,hit_count,source,stored_at,fresh_until FROM entries "
            "ORDER BY hit_count DESC, size DESC LIMIT ?", (int(a.limit),)).fetchall()
        print(json.dumps([dict(r) for r in rows], ensure_ascii=False, indent=2))
    elif a.action == "lookup":
        e = store.lookup(a.url)
        print(json.dumps(e.__dict__ if e else None, ensure_ascii=False, indent=2))
    elif a.action == "purge":
        print("purg 过期 " + str(store.purge_expired()) + " 条")
    elif a.action == "export-seed":
        print("导出 " + str(export_seed(store, a.out or "./cache_seed")) + " 条")
    elif a.action == "import-seed":
        print("导入 " + str(import_seed(store, a.out or "./cache_seed")) + " 条")
    elif a.action == "flows":
        from . import flows as flows_mod
        rows = list(flows_mod.iter_flows(
            store.root / "flows", host=a.host, status=(a.status or None),
            q=a.q, kind=a.kind, limit=int(a.limit), days=(a.days or None)))
        print(json.dumps(rows, ensure_ascii=False, indent=2))
        print("共 " + str(len(rows)) + " 条 (root=" + str(store.root / "flows") + ")")
    elif a.action == "flow-stats":
        from . import flows as flows_mod
        print(json.dumps(flows_mod.flow_stats(store.root / "flows"),
                         ensure_ascii=False, indent=2))
    elif a.action == "flow-purge":
        from . import flows as flows_mod
        print("删除 " + str(flows_mod.purge(store.root / "flows", a.days or 7))
              + " 个 flow 文件 (保留 " + str(a.days or 7) + " 天)")
    store.close()
    return 0


# ── 种子: 让新环境冷启动就命中 ────────────────────────────
def export_seed(store: Store, out_dir: str) -> int:
    """导出为 manifest.jsonl + blobs/ (内容寻址, 天然去重)。"""
    import shutil
    out = os.path.abspath(out_dir)
    bdir = os.path.join(out, "blobs")
    os.makedirs(bdir, exist_ok=True)
    n = 0
    with open(os.path.join(out, "manifest.jsonl"), "w", encoding="utf-8") as f:
        for r in store._conn.execute("SELECT * FROM entries"):
            src = store._blob_path(r["body_hash"])
            if not src.exists():
                continue
            dst = os.path.join(bdir, r["body_hash"] + ".bin")
            if not os.path.exists(dst):
                shutil.copy2(src, dst)
            f.write(json.dumps({
                "url": r["url"], "method": r["method"], "status": r["status"],
                "headers": json.loads(r["headers"]), "body_hash": r["body_hash"],
                "size": r["size"], "etag": r["etag"],
            }, ensure_ascii=False) + "\n")
            n += 1
    return n


def import_seed(store: Store, seed_dir: str) -> int:
    """从种子导入。blob 按 hash 存放, 缺失的条目跳过 (种子是外部输入, 必须容错)。"""
    import shutil
    man = os.path.join(seed_dir, "manifest.jsonl")
    if not os.path.exists(man):
        return 0
    n = 0
    with open(man, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                m = json.loads(line)
                src = os.path.join(seed_dir, "blobs", m["body_hash"] + ".bin")
                if not os.path.exists(src):
                    continue
                if not store.get_blob(m["body_hash"]):
                    dst = store._blob_path(m["body_hash"])
                    dst.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(src, dst)
                store.put(m["url"], m.get("method", "GET"), int(m.get("status", 200)),
                          m.get("headers", []), open(src, "rb").read(),
                          ttl=float(m.get("ttl", 365 * 86400)),
                          etag=m.get("etag", ""), source="seed", tags=["seed"])
                n += 1
            except Exception:
                continue
    return n


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="hxcp", description="HX-Capture-Proxy")
    ap.add_argument("--cache-dir", default=os.path.expanduser("~/loli_data/hxcp"))
    ap.add_argument("--max-bytes", type=int, default=8 * 1024 * 1024)
    ap.add_argument("--seed-dir", default="")
    ap.add_argument("--allow-host", default="", help="逗号分隔; 非空则只处理这些 host 后缀")
    ap.add_argument("--deny-host", default="")
    ap.add_argument("--block-images", action="store_true")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("run", help="对已有 CDP 环境挂引擎")
    p.add_argument("--endpoint", default="http://127.0.0.1:9333")
    p.add_argument("--duration", type=float, default=0, help="跑多少秒后自动收尾 (0=一直跑)")
    p.add_argument("--verbose", action="store_true")
    p.add_argument("--record-flows", action="store_true",
                   help="把请求级 flow 落盘到 <cache-dir>/flows/ (默认只打印不落盘)")
    p.add_argument("--flow-max-body", type=int, default=0, help="flow 里 body 字段上限, 0=默认 8192")
    add_forensic_args(p)
    p.set_defaults(fn=cmd_run)

    p = sub.add_parser("launch", help="起环境+挂引擎, 跑完自动清理")
    p.add_argument("--port", type=int, default=9333)
    p.add_argument("--profile", default="")
    p.add_argument("--proxy", default="")
    p.add_argument("--headed", action="store_true")
    p.add_argument("--keylog", default="", help="--ssl-key-log-file 路径 (抓包解密用)")
    p.add_argument("--keep-profile", action="store_true")
    p.add_argument("--extra", action="append", default=[])
    p.add_argument("--duration", type=float, default=0)
    p.add_argument("--verbose", action="store_true")
    p.add_argument("--record-flows", action="store_true")
    p.add_argument("--flow-max-body", type=int, default=0)
    add_forensic_args(p)
    p.set_defaults(fn=cmd_launch)

    p = sub.add_parser("cache", help="缓存库操作")
    p.add_argument("action", choices=["stats", "list", "lookup", "purge",
                                      "export-seed", "import-seed",
                                      "flows", "flow-stats", "flow-purge"])
    p.add_argument("--url", default="")
    p.add_argument("--out", default="")
    p.add_argument("--limit", type=int, default=50)
    p.add_argument("--host", default="", help="flows: 按 host 后缀过滤")
    p.add_argument("--status", type=int, default=0, help="flows: 按状态码过滤")
    p.add_argument("--q", default="", help="flows: 原文子串过滤 (URL/头/参数都能命中)")
    p.add_argument("--kind", default="", help="flows: hit | flow")
    p.add_argument("--days", type=float, default=0, help="flows: 最近 N 天; flow-purge: 保留 N 天")
    p.set_defaults(fn=cmd_cache)
    a = ap.parse_args(argv)
    return int(a.fn(a) or 0)


if __name__ == "__main__":
    sys.exit(main())
