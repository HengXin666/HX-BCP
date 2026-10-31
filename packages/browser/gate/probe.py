"""Open the public checkers and save what each page actually wrote.

This file does not score. ``score.ts`` reads the JSON.

Attach to a running hx-browser environment:

    uv run python packages/browser/gate/probe.py \\
        --cdp http://127.0.0.1:43000 --out /tmp/sites.json
    node packages/browser/gate/score.ts /tmp/sites.json /tmp/scorecard.svg

Without ``--cdp`` it launches the Chromium on this machine and labels the
capture ``local-chromium``. That run is not an hx-browser score.
"""

from __future__ import annotations

import argparse
import json
from datetime import datetime, timezone
from pathlib import Path

SITES = [
    {"id": "sannysoft", "name": "Sannysoft", "url": "https://bot.sannysoft.com/", "wait_ms": 5000},
    {"id": "creepjs", "name": "CreepJS", "url": "https://abrahamjuliot.github.io/creepjs/", "wait_ms": 28000},
    {"id": "iphey", "name": "Iphey", "url": "https://iphey.com/", "wait_ms": 18000},
    {"id": "pixelscan", "name": "Pixelscan", "url": "https://pixelscan.net/fingerprint-check", "wait_ms": 8000},
    {"id": "browserscan", "name": "BrowserScan", "url": "https://www.browserscan.net/", "wait_ms": 18000},
    {"id": "browserleaks-canvas", "name": "BrowserLeaks Canvas", "url": "https://browserleaks.com/canvas", "wait_ms": 8000},
    {"id": "browserleaks-webrtc", "name": "BrowserLeaks WebRTC", "url": "https://browserleaks.com/webrtc", "wait_ms": 10000},
    {"id": "deviceinfo", "name": "Device & Browser Info", "url": "https://deviceandbrowserinfo.com/are_you_a_bot", "wait_ms": 8000},
    {"id": "clearcote", "name": "Clearcote", "url": "https://www.clearcotelabs.com/audit", "wait_ms": 4000, "click": "audit"},
]


NO_CDP = "该内核不说 CDP, 当前采集器取不到"


def capture_pages(context, browser_label: str) -> dict:
    visits = [_visit(context, site, browser_label) for site in SITES]
    return {
        "capturedAt": datetime.now(timezone.utc).isoformat(),
        "browser": browser_label,
        "note": "分数在 score.ts 里算。",
        "visits": visits,
    }


def unmeasured(browser_label: str, reason: str) -> dict:
    """A capture with no visits and the reason every site stays unmeasured, no score is produced"""
    return {
        "capturedAt": datetime.now(timezone.utc).isoformat(),
        "browser": browser_label,
        "unmeasured": reason,
        "visits": [],
    }


def main() -> None:
    parser = argparse.ArgumentParser(description="Capture public fingerprint-checker pages")
    parser.add_argument("--cdp", default="", help="hx-browser CDP endpoint, e.g. http://127.0.0.1:43000")
    parser.add_argument("--out", required=True, help="where to write the capture JSON")
    parser.add_argument("--kernel", default="", help="hx-browser kernel id this capture belongs to")
    parser.add_argument("--family", default="", help="kernel family, blink, gecko or webkit")
    parser.add_argument("--unmeasured", default="", help="write an unmeasured capture with this reason, open nothing")
    args = parser.parse_args()

    browser_label = "hx-browser-cdp" if args.cdp else "local-chromium"
    if args.unmeasured or args.family == "webkit":
        payload = unmeasured("not-captured", args.unmeasured or NO_CDP)
    else:
        payload = _capture(args.cdp, browser_label)
    if args.kernel:
        payload.update(kernel=args.kernel, family=args.family)
    Path(args.out).write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"wrote {len(payload['visits'])} visits to {args.out} ({browser_label})")


def _capture(cdp: str, browser_label: str) -> dict:
    """Opens every checker in the CDP browser, or in local Chromium without cdp, a failed CDP attach is an unmeasured capture"""
    from playwright.sync_api import sync_playwright

    with sync_playwright() as playwright:
        if cdp:
            try:
                browser = playwright.chromium.connect_over_cdp(cdp, timeout=30000)
            except Exception as exc:  # noqa: BLE001
                return unmeasured(browser_label, "CDP 连接失败, " + str(exc).splitlines()[0][:160])
            context = browser.contexts[0] if browser.contexts else browser.new_context()
            return capture_pages(context, browser_label)
        browser = playwright.chromium.launch(headless=True, args=["--no-sandbox", "--disable-dev-shm-usage"])
        context = browser.new_context(viewport={"width": 1280, "height": 900})
        payload = capture_pages(context, browser_label)
        browser.close()
        return payload


def _visit(context, site: dict, browser_label: str) -> dict:
    page = None
    visit = {
        "siteId": site["id"],
        "name": site["name"],
        "url": site["url"],
        "capturedAt": datetime.now(timezone.utc).isoformat(),
        "browser": browser_label,
        "ok": False,
        "status": 0,
        "title": "",
        "excerpt": "",
        "passed": None,
        "failed": None,
        "ua": "",
        "webdriver": None,
        "error": "",
        "href": "",
    }
    try:
        page = context.new_page()
        response = page.goto(site["url"], wait_until="domcontentloaded", timeout=25000)
        visit["status"] = response.status if response else 0
        page.wait_for_timeout(site["wait_ms"])
        if site.get("click") == "audit":
            button = page.get_by_role("button", name="Run the audit")
            button.first.wait_for(state="visible", timeout=15000)
            button.first.click(timeout=8000)
            page.wait_for_timeout(22000)
        extracted = page.evaluate(
            """() => {
                const text = (document.body?.innerText || "").replace(/\\s+\\n/g, "\\n").trim();
                const count = (sel) => document.querySelectorAll(sel).length;
                return {
                    title: document.title,
                    excerpt: text.slice(0, 50000),
                    passed: count(".passed, .pass, td.ok, .success"),
                    failed: count(".failed, td.failed, .fail"),
                    ua: navigator.userAgent,
                    webdriver: navigator.webdriver,
                    href: location.href,
                };
            }"""
        )
        visit.update(extracted)
        visit["ok"] = True
    except Exception as exc:  # noqa: BLE001 — one dead site must not drop the rest
        visit["error"] = str(exc)
    finally:
        if page is not None:
            try:
                page.close()
            except Exception:
                pass
    print(f"{site['id']} {'ok' if visit['ok'] else 'ERR'}")
    return visit


if __name__ == "__main__":
    main()
