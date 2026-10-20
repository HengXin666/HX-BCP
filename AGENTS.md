# AGENT

本文件约束在本仓库工作的 Agent。目标 ≤150 行, 新增指引先征得维护者同意。

## 项目是什么

三段可拆分、可组合的调试链路: 指纹浏览器 + 省流缓存代理 + 统一出口代理。
使用者有**人和 Agent 两类**, 两条通道互不相通, 见 docs/architecture/lanes.md。

## 布局

| 路径 | 是什么 | 首读 |
|---|---|---|
| packages/browser/ | 指纹浏览器: 环境 / 启动 / CDP / 指纹派生 | 该目录 README |
| packages/cache-proxy/ | 省流缓存代理: 静态缓存 + 抓包回放 | 该目录 README |
| packages/fingerprint/ | 指纹采集与判定 | 该目录 README |
| packages/fingerprint-audit/ | 环境质量自检 (红/黄/绿), 可当流水线闸门 | KNOWN-ISSUES.md |
| packages/fingerprint-insight/ | 指纹画像与对比 | 该目录 README |
| apps/console-web/ | 控制台, UI 只用 @hx/ui | 该目录 README |
| docs/ | 架构 / 用法 / 参考 | docs/architecture/overview.md |

## 铁律

### 通道隔离
- 人类通道的会话只能由人发起: 人类令牌只在本机生成、只落本地文件, **不进仓库、不进 Agent 环境**。
- Agent 通道的每个请求必须带项目名 (参数或 `X-BCP-Project`), 缺失即 400, 不允许"默认项目"。
- 禁止让 Agent 通道去操作人类通道的会话; 判据只有一处: `packages/lane/`。

### 中性与合规
- 对外文案写**调试 / 兼容 / 自检**, 不写对抗性表述。
- 不内置任何站点的专用破解逻辑; 站点差异用配置表达。
- 不收集、不落盘真实凭据; 示例与测试只用占位值。

### 链路
- 不终结 TLS: 缓存代理不改证书。
- 三段可独立使用; 任一段不得强依赖另一段。
- 浏览器必须支持自定义代理地址 (http / socks5), 不强制本仓库上游代理。

### 验证
- "传了参数" ≠ "生效了": 断言前必须采集实际值。
- 同一事实的判据只写一处, 其它地方引用。
- 失败 ≥2 次先查历史与已知问题, 再改代码。

### 代码
- 单文件 ≤300 行; 注释只描述函数语义且 ≤10 行, 不写决策。
- 决策写 `.agents/notes/implemented/<class>/<yyyy-mm-dd-topic>.md`, 由函数签名引用。
- 非 .md 文件禁止 markdown 语法与中文标点, 标点只用 ASCII 逗号。
- Python 用 uv 管理; 前端只用 React + TypeScript, 包管理用 pnpm。
- 不手动跑仓库门禁与测试, 交给 GitHub 工作流; 本地只允许类型检查。

## 提交

- conventional commits; 一个 PR 做一件事。
- PR 描述用模板: 实现人 / 需求 / 变更实现 / 高危变更 / 测试效果 / 浏览器预览。
- 合并前必须 CI 绿; 合并动作由维护者执行。
