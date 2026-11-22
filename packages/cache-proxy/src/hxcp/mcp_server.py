"""MCP server — 让 AI 直接查缓存与抓包结果。

**不加新逻辑**: 每个工具都只是 store/engine 的一次调用。
工具描述写清"什么时候用它", 因为 MCP 的可用性取决于模型能否在正确时机想起它
(本仓库已有教训: 工具存在但不会被调用)。

只读优先: 除 `purge_expired` 外全部只读。

关于 API 形态 (实测记录): 本机装的是 **mcp 2.2.0**, 其中 `FastMCP` 已改名为
`mcp.server.mcpserver.MCPServer`。旧写法 `from mcp.server.fastmcp import FastMCP`
会 ImportError —— 这不是"依赖没装", 是入口改名。
"""
from __future__ import annotations

import json
import os
import sys

from . import flows as flows_mod
from .store import Store

DEFAULT_CACHE = os.path.expanduser("~/loli_data/hxcp")


def _store() -> Store:
    return Store(os.environ.get("HXCP_CACHE", DEFAULT_CACHE))


def build_server():
    try:
        from mcp.server.mcpserver import MCPServer
    except ImportError as e:  # pragma: no cover
        raise RuntimeError("需要 MCP 依赖: uv sync --extra mcp (" + str(e)[:80] + ")")

    mcp = MCPServer(
        name="hx-capture-proxy",
        title="HX-Capture-Proxy",
        instructions=("多浏览器共用的静态缓存代理的观测与接入面。"
                      "用它查「怎么接入 / 某个静态资源缓存过没有 / 抓到了哪些请求 / 哪些站点在省流量」。"
                      "两种入口: 标准 HTTP/SOCKS5 代理 (任意浏览器), CDP 档 (Chromium, 命中不出网也不改握手)。"
                      "HTTPS 默认隧道不解密, 只有显式切到 decrypt 的 host 才会被缓存。"),
        version="0.1.0",
    )

    @mcp.tool(description="看缓存库规模与命中情况 (条目数/占用/命中次数/已省字节), 以及引擎当前是否在跑。")
    def cache_stats() -> str:
        s = _store()
        try:
            from .api import read_runtime
            rt = read_runtime(s) or {}
            out = dict(s.stats())
            out["engine_state"] = rt.get("state", "absent")
            out["engine_stats"] = rt.get("stats")
            return json.dumps(out, ensure_ascii=False, indent=2)
        finally:
            s.close()

    @mcp.tool(description="按 URL 片段搜缓存条目, 按命中次数与体积排序。")
    def cache_search(query: str = "", limit: int = 50) -> str:
        s = _store()
        try:
            sql = ("SELECT url,size,hit_count,source,fresh_until FROM entries"
                   + (" WHERE url LIKE ?" if query else "")
                   + " ORDER BY hit_count DESC, size DESC LIMIT ?")
            args = (["%" + query + "%"] if query else []) + [int(limit)]
            rows = s._conn.execute(sql, args).fetchall()
            return json.dumps([dict(r) for r in rows], ensure_ascii=False, indent=2)
        finally:
            s.close()

    @mcp.tool(description="查一个确切 URL 是否在缓存里、是否新鲜、来自网络还是种子。")
    def cache_lookup(url: str) -> str:
        s = _store()
        try:
            e = s.lookup(url)
            if e is None:
                return json.dumps({"hit": False, "url": url}, ensure_ascii=False)
            return json.dumps({"hit": True, "fresh": e.is_fresh(), "size": e.size,
                               "source": e.source, "hit_count": e.hit_count,
                               "etag": e.etag, "key": e.key}, ensure_ascii=False, indent=2)
        finally:
            s.close()

    @mcp.tool(description="取缓存条目的正文 (截断)。要读被缓存下来的 js/css/body 时用它。")
    def cache_body(url: str, max_chars: int = 2000) -> str:
        s = _store()
        try:
            e = s.lookup(url)
            if e is None:
                return "没有该条目: " + url
            b = s.get_blob(e.body_hash)
            if b is None:
                return "blob 缺失 (索引有记录但文件不在)"
            return b[: max(1, int(max_chars))].decode("utf-8", "replace")
        finally:
            s.close()

    @mcp.tool(description=("查请求级流量历史 (不是缓存索引)。找接口、找请求参数、找签名来源、"
                           "看某个响应什么时候下发了 Set-Cookie、跟进被拒缓存的 API 调用时用它。"
                           "能按 host/状态码/子串过滤。缓存索引里的东西只是流量的子集。"))
    def flow_search(host: str = "", status: int = 0, q: str = "", kind: str = "",
                    limit: int = 50, days: float = 0, client: str = "") -> str:
        s = _store()
        try:
            rows = list(flows_mod.iter_flows(
                s.root / "flows", host=host, status=(int(status) if status else None),
                q=q, kind=kind, limit=max(1, min(int(limit), 500)), days=(days or None),
                client=client))
            # 与 HTTP /flows 同形态 (count + flows): 同一事实两种形状就是两处判据。
            return json.dumps({"count": len(rows), "flows": rows},
                              ensure_ascii=False, indent=2)
        finally:
            s.close()

    @mcp.tool(description=("看 flow 记录的总量/时间跨度/判定分布。判断「这段历史里有没有我想要的东西」"
                           "以及「记录是不是还在写」。"))
    def flow_stats() -> str:
        s = _store()
        try:
            return json.dumps(flows_mod.flow_stats(s.root / "flows"),
                              ensure_ascii=False, indent=2)
        finally:
            s.close()

    @mcp.tool(description=("取证档回放: 按 flow 记录里 body_ref.sha256 取回**完整请求体**。"
                           "flow_search 返回的 post_data 是 8K 摘要, 要拿原始请求体 (回放/重放协议)"
                           "就先用 flow_search 找到记录, 再拿它的 body_ref.sha256 来这里取。"
                           "只有引擎以 --forensic 启动过的时段才有数据, 取不到会明说原因。"))
    def flow_payload(sha256: str, max_chars: int = 200000) -> str:
        s = _store()
        try:
            fs = flows_mod.open_forensic(s.root)
            if not fs.enabled:
                return "取证档未开启"
            b = fs.get(sha256)
            if b is None:
                return json.dumps({"sha256": sha256, "found": False,
                                  "hint": "该体未进池 (取证档开启前的记录 / 超 16MiB / 已被轮转删除)",
                                  "stats": fs.stats()}, ensure_ascii=False)
            cut = max(1, int(max_chars))
            txt = b[:cut].decode("utf-8", "replace")
            return json.dumps({"sha256": sha256, "found": True, "size": len(b),
                               "returned": len(txt), "truncated": len(b) > cut,
                               "text": txt}, ensure_ascii=False)
        finally:
            s.close()

    @mcp.tool(description=("怎么接入缓存代理: 代理地址, 每个客户端的入口端口, 浏览器/curl/Playwright 配置片段, "
                           "CA 路径, 当前哪些 host 被解密。要让一个浏览器或程序走缓存时先调它。"))
    def proxy_access() -> str:
        from .access import access_info
        s = _store()
        try:
            return json.dumps(access_info(s.root), ensure_ascii=False, indent=2)
        finally:
            s.close()

    @mcp.tool(description=("按站点看缓存效果: 每个 host 的缓存条目, 占用, 已省字节, 以及 TLS 是隧道还是解密。"
                           "判断该不该把某个静态域名切到解密时用它。"))
    def host_summary(limit: int = 50) -> str:
        from . import proxy_rules
        s = _store()
        try:
            rules = proxy_rules.load(s.root)
            rows = [dict(h, mode=proxy_rules.decide(rules, h["host"])[0])
                    for h in s.host_summary(max(1, min(int(limit), 500)))]
            return json.dumps({"rules": rules, "hosts": rows}, ensure_ascii=False, indent=2)
        finally:
            s.close()

    @mcp.tool(description=("切换一个 host 后缀的 TLS 处理: decrypt (解密并缓存, 上游会看到代理的 TLS 指纹), "
                           "tunnel (默认, 浏览器原生握手), never (永不解密)。约 1 秒内对新连接生效。"))
    def set_tls_mode(host: str, mode: str) -> str:
        from . import proxy_rules
        try:
            return json.dumps(proxy_rules.set_host(os.environ.get("HXCP_CACHE", DEFAULT_CACHE), host, mode),
                              ensure_ascii=False)
        except ValueError as e:
            return "error: " + str(e)

    @mcp.tool(description="清理过期条目并回收 blob。会动磁盘, 只在确实要腾空间时用。")
    def purge_expired() -> str:
        s = _store()
        try:
            return "purged " + str(s.purge_expired()) + " entries"
        finally:
            s.close()

    return mcp


def main() -> int:
    build_server().run(transport="stdio")
    return 0


if __name__ == "__main__":
    sys.exit(main())
