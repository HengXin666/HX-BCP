#!/usr/bin/env bash
# 把评分卡 SVG 发布到 fingerprint-scorecard 分支, 并贴回 PR 评论 / 步骤摘要.
#
# GitHub 只渲染能被公开拉取的图片 URL, artifact 链接不行, data URI 会被过滤.
# 所以把 SVG 提交到一个固定分支, 再用 raw 地址引用.
#
# 环境变量:
#   GH_TOKEN  必需, 需要 contents:write (推分支) 与 pull-requests:write (评论)
#   SVG       必需, 评分卡路径
#   TXT       可选, score.ts 的文本输出
#   PR        可选, PR 号; 为空时按 main 推送处理
#   REPO      可选, 默认 $GITHUB_REPOSITORY
#   BRANCH    可选, 默认 fingerprint-scorecard
set -euo pipefail

REPO="${REPO:-${GITHUB_REPOSITORY:?GITHUB_REPOSITORY 未设置}}"
BRANCH="${BRANCH:-fingerprint-scorecard}"
SVG="${SVG:?SVG 未设置}"
PR="${PR:-}"
MARKER="<!-- hx-scorecard -->"

if [ ! -s "$SVG" ]; then
    echo "::warning::没有评分卡文件, 跳过发布"
    exit 0
fi

# 每个 PR 一个文件, 避免并发 run 互相覆盖; main 推送更新同一个 latest.svg
if [ -n "$PR" ]; then
    PATH_IN_REPO="scorecards/pr-${PR}.svg"
else
    PATH_IN_REPO="scorecards/latest.svg"
fi
RAW="https://raw.githubusercontent.com/${REPO}/${BRANCH}/${PATH_IN_REPO}"

ensure_branch() {
    if gh api "repos/${REPO}/git/ref/heads/${BRANCH}" >/dev/null 2>&1; then
        return 0
    fi
    local base
    base=$(gh api "repos/${REPO}/git/ref/heads/main" --jq .object.sha)
    gh api -X POST "repos/${REPO}/git/refs" -f ref="refs/heads/${BRANCH}" -f sha="$base" >/dev/null
}

put_file() {
    local sha
    sha=$(gh api "repos/${REPO}/contents/${PATH_IN_REPO}?ref=${BRANCH}" --jq .sha 2>/dev/null || true)
    local b64
    b64=$(base64 -w0 "$SVG")
    local args=(-X PUT "repos/${REPO}/contents/${PATH_IN_REPO}"
        -f message="ci(scorecard): 更新 ${PATH_IN_REPO}"
        -f content="$b64"
        -f branch="${BRANCH}")
    if [ -n "$sha" ]; then
        args+=(-f sha="$sha")
    fi
    gh api "${args[@]}" >/dev/null
}

# 并发 run 写同一个分支会 409, 重取 sha 后重试
publish() {
    ensure_branch
    local attempt
    for attempt in 1 2 3; do
        if put_file; then
            return 0
        fi
        echo "发布第 ${attempt} 次失败, 重试" >&2
        sleep $((attempt * 2))
    done
    return 1
}

if ! publish; then
    echo "::warning::评分卡没能推到 ${BRANCH} 分支 (可能缺 contents:write), 只写步骤摘要"
    RAW=""
fi

# 代码块围栏单独存变量: 直接写反引号会被当成命令替换
FENCE=$(printf '%s' '```')
BODY="${MARKER}"$'\n'"## 指纹评分卡"$'\n\n'
if [ -n "$RAW" ]; then
    BODY="${BODY}![指纹评分卡](${RAW})"$'\n\n'
fi
if [ -n "${TXT:-}" ] && [ -s "${TXT}" ]; then
    BODY="${BODY}${FENCE}"$'\n'"$(grep -v '^RESULT ' "$TXT")"$'\n'"${FENCE}"$'\n\n'
fi
BODY="${BODY}<sub>分数是公开检测站自己写出的结论; 检测站改版或扫描没跑完记为未测, 不是产品回归.</sub>"

# 步骤摘要: 同一条内容, 不依赖评论权限
{
    echo "## 指纹评分卡"
    if [ -n "$RAW" ]; then
        echo "![指纹评分卡](${RAW})"
    fi
    echo ""
} >> "${GITHUB_STEP_SUMMARY:-/dev/null}"

if [ -z "$PR" ]; then
    echo "评分卡: ${RAW:-未发布}"
    exit 0
fi

COMMENT_ID=$(gh api "repos/${REPO}/issues/${PR}/comments" --paginate \
    --jq ".[] | select(.body | startswith("${MARKER}")) | .id" | head -n 1)

set +e
if [ -n "$COMMENT_ID" ]; then
    gh api -X PATCH "repos/${REPO}/issues/comments/${COMMENT_ID}" -f body="$BODY" >/dev/null
    code=$?
else
    gh api -X POST "repos/${REPO}/issues/${PR}/comments" -f body="$BODY" >/dev/null
    code=$?
fi
set -e

if [ "$code" != "0" ]; then
    echo "::warning::评分卡没能贴到 PR #${PR} (fork PR 的 token 只读), 已写入步骤摘要"
fi
echo "评分卡: ${RAW:-未发布}"
