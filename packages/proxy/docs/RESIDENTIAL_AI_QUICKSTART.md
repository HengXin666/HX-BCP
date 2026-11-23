# 住宅代理对接指南（给 AI / 自动化）

你只需要两样东西：
- **订阅链接**：`https://<host>/sub/<share-token>?format=clash`，导入本机 Mihomo/Clash 得到 N 个声明节点 `住宅XX-01..NN`。
- **控制 URL**：`https://<host>/ctl/<control-token>`，用来换 IP。token 是高权限凭据，保密、不进日志/截图。

## 核心规则
1. **一个节点 = 一个固定的出口 IP 窗口**。IP 不会自己变；节点名和凭据在换 IP 后保持不变，不用重新拉订阅。
2. **每次任务/会话的第一次使用前，必须先 `next` 一次**，换一个全新的出口 IP，再用它执行。"上一次用过的 IP 不能在下一次开头直接复用。"
3. 换 IP 只变后端出口，不影响节点身份与认证，轮换后无需更新订阅。

## 每次使用顺序
```http
# 1. 读取节点池（含 alloc_version）
GET  https://<host>/ctl/<control-token>/nodes

# 2. 每次新任务开始、第一次用目标节点前，先换 IP
POST https://<host>/ctl/<control-token>/nodes/<index>/next
# body（可选护栏）: {"lease_id":"...","expected_alloc_version":N}
# 轮换过快返回 429；改完的窗口会读出新的 alloc_version
```

## 落地
- 订阅导入本机 Mihomo/Clash 落在本地环回代理；`/sub/`、`/ctl/` 是控制地址，不是 Playwright 的 proxy。
- 多服务/多实例并用地同一渠道时，必须先 `claim` 独占租约再轮换，避免撞 IP 窗口（详见 `RESIDENTIAL_INTEGRATION_STANDARD.md`）。