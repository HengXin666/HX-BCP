#!/usr/bin/env bash
set -Eeuo pipefail

readonly REPO_URL="https://github.com/daimon3332/easy-proxies"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly PROJECT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
readonly TARGET_DIR="${PROJECT_ROOT}/ref/easy-proxies"

log() {
    printf '[sync-reference] %s\n' "$*"
}

fail() {
    printf '[sync-reference] error: %s\n' "$*" >&2
    exit 1
}

command -v git >/dev/null 2>&1 || fail "git is required"

if [[ ! -e "${TARGET_DIR}" ]]; then
    log "cloning ${REPO_URL} into ${TARGET_DIR}"
    git clone --depth 1 "${REPO_URL}" "${TARGET_DIR}"
    log "easy-proxies reference cloned"
    exit 0
fi

[[ -d "${TARGET_DIR}/.git" ]] || fail "${TARGET_DIR} exists but is not a Git repository"

if [[ -n "$(git -C "${TARGET_DIR}" status --porcelain)" ]]; then
    fail "reference repository has local changes; refusing to overwrite"
fi

log "fetching the latest main branch"
git -C "${TARGET_DIR}" fetch --depth 1 origin main
git -C "${TARGET_DIR}" merge --ff-only FETCH_HEAD
log "easy-proxies reference updated"
