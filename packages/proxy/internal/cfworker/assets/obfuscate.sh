#!/usr/bin/env bash
# obfuscate.sh — HX-CF-Tunnel 混淆脚本（多套混淆算法，防风控）
#
# 用途：把 CF worker 脚本（cfnew / BPB）或本地 tunnel.js 混淆成多个不同版本，
#       每个 worker 用不同混淆算法组合 → 即使某个混淆被 CF/目标风控识别，
#       其余 worker 仍可用（批量部署时错开混淆）。
#
# 用法：
#   ./obfuscate.sh <输入js> <输出目录> [配方名]
#     - 输入js：明文 worker 脚本（如 cfnew 明文版 / BPB dist/worker.js）
#     - 输出目录：混淆产物目录（缺省 ./dist-obf）
#     - 配方名：recipes_* 里的某套（缺省 = 全部配方，各产一个文件）
#     - 环境变量 OB_EXTRA 可追加任意 javascript-obfuscator 参数
#
# 配方（recipe_* 函数）：每套是不同算法组合，覆盖低/中/高混淆强度。
# 全部保留 `import`（ES module worker 必须），混淆不破坏运行。

set -euo pipefail

INPUT="${1:?用法: ./obfuscate.sh <输入js> <输出目录> [配方名]}"
OUT_DIR="${2:-dist-obf}"
ONLY="${3:-}"

OB_CMD="javascript-obfuscator"
command -v "$OB_CMD" >/dev/null || { echo "需要 javascript-obfuscator（npm i -g javascript-obfuscator）"; exit 1; }

mkdir -p "$OUT_DIR"
INPUT_NAME="$(basename "$INPUT")"
echo "输入: $INPUT ($(wc -c < "$INPUT") 字节)"

# ---- 配方：每套返回要拼到命令末尾的参数串（bash 数组）----
# 配方 1：标准字符串数组 + 控制流 + 死代码（中强度，cfnew 混淆版同款风格）
recipe_std() {
  echo "--compact true --string-array true --string-array-encoding rc4 \
--string-array-threshold 1 --string-array-rotate true --string-array-shuffle true \
--string-array-indexes-type hexadecimal-number \
--control-flow-flattening true --control-flow-flattening-threshold 0.75 \
--dead-code-injection true --dead-code-injection-threshold 0.4 \
--numbers-to-expressions true --simplify true --transform-object-keys true \
--identifier-names-generator hexadecimal --rename-globals false \
--ignore-imports true --log false"
}
# 配方 2：高强度（debug protection 间隔 + 自防御 + 属性重命名关闭防破坏）
recipe_high() {
  echo "--compact true --string-array true --string-array-encoding base64 \
--string-array-threshold 1 --string-array-rotate true --string-array-shuffle true \
--control-flow-flattening true --control-flow-flattening-threshold 1 \
--dead-code-injection true --dead-code-injection-threshold 0.5 \
--numbers-to-expressions true --simplify true --transform-object-keys true \
--self-defending true --debug-protection true --debug-protection-interval 2000 \
--identifier-names-generator mangled-shuffled --rename-globals false \
--ignore-imports true --log false"
}
# 配方 3：轻量（仅字符串数组 + 控制流，体积小、稳）
recipe_light() {
  echo "--compact true --string-array true --string-array-encoding rc4 \
--string-array-threshold 0.8 --string-array-rotate true --string-array-shuffle true \
--control-flow-flattening true --control-flow-flattening-threshold 0.5 \
--simplify true --identifier-names-generator hexadecimal --rename-globals false \
--ignore-imports true --log false"
}
# 配方 4：字典标识符（可读性最低的变量名） + 死代码
recipe_dict() {
  echo "--compact true --string-array true --string-array-encoding rc4 \
--string-array-threshold 1 --string-array-rotate true --string-array-shuffle true \
--identifier-names-generator mangled-shuffled \
--control-flow-flattening true --control-flow-flattening-threshold 0.6 \
--dead-code-injection true --dead-code-injection-threshold 0.3 \
--simplify true --ignore-imports true --log false"
}

declare -A RECIPES=(
  [std]=recipe_std
  [high]=recipe_high
  [light]=recipe_light
  [dict]=recipe_dict
)

run_one() {
  local name="$1"
  local args
  args="$(${RECIPES[$name]}) ${OB_EXTRA:-}"
  local out="$OUT_DIR/${INPUT_NAME%.js}.$name.js"
  # shellcheck disable=SC2086
  $OB_CMD "$INPUT" --output "$out" $args 2>"$OUT_DIR/$name.err" || {
    echo "  ✗ $name 混淆失败（见 $OUT_DIR/$name.err）"
    return 1
  }
  echo "  ✓ $name: $out ($(wc -c < "$out") 字节)"
}

echo "=== 混淆配方 ==="
if [ -n "$ONLY" ]; then
  [[ -n "${RECIPES[$ONLY]:-}" ]] || { echo "未知配方: $ONLY（可用: ${!RECIPES[*]}）"; exit 1; }
  run_one "$ONLY"
else
  for name in "${!RECIPES[@]}"; do
    run_one "$name"
  done
fi
echo "=== 完成: $OUT_DIR ==="
ls -la "$OUT_DIR" | grep -E "\.js$" || true
