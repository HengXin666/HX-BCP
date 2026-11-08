#!/usr/bin/env bash
# shellcheck shell=bash
# 前端开发服务器的探测, 依赖安装与启动, 供 run.sh source.

detect_frontend_package_manager() {
    if [[ -f "${FRONTEND_DIR}/pnpm-lock.yaml" ]]; then
        printf '%s\n' pnpm
    elif [[ -f "${FRONTEND_DIR}/yarn.lock" ]]; then
        printf '%s\n' yarn
    elif [[ -f "${FRONTEND_DIR}/bun.lock" || -f "${FRONTEND_DIR}/bun.lockb" ]]; then
        printf '%s\n' bun
    else
        printf '%s\n' npm
    fi
}

install_frontend_dependencies() {
    local package_manager="$1"
    log "installing frontend dependencies with ${package_manager}"
    case "${package_manager}" in
        pnpm)
            (cd "${FRONTEND_DIR}" && pnpm install --frozen-lockfile) 2>&1 | tee -a "${RUN_LOG}"
            ;;
        yarn)
            (cd "${FRONTEND_DIR}" && yarn install --frozen-lockfile) 2>&1 | tee -a "${RUN_LOG}"
            ;;
        bun)
            (cd "${FRONTEND_DIR}" && bun install --frozen-lockfile) 2>&1 | tee -a "${RUN_LOG}"
            ;;
        npm)
            if [[ -f "${FRONTEND_DIR}/package-lock.json" ]]; then
                (cd "${FRONTEND_DIR}" && npm ci) 2>&1 | tee -a "${RUN_LOG}"
            else
                (cd "${FRONTEND_DIR}" && npm install) 2>&1 | tee -a "${RUN_LOG}"
            fi
            ;;
        *)
            fail "unsupported frontend package manager: ${package_manager}"
            ;;
    esac
}

start_frontend_if_available() {
    if [[ "${BACKEND_ONLY}" == "true" ]]; then
        log "frontend disabled by --backend-only"
        return
    fi
    if [[ ! -f "${FRONTEND_DIR}/package.json" ]]; then
        if [[ "${REQUIRE_FRONTEND}" == "true" ]]; then
            fail "frontend package.json not found: ${FRONTEND_DIR}/package.json"
        fi
        warn "frontend package.json is missing; backend continues without the management UI"
        return
    fi

    local package_manager
    package_manager="$(detect_frontend_package_manager)"
    require_command "${package_manager}"
    if [[ ! -d "${FRONTEND_DIR}/node_modules" ]]; then
        if [[ "${INSTALL_FRONTEND_DEPS}" != "true" ]]; then
            fail "frontend dependencies are missing; rerun with --install-frontend-deps"
        fi
        install_frontend_dependencies "${package_manager}"
    fi

    local probe_host="${FRONTEND_HOST}"
    if [[ "${probe_host}" == "0.0.0.0" || "${probe_host}" == "::" ]]; then
        probe_host="127.0.0.1"
    fi
    if [[ -z "${FRONTEND_PORT}" ]]; then
        FRONTEND_PORT="$(find_available_random_high_port "${probe_host}")"
    else
        local requested_port="${FRONTEND_PORT}"
        local maximum_port=$((requested_port + 20))
        ((maximum_port > 65535)) && maximum_port=65535
        while ((FRONTEND_PORT <= maximum_port)); do
            if ! tcp_port_in_use "${probe_host}" "${FRONTEND_PORT}"; then
                break
            fi
            FRONTEND_PORT=$((FRONTEND_PORT + 1))
        done
        if ((FRONTEND_PORT > maximum_port)); then
            fail "no available frontend port between ${requested_port} and ${maximum_port}"
        fi
        if [[ "${FRONTEND_PORT}" != "${requested_port}" ]]; then
            warn "frontend port ${requested_port} is in use; using ${FRONTEND_PORT} instead"
        fi
    fi

    local api_base="http://${LISTEN_ADDRESS}"
    log "starting frontend at http://${FRONTEND_HOST}:${FRONTEND_PORT}"
    log "frontend log: ${FRONTEND_LOG}"
    (
        cd "${FRONTEND_DIR}"
        export VITE_BACKEND_TARGET="${api_base}"
        # `--` is required to forward flags to the underlying Vite CLI only for
        # npm and yarn, which consume unknown flags themselves. pnpm and bun
        # forward everything after the script name verbatim, so passing `--`
        # there hands it to Vite as a positional argument and the flags are
        # dropped — Vite then falls back to its default port 5173 and dies with
        # `Port 5173 is already in use` instead of using ${FRONTEND_PORT}.
        local forward_terminator="--"
        case "${package_manager}" in
            pnpm | bun) forward_terminator="" ;;
        esac
        # shellcheck disable=SC2086 # the empty value must disappear, not become an empty argument
        exec setsid "${package_manager}" run dev ${forward_terminator} \
            --host "${FRONTEND_HOST}" \
            --port "${FRONTEND_PORT}" \
            --strictPort \
            --clearScreen false
    ) >>"${FRONTEND_LOG}" 2>&1 &
    FRONTEND_PID="$!"
    FRONTEND_PROCESS_GROUP="${FRONTEND_PID}"
    chmod 0600 "${FRONTEND_LOG}"
}

