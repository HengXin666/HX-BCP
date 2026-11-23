#!/usr/bin/env bash
set -Eeuo pipefail

proxy=""
if [[ $# -ge 2 && "$1" == "--proxy" ]]; then
    proxy="$2"
    shift 2
fi

if [[ -n "${proxy}" ]]; then
    export HTTP_PROXY="${proxy}"
    export HTTPS_PROXY="${proxy}"
    export ALL_PROXY="${proxy}"
    export NO_PROXY="127.0.0.1,localhost"
fi

if [[ $# -eq 0 ]]; then
    set -- ./...
fi

exec go test -mod=mod "$@"
