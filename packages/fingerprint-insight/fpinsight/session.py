"""fp-insight 会话层: 一个受控 Chromium 实例 = 一条"通话"。

三条硬约束 (都会影响结论的有效性):
1. **探针自身零页面 JS 执行** —— 导航/截图/DOM 快照/输入全部走原始 CDP 域
   (Page.navigate / Page.captureScreenshot / DOM.getDocument / Input.*)。
   若用 Playwright 的 page.evaluate 去读 DOM, 读到的每个属性都会经过注入的 hook,
   变量纠察立刻被自己的读数污染 —— 取证工具不能留下自己的脚印。
2. **注入走 CDP Page.addScriptToEvaluateOnNewDocument**, 在文档创建前落地,
   保证站点自己的采集脚本跑之前 hook 已就位。
3. **不伪造任何返回值**。本工具只观测, 返回原值 —— 篡改会让被测站点的行为偏离真实,
   那是 fp-audit 的领域, 不是这里。
"""
from __future__ import annotations

import asyncio
import base64
import contextlib
import json
import os
import shutil
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

# ★ 2026-09-22 关键: Playwright 的 shouldProxyLoopback() **默认返回 true**,
#   于是只要传了 proxy, 它就给 Chromium 加 `--proxy-bypass-list=<-loopback>`,
#   **反而把 127.0.0.1 也塞进代理** —— 探针的 ws://127.0.0.1:<port>/ws/fp 因此连不上。
#
#   实测证据 (同一 URL / 同一代理, 只改这一个变量):
#     不设 -> 回传通道 未就绪, 库内 calls=0;  WS 报
#             "Establishing a tunnel via proxy server failed"
#     设 1 -> 回传就绪, 主文档 1830 条, **跨域 iframe 也能连上**
#
#   为什么必须在 import 时就设: Playwright 的 node driver 在
#   `async_playwright().start()` 那一刻读环境变量, 之后再设无效。
#   源码依据: playwright/driver/.../coreBundle.js:38303
#             reads PLAYWRIGHT_DISABLE_FORCED_CHROMIUM_PROXIED_LOOPBACK
os.environ.setdefault('PLAYWRIGHT_DISABLE_FORCED_CHROMIUM_PROXIED_LOOPBACK', '1')

from playwright.async_api import async_playwright

from .hub import Hub

HERE = Path(__file__).resolve().parent
INJECT_JS = (HERE / "inject.js").read_text(encoding="utf-8")

# 保真优先的启动参数: 不加任何会改变指纹面的开关。
STEALTH_ARGS = [
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-features=Translate,MediaRouter,OptimizationHints",
    "--disable-blink-features=AutomationControlled",
    "--disable-background-timer-throttling",
    "--disable-renderer-backgrounding",
    "--disable-backgrounding-occluded-windows",
    "--metrics-recording-only",
    "--no-service-autorun",
    "--password-store=basic",
    "--use-mock-keychain",
    "--hide-crash-restore-bubble",
]


def find_chromium() -> str | None:
    """按优先级找一个真实 Chromium 可执行文件。"""
    env = os.environ.get("FPI_CHROMIUM")
    if env and Path(env).exists():
        return env
    cands: list[Path] = []
    base = Path.home() / ".cache" / "ms-playwright"
    if base.is_dir():
        for d in sorted(base.glob("chromium-*"), reverse=True):
            for sub in ("chrome-linux64", "chrome-linux"):
                p = d / sub / "chrome"
                if p.exists():
                    cands.append(p)
    for p in ("/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"):
        if Path(p).exists():
            cands.append(Path(p))
    return str(cands[0]) if cands else None


@dataclass
class LaunchConfig:
    headless: bool = True
    chromium: str | None = None
    proxy: str | None = None
    width: int = 1440
    height: int = 900
    screen: tuple[int, int] | None = None
    dsf: float = 1.0
    timezone: str | None = None
    locale: str | None = None
    user_agent: str | None = None
    extra_args: list[str] = field(default_factory=list)
    keep_profile: bool = False


