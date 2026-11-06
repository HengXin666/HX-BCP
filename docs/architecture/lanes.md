# 人机双通道

两类使用者共用同一套组件, 但**会话归属与准入不同**。本文件是判定依据, 代码实现在 `packages/lane/`。

## 两条通道

| | 人类通道 | Agent 通道 |
|---|---|---|
| 发起者 | 人 (控制台或本地 CLI) | 程序 (HTTP API) |
| 准入 | 本机回环连接, 或人类令牌 (本机生成, 只落本地文件) | 项目名必填 |
| 会话归属 | `lane=human` | `lane=agent`, `project=<名字>` |
| 分组 | 按时间 | 按项目名原生分组 |
| Agent 能否接管 | **否** | 是 (仅自己的项目) |
| 人能否查看 | 是 | 是 (只读) |

## 判据

1. 请求带 `lane=human` 时, 下列任一即可: 连接对端是本机回环且 `Host` 是 localhost / 127.x / `[::1]`、请求头等于本机人类令牌、或带由令牌换来的会话 cookie; 都不满足 -> `401 human_token_required`。通道仍只由 `x-bcp-lane` 头选择, 回环不带该头仍是 Agent 通道。8325 前面加任何反向代理前必须先改 `packages/lane/lane.py` 的 `loopback_admitted`。
2. 请求带 `lane=agent` (默认) 时, 必须带项目名; 缺失 -> `400 project_required`。
3. 项目名规则: 小写字母 / 数字 / `-`, 长度 2..40; 归一化后作为分组键。
4. 任何写操作 (启动会话 / 关闭会话 / 修改环境) 都记录 `lane` 与 `project`, 审计不可改。

## 人类令牌

- 生成: `packages/lane` 的 CLI, 落 `~/.bcp/human.token` (权限 0600)。
- 不进仓库, 不进 Agent 环境变量; 泄露即失效并重新生成。
- 令牌只证明"这是人在这台机器上操作", 不承担其它授权。

## 为什么这样分

人手动操作的会话与 Agent 批量会话混在一起时, 排障无法区分"我点的"和"程序点的";
更实际的问题是: 若 Agent 能操作人类会话, 它就等于拿到了人在浏览器里的登录态。
把通道拆开, 两边各自可审计。

## 与链路的关系

通道决定**谁发起会话**; 链路决定**流量怎么走**。二者正交:

```text
lane=human  + chain=browser->cache->upstream
lane=agent  + chain=browser->upstream
lane=agent  + chain=browser (直连)
```
