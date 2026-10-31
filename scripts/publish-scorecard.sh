#!/usr/bin/env bash
# 把每个内核的评分卡 SVG 与总览 SVG 发布到 fingerprint-scorecard 分支, 并贴回 PR 评论与步骤摘要.
#
# GitHub 只渲染能被公开拉取的图片 URL, artifact 链接不行, data URI 会被过滤.
# 所以把 SVG 提交到一个固定分支, 再用 raw 地址引用.
#
# 环境变量
#   GH_TOKEN  必需, 需要 contents:write 推分支, pull-requests:write 评论
#   SVG_DIR   必需, score.ts 多内核模式写出的目录, 含 all-kernels.svg 与 kernel-<kernel>.svg
#   TXT       可选, score.ts 的文本输出
#   PR        可选, PR 号, 为空时按 main 推送处理
#   REPO      可选, 默认 $GITHUB_REPOSITORY
#   BRANCH    可选, 默认 fingerprint-scorecard
set -euo pipefail

REPO="${REPO:-${GITHUB_REPOSITORY:?GITHUB_REPOSITORY 未设置}}"
BRANCH="${BRANCH:-fingerprint-scorecard}"
SVG_DIR="${SVG_DIR:?SVG_DIR 未设置}"
PR="${PR:-}"
MARKER="<!-- hx-scorecard -->"

shopt -s nullglob
KERNEL_SVGS=("$SVG_DIR"/kernel-*.svg)
if [ ! -s "$SVG_DIR/all-kernels.svg" ] && [ "${#KERNEL_SVGS[@]}" = "0" ]; then
    echo "::warning::没有评分卡文件, 跳过发布"
    exit 0
fi

# 每个 PR 一个目录, 避免并发 run 互相覆盖, main 推送更新同一个 latest 目录
if [ -n "$PR" ]; then
    PREFIX="scorecards/pr-${PR}"
else
    PREFIX="scorecards/latest"
fi
RAW_BASE="https://raw.githubusercontent.com/${REPO}/${BRANCH}/${PREFIX}"

ensure_branch() {
    if gh api "repos/${REPO}/git/ref/heads/${BRANCH}" >/dev/null 2>&1; then
        return 0
    fi
    local base
    base=$(gh api "repos/${REPO}/git/ref/heads/main" --jq .object.sha)
    gh api -X POST "repos/${REPO}/git/refs" -f ref="refs/heads/${BRANCH}" -f sha="$base" >/dev/null
}

# put_file 本地文件 仓库内路径, 已存在时带上 sha 覆盖
put_file() {
    local file="$1" path="$2" sha b64
    sha=$(gh api "repos/${REPO}/contents/${path}?ref=${BRANCH}" --jq .sha 2>/dev/null || true)
    b64=$(base64 -w0 "$file")
    local args=(-X PUT "repos/${REPO}/contents/${path}"
        -f message="ci(scorecard): 更新 ${path}"
        -f content="$b64"
        -f branch="${BRANCH}")
    if [ -n "$sha" ]; then
        args+=(-f sha="$sha")
    fi
    gh api "${args[@]}" >/dev/null
}

# publish 本地文件 仓库内路径, 并发 run 写同一个分支会 409, 重取 sha 后重试
publish() {
    local attempt
    for attempt in 1 2 3; do
        if put_file "$1" "$2"; then
            return 0
        fi
        echo "发布 $2 第 ${attempt} 次失败, 重试" >&2
        sleep $((attempt * 2))
    done
    return 1
}

PUSHED=1
if ! ensure_branch; then
    PUSHED=0
fi
FILES=()
[ -s "$SVG_DIR/all-kernels.svg" ] && FILES+=("$SVG_DIR/all-kernels.svg")
FILES+=("${KERNEL_SVGS[@]}")
for file in "${FILES[@]}"; do
    if [ "$PUSHED" = "1" ] && ! publish "$file" "${PREFIX}/$(basename "$file")"; then
        PUSHED=0
    fi
done
if [ "$PUSHED" = "0" ]; then
    echo "::warning::评分卡没能全部推到 ${BRANCH} 分支 (可能缺 contents:write), 只写文字摘要"
fi

# 结构化记录: 交互页读 data/, 与图分开存. BUNDLE 缺失或 PR 未知时跳过, 不影响出图
if [ -n "${BUNDLE:-}" ] && [ -s "$BUNDLE" ] && [ -n "$PR" ] && [ "$PUSHED" = "1" ]; then
    if bash scripts/scorecard-record.sh "$BUNDLE" "$PR" "$SVG_DIR" "$REPO" "$BRANCH"; then
        echo "已写 data/pr-${PR}"
    else
        echo "::warning::结构化记录没写成功, 交互页这次不会有数据"
    fi
fi

# 代码块围栏单独存变量, 直接写反引号会被当成命令替换
FENCE=$(printf '%s' '```')
NL=$'\n'
SECTIONS="## 指纹评分卡, 全部内核${NL}${NL}"
if [ "$PUSHED" = "1" ] && [ -s "$SVG_DIR/all-kernels.svg" ]; then
    SECTIONS="${SECTIONS}![全部内核](${RAW_BASE}/all-kernels.svg)${NL}${NL}"
fi
for file in "${KERNEL_SVGS[@]}"; do
    name=$(basename "$file" .svg)
    kernel="${name#kernel-}"
    SECTIONS="${SECTIONS}### 内核 ${kernel}${NL}${NL}"
    if [ "$PUSHED" = "1" ]; then
        SECTIONS="${SECTIONS}![内核 ${kernel}](${RAW_BASE}/${name}.svg)${NL}${NL}"
    else
        SECTIONS="${SECTIONS}图没发布出去, 见 artifact fingerprint-sites${NL}${NL}"
    fi
done
BODY="${MARKER}${NL}${SECTIONS}"
if [ -n "${TXT:-}" ] && [ -s "${TXT}" ]; then
    BODY="${BODY}<details><summary>文字版</summary>${NL}${NL}${FENCE}${NL}$(grep -v '^RESULT ' "$TXT")${NL}${FENCE}${NL}</details>${NL}${NL}"
fi
BODY="${BODY}<sub>分数是公开检测站自己写出的结论, 检测站改版, 扫描没跑完, 内核未安装或不说 CDP 都记为未测, 不是 0 分, 也不是产品回归.</sub>"

# 步骤摘要, 同一份图, 不依赖评论权限
echo "$SECTIONS" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"

if [ -z "$PR" ]; then
    echo "评分卡: ${RAW_BASE} (pushed=${PUSHED})"
    exit 0
fi

# 用 jq 的 --arg 传 marker, 直接把变量拼进 gh --jq 的双引号里会被 shell 拆词
COMMENT_ID=$(gh api "repos/${REPO}/issues/${PR}/comments" --paginate --slurp 2>/dev/null \
    | jq -r --arg m "$MARKER" 'add | .[] | select(.body | startswith($m)) | .id' 2>/dev/null \
    | head -n 1) || true

code=1
if [ -n "$COMMENT_ID" ]; then
    gh api -X PATCH "repos/${REPO}/issues/comments/${COMMENT_ID}" -f body="$BODY" >/dev/null && code=0 || code=$?
else
    gh api -X POST "repos/${REPO}/issues/${PR}/comments" -f body="$BODY" >/dev/null && code=0 || code=$?
fi

if [ "$code" != "0" ]; then
    echo "::warning::评分卡没能贴到 PR #${PR} (fork PR 的 token 只读), 已写入步骤摘要"
fi
echo "评分卡: ${RAW_BASE} (pushed=${PUSHED})"
