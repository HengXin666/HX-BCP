# Agent Note: 人类通道收敛, Agent 通道必须带密钥

日期: 2026-11-22 · 类别: feature (安全收敛) · 取代: 2026-11-01-loopback-human.md 的默认放行部分

## Problem

维护者反馈: 明明给外部单独分了 Agent 通道, 外部程序直接调用时仍能用人类接口. 实测三处漏口:

1. `lane.decide` 对回环对端直接算人类, 本机任何进程 (本机跑的 Agent, 脚本) 只要加 `x-bcp-lane: human` 就是人类.
2. `api_key_gate` 对 loopback 同样免 `x-api-key`, 所以连密钥都不用.
3. `api_key` 默认为空时整个 Agent 通道只靠项目名, 而 `hxb serve` 默认监听 0.0.0.0, 局域网可直接用.

## Decision

- 通道钉死: 请求带 `x-api-key` 即属于 Agent, 再声明 human 一律 403 `lane_forbidden` (`lane.decide(agent_credential=True)`). 判据仍只在 packages/lane.
- 回环放行改为开关 `settings.human_loopback`, 默认关. 关时人类只认控制台会话 cookie (同源页面) 或本机令牌头.
- Agent 通道永远要密钥: `create_app` 启动时 `api_key_gate.ensure_key` 没有就生成并落盘; `admitted` 在没有密钥时也不再放行 (只剩人类通道能进).
- 声明 human 的 /api/v1 请求先放过密钥闸, 交给 lane_guard 回准确的 401 `human_token_required` / 403, 不再统一回 `invalid api key`.
- 本机免粘令牌改用短时解锁码: `lane.unlock_code()` = `<unix>.<hmac16>`, 令牌做 key, 10 分钟有效, 不含令牌. `hxb serve` 启动打印链接, `bcp-lane unlock-url` 随时取新的; 控制台读 `#/unlock?code=` 换 12 小时会话后从地址栏抹掉.
- WS 实时预览: 控制台会话可免密钥 (同 http), 否则必须 `x-api-key` 或 `?key=`, 常量时间比较; 没有密钥不再等于不设防.
- `hxb serve` 默认监听 127.0.0.1, 要局域网访问显式 `--host 0.0.0.0`.
- 新增 `GET /api/v1/access` 与 `POST /api/v1/access/rotate-key` (只对人类), 控制台 "系统 / 外部接入" 页展示密钥, 项目规则, 可调接口, curl / Python / MCP 示例与回环开关.
- `/api/v1/status` 作为健康探针免密钥 (与 lane_guard.PUBLIC_PATHS 一致).

## 兼容影响

- 之前不带密钥的 Agent 脚本会收到 401 `api_key_required`, 去 "外部接入" 页复制密钥加到 `x-api-key`.
- 之前靠回环免登录打开控制台的, 现在用解锁链接或令牌一次, 之后 12 小时会话.

## 验证

packages/lane/tests/test_agent_pin.py, packages/browser/tests/test_lane_scope.py (回环默认不是人类, 开关后才是, 带密钥声明 human 403, 无密钥 401, 解锁码换会话).
