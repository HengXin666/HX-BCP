#!/usr/bin/env bash
# 列出所有要构建的测评, 每行 "<pr> <no>", 供 scorecard-pages 工作流逐个构建。
#
# 读 data/pr-<pr>/index.json 的 runs 数组。目录不存在、index 缺失或 JSON 坏掉
# 都当作"这一次没有测评", 输出空文件而不是报错 —— 首次上线时数据分支可能还没有。
set -euo pipefail

DATA_DIR="${1:?需要一个 data 目录}"
OUT="${2:?需要一个输出文件}"

: > "$OUT"
[ -d "$DATA_DIR" ] || { echo "没有 data 目录, 只出入口页"; exit 0; }

shopt -s nullglob
for dir in "$DATA_DIR"/pr-*; do
    pr="${dir##*/pr-}"
    case "$pr" in ''|*[!0-9]*) continue ;; esac
    index="$dir/index.json"
    [ -s "$index" ] || continue
    # 用 node 解析, 避免依赖 jq; index 坏掉时跳过这个 PR 而不是整条流水线失败
    runs=$(node -e '
        const fs = require("node:fs");
        try {
            const doc = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
            for (const run of doc.runs ?? []) {
                if (Number.isInteger(run.no) && run.no > 0) console.log(run.no);
            }
        } catch (error) {
            console.error("index 解析失败, 跳过: " + process.argv[1]);
        }
    ' "$index") || continue
    for no in $runs; do
        echo "$pr $no" >> "$OUT"
    done
done

sort -t' ' -k1,1n -k2,2n -o "$OUT" "$OUT"
echo "共 $(wc -l < "$OUT") 个测评待构建"
