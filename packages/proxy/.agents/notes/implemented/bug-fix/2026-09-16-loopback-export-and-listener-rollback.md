# Agent Note: 环回 Listener 只能回给环回调用者, 且 Apply 失败的 Listener 必须自己撤销

Status: implemented

## Problem

这一篇记录两个独立但同源的缺陷: 都是"创建路径在失败/边界情况下留下一个不该存在的东西"。

**缺陷一: 本机部署取不到订阅。** `internal/listener` 导出订阅时, 对**只绑环回地址**的
Listener 一律返回 `ErrShareDisabled`。这个判断只看 Listener 自己的绑定地址, 不看**请求是从哪来的**。
后果是: 在控制面本机跑 `curl /sub/<token>` 或 `/nodes/<token>` 会拿到 404 ——
而"控制面 + 消费者在同一台机器上"正是最常见的部署形态。
(没有 workaround: 创建时 `validEndpointHost` 会拒绝把环回地址设成 `public_endpoint`。)

**缺陷二: Listener 创建失败会留下孤儿, 还会挡住清理。** `listener.Service.Create` 先写
库、再 `Apply`。`Apply` 失败时它直接返回错误, **不回滚那行记录**。于是:

- 调用者拿到的是一个错误, 没有 Listener id, 所以**它无法清理**这条记录;
- 更糟的是 `proxyservice.Create` 的补偿逻辑只会 `groups.Delete`。现在库里存在一个
  Listener 引用着那个组, 删组因此**也失败**, 报 `proxy group conflict` ——
  一次失败留下**两个**孤儿, 并且**永久挡住同名重试**。

这不是理论问题: 在真实进程上跑 `quickstart` 时, 用了一个指向受管端口的节点触发 Apply 失败,
之后 `proxy-groups` 和 `listeners` **各留下一个**条目。同一篇 note 的 `## Testing`
记录了修复前后的实测计数。

## Decision

**一、导出订阅时按「请求来源」判断, 而不是按「绑定地址」。**
`internal/listener/share.go` 的守卫改为: 只有当 Listener 绑定环回 **且** 请求本身不是从环回
到达时, 才拒绝(`isLoopbackHost(requestHost)`)。空 `requestHost` **保持拒绝** ——
拿不到来源就必须保守。

这样"本机 curl 本机端口"放行, 而"反向代理把公网 Host 转发进来"仍然被拒
(`TestShareExportDoesNotExposeLoopbackPortThroughExternalHost` 继续通过)。
理由是与既有行为对齐: `ResolveSharedEndpoint` 早就允许同类的回退, 只有这条专用判断不一致。

**二、`listener.Create` 在 Apply 失败时自己删掉刚写的行。**
与 `Update` 既有的做法一致(它已经会在 Apply 失败时恢复原记录)。错误信息显式说明
"the created listener was removed again", 让调用者知道库里没有残留。
修好这条之后, `proxyservice.Create` 的补偿链就能正常删掉组 —— 因为已经没有被引用的 Listener 了。

## Alternatives considered

**什么都不做, 让本机部署改用共享入口模式(shared inbound)。**
这确实能绕过缺陷一 —— 而且共享入口是本项目的默认模式, 所以"默认没问题"是它最强的理由。
但它是**默认值**而不是**保证**: 用户可以切到 per_service 模式, 那时本机就会 404,
而错误信息(404)完全指不到"你被自己的环回判断拦住了"。让一条正常的本机调用依赖配置恰好是某个值,
不是修复。否决。

**缺陷一改成「允许所有调用者看到环回 Listener」。**
最省事, 也是一个常见做法(反正订阅链接本身带 token)。
但这会把一个**内部 Mihomo 端口**变成可对外播发的地址: 反向代理后面只要有任何一条路径
把请求带上公网 Host 转发进来, 输出就会是 `127.0.0.1:7890` —— 对消费者毫无用处,
而且泄露了内部拓扑。判据必须是"请求从哪来", 不是"要不要检查"。否决。

**缺陷二只改 `proxyservice` 的补偿: 先删 Listener 再删组。**
不动 `listener` 包, 改动面最小。
但调用者拿不到失败时的 Listener id(`Create` 返回零值), 所以它**根本删不掉**;
要实现就得让 `listener.Create` 把部分记录也返回出来, 等于把"库里有残留"这件事
泄漏给每一个调用者去处理。回滚应该发生在写下那行记录的同一层。否决。

**缺陷二把 Apply 挪到写库之前(先 apply 后落库)。**
顺序上更"干净", 理论上不会留下不一致。
但 `Apply` 编译的是**库里的期望状态**, 记录还没落库时它编译出来的配置里根本没有这个
Listener, 所以这个顺序在本架构里不成立。撤销是唯一可行的方向。否决。

## Consequences

- 本机部署(控制面与消费者同机)现在能正常取订阅; 公网反向代理路径的拒绝行为不变。
- `listener.Create` 的成功路径多了一次出错分支, 但**只在 Apply 失败时**触发, 不改变正常流程。
- 一次失败的创建**不再需要外部清理**, 也不再有"孤儿挡住同名重试"的状态。
- 这条"空请求 host 保持拒绝"的保守选择意味着: 若将来出现确实拿不到 host 的合法调用方,
  需要显式传入, 而不是放宽默认。

## Testing

- `go test ./internal/listener/`: `TestShareExportServesLoopbackListenerToLoopbackCaller`
  覆盖 `127.0.0.1` / `localhost` / `::1` 三种来源, 并断言空 host 仍被拒;
  `TestShareExportDoesNotExposeLoopbackPortThroughExternalHost` 断言外部 Host 仍被拒。
- `go test ./internal/quickstart/`: 失败路径回滚订阅; 成功路径不删任何东西。
- 真实进程端到端(真 mihomo, 故意用指向受管端口的节点触发 Apply 失败):
  修复前 `groups=1, listeners=1`; 修复后 `groups=0, listeners=0`, 且错误信息带
  "the created listener was removed again"。
