# Agent Note: 共享入口按"家族载体"而不是"成员协议"建立聚合 Listener

Status: implemented

## Problem

共享入口下, 一个家族的聚合 Listener 原先按**成员协议**建索引: 来一个 `kind=http` 的服务就建一个
`kind=http` 的聚合行, 来一个 `kind=socks` 的再建一个。而数据面编译器只接受 `kind=mixed` 的
standard 聚合 Listener。

两边判断标准不一致, 后果是一个 `kind=http` / `kind=socks` 的服务被迁移进共享家族后:

- 数据库里聚合行存在, 端口被它占着;
- 数据面里**什么都没有** —— 既没有 Listener, 也没有对应的 `IN-USER` 规则;
- 客户端拿订阅里的 URL 去连, 直接被拒(HTTP 代理返回 403)。

同一个家族还会因为成员协议不同而生成**多个聚合行**, 互相争抢同一个端口; 升级时这些
"每协议一行"的旧行不会被回收, 留下占着端口却不承载任何流量的死行。

## Decision

家族与载体的映射**单点定义**在 `listener.SharedInboundCarrierKind` / `SharedInboundCarrierKey`,
控制面的聚合行与数据面编译共用同一份规则:

| 家族 | 成员协议 | 载体 Listener |
| --- | --- | --- |
| standard | http / socks / mixed | `mixed`(一个) |
| websocket | vless / vmess / trojan | 每种协议各一个(共享端口与 ws-path) |

standard 家族收敛成**唯一**一个 Mixed Listener: Mihomo 的 Mixed 在同一个 socket 上同时讲 HTTP 代理
与 SOCKS5, 这正是 standard 入口承诺的协议集合, `http` / `socks` 成员由它承载、靠 `IN-USER` 分流。
websocket 家族不同: 每个协议是真正不同的 Mihomo Listener 类型, 所以保持每协议一个, 只是共享端口与 ws-path。

升级时旧的"每协议一行"聚合行被自动回收(保留家族的 Mixed 行), 与聚合行收敛在同一次原子 Apply 中, 失败回滚。

## Consequences

- 一个家族永远只有一个 standard 聚合行, 不会再争抢端口。
- 被跳过的服务(未启用用户名/密码认证的)现在会以 `WARN` 列出**服务名与原因**。
  共享入口靠用户名分流, 无凭据的服务无法被承载; 只报计数会让操作者靠试错才能发现某个服务为什么没被迁移。
- 家族规则从"按成员协议"变成"按家族载体", 是一个**行为变更**: 升级时会移动存量聚合行。
  迁移测试(`TestEnsureSharedInboundsRetiresPerProtocolAggregates` 等)覆盖了这条路径。

## Alternatives considered

**什么都不做, 让操作者把 `kind=http` / `kind=socks` 的服务手动改成 `mixed`。**
零改动, 且"服务本来就该配成 mixed"听起来合理。
但订阅里发布的 URL 与协议是控制面自己写的, 让用户去改是为了绕开控制面的内部不一致;
而且编译器**静默丢弃**这些服务 —— 没有任何报错, 只有客户端连不上。把静默失败转嫁成用户排查, 否决。

**在编译器一侧放宽: 让 `kind=http` / `kind=socks` 的聚合行也能编译出 Mixed Listener。**
看起来更小, 只改一处。
但聚合行仍会按协议分裂成多行、仍会争抢同一端口, "一个家族一个入口"这个不变量就没了;
重复解析同一家族成员的问题只是从编译期挪到了聚合期。否决。

**给 standard 家族的每个成员协议都建一个真正的同类型 Listener(真 http listener + 真 socks listener)。**
最"忠于协议", 表面上最不容易被误解。
但那就不是共享入口了: 每个协议一个端口, 与"1 个客户端 2 条 URL 覆盖全部组/协议"的目标直接冲突,
也放弃了 Mixed 单 socket 双协议这个 Mihomo 已有能力。否决。

**让聚合行不落库, 每次从成员行实时推导。**
能彻底避免"行与实际承载不一致"。
但聚合行承载着端口与 endpoint 配置, 需要参与版本号乐观锁与原子 Apply; 不落库会让
"改端口"这类操作失去冲突检测, 也让热重载无法知道端口该留给谁。否决。
