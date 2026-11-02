# Agent Note: 人类组与 API 组双线隔离, 组内一层文件夹

Status: implemented

## Problem

控制台 (apps/console-web) 与 API/MCP 共用同一份浏览器环境列表, 没有归属. 人类在控制台上能停掉 Agent 正在用的浏览器, Agent 也能改人类手工养的环境. 用户要求两条线:

- 人类组: 只在前端手动创建和操作, 结构 `文件夹 / 浏览器`.
- API 组: 只由 API/MCP 创建和操作, 必须带项目名, 结构 `项目 / [文件夹] / 浏览器`.
- 人类能看到 API 组, 并看到哪个浏览器正被占用, 但不能写.
- 判据只有一处, 在 `packages/lane/`. API 缺项目名回 HTTP 400, 不允许默认项目.

## Decision

1. 判据唯一落点 `packages/lane/lane.py`:
   - `decide(lane, token, project)` 判定请求属于哪条线. 人类线要本机令牌 (`~/.bcp/human.token`, 常量时间比较), 缺令牌 `human_token_required`. 其余一律按 Agent 线, 项目名不合法或缺失 `project_required`.
   - `may_operate(decision, env_lane, env_project, write)`: 人类读写人类环境, 只读 Agent 环境; Agent 只读写自己项目的环境.
   - `group_key(lane, project)`: 人类组键 `human`, API 组键 `agent:<project>`.
   - 头名常量 `HEADER_LANE` / `HEADER_PROJECT` / `HEADER_HUMAN_TOKEN` 也只在这里定义.
2. 服务端准入 `packages/browser/src/hx_browser/lane_guard.py`: HTTP 中间件调 `lane.decide`, 把结果放进 contextvar. `project_required` -> 400, `human_token_required` -> 401, 越权写 -> 403. WebSocket 用同一判定. 本文件只做 HTTP 映射与打标, 不含判据.
3. 环境打标: `Environment` 增加 `lane` / `project` / `folder`. `store.add_env` 统一按请求范围打标, lane 与 project 永不从请求体读. 旧 `group` 字段保留兼容, 非默认值映射为 folder.
4. 文件夹: `folders.py` 存 `data/folders.json`, 形如 `{"human": [...], "agent:<project>": [...]}`. 只有一层. 非空文件夹删除回 409. 路由在 `api_folders.py`, 只作用于调用方自己的分组.
5. 列表与创建拆到 `api_envs.py`, 按可见性过滤. 回收站, 标签, live 列表与 WS 同样过滤.
6. 占用: `envview.py` 输出 `in_use` (Agent 环境且运行时存在), `in_use_since`, `readonly` (调用方不可写).
7. MCP: 每个工具 schema 的 required 含 `project`, 原样转发 `X-BCP-Project`, 没有默认值.
8. 控制台: vite 开发代理在 Node 侧读本机令牌, 只给同源请求注入 `x-bcp-lane: human` 与令牌, 令牌不进打包产物. 页面只按后端返回的 `lane` 字段分组展示, 写权限以后端为准.

## Alternatives considered

- **缺项目名落到默认项目 `default`**: 用户明确否决. 默认项目会让不同 Agent 的环境混在一起, 正是要消除的问题.
- **判据分散在前端和各路由里各写一份**: 两份规则迟早分叉. 前端只展示, 后端准入集中在 lane_guard, 判定集中在 lane.py.
- **用 group 字段同时表达线和文件夹**: group 是自由文本, 旧数据里已有任意值, 无法可靠区分归属. 新增独立字段, group 只做兼容.
- **多层嵌套文件夹**: 需求只要一层, 多层会让移动, 删除, 树渲染都变复杂. 以后需要再扩.
- **按 Origin 或端口区分人类与 Agent**: 可伪造. 改为本机令牌文件, 只有本机控制台代理能读到.
- **拖拽移动**: `@hx/ui` v0.1.2 的 TreeNav 没有拖拽回调, 不自造组件. 用 "移动到文件夹" 对话框代替, 缺口记在 `.review/hx-ui-issue.md`.

## Consequences

- 所有不带人类令牌的请求都按 Agent 处理, 必须带项目名. 旧脚本若不带 `X-BCP-Project` 会收到 400, 需要补头.
- 旧环境没有 lane 字段时默认归人类组 (`lane.HUMAN`).
- 控制台必须经 vite 代理访问后端, 直连 8325 拿不到人类身份.
- 人类无法在控制台里停掉 Agent 占用的浏览器, 只能看到占用标记.

## Testing

- `packages/lane/tests/test_operate.py`, `packages/lane/tests/test_lane.py`
- `packages/browser/tests/test_lane_scope.py`: 400, 401, 跨项目拒绝, 人类只读 API 环境, 占用字段, 文件夹隔离, MCP project
- 前端: `tsc --noEmit`