class FpSession:
    """一条被观测的浏览会话。"""

    def __init__(self, sid: str, hub: Hub, workdir: Path, cfg: LaunchConfig,
                 label: str = "", target: str = "", mode: str = "cdp") -> None:
        self.sid = sid
        self.hub = hub
        self.workdir = workdir
        self.cfg = cfg
        self.label = label
        self.target = target
        self.mode = mode
        self.profile = workdir / "profile"
        self.shots = workdir / "shots"
        self.shots.mkdir(parents=True, exist_ok=True)
        self._pw = None
        self.browser = None
        self.context = None
        self.page = None
        self.cdp = None
        self.started = time.time()
        self.status = "starting"
        self.console: list[dict] = []
        self.nav_log: list[dict] = []
        self._last_shot: bytes | None = None
        self._shot_n = 0
        # 已装探针的 frame (id(frame)) 与它们的 CDP session (收尾时关掉)
        self._framed: set[int] = set()
        self._sub_sessions: list[Any] = []
        self._rearm_stop = asyncio.Event()
        self._rearm_task: Any = None
        self._arm_log: list[str] = []

    # ------------------------------------------------------------------ 启动
    async def start(self) -> dict:
        exe = self.cfg.chromium or find_chromium()
        self._pw = await async_playwright().start()
        args = list(STEALTH_ARGS) + [
            f"--window-size={self.cfg.width},{self.cfg.height}",
            f"--force-device-scale-factor={self.cfg.dsf}",
        ] + list(self.cfg.extra_args)
        kw: dict[str, Any] = {"headless": self.cfg.headless, "args": args, "chromium_sandbox": False}
        if exe:
            kw["executable_path"] = exe
        if self.cfg.proxy:
            kw["proxy"] = {"server": self.cfg.proxy}
        self.browser = await self._pw.chromium.launch(**kw)
        sw, sh = self.cfg.screen or (self.cfg.width, self.cfg.height)
        ckw: dict[str, Any] = {
            "viewport": {"width": self.cfg.width, "height": self.cfg.height},
            "screen": {"width": sw, "height": sh},
            "device_scale_factor": self.cfg.dsf,
            "is_mobile": False,
            "has_touch": False,
        }
        if self.cfg.timezone:
            ckw["timezone_id"] = self.cfg.timezone
        if self.cfg.locale:
            ckw["locale"] = self.cfg.locale
        if self.cfg.user_agent:
            ckw["user_agent"] = self.cfg.user_agent
        self.context = await self.browser.new_context(**ckw)
        self.page = await self.context.new_page()
        self.cdp = await self.context.new_cdp_session(self.page)
        await self._arm(self.cdp)
        self.status = "running"
        # ★ 周期补注 (见 _periodic_rearm 文档): 让运行中才出现的 OOPIF 也被插桩
        self._rearm_task = asyncio.ensure_future(self._periodic_rearm())
        self.hub.submit(("session", self.sid, self.label or self.sid, self.target, self.mode,
                         self.status, self.describe(), None))
        return self.describe()

    def _probe_src(self, frame_label: str = "top") -> str:
        """按 realm 生成注入脚本。frame 标签**运行时决定** —— 原实现硬编码 "top",
        于是跨域 iframe 里采到的事件被误记为 top, 完全看不出真实来源。

        2026-09-22 实测: DataDome 的挑战页跑在 geo.captcha-delivery.com 的跨域 iframe 里,
        主文档侧只有 i.js 写一个 localStorage 锚点。**只看 top 等于没看到主战场。**
        """
        cfg = {"sid": self.sid, "frame": frame_label,
               "report": f"ws://127.0.0.1:{self.hub.port}/ws/fp", "href0": self.target}
        return "window.__FPI_CFG__=" + json.dumps(cfg, ensure_ascii=False) + ";\n" + INJECT_JS

    async def _arm_frame(self, frame) -> None:
        """给一个 frame (= 一个 realm, 可能是独立进程的 OOPIF) 单独装探针。

        为什么必须 per-frame:
          · `context.new_cdp_session(page)` 的 Page.addScriptToEvaluateOnNewDocument
            只覆盖**与主 frame 同进程**的 frame;
          · 跨站 iframe 在现代 Chrome 里是 **OOPIF**(独立进程), 必须对该 frame
            单独 new_cdp_session 才能注入 (实测: 只对 page 注入时, 2 个 realm 只有 1 个被注入)。
          · `context.add_init_script` 同样到不了 OOPIF (实测同一结果)。
        实测修法: ctx.new_cdp_session(frame) -> Page.enable + Runtime.enable +
                  Page.setBypassCSP + Page.addScriptToEvaluateOnNewDocument。
                  对已存在的 realm 再补一次 Runtime.evaluate 立即生效。
        """
        try:
            u = str(frame.url or "")
        except Exception:
            u = ""
        # 空 url = frame 刚创建 (frameattached 早期调用), 仍要装 ——
        # 它的后续 navigations 会命中我们刚注册的 addScriptToEvaluateOnNewDocument。
        if u.startswith("about:") or u.startswith("chrome-") or u.startswith("devtools:"):
            return
        # ★ 2026-09-22 根因修复: **必须先确认该 frame 拿到真实 URL 且已成为 OOPIF**
        #   才把它标进 _framed。原实现在 frame 刚创建 (url 还是空串) 时就标记,
        #   于是等它真正导航到 geo.captcha-delivery.com 时已被视为"装过"而跳过。
        #   实测证据: arm_log 里全是
        #     ERR (empty) :: BrowserContext.new_cdp_session:
        #       This frame does not have a separate CDP session,
        #       it is a part of the parent frame
        #   —— 那时它还不是独立进程, 建不了 session。等它变 OOPIF 时我们却不试了。
        #   现在: 只在 new_cdp_session **成功** 之后才写进 _framed。
        key = (u, id(frame))
        if key in self._framed:
            return
        # 早退保护: 同一 frame 若已按 URL 装过 (id 变化), 也不重装
        if any(k[0] == u for k in self._framed):
            return
        try:
            fs = await self.context.new_cdp_session(frame)
            for d in ("Page", "Runtime", "DOM"):
                with contextlib.suppress(Exception):
                    await fs.send(f"{d}.enable")
            with contextlib.suppress(Exception):
                await fs.send("Page.setBypassCSP", {"enabled": True})
            label = "sub:" + (u.split("/")[2] if "://" in u else u)[:40]
            src = self._probe_src(label)
            await fs.send("Page.addScriptToEvaluateOnNewDocument", {"source": src})
            # 该 realm 已存在 -> 立即注入一次 (addScript... 只对**之后**新建的文档生效)
            with contextlib.suppress(Exception):
                await fs.send("Runtime.evaluate", {"expression": src, "returnByValue": False})
            self._sub_sessions.append(fs)
            self._framed.add(key)          # ★ 只在成功后标记
            self._arm_log.append('OK  ' + label)
        except Exception as _e:
            self._arm_log.append('ERR ' + (u[:50] or '(empty)') + ' :: ' + str(_e)[:110])

    async def _arm_all_frames(self) -> int:
        """遍历当前**全部** frame 各装一次探针 —— 这是让探针进入 OOPIF 的可靠路径。

        实测依据 (2026-09-22):
          · `context.on('frameattached')` 只在 frame 创建时触发, 那时 url 还是空串,
            且跨域子 frame 创建时机早于其文档加载, 只靠它注入不可靠;
          · 但 `ctx.new_cdp_session(frame)` 对**已存在**的 OOPIF 有效 ——
            实测注入后台 `window.__MK__` 返回 `SUB::geo.captcha-delivery.com`。
        所以两者都要: frameattached 兜早期, 导航后全量补一次 (本函数)。
        返回本次新装的 frame 数。"""
        n = 0
        try:
            frames = list(self.page.frames)
        except Exception:
            return 0
        for fr in frames:
            before = len(self._framed)
            await self._arm_frame(fr)
            if len(self._framed) > before:
                n += 1
        return n

    def _on_frame(self, frame) -> None:
        """frame 一出现就装探针 (含 OOPIF)。"""
        try:
            asyncio.ensure_future(self._arm_frame(frame))
        except Exception:
            pass

    async def _periodic_rearm(self) -> None:
        """每 2 秒把探针补进所有 frame。

        为什么必须有这个循环 (2026-09-22 实测):
          DataDome 的挑战 iframe **不是**在导航时就存在的 —— 它是 i.js 跑起来之后
          动态插入的 (实测 t=5s 才出现)。只在 navigate 后补一次, 那时它还不存在,
          于是整个挑战页从未被插桩, 采到的永远只有主文档侧 i.js 的 6 条事件。
          周期补注保证「后出现的 realm」也能在它出现后 2 秒内被装上。
        收尾: 由 _rearm_stop 事件终止。"""
        while not self._rearm_stop.is_set():
            with contextlib.suppress(Exception):
                await self._arm_all_frames()
            with contextlib.suppress(asyncio.TimeoutError, Exception):
                await asyncio.wait_for(self._rearm_stop.wait(), timeout=2.0)

    async def _arm(self, cdp) -> None:
        src = self._probe_src("top")
        for domain in ("Page", "Runtime", "DOM", "Network"):
            try:
                await cdp.send(f"{domain}.enable")
            except Exception:
                pass
        try:
            await cdp.send("Page.setBypassCSP", {"enabled": True})
        except Exception:
            pass
        await cdp.send("Page.addScriptToEvaluateOnNewDocument", {"source": src})
        cdp.on("Page.frameNavigated", self._on_nav)
        cdp.on("Runtime.consoleAPICalled", self._on_console)
        # ★ 关键: 覆盖跨域 iframe (OOPIF)
        with contextlib.suppress(Exception):
            self.context.on("frameattached", self._on_frame)
        for fr in self.page.frames:
            await self._arm_frame(fr)

    def _on_nav(self, params: dict) -> None:
        fr = params.get("frame") or {}
        if fr.get("parentId"):
            return
        self.nav_log.append({"url": fr.get("url"), "ts": time.time()})

    def _on_console(self, params: dict) -> None:
        try:
            args = [str(a.get("value", a.get("description", a.get("type"))))[:300]
                    for a in (params.get("args") or [])]
            if len(self.console) < 500:
                self.console.append({"type": params.get("type"), "args": args, "ts": time.time()})
        except Exception:
            pass

    def describe(self) -> dict:
        return {"sid": self.sid, "label": self.label, "target": self.target, "mode": self.mode,
                "arm_log": self._arm_log[-14:], "framed": len(self._framed),
                "status": self.status, "started": self.started, "url": self.url(),
                "profile": str(self.profile), "headless": self.cfg.headless,
                "proxy": self.cfg.proxy, "tz": self.cfg.timezone,
                "chromium": self.cfg.chromium or find_chromium()}

    # ------------------------------------------------------------------ 动作
    def url(self) -> str:
        try:
            return self.page.url if self.page else ""
        except Exception:
            return ""

    async def navigate(self, url: str, wait_ms: int = 2500) -> dict:
        self.hub.submit(("moment", self.sid, "navigate", "", url, {}))
        res = await self.cdp.send("Page.navigate", {"url": url})
        await asyncio.sleep(wait_ms / 1000)
        # ★ 导航后把探针补进所有 frame (含 OOPIF 挑战页) —— 见 _arm_all_frames
        with contextlib.suppress(Exception):
            await self._arm_all_frames()
        return {"ok": True, "frameId": res.get("frameId"), "error": res.get("errorText"),
                "url": self.url()}

    async def screenshot(self) -> bytes:
        r = await self.cdp.send("Page.captureScreenshot",
                                {"format": "png", "captureBeyondViewport": False})
        self._last_shot = base64.b64decode(r["data"])
        return self._last_shot

    async def save_shot(self) -> str:
        data = await self.screenshot()
        self._shot_n += 1
        p = self.shots / f"{self._shot_n:03d}.png"
        p.write_bytes(data)
        return str(p)

    async def mark(self, label: str, note: str = "") -> dict:
        """关键标记: 与当前各 API 计数快照一起入库, 用来做 (动作 -> 指纹读取) 的因果对照。"""
        snap = await self.counters()
        self.hub.submit(("moment", self.sid, label or "mark", note, self.url(),
                         {"counters": snap, "t": round(time.time() - self.started, 3)}))
        return {"ok": True, "label": label, "counter_paths": len(snap)}

    async def diag(self) -> dict:
        """探针自检: 装了多少钩子 / 回传通道是否就绪。判据必须能证伪 —— 抓不到就要报出来。"""
        try:
            r = await self.cdp.send("Runtime.evaluate", {
                "expression": "(window.__FPI__&&window.__FPI__.diag&&JSON.stringify(window.__FPI__.diag()))||'null'",
                "returnByValue": True, "silent": True})
            v = r.get("result", {}).get("value")
            return json.loads(v) if v and v != "null" else {}
        except Exception:
            return {}

    async def counters(self) -> dict:
        """读回探针自己的精确计数表 (window.__FPI__.counts())。
        这是唯一允许的自我求值: 它只读探针闭包里的 Map, 不触碰任何被观测 API。"""
        try:
            r = await self.cdp.send("Runtime.evaluate", {
                "expression": "(window.__FPI__&&window.__FPI__.counts&&JSON.stringify(window.__FPI__.counts()))||'{}'",
                "returnByValue": True, "silent": True})
            return json.loads(r.get("result", {}).get("value") or "{}")
        except Exception:
            return {}

    async def commit_counts(self) -> int:
        """把精确计数写入库 (与"事件上报"分开: 上报受令牌桶节流, 计数不受)。"""
        c = await self.counters()
        self.hub.submit(("counts", self.sid, c))
        return sum(1 for v in c.values() if v)

    async def origin(self) -> str:
        u = self.url()
        try:
            from urllib.parse import urlparse
            p = urlparse(u)
            return f"{p.scheme}://{p.netloc}" if p.scheme else u
        except Exception:
            return u

    async def clear(self, what: str = "all") -> dict:
        try:
            await self.cdp.send("Network.clearBrowserCache")
        except Exception:
            pass
        if what in ("all", "cookies"):
            try:
                await self.cdp.send("Network.clearBrowserCookies")
            except Exception:
                pass
        if what in ("all", "storage"):
            try:
                await self.cdp.send("Storage.clearDataForOrigin",
                                    {"origin": await self.origin(), "storageTypes": "all"})
            except Exception:
                pass
        return {"ok": True, "what": what}

    async def dom(self, limit: int = 200) -> dict:
        """DOM 快照 —— 全部经 CDP DOM 域, 不执行页面 JS (保证纠察样本不含探针自己的读数)。"""
        try:
            await self.cdp.send("DOM.enable")
        except Exception:
            pass
        doc = await self.cdp.send("DOM.getDocument", {"depth": -1, "pierce": True})
        nodes: list[dict] = []
        INTERACTIVE = {"a", "button", "input", "textarea", "select", "option", "label", "summary"}
        ROLES = {"button", "link", "textbox", "checkbox", "tab", "radio", "combobox", "menuitem"}

        def walk(n: dict) -> None:
            if len(nodes) > 6000:
                return
            name = (n.get("nodeName") or "").lower()
            raw = n.get("attributes") or []
            amap = {raw[i]: raw[i + 1] for i in range(0, len(raw) - 1, 2)}
            role = amap.get("role") or ""
            if (name in INTERACTIVE or role in ROLES) and amap.get("type") != "hidden":
                nodes.append({
                    "nodeId": n.get("nodeId"), "tag": name, "id": amap.get("id"),
                    "name": amap.get("name"), "type": amap.get("type"), "role": role or None,
                    "placeholder": amap.get("placeholder"), "title": amap.get("title"),
                    "href": (amap.get("href") or "")[:300] or None,
                    "value": (amap.get("value") or "")[:100] or None,
                    "cls": (amap.get("class") or "")[:120] or None,
                })
            for c in (n.get("children") or []):
                walk(c)
            for sr in (n.get("shadowRoots") or []):
                walk(sr)
            if n.get("contentDocument"):
                walk(n["contentDocument"])
            for pn in (n.get("pseudoElements") or []):
                walk(pn)

        walk(doc.get("root") or {})
        out: list[dict] = []
        for nd in nodes:
            if len(out) >= limit:
                break
            try:
                bm = await self.cdp.send("DOM.getBoxModel", {"nodeId": nd["nodeId"]})
            except Exception:
                continue
            q = bm.get("model", {}).get("content")
            if not q or len(q) < 8:
                continue
            w = q[2] - q[0]
            h = q[5] - q[1]
            if w <= 0 or h <= 0:
                continue
            nd["box"] = {"x": round(q[0]), "y": round(q[1]), "w": round(w), "h": round(h)}
            nd["center"] = [round((q[0] + q[2]) / 2), round((q[1] + q[5]) / 2)]
            try:
                nm = await self.cdp.send("DOM.getNodeForLocation", {"x": nd["center"][0], "y": nd["center"][1]})
                nd["hitNodeId"] = nm.get("nodeId")
                nd["hitBackendNodeId"] = nm.get("backendNodeId")
            except Exception:
                pass
            out.append(nd)
        return {"url": self.url(), "elements": out, "seen": len(nodes), "title": self.title()}

    def title(self) -> str:
        try:
            return self.page.title() if self.page else ""
        except Exception:
            return ""

    # ------------------------------------------------------------------ 收尾
    async def stop(self) -> dict:
        try:
            await self.commit_counts()
        except Exception:
            pass
        self.status = "closed"
        def _t(out: str) -> str:
            return out if isinstance(out, str) else str(out)

        for closer in (self.page, self.context, self.browser):
            try:
                if closer is not None:
                    await closer.close()
            except Exception:
                pass
        try:
            if self._pw:
                await self._pw.stop()
        except Exception:
            pass
        if not self.cfg.keep_profile and self.profile.exists():
            shutil.rmtree(self.profile, ignore_errors=True)
        meta = json.dumps({"console": self.console[-200:], "nav": self.nav_log[-100:]},
                          ensure_ascii=False)
        self.hub.submit(("session", self.sid, self.label or self.sid, self.target, self.mode,
                         self.status, self.describe(), meta))
        return {"ok": True, "sid": self.sid, "status": self.status}
