"""HX-Browser 服务入口: uv run python run.py

归属: 8325 由 HX-WebX 面板 (36000) 独占托管, 它已从仓库根 run.sh 的 REGISTRY 移除.
一个服务只归一处管: 两处同时拉起会各自 spawn 一份抢 8325, 而面板会把"端口被占"
误判成自己的进程死了, 于是陷入"异常退出 -> 连崩熔断"的 5 分钟循环 (实测 172 次自动重启
而服务全程健康). 不要把本服务加回 run.sh.
决策与实测见 .agents/notes/implemented/architecture/2026-10-11-single-owner-per-service.md
"""

from __future__ import annotations

from hx_browser.cli import main

if __name__ == "__main__":
    raise SystemExit(main())
