#!/usr/bin/env bash
# 写站点根目录的入口页: 列出所有 PR 与它们的每次测评, 链到 /ci/case/<pr>/<no>/。
#
# 根路径留给以后的产品目录页与文档页, 所以这里不放任何业务内容, 只有索引。
# 纯 HTML, 不依赖构建产物, 数据直接读 site/data 下已有的 JSON。
set -euo pipefail

SITE="${1:?需要站点目录}"
DATA="$SITE/data"

rows=""
if [ -d "$DATA" ]; then
    shopt -s nullglob
    for dir in "$DATA"/pr-*; do
        pr="${dir##*/pr-}"
        case "$pr" in ''|*[!0-9]*) continue ;; esac
        index="$dir/index.json"
        [ -s "$index" ] || continue
        links=$(node -e '
            const fs = require("node:fs");
            try {
                const doc = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
                for (const run of doc.runs ?? []) {
                    if (!Number.isInteger(run.no) || run.no < 1) continue;
                    const when = (run.recordedAt ?? "").replace("T", " ").slice(0, 16);
                    console.log(`        <li><a href="./ci/case/${doc.pr}/${run.no}/">第 ${run.no} 次</a> <span>${when}</span></li>`);
                }
            } catch (error) {
                console.error("index 解析失败, 跳过: " + process.argv[1]);
            }
        ' "$index") || continue
        [ -n "$links" ] || continue
        rows="${rows}    <section>
      <h2>PR #${pr}</h2>
      <ul>
${links}
      </ul>
    </section>
"
    done
fi

[ -n "$rows" ] || rows="    <p class=\"empty\">还没有测评记录。</p>
"

cat > "$SITE/index.html" <<HTML
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>HX-BCP 测评记录</title>
    <style>
      :root { color-scheme: light dark; }
      body { margin: 0; padding: 48px 24px; font-family: ui-sans-serif, system-ui, "Noto Sans SC", sans-serif; line-height: 1.6; }
      main { max-width: 720px; margin: 0 auto; }
      h1 { font-size: 1.6rem; margin: 0 0 4px; }
      p.lead { margin: 0 0 32px; opacity: .65; font-size: .9rem; }
      section { margin-bottom: 28px; }
      h2 { font-size: 1rem; margin: 0 0 8px; font-weight: 600; }
      ul { list-style: none; margin: 0; padding: 0; }
      li { display: flex; gap: 12px; align-items: baseline; padding: 4px 0; }
      a { text-decoration: none; border-bottom: 1px solid currentColor; }
      a:hover { opacity: .7; }
      span { opacity: .55; font-size: .85rem; font-variant-numeric: tabular-nums; }
      p.empty { opacity: .6; }
    </style>
  </head>
  <body>
    <main>
      <h1>HX-BCP 测评记录</h1>
      <p class="lead">按 PR 与测评次序归档。每次都记录各指纹内核在各检测站的表现, 并与上一次对比。</p>
${rows}    </main>
  </body>
</html>
HTML

echo "已写入 $SITE/index.html"
