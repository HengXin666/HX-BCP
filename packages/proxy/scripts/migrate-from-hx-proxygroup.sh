#!/usr/bin/env bash
#
# 把改名前的 HX-ProxyGroup 部署迁移到 hx-pxy 布局。
#
# 改名 (HX-ProxyGroup -> HX-Pxy) 换掉了全部机器标识符: 服务账号、systemd 单元、
# 安装根、配置/数据/日志目录, 以及数据库文件名。新版安装器按新布局工作, 在旧布局
# 上它会新建一个空数据目录 —— 订阅、节点、Listener 与管理员账号都留在原地, 而
# 表面症状只是"登录不进"。本脚本在升级前跑一次, 把旧布局搬到新路径上。
#
# 见 .agents/notes/implemented/architecture/2026-10-13-upgrade-adopts-pre-rename-installation.md
#
# 用法:
#   sudo bash scripts/migrate-from-hx-proxygroup.sh --dry-run   # 只打印将要执行的步骤
#   sudo bash scripts/migrate-from-hx-proxygroup.sh --yes       # 执行迁移
#
# 迁移之后再装新版:
#   curl -fsSLO https://raw.githubusercontent.com/HengXin666/hx-pxy/main/install.sh
#   sudo bash install.sh install
#
# 本脚本只搬文件、改账号名、停旧单元; 它不下载也不安装任何新版本, 因此任何一步
# 失败都可以按结尾打印的路径手工退回。
set -Eeuo pipefail

# 旧布局 (v0.12.0 及更早)。可用 HX_LEGACY_* 覆盖, 用于非默认安装根。
LEGACY_INSTALL_ROOT="${HX_LEGACY_INSTALL_ROOT:-/usr/local/lib/hx-proxygroup}"
LEGACY_INSTALLER_PATH="${HX_LEGACY_INSTALLER_PATH:-/usr/local/sbin/hx-proxygroup-install}"
LEGACY_CONFIG_DIR="${HX_LEGACY_CONFIG_DIR:-/etc/hx-proxygroup}"
LEGACY_DATA_DIR="${HX_LEGACY_DATA_DIR:-/var/lib/hx-proxygroup}"
LEGACY_LOG_DIR="${HX_LEGACY_LOG_DIR:-/var/log/hx-proxygroup}"
LEGACY_SERVICE_USER="${HX_LEGACY_SERVICE_USER:-hx-proxygroup}"
LEGACY_SERVICE_GROUP="${HX_LEGACY_SERVICE_GROUP:-hx-proxygroup}"
readonly LEGACY_UNITS=(hx-proxygroup.service hx-proxygroup-dataplane.service hx-proxygroup-terminal.service)

# 新布局。与 install.sh 的默认值保持一致。
INSTALL_ROOT="${HX_INSTALL_ROOT:-/usr/local/lib/hx-pxy}"
CONFIG_DIR="${HX_CONFIG_DIR:-/etc/hx-pxy}"
DATA_DIR="${HX_DATA_DIR:-/var/lib/hx-pxy}"
LOG_DIR="${HX_LOG_DIR:-/var/log/hx-pxy}"
UNIT_DIR="${HX_UNIT_DIR:-/etc/systemd/system}"
SERVICE_USER="${HX_SERVICE_USER:-hx-pxy}"
SERVICE_GROUP="${HX_SERVICE_GROUP:-hx-pxy}"

DRY_RUN="false"
ASSUME_YES="false"

log() { printf '[migrate] %s\n' "$*"; }
warn() { printf '[migrate] warning: %s\n' "$*" >&2; }
fail() { printf '[migrate] error: %s\n' "$*" >&2; exit 1; }

# run 是唯一的写入入口: --dry-run 下它只打印, 因此"预览"与"执行"走的是同一份代码
# 路径, 不会出现预览漏掉某一步、执行却做了的情况。
run() {
    if [[ "${DRY_RUN}" == "true" ]]; then
        printf '[dry-run] %s\n' "$*"
        return 0
    fi
    "$@"
}

usage() {
    sed -n '3,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --dry-run) DRY_RUN="true"; shift ;;
            --yes|-y) ASSUME_YES="true"; shift ;;
            -h|--help) usage; exit 0 ;;
            *) fail "unknown option: $1" ;;
        esac
    done
}

