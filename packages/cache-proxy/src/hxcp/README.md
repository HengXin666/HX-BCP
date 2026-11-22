# hxcp

> 路径: `toolbox/hx-capture-proxy/src/hxcp/` · 类型: Python

## 这是什么

hxcp: 多浏览器共用的静态缓存代理. 两个入口 (代理档 proxy.py, CDP 档 engine.py) 写同一个 store.

## 从哪开始

- 入口: `cli.py` (子命令 run / launch / proxy / cache), `api.py` (控制台与 HTTP), `mcp_server.py`

## 内容划分

文件: `__init__.py`, `api.py`, `cdp.py`, `cli.py`, `engine.py`, `flows.py`, `launcher.py`, `mcp_server.py`, `policy.py`, `store.py`

## 约定与坑

- 代理档默认隧道, 只有 proxy-rules.json 列出的 host 才解密, 解密会改上游看到的 TLS 握手.
- 多个进程共用一个缓存目录: store 用 WAL, flow 用 O_APPEND 单次 write, 运行态每实例一个文件.

---

本文件由 `.agents/tools/readme_forge.py` 生成骨架。**请把「约定与坑」补成实话** ——
骨架的价值在于让目录自解释, 空话没有价值。
