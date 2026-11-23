#!/usr/bin/env python3
"""Drive the HX-ProxyGroup management API from its own capability catalog.

The control plane publishes GET /api/v1/capabilities: every management endpoint
with its field names, closed enums and an accepted example. This script reads
that document instead of hard-coding a field list, so it cannot drift from the
server. Use it whenever you need to know a field name, an enum value, or
whether a body you are about to send will be rejected.

    # What can I call, and with which values?
    hx-catalog.py enums
    hx-catalog.py endpoints
    hx-catalog.py show proxy_service.create

    # Will this body be accepted? (checks enums locally, no write happens)
    hx-catalog.py check quickstart.create --body '{"name":"hk","subscription_url":"https://x/s"}'

    # One call to a working proxy; prints the share path for consumers.
    hx-catalog.py quickstart --body '{"name":"hk","subscription_url":"https://x/s"}'

Environment (a flag wins over the variable):
    HX_BASE_URL   e.g. https://proxy.example.com   (default http://127.0.0.1:8080)
    HX_API_KEY    the API key from Settings -> API keys

Exit codes: 0 ok, 1 request/validation failed, 2 usage error.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request

DEFAULT_BASE_URL = "http://127.0.0.1:8080"
TIMEOUT_SECONDS = 30


def request_json(base_url: str, api_key: str, method: str, path: str, body: dict | None):
    """Call the API and return (status, decoded_body). A non-2xx status is
    returned rather than raised so the caller can print the server's error code,
    which is part of the contract and more useful than a stack trace."""
    data = None
    headers = {"Accept": "application/json"}
    if api_key:
        headers["Authorization"] = "Bearer " + api_key
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    url = base_url.rstrip("/") + path
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT_SECONDS) as response:
            return response.status, decode(response.read())
    except urllib.error.HTTPError as error:
        return error.code, decode(error.read())
    except urllib.error.URLError as error:
        raise SystemExit(f"cannot reach {url}: {error.reason}")


def decode(raw: bytes):
    if not raw:
        return None
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw.decode("utf-8", "replace")


def fetch_catalog(base_url: str, api_key: str) -> dict:
    status, payload = request_json(base_url, api_key, "GET", "/api/v1/capabilities", None)
    if status != 200:
        raise SystemExit(
            f"GET /api/v1/capabilities returned {status}: {payload}\n"
            "The catalog needs the same credential as any other /api/v1 call. "
            "Set HX_API_KEY, or pass --api-key."
        )
    return payload


def find_endpoint(catalog: dict, endpoint_id: str) -> dict:
    for endpoint in catalog.get("endpoints", []):
        if endpoint.get("id") == endpoint_id:
            return endpoint
    known = ", ".join(sorted(e.get("id", "?") for e in catalog.get("endpoints", [])))
    raise SystemExit(f"unknown endpoint id {endpoint_id!r}. Known ids: {known}")


def enum_values(catalog: dict) -> dict:
    return {enum["name"]: enum.get("values", []) for enum in catalog.get("enums", [])}


def check_body(catalog: dict, endpoint: dict, body: dict) -> list[str]:
    """Return a list of problems a local check can find. This checks the two
    things the server is strict about and that a caller most often gets wrong:
    an unknown field is rejected outright, and a closed enum rejects any value
    outside its list. It cannot replace server validation."""
    problems: list[str] = []
    known = {field["name"] for field in endpoint.get("fields", [])}
    values = enum_values(catalog)

    # A nested field key ("source_spec.limit") belongs to a sub-object; a flat
    # body must not be told to declare it at the top level.
    top_level = {name for name in known if "." not in name}
    for key in body:
        if key not in top_level:
            problems.append(f"unknown field {key!r}; the server rejects unknown fields rather than ignoring them")

    for field in endpoint.get("fields", []):
        enum_name = field.get("enum")
        if not enum_name:
            continue
        value = lookup(body, field["name"])
        if value is None:
            continue
        allowed = values.get(enum_name, [])
        candidates = value if isinstance(value, list) else [value]
        for candidate in candidates:
            if candidate not in allowed:
                problems.append(
                    f"{field['name']} = {candidate!r} is not in enum {enum_name}: {allowed}"
                )
    return problems


def lookup(body: dict, dotted: str):
    current = body
    for part in dotted.split("."):
        if not isinstance(current, dict) or part not in current:
            return None
        current = current[part]
    return current


def parse_body(raw: str | None) -> dict:
    if not raw:
        raise SystemExit("--body is required (a JSON object)")
    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError as error:
        raise SystemExit(f"--body is not valid JSON: {error}")
    if not isinstance(parsed, dict):
        raise SystemExit("--body must be a JSON object")
    return parsed


def command_enums(catalog: dict, _args) -> int:
    for enum in catalog.get("enums", []):
        default = enum.get("default")
        suffix = f"  (default {default})" if default else ""
        print(f"{enum['name']}{suffix}")
        print(f"    {', '.join(enum.get('values', []))}")
        if enum.get("description"):
            print(f"    {enum['description']}")
    return 0


def command_endpoints(catalog: dict, _args) -> int:
    for endpoint in catalog.get("endpoints", []):
        print(f"{endpoint['id']:28} {endpoint.get('method', '?'):5} {endpoint.get('path', '?')}")
        if endpoint.get("summary"):
            print(f"    {endpoint['summary']}")
    return 0


def command_show(catalog: dict, args) -> int:
    endpoint = find_endpoint(catalog, args.endpoint_id)
    print(json.dumps(endpoint, indent=2, ensure_ascii=False))
    return 0


def command_check(catalog: dict, args) -> int:
    endpoint = find_endpoint(catalog, args.endpoint_id)
    body = parse_body(args.body)
    problems = check_body(catalog, endpoint, body)
    if problems:
        for problem in problems:
            print("FAIL " + problem)
        return 1
    print(f"ok   {args.endpoint_id}: no locally detectable problem")
    print("     The server still validates semantics (existence, ranges, cycles).")
    return 0


def command_call(catalog: dict, args) -> int:
    endpoint = find_endpoint(catalog, args.endpoint_id)
    body = parse_body(args.body)
    problems = check_body(catalog, endpoint, body)
    if problems:
        # Refuse to send a body already known to be rejected: the whole point of
        # reading the catalog is to avoid a round trip that cannot succeed.
        for problem in problems:
            print("FAIL " + problem)
        return 1
    method = endpoint.get("method", "POST")
    path = endpoint.get("path", "")
    if "<" in path:
        raise SystemExit(f"{args.endpoint_id} needs path parameters, which this command does not fill in")
    status, payload = request_json(args.base_url, args.api_key, method, path, body)
    print(json.dumps(payload, indent=2, ensure_ascii=False))
    return 0 if 200 <= status < 300 else 1


def command_quickstart(catalog: dict, args) -> int:
    # The one call that reaches a working proxy. Printing the share path is what
    # the caller actually needs next, so it is surfaced explicitly.
    endpoint = find_endpoint(catalog, "quickstart.create")
    body = parse_body(args.body)
    problems = check_body(catalog, endpoint, body)
    if problems:
        for problem in problems:
            print("FAIL " + problem)
        return 1
    status, payload = request_json(args.base_url, args.api_key, "POST", endpoint["path"], body)
    if not 200 <= status < 300:
        print(json.dumps(payload, indent=2, ensure_ascii=False))
        return 1
    result = payload or {}
    print("group:    " + str(lookup(result, "group.id")))
    print("listener: " + str(lookup(result, "listener.id")))
    print("share:    " + str(result.get("share_path")))
    print("nodes:    " + str(result.get("consumer_nodes_path")))
    auth = result.get("auth") or {}
    if auth:
        print("auth:     " + str(auth.get("username")) + " / " + str(auth.get("password")))
    if result.get("workflow"):
        print("steps:")
        for step in result["workflow"]:
            print("  - " + step)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--base-url", default=os.environ.get("HX_BASE_URL", DEFAULT_BASE_URL))
    parser.add_argument("--api-key", default=os.environ.get("HX_API_KEY", ""))
    subparsers = parser.add_subparsers(dest="command", required=True)

    subparsers.add_parser("enums", help="list every closed vocabulary")
    subparsers.add_parser("endpoints", help="list every management endpoint")

    show = subparsers.add_parser("show", help="print one endpoint's full description")
    show.add_argument("endpoint_id")

    check = subparsers.add_parser("check", help="validate a body locally without sending it")
    check.add_argument("endpoint_id")
    check.add_argument("--body", required=True)

    call = subparsers.add_parser("call", help="validate a body, then send it")
    call.add_argument("endpoint_id")
    call.add_argument("--body", required=True)

    quickstart = subparsers.add_parser("quickstart", help="reach a working proxy in one call")
    quickstart.add_argument("--body", required=True)

    args = parser.parse_args()
    catalog = fetch_catalog(args.base_url, args.api_key)
    handlers = {
        "enums": command_enums,
        "endpoints": command_endpoints,
        "show": command_show,
        "check": command_check,
        "call": command_call,
        "quickstart": command_quickstart,
    }
    return handlers[args.command](catalog, args)


if __name__ == "__main__":
    sys.exit(main())
