"""Read the FuckClaude / NodeCloak environment signals from a browser.

    uv run --with playwright python gate/claude_env_probe.py --out /tmp/claude-env.json
    uv run --with playwright python gate/claude_env_probe.py --cdp <hx-browser CDP endpoint> --out /tmp/claude-env.json
    node --experimental-strip-types gate/claude-env.ts /tmp/claude-env.json

With --cdp it reads a running hx-browser profile as-is. Without it, it launches the local
Chromium with --timezone / --locale applied, which is what CI does.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import sync_playwright

PROBE = (Path(__file__).parent / "claude-env.js").read_text(encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description="Read Claude environment signals")
    parser.add_argument("--cdp", default="", help="hx-browser CDP endpoint")
    parser.add_argument("--out", required=True, help="where to write the signal JSON")
    parser.add_argument("--timezone", default="America/Los_Angeles")
    parser.add_argument("--locale", default="en-US")
    args = parser.parse_args()
    with sync_playwright() as playwright:
        if args.cdp:
            browser = playwright.chromium.connect_over_cdp(args.cdp)
            context = browser.contexts[0] if browser.contexts else browser.new_context()
            source = "cdp:" + args.cdp
        else:
            browser = playwright.chromium.launch(headless=True, args=["--no-sandbox", "--disable-dev-shm-usage"])
            context = browser.new_context(timezone_id=args.timezone, locale=args.locale)
            source = f"local chromium tz={args.timezone} locale={args.locale}"
        page = context.new_page()
        page.set_content("<!doctype html><html><body></body></html>")
        env = page.evaluate(PROBE)
        page.close()
        if not args.cdp:
            browser.close()
    env["source"] = source
    Path(args.out).write_text(json.dumps(env, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(env, ensure_ascii=False))


if __name__ == "__main__":
    main()
