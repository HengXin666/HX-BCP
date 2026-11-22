# 控制台逐环境检测 (接入 fingerprint-score 流水线的工具)

日期: 2026-11-22

## 背景
用户要求把 GitHub 流水线 fingerprint-score.yml 里的浏览器检测接进产品. plan.md 红线三也要求单环境体检可视化.

## 做法
- 后端 `site_checks.py`, 不新写任何判定:
  - sites: 跑 `gate/probe.py --cdp <环境 CDP>` 采集, 再跑 `node --experimental-strip-types gate/score.ts --no-fail`, 读最后一行 RESULT.
  - audit: 跑 `fingerprint-audit/audit.py --endpoint <CDP> --expect-tz auto --label env-<id>-<ts>`, 读它写进 reports/ 的那份报告. 同一份报告也更新全局 GET /fingerprint/verdict.
- 接口: GET /api/v1/environments/{id}/site-checks (两种的最近一次), POST /api/v1/environments/{id}/site-checks/{sites|audit} (后台开跑, 同环境同类型只跑一个).
- 结果存 `<data_dir>/site-checks/<env_id>-<kind>.json`. 服务重启时还标着 running 的读出来是 failed (中断).
- 只支持 Chromium 系 (要 CDP); 环境未运行直接拒绝并给原因.
- 通道: 路径带环境 id, 由 lane_guard 按环境归属判定, 与 /fingerprint/check 一致.
- 前端: 环境详情 指纹页下半部两块, 开始检测 + 4 秒轮询 + 逐行结论.

## 没接的
- 流水线里的 claude-env 两个 job (按某个 AI 服务的地区判断信号打分) 不接入产品. 它的用途是让环境不被该服务识别出真实地区, 不属于通用自检.
- 两套检测站评分合并成一处 (plan 4.1) 没做, 这里只是两套都能从界面跑.

## 依赖
- sites 要 playwright 和 node 22+; audit 要 websocket-client. 当前 Python 能 import 就用当前解释器, 否则走 uv run.
