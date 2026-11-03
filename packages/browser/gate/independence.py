"""Independence gate: start N hx-browser environments with different configs, read what each page really sees, compare every pair.

up:   uv run python gate/independence.py --spawn --out /tmp/independence.json
offline: uv run python gate/independence.py --from /tmp/independence.json

Each pair of environments gets a count of fields that differ. A pair is linkable when that count is below
--min-diff or when any --must-differ field is identical. Any linkable pair, or a HeadlessChrome / SwiftShader leak, exits 1.

Exit codes: 0 all pairs independent, 1 linkable pairs or leaks found, 3 could not start or read the environments (unscored).
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from datetime import datetime, timezone
from itertools import combinations
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from fontcheck import PROBE_FONTS, fc_fonts  # noqa: E402
from real_profile import PKG, TMP, UNSCORED, call, local, stop_group, wait_api, wait_cdp  # noqa: E402

FIELDS = ["ua", "platform", "cores", "memory", "screen", "dpr", "languages", "timezone", "canvas", "webgl", "fonts", "plugins"]
LINKABLE = 1
NAME_PREFIX = "ci-independence-"
LEAKS = {"ua": "HeadlessChrome", "http_ua": "HeadlessChrome", "brands": "HeadlessChrome", "webgl": "SwiftShader"}

# Three desktop profiles with every configurable field different.
# A and B are the two profiles from the 2026-10-31 manual check, C adds a Linux host profile.
DEFAULT_CONFIGS = [
    {"label": "A-mac-ny", "fingerprint": {"os": "macOS", "hardware_concurrency": 8, "device_memory": 8, "resolution": "2560x1440",
                                          "timezone": "America/New_York", "language": "en-US,en"}, "languages": ["en-US", "en"]},
    {"label": "B-win-berlin", "fingerprint": {"os": "Windows", "hardware_concurrency": 16, "device_memory": 16, "resolution": "1920x1080",
                                              "timezone": "Europe/Berlin", "language": "de-DE,de"}, "languages": ["de-DE", "de"]},
    {"label": "C-linux-tokyo", "fingerprint": {"os": "Linux", "hardware_concurrency": 4, "device_memory": 4, "resolution": "1366x768",
                                               "timezone": "Asia/Tokyo", "language": "ja-JP,ja"}, "languages": ["ja-JP", "ja"]},
]
COMMON_FP = {"headless": True, "canvas_noise": True, "webgl_noise": True, "audio_noise": True, "webrtc_mode": "proxy"}

# Reads the values a checker page would read, hashes the large ones in page so the capture stays small.
COLLECT_JS = r"""
(async () => {
  const h = (s) => { let x = 0x811c9dc5; for (let i = 0; i < s.length; i++) x = Math.imul(x ^ s.charCodeAt(i), 0x01000193) >>> 0; return x.toString(16).padStart(8, '0'); };
  const out = {};
  out.injected = !!window.__hx_fp_injected__;
  out.ua = navigator.userAgent;
  out.brands = (navigator.userAgentData && navigator.userAgentData.brands || []).map(b => b.brand).join('|');
  out.platform = navigator.platform;
  out.cores = navigator.hardwareConcurrency;
  out.memory = navigator.deviceMemory === undefined ? null : navigator.deviceMemory;
  out.screen = screen.width + 'x' + screen.height;
  out.dpr = window.devicePixelRatio;
  out.languages = (navigator.languages || []).join(',');
  out.timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  try {
    const c = document.createElement('canvas'); c.width = 280; c.height = 60;
    const x = c.getContext('2d'); x.textBaseline = 'top'; x.font = '16px Arial'; x.fillStyle = '#f60'; x.fillRect(100, 1, 62, 20);
    x.fillStyle = '#069'; x.fillText('hx independence 1.0', 2, 15); x.fillStyle = 'rgba(102,204,0,0.7)'; x.fillText('hx independence 1.0', 4, 17);
    out.canvas = h(c.toDataURL());
  } catch (e) { out.canvas = 'error ' + e.message; }
  try {
    const g = document.createElement('canvas').getContext('webgl');
    const d = g.getExtension('WEBGL_debug_renderer_info');
    out.webgl = g.getParameter(d.UNMASKED_VENDOR_WEBGL) + ' / ' + g.getParameter(d.UNMASKED_RENDERER_WEBGL);
  } catch (e) { out.webgl = 'error ' + e.message; }
  const fonts = %PROBE_FONTS%;
  const m = document.createElement('canvas').getContext('2d');
  const w = (f) => { m.font = '72px ' + f; return m.measureText('mmmmmmmmlli10OQ').width; };
  const bm = w('monospace'), bs = w('serif');
  out.fonts = fonts.filter(f => w('"' + f + '",monospace') !== bm || w('"' + f + '",serif') !== bs).join(',');
  out.plugins = Array.from(navigator.plugins || []).map(p => p.name).join(',');
  return out;
})()
"""


class Echo:
    """Local http page on 127.0.0.1 (a secure context, so userAgentData exists) that records request headers per path."""

    def __init__(self) -> None:
        import http.server
        import threading

        seen: dict[str, dict] = {}
        self.seen = seen

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802
                if self.path.startswith("/probe/"):
                    seen[self.path] = {k.lower(): v for k, v in self.headers.items()}
                body = b"<!doctype html><title>independence</title><body></body>"
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.send_header("Accept-CH", "Sec-CH-UA-Platform-Version, Sec-CH-UA-Full-Version-List")
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_args) -> None:
                pass

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def url(self, label: str) -> str:
        return "http://127.0.0.1:%d/probe/%s" % (self.server.server_address[1], label)


def cdp_eval(cdp_http: str, url: str, timeout: float = 40) -> dict:
    """Navigates the first page target to url, waits until the fingerprint script ran, returns COLLECT_JS output.

    Uses the existing tab: hx-browser hooks new documents only on tabs it saw while injecting.
    """
    import urllib.request

    from websockets.sync.client import connect

    with urllib.request.urlopen(cdp_http + "/json/list", timeout=15) as resp:
        pages = [t for t in json.loads(resp.read()) if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]
    if not pages:
        raise RuntimeError("no page target at %s" % cdp_http)
    msg_id = 0
    with connect(pages[0]["webSocketDebuggerUrl"], max_size=None, open_timeout=15) as ws:
        def send(method: str, params: dict) -> dict:
            nonlocal msg_id
            msg_id += 1
            ws.send(json.dumps({"id": msg_id, "method": method, "params": params}))
            while True:
                data = json.loads(ws.recv(timeout=timeout))
                if data.get("id") == msg_id:
                    return data.get("result") or {}
        end = time.time() + timeout
        value: dict = {}
        while time.time() < end:
            send("Page.navigate", {"url": url})
            time.sleep(2)
            js = COLLECT_JS.replace("%PROBE_FONTS%", json.dumps(PROBE_FONTS))
            res = send("Runtime.evaluate", {"expression": js, "awaitPromise": True, "returnByValue": True})
            value = (res.get("result") or {}).get("value") or {}
            if value.get("injected"):
                break
    return value


def env_body(cfg: dict, extra_args: list[str]) -> dict:
    """API body for one config, the same prefs real_profile.py uses so language reaches LANG and Accept-Language."""
    fp = {**COMMON_FP, **cfg["fingerprint"]}
    prefs = {"font_isolation": "strict", "language_mode": "custom", "languages": cfg["languages"]}
    return {"name": NAME_PREFIX + cfg["label"], "fingerprint": fp, "prefs": prefs, "launch": {"extra_args": extra_args}}


def start_env(api: str, body: dict) -> tuple[str, str, dict]:
    """Creates or reuses the environment by name, starts it, returns (env id, cdp url, runtime).
    Tries both loopbacks: Chrome 146 ignored --remote-debugging-address and listened on [::1] only (2026-10-31)."""
    envs = call(api, "GET", "/environments")
    if isinstance(envs, dict):
        envs = envs.get("list") or envs.get("items") or envs.get("environments") or []
    env_id = next((e.get("id") for e in envs or [] if isinstance(e, dict) and e.get("name") == body["name"]), None)
    if env_id:
        call(api, "PUT", "/environments/%s" % env_id, body)
    else:
        env_id = call(api, "POST", "/environments", body)["id"]
    rt = (call(api, "POST", "/environments/%s/start" % env_id, timeout=180) or {}).get("runtime") or {}
    port = rt.get("debug_port")
    bases = [b for b in (rt.get("cdp_http"), port and local(port), port and "http://[::1]:%d" % port) if b]
    end = time.time() + 30
    while time.time() < end:
        cdp = next((b for b in dict.fromkeys(bases) if wait_cdp(b, 1)), "")
        if cdp:
            return env_id, cdp, rt
    raise RuntimeError("no CDP endpoint for %s on %s" % (body["name"], ", ".join(bases)))


def capture(args: argparse.Namespace, configs: list[dict]) -> list[dict]:
    """Starts every config, reads it, stops it again; raises when any environment cannot be read."""
    rows = []
    echo = Echo()
    for cfg in configs:
        env_id, cdp, rt = start_env(args.api, env_body(cfg, args.extra_arg))
        try:
            value = cdp_eval(cdp, echo.url(cfg["label"]))
        finally:
            call(args.api, "POST", "/environments/%s/stop" % env_id, timeout=30)
        if not value.get("injected"):
            raise RuntimeError("fingerprint script never ran in %s" % cfg["label"])
        headers = echo.seen.get("/probe/" + cfg["label"], {})
        value["http_ua"] = headers.get("user-agent", "")
        value["ch_ua"] = headers.get("sec-ch-ua", "") + " platform=" + headers.get("sec-ch-ua-platform", "")
        fc = fc_fonts(rt.get("profile_dir", ""))
        value["fonts_fc"], value["fonts_expected"] = (None, None) if fc is None else (",".join(fc[0]), ",".join(fc[1]))
        rows.append({"label": cfg["label"], "config": cfg["fingerprint"], "seen": value})
    return rows


def compare(rows: list[dict], min_diff: int, must_differ: list[str]) -> dict:
    """Pairwise field comparison; a pair is linkable when too few fields differ or a must-differ field is equal."""
    pairs = []
    for a, b in combinations(rows, 2):
        same = [f for f in FIELDS if a["seen"].get(f) == b["seen"].get(f)]
        diff = len(FIELDS) - len(same)
        blocked = [f for f in must_differ if f in same]
        pairs.append({"a": a["label"], "b": b["label"], "diff": diff, "same": same,
                      "linkable": diff < min_diff or bool(blocked), "why": blocked})
    shared = [f for f in FIELDS if len({json.dumps(r["seen"].get(f)) for r in rows}) == 1] if len(rows) > 1 else []
    leaks = [{"label": r["label"], "field": f, "value": r["seen"].get(f)} for r in rows for f, mark in LEAKS.items()
             if mark in str(r["seen"].get(f) or "")]
    leaks += [{"label": r["label"], "field": "fonts", "value": r["seen"]["fonts"]} for r in rows
              if r["seen"].get("fonts_expected") is not None and r["seen"]["fonts_expected"] != r["seen"].get("fonts")]
    return {"pairs": pairs, "sharedByAll": shared, "leaks": leaks, "minDiff": min_diff, "mustDiffer": must_differ}


def print_report(rows: list[dict], result: dict) -> None:
    """Prints the per field values, the pair matrix and the linkable verdicts."""
    width = max(len(r["label"]) for r in rows)
    print("values seen by the page")
    for f in FIELDS + ["fonts_fc", "fonts_expected", "brands", "http_ua", "ch_ua"]:
        for r in rows:
            print("  %-9s %-*s %s" % (f, width, r["label"], str(r["seen"].get(f))[:140]))
    print("pair matrix (fields that differ out of %d, min %d)" % (len(FIELDS), result["minDiff"]))
    print("  " + " " * width + "  " + "  ".join(r["label"].ljust(width) for r in rows))
    for r in rows:
        cells = []
        for o in rows:
            p = next((p for p in result["pairs"] if {p["a"], p["b"]} == {r["label"], o["label"]}), None)
            cells.append(("-" if p is None else "%d%s" % (p["diff"], "!" if p["linkable"] else "")).ljust(width))
        print("  " + r["label"].ljust(width) + "  " + "  ".join(cells))
    print("shared by all: " + (", ".join(result["sharedByAll"]) or "none"))
    for leak in result["leaks"]:
        print("  LEAK %s %s %s" % (leak["label"], leak["field"], "contains " + LEAKS[leak["field"]]
                                   if leak["field"] in LEAKS else "differs from fc-list under its fonts.conf (Chrome equivalences applied)"))
    for p in result["pairs"]:
        tag = "LINKABLE" if p["linkable"] else "ok"
        print("  %s %s <-> %s diff=%d same=[%s]%s" % (tag, p["a"], p["b"], p["diff"], ", ".join(p["same"]),
                                                      " must-differ equal: " + ",".join(p["why"]) if p["why"] else ""))


def main() -> None:
    parser = argparse.ArgumentParser(description="hx-browser cross environment independence gate")
    parser.add_argument("--spawn", action="store_true", help="start the hx-browser API here (CI)")
    parser.add_argument("--api", default="", help="running API, default local 8325")
    parser.add_argument("--port", type=int, default=8325)
    parser.add_argument("--configs", default="", help="JSON list of configs, default DEFAULT_CONFIGS")
    parser.add_argument("--from", dest="source", default="", help="re-score a saved capture instead of starting browsers")
    parser.add_argument("--out", default=str(TMP / "independence.json"))
    parser.add_argument("--min-diff", type=int, default=7, help="a pair needs at least this many differing fields")
    parser.add_argument("--must-differ", default="canvas", help="comma list of fields that must never be equal")
    parser.add_argument("--extra-arg", action="append", default=[], help="extra Chromium arg for every environment")
    args = parser.parse_args()
    must = [f for f in args.must_differ.split(",") if f]
    pid = 0
    try:
        if args.source:
            rows = json.loads(Path(args.source).read_text(encoding="utf-8"))["rows"]
        else:
            if args.spawn:
                args.api = local(args.port)
                log = open(TMP / "hx-browser-api.log", "w", encoding="utf-8")  # noqa: SIM115 held by the child
                pid = subprocess.Popen([sys.executable, "run.py", "--host", "127.0.0.1", "--port", str(args.port)],
                                       cwd=PKG, stdout=log, stderr=subprocess.STDOUT, start_new_session=True).pid
            args.api = args.api or local(8325)
            if not wait_api(args.api, 60):
                raise RuntimeError("hx-browser API did not come up at %s" % args.api)
            configs = json.loads(Path(args.configs).read_text(encoding="utf-8")) if args.configs else DEFAULT_CONFIGS
            rows = capture(args, configs)
    except (OSError, ValueError, RuntimeError, KeyError, TypeError) as exc:
        print("unscored: %s" % exc, file=sys.stderr)
        sys.exit(UNSCORED)
    finally:
        if pid:
            stop_group(pid)
    result = compare(rows, args.min_diff, must)
    Path(args.out).write_text(json.dumps({"capturedAt": datetime.now(timezone.utc).isoformat(), "rows": rows, **result},
                                         ensure_ascii=False, indent=1), encoding="utf-8")
    print_report(rows, result)
    bad = [p for p in result["pairs"] if p["linkable"]]
    sys.exit(LINKABLE if bad or result["leaks"] else 0)


if __name__ == "__main__":
    main()