require_root() { [[ "${EUID}" -eq 0 ]] || fail "this script must run as root (or use --dry-run)"; }

have_legacy_installation() {
    [[ -d "${LEGACY_DATA_DIR}" || -d "${LEGACY_CONFIG_DIR}" || -d "${LEGACY_INSTALL_ROOT}" || -f "${UNIT_DIR}/${LEGACY_UNITS[0]}" ]]
}

new_layout_exists() {
    [[ -e "${DATA_DIR}" || -e "${INSTALL_ROOT}" || -f "${UNIT_DIR}/hx-pxy.service" ]]
}

# migrate_layout 只做文件系统动作, 不碰 systemd 也不碰账号 —— 因此它可以被测试
# 直接调用, 而"搬完一半失败"的风险被限制在纯 mv 上 (同盘 mv 是原子的)。
migrate_layout() {
    move_tree "${LEGACY_DATA_DIR}" "${DATA_DIR}"
    move_tree "${LEGACY_CONFIG_DIR}" "${CONFIG_DIR}"
    move_tree "${LEGACY_LOG_DIR}" "${LOG_DIR}"
    move_tree "${LEGACY_INSTALL_ROOT}" "${INSTALL_ROOT}"
    rename_database
}

# move_tree 只在目标还不存在时搬。目标已存在意味着这一步之前已经跑过, 或者是
# 有人在同一个位置放了别的东西 —— 两种情况下覆盖都比跳过危险。
move_tree() {
    local from="$1" to="$2"
    [[ -e "${from}" ]] || return 0
    if [[ -e "${to}" ]]; then
        warn "skipped ${from}: ${to} already exists"
        return 0
    fi
    log "moving ${from} -> ${to}"
    run mv -- "${from}" "${to}"
}

# rename_database 把 hx-proxygroup.db 连同 -wal / -shm 改名。WAL 模式下已提交数据
# 留在 -wal 里直到 checkpoint, 只改主文件会静默丢掉最近的写入。控制面自己也会在
# 启动时接管旧文件名, 但那只在同一个目录内生效 —— 这里先做好, 启动路径就永远不
# 需要猜。
rename_database() {
    local legacy_database="${DATA_DIR}/hx-proxygroup.db" suffix
    [[ -f "${legacy_database}" ]] || return 0
    if [[ -e "${DATA_DIR}/hx-pxy.db" ]]; then
        warn "skipped ${legacy_database}: ${DATA_DIR}/hx-pxy.db already exists"
        return 0
    fi
    for suffix in -wal -shm; do
        [[ ! -f "${legacy_database}${suffix}" ]] || run mv -- "${legacy_database}${suffix}" "${DATA_DIR}/hx-pxy.db${suffix}"
    done
    run mv -- "${legacy_database}" "${DATA_DIR}/hx-pxy.db"
    log "renamed database to ${DATA_DIR}/hx-pxy.db"
}

# rename_account 把服务账号与组就地改名。单元按名字引用它们, 所以必须改名而不是
# 新建: 新建会换 uid, 刚搬过来的数据就没人认领了。改名失败时保留原名并按现存的
# 名字 chown, 而不是把文件留给一个不存在的属主。
rename_account() {
    local owner_user="${SERVICE_USER}" owner_group="${SERVICE_GROUP}"
    if getent group "${SERVICE_GROUP}" >/dev/null; then
        owner_group="${SERVICE_GROUP}"
    elif getent group "${LEGACY_SERVICE_GROUP}" >/dev/null; then
        if run groupmod --new-name "${SERVICE_GROUP}" "${LEGACY_SERVICE_GROUP}"; then
            owner_group="${SERVICE_GROUP}"
        else
            owner_group="${LEGACY_SERVICE_GROUP}"
            warn "could not rename group ${LEGACY_SERVICE_GROUP}; files keep their current group"
        fi
    fi
    if id -u "${SERVICE_USER}" >/dev/null 2>&1; then
        owner_user="${SERVICE_USER}"
    elif id -u "${LEGACY_SERVICE_USER}" >/dev/null 2>&1; then
        if run usermod --login "${SERVICE_USER}" "${LEGACY_SERVICE_USER}"; then
            owner_user="${SERVICE_USER}"
        else
            owner_user="${LEGACY_SERVICE_USER}"
            warn "could not rename account ${LEGACY_SERVICE_USER}; files keep their current owner"
        fi
    fi
    if [[ -d "${DATA_DIR}" ]]; then
        run chown -R "${owner_user}:${owner_group}" "${DATA_DIR}" || warn "could not chown ${DATA_DIR}"
    fi
    if [[ "${owner_user}" == "${SERVICE_USER}" ]] && id -u "${SERVICE_USER}" >/dev/null 2>&1; then
        run usermod --home "${DATA_DIR}" "${SERVICE_USER}" || true
    fi
    RETURNED_OWNER="${owner_user}:${owner_group}"
}

