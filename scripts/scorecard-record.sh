#!/usr/bin/env bash
# 把一次采集写成交互页读的结构化记录, 推到评分卡分支的 data/ 下。
#
# 先用 scorecard-web 的 record.ts 在本地生成 data/pr-<pr>/, 再用 gh api
# 逐个文件 PUT。不 clone 分支: 并发 run 各自写自己的 no, 冲突面最小。
#
# 用法: scorecard-record.sh <bundle.json> <pr> <svg目录> <repo> <branch>
set -euo pipefail

BUNDLE="${1:?需要采集结果 json}"
PR="${2:?需要 PR 号}"
SVG_DIR="${3:?需要 SVG 目录}"
REPO="${4:?需要仓库 full name}"
BRANCH="${5:?需要数据分支}"

APP="${APP_DIR:-apps/scorecard-web}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

node --experimental-strip-types "$APP/scripts/record.ts" \
    --bundle "$BUNDLE" --pr "$PR" --data "$WORK/data" --svg "$SVG_DIR" \
    --commit "${GITHUB_SHA:-}" \
    --raw-base "https://raw.githubusercontent.com/${REPO}/${BRANCH}" >/dev/null

[ -d "$WORK/data/pr-$PR" ] || { echo "record.ts 没产出 data/pr-$PR" >&2; exit 1; }

# 已存在的文件要带 sha 才能覆盖
put() {
    local file="$1" path="$2" sha
    sha=$(gh api "repos/${REPO}/contents/${path}?ref=${BRANCH}" --jq .sha 2>/dev/null || true)
    local args=(-X PUT "repos/${REPO}/contents/${path}"
        -f message="ci(scorecard): 更新 ${path}"
        -f content="$(base64 -w0 "$file")"
        -f branch="${BRANCH}")
    [ -n "$sha" ] && args+=(-f sha="$sha")
    gh api "${args[@]}" >/dev/null
}

count=0
while IFS= read -r file; do
    rel="${file#"$WORK/"}"
    put "$file" "$rel" || { echo "推送 $rel 失败" >&2; exit 1; }
    count=$((count + 1))
done < <(find "$WORK/data" -type f | sort)

echo "推了 ${count} 个文件到 ${BRANCH}/data/pr-${PR}"
