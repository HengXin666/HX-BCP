#!/usr/bin/env python3
"""MCP 端到端: 真起 stdio server, 用真 MCP 客户端列出并调用工具。"""
import asyncio, os, shutil, sys
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))
from hxcp.store import Store

CACHE = "/tmp/hxcp-mcp-e2e"
os.environ["HXCP_CACHE"] = CACHE

async def main():
    from mcp import ClientSession, StdioServerParameters
    from mcp.client.stdio import stdio_client

    shutil.rmtree(CACHE, ignore_errors=True)
    s = Store(CACHE)
    s.put("https://cdn.test/mcp.js", "GET", 200, [["content-type", "text/javascript"]],
          b"/* mcp-probe-body */", ttl=3600)
    s.record_hit(s.lookup("https://cdn.test/mcp.js").key)
    s.close()

    params = StdioServerParameters(
        command=sys.executable,
        args=["-m", "hxcp.mcp_server"],
        env={**os.environ, "HXCP_CACHE": CACHE,
             "PYTHONPATH": os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src")},
    )
    ok = True
    def chk(name, cond, extra=""):
        nonlocal ok
        print(("  PASS " if cond else "  FAIL ") + name + ("  " + str(extra)[:110] if extra else ""))
        ok = ok and cond

    async with stdio_client(params) as (r, w):
        async with ClientSession(r, w) as ses:
            await ses.initialize()
            tools = await ses.list_tools()
            names = sorted(t.name for t in tools.tools)
            print("  工具:", names)
            # 工具数是**清单判据**不是魔法数: 改工具面就要改这里, 否则测试会静默过期。
            chk("列出 8 个工具 (5 缓存 + 3 flow)", len(names) == 8, names)
            chk("含 cache_stats", "cache_stats" in names)
            chk("含 flow_search (记录与判定解耦的对外面)", "flow_search" in names)
            chk("含 flow_stats", "flow_stats" in names)
            chk("含 flow_payload (取证档完整体的取回口)", "flow_payload" in names)

            res = await ses.call_tool("cache_stats", {})
            txt = res.content[0].text
            chk("cache_stats 返回条目数 1", '"entries": 1' in txt.replace(" ", " "), txt[:100])

            res = await ses.call_tool("cache_search", {"query": "mcp.js"})
            chk("cache_search 能找到", "mcp.js" in res.content[0].text)

            res = await ses.call_tool("cache_lookup", {"url": "https://cdn.test/mcp.js"})
            chk("cache_lookup 命中", '"hit": true' in res.content[0].text.lower())

            res = await ses.call_tool("cache_body", {"url": "https://cdn.test/mcp.js"})
            chk("cache_body 取回正文", "mcp-probe-body" in res.content[0].text)

            res = await ses.call_tool("flow_stats", {})
            chk("flow_stats 可调用", '"files"' in res.content[0].text, res.content[0].text[:80])

            res = await ses.call_tool("flow_payload", {"sha256": "0" * 64})
            chk("flow_payload 对未知引用给出可判定答复 (不崩)", "found" in res.content[0].text,
                res.content[0].text[:90])

            res = await ses.call_tool("flow_search", {"host": "cdn.test", "limit": 10})
            chk("flow_search 可调用 (空历史也应是合法 JSON 数组)", '"flows"' in res.content[0].text,
                res.content[0].text[:80])

            res = await ses.call_tool("purge_expired", {})
            chk("purge_expired 可调用", "purged" in res.content[0].text)

    print()
    print("MCP E2E:", "PASS" if ok else "FAIL")
    return 0 if ok else 1

if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