# stop_legacy_units 必须先于安装新版: 两代控制面都绑 127.0.0.1:19090, 旧的不放手,
# 新的过不了 readiness, 安装器会回滚, 而管理员看到的是"升级失败"。
stop_legacy_units() {
    LEGACY_WAS_ACTIVE="false"
    if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "${LEGACY_UNITS[0]}"; then
        LEGACY_WAS_ACTIVE="true"
    fi
    local unit
    for unit in "${LEGACY_UNITS[@]}"; do
        if command -v systemctl >/dev/null 2>&1; then
            run systemctl disable --now "${unit}" >/dev/null 2>&1 || true
        fi
        [[ -f "${UNIT_DIR}/${unit}" ]] || continue
        run rm -f -- "${UNIT_DIR}/${unit}"
    done
    if [[ -f "${LEGACY_INSTALLER_PATH}" ]]; then
        run rm -f -- "${LEGACY_INSTALLER_PATH}"
    fi
    command -v systemctl >/dev/null 2>&1 && { run systemctl daemon-reload || true; }
    return 0
}

rollback_hint() {
    cat >&2 <<HINT
[migrate] 迁移未完成。当前可用的原始路径:
  数据:   ${LEGACY_DATA_DIR}
  配置:   ${LEGACY_CONFIG_DIR}
  安装根: ${LEGACY_INSTALL_ROOT}
  日志:   ${LEGACY_LOG_DIR}
已完成的那几步是纯 mv, 按上面路径反向 mv 回去即可; 服务账号改名只影响名字, uid 不变。
HINT
    if [[ "${LEGACY_WAS_ACTIVE:-false}" == "true" ]] && command -v systemctl >/dev/null 2>&1; then
        warn "attempting to restart the pre-rename services"
        systemctl start "${LEGACY_UNITS[@]}" 2>/dev/null || true
    fi
}

main() {
    parse_arguments "$@"
    [[ "${DRY_RUN}" == "true" ]] || require_root
    if ! have_legacy_installation; then
        log "no pre-rename installation found (${LEGACY_DATA_DIR} and ${LEGACY_CONFIG_DIR} are absent); nothing to do"
        return 0
    fi
    if new_layout_exists; then
        fail "the hx-pxy layout already exists (${DATA_DIR} or ${INSTALL_ROOT} or hx-pxy.service); this script migrates once and never merges. Inspect both layouts by hand before continuing."
    fi
    if [[ "${DRY_RUN}" == "false" && "${ASSUME_YES}" == "false" ]]; then
        if [[ ! -t 0 ]]; then
            fail "refusing to migrate without confirmation on a non-interactive stdin; pass --yes"
        fi
        log "will stop ${LEGACY_UNITS[*]}, move the data/configuration/log/install trees into place and rename the database and service account"
        read -r -p "[migrate] continue? [y/N] " answer
        [[ "${answer}" =~ ^[Yy]$ ]] || fail "aborted before making any change"
    fi
    trap rollback_hint ERR
    stop_legacy_units
    migrate_layout
    rename_account
    trap - ERR
    log "migration finished; data now lives in ${DATA_DIR} and the service account is ${RETURNED_OWNER:-${SERVICE_USER}:${SERVICE_GROUP}}"
    cat <<NEXT

下一步 —— 安装新版并启动:
  curl -fsSLO https://raw.githubusercontent.com/HengXin666/hx-pxy/main/install.sh
  sudo bash install.sh install

安装完成后, 后续升级都是一行: sudo hx-pxy-install upgrade
NEXT
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
