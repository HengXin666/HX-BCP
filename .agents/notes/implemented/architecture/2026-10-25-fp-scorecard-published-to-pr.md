# Agent Note: 指纹评分卡贴回 PR / commit

## Problem

fingerprint-score 流水线一直在产评分卡 SVG, 但只有 `upload-artifact` 一条出口: 要下载 zip 才能看到图, 而且 `sites` job 标着"只记录", 分数是阻断级也照样绿。PR 上看不到任何反馈。

更根本的是两条链路当时都不对:

- `sites` 用 `probe.py` 不带 `--cdp`, 采的是 runner 上的裸 Playwright Chromium, 标签写 `local-chromium`。gate/README 自己写着"不要把本机无头 Chromium 的结果当成产品分", 流水线做的正是这件事。
- `audit` 与 `claude-env-profile` 用 `real_profile.py up --spawn` 拉 API, 命令是 `python run.py --host ... --port ...`。run.py 在 #13 之后变成 `hxb` 的壳, 只认 `serve` 子命令, 于是 argparse 报 `invalid choice: '127.0.0.1'`, 两个 job 每次都 exit 3。又因为设计成"拉不起来记为未评分, 不阻断", 它永远不红, 没人发现。

## Decision

1. `real_profile.py` 的 spawn 命令抽成 `spawn_command(port)`, 返回 `[python, run.py, serve, --host, --port]`。`wait_api` 接收子进程句柄, 子进程已退出就立刻返回, 不空等满 `--wait` 秒。
2. `sites` job 改成和 `claude-env-profile` 同一条路: `up --spawn` -> `probe.py --cdp` -> `score.ts` -> `down`。拉不起来或站点全挂记为未评分, 不阻断 (检测站与 runner 出口 IP 不受我们控制)。
3. 评分卡发到 PR: `scripts/publish-scorecard.sh` 把 SVG 提交到本仓 `fingerprint-scorecard` 分支, 用 raw 地址贴成 PR 评论, 同一条评论按 `<!-- hx-scorecard -->` marker 原地更新; 同时写步骤摘要。
4. 写权限只给 `sites` 这一条 job (`contents: write` + `pull-requests: write`), 其余 job 保持 `contents: read`。
5. 评分卡抬头加实测浏览器标签, 图上就能看出这份分是 `hx-browser-cdp` 还是 `local-chromium`。

## Alternatives considered

- **贴 artifact 链接** — GitHub 不渲染 artifact 的图片链接, 点开是下载页。否决。
- **data URI 内联** — GitHub 的 markdown 清洗会剥掉 `data:` 图片。否决。
- **提交到 HX-Workflows 仓** — 那个仓已有 `hygiene/badges/*.svg` 的先例, 但评分卡是 HX-BCP 的产物, 放本仓才能跟着 PR 走、也不跨仓要 token。
- **用 commit status / check run** — 只能给绿黄红, 给不出一张图; 而且 `sites` 明确不阻断, 做成 status 会误导。步骤摘要 + PR 评论已经够看。
- **每次新建评论** — 一个 PR 跑几轮就刷屏。用 marker 找已有评论原地 PATCH。
- **让 sites 也阻断** — 检测站改版、runner 出口 IP 都会影响结论, 把它当门禁会让 PR 因为外部原因变红。保持只记录。

## Consequences

- PR 上多一条 bot 评论, 每次 run 原地更新。
- 仓库多一个长期分支 `fingerprint-scorecard`, 只放 `scorecards/*.svg`, 每个 PR 一个文件, main 推送更新 `latest.svg`。
- fork PR 的 token 只读, 那时只写步骤摘要并给 warning, 不阻断。
- `sites` job 现在要起真实副本, 时间从约 3 分钟涨到 5-8 分钟 (多一次 `up`/`down` 与真实内核启动)。

## Testing

- `cd packages/browser && uv run --with pytest pytest -q tests/test_real_profile.py` — 含 `spawn_command` 与 `wait_api` 子进程退出的回归用例。
- 本地 `real_profile.py up --spawn --port 8399` 实测输出 `CDP=http://127.0.0.1:43000`, `down` 后端口释放。
- `publish-scorecard.sh` 对临时分支做过端到端跑: 分支建出来, SVG 提交成功, raw 地址返回 `content-type: image/svg+xml`。
