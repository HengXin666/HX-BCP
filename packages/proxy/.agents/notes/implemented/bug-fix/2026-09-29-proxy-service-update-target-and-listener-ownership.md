# Agent Note: 代理服务 PUT 的地址被丢弃, 且可改到别人的 Listener

Status: implemented

## Problem

三条互相独立的缺陷叠在同一个 handler 上, 合起来让「编辑代理服务」既会改错对象, 也不会报对错。

`internal/api/proxy_services.go` 的 `handleProxyService` 把路径里的 `{id}` 解析出来、判空、判斜杠, 然后**丢掉不用**。真正决定改哪个服务的是 body 里的 `group_id`。实测:

`@
PUT /api/v1/proxy-services/listener-NOT-A-GROUP   body.group_id = group-33f40f46...  -> 200, 改的是后者
PUT /api/v1/proxy-services/<A>                     body.group_id = <B>                -> 200, 改的是 B
`@

也就是说地址只是一个装饰: 任何持有会话的调用方都能用「改这个服务」的 URL 去改任意另一个服务。

第二个缺陷是 body 里的 `listener_id` 同样不受约束。`proxyservice.Update` 只把 id 转交给 `listeners.Update`, 而后者只认 id、不知道这个 listener 原本属于哪个组。实测把一个组的 `listener_id` 填进另一个组的更新请求:

`@
PUT /api/v1/proxy-services/<A>  body.listener_id = <B 的 listener> -> 200
结果: B 的 listener 的 proxy_group_id 被改成 A, B 分组当场失去入口 (现场已还原)
`@

第三, 请求体没送到时不会有任何提示。真实场景是浏览器里发出的一条 619 字节的 PUT, body 在链路上丢失。空 body 被 `decodeJSONBody` 当成合法的零值请求 (它在 `io.EOF` 时直接返回 nil), 于是一路走到 `proxygroup.Get(ctx, "")`, 回 `404 resource not found`。把「请求畸形」报成了「资源不存在」, 排查方向被带偏到数据缺失上。

## Decision

请求地址是唯一权威, body 只能确认它。

`internal/api/proxy_services.go` 新增 `bindProxyServiceTarget`: 地址里的 id 写进 `GroupID`; body 若给了 `group_id` 则必须与地址一致, 不一致回 400 `invalid_request`。同时要求 `listener_id`、`group_version`、`listener_version` 非空 —— 这三个是乐观锁前提和写入目标, 缺失就是畸形请求, 不是 404, 也不是 422 版本冲突。

`internal/proxyservice/service.go` 新增 `ensureListenerOwnedBy`: 更新前先通过 `listeners.Get` 取当前 listener, 其 `ProxyGroupID` 必须等于本次要写的组, 否则回 `listener.ErrConflict` (HTTP 409)。检查放在**第一次写入之前**, 因为被拒的更新不该消耗组版本 —— 否则调用方的下一次合法重试会因为版本过期而失败。

归属检查需要 `ListenerService` 暴露 `Get`, 已加入接口。

## Alternatives considered

**什么都不做, 把 body 里的 `group_id` 当作权威。**

零改动, 而且前端两个调用点 (`routing-page.tsx` 的创建、`edit-proxy-service-form.tsx` 的编辑) 本来就同时填了地址和 body, 正常路径两条一致, 所以不修也「能跑」。否决: 地址被解析、被校验、被丢弃这段代码本身就在宣称一个不存在的约束; 一个写接口的 URL 不决定写谁, 任何拿到会话的进程都能改任意服务, 而前端一致性不构成服务端保证。

**只修地址, 不动 listener 归属。**

能堵住「改任意服务」, 改动最小。否决: 实测能把 B 的 listener 抢进 A, 是一个独立的越权, 且后果更重 —— 它不是改错一个字段, 而是让整个 B 服务失去入口。两个缺陷在同一条调用链上, 只修一个等于留着另一半。

**空 body 回 422 而不是 400。**

422 已经是「内容不合语义但它是个合法请求」的出口 (validation_failed)。否决: 一个连 `listener_id` 都没有的 body 不是语义问题, 是请求没成形; 用 400 才能把「body 没发出来」和「字段值不合法」在客户端日志里区分开。

**让 `decodeJSONBody` 在 EOF 时报错。**

在唯一入口处堵死空 body, 覆盖面最广。否决: 它服务 38 个 handler, 其中若干确实接受空 body (例如无参数的操作类 POST); 在那里加严格性会误伤。约束属于这个具体的写接口, 就写在它自己的 handler 里。

## Consequences

- `PUT /api/v1/proxy-services/{id}` 只改被寻址的那个服务; body 与地址矛盾时 400, 不再静默改到别处。
- 空 body / 缺 `listener_id` / 缺版本号统一回 400 `invalid_request`, 不再伪装成 404。
- 引用他人 listener 回 409 `conflict`, 且不消耗组版本; 合法路径不受影响。
- 归属检查多一次读; 它在同一条 SQLite 连接上, 相对于随后的写事务与数据面 apply 可以忽略。
- `internal/api/proxy_services_test.go` 锁住三条契约 (地址权威 / 矛盾 400 / 空 body 400); `internal/proxyservice/service_test.go` 锁住归属拒绝与「被拒时不写库」。
- 修复已按原参数重启本地实例复验: 三种越权形状分别回 400 / 400 / 409, listener 归属未变, 合法更新回 200。
