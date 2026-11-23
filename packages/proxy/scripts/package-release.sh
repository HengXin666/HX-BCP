#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION=""
ARCH=""
CONTROL_BINARY=""
MIHOMO_BINARY=""
WEB_DIR=""
OUTPUT_DIR=""
# Fleet deploy assets: default to the repo-embedded copies (internal/cfworker/
# assets, the same files embedded into the binary via go:embed) so the release
# workflow works on CI without the development HX-CF-Tunnel checkout. Point
# --cfworker-dir at HX-CF-Tunnel to prefer newer local copies.
CFWORKER_DIR="${CFWORKER_DIR:-${ROOT_DIR}/internal/cfworker/assets}"

fail() { printf 'package-release: %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version) VERSION="${2:-}"; shift 2 ;;
        --arch) ARCH="${2:-}"; shift 2 ;;
        --control) CONTROL_BINARY="${2:-}"; shift 2 ;;
        --mihomo) MIHOMO_BINARY="${2:-}"; shift 2 ;;
        --web-dir) WEB_DIR="${2:-}"; shift 2 ;;
        --cfworker-dir) CFWORKER_DIR="${2:-}"; shift 2 ;;
        --output-dir) OUTPUT_DIR="${2:-}"; shift 2 ;;
        *) fail "unknown option: $1" ;;
    esac
done

[[ "${VERSION}" =~ ^[A-Za-z0-9._-]+$ ]] || fail "--version is required"
case "${ARCH}" in amd64|arm64) ;; *) fail "--arch must be amd64 or arm64" ;; esac
[[ -x "${CONTROL_BINARY}" ]] || fail "--control must be an executable"
[[ -x "${MIHOMO_BINARY}" ]] || fail "--mihomo must be an executable"
[[ -f "${WEB_DIR}/index.html" ]] || fail "--web-dir must contain index.html"
[[ -n "${OUTPUT_DIR}" ]] || fail "--output-dir is required"

work_dir="$(mktemp -d)"
trap 'rm -rf -- "${work_dir}"' EXIT
stage="${work_dir}/stage"
install -d -m 0755 "${stage}/bin" "${stage}/web" "${stage}/deploy/systemd" "${OUTPUT_DIR}"
install -m 0755 "${CONTROL_BINARY}" "${stage}/bin/hx-pxyd"
install -m 0755 "${MIHOMO_BINARY}" "${stage}/bin/mihomo"
install -m 0755 "${ROOT_DIR}/install.sh" "${stage}/install.sh"
install -m 0644 "${ROOT_DIR}/LICENSE" "${stage}/LICENSE"
cp -a "${WEB_DIR}/." "${stage}/web/"
# Fleet deploy assets (20260823): the cfnew worker template + HX-CF-Tunnel
# obfuscation script travel inside the bundle so remote installations can run
# the fleet without SSH access to the development machine.
install -d -m 0755 "${stage}/cfworker"
install -m 0644 "${CFWORKER_DIR}/worker.js" "${stage}/cfworker/worker.js"
install -m 0755 "${CFWORKER_DIR}/obfuscate.sh" "${stage}/cfworker/obfuscate.sh"
cp -a "${ROOT_DIR}/deploy/systemd/." "${stage}/deploy/systemd/"

bundle_name="hx-pxy_${VERSION}_linux_${ARCH}.tar.gz"
tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner -C "${stage}" -czf "${OUTPUT_DIR}/${bundle_name}" .
checksum="$(sha256sum "${OUTPUT_DIR}/${bundle_name}" | awk '{print $1}')"
checksum_stage="${work_dir}/SHA256SUMS"
if [[ -f "${OUTPUT_DIR}/SHA256SUMS" ]]; then
    awk -v name="${bundle_name}" '$2 != name {print}' "${OUTPUT_DIR}/SHA256SUMS" >"${checksum_stage}"
fi
printf '%s  %s\n' "${checksum}" "${bundle_name}" >>"${checksum_stage}"
mv -f -- "${checksum_stage}" "${OUTPUT_DIR}/SHA256SUMS"
printf '%s\n' "${VERSION}" >"${OUTPUT_DIR}/VERSION"
printf '%s\n' "${OUTPUT_DIR}/${bundle_name}"
