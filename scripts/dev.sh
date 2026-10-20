#!/usr/bin/env bash
# 本地一键起三段链路 (可控开关), 只用于开发.
set -euo pipefail

WITH_CACHE="${WITH_CACHE:-1}"
WITH_BROWSER="${WITH_BROWSER:-1}"
UPSTREAM="${UPSTREAM:-}"

pids=()
cleanup() {
    for pid in "${pids[@]:-}"; do
        kill "$pid" 2>/dev/null || true
    done
}
trap cleanup EXIT INT TERM

if [ "$WITH_CACHE" = "1" ]; then
    cd packages/cache-proxy
    if [ -n "$UPSTREAM" ]; then
        uv run hxcp serve --port 8898 --upstream "$UPSTREAM" &
    else
        uv run hxcp serve --port 8898 &
    fi
    pids+=($!)
    cd ../..
    echo "缓存代理: http://127.0.0.1:8898"
fi

if [ "$WITH_BROWSER" = "1" ]; then
    cd packages/browser
    uv run hxb serve --port 8325 &
    pids+=($!)
    cd ../..
    echo "浏览器服务: http://127.0.0.1:8325"
fi

wait
