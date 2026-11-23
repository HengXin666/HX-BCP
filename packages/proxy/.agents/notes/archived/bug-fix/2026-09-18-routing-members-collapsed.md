# Agent Note: 代理服务首次进入默认展开节点成员

Status: implemented

Archived: 2026-09-29

## Problem

共享 Mixed 入口下, `GET /nodes/<share-token>` 只返回 1 条可直拨的 mixed 入口, 机场 vless/hysteria2 成员只活在数据面 url-test 组里。控制面「代理服务」把这些成员放在折叠的「节点成员」里, `expanded` 初始是空 Set。没点箭头时, 页面只剩服务名和 `29/41 可用` 徽章, 看起来像没节点。

## Decision

首次加载 (expanded 仍为空) 把当前可见 Listener 全部展开。用户手动收起后刷新保留收起; 只有还没点过时才自动展开。

## Alternatives considered

**什么都不做, 让用户自己点展开。**
零改动, 徽章已经写了 `健康/成员`。否决: 「看不到节点」会被理解成库存空了或建组失败, 刚配完控制面时这个误判代价最高。

**把机场节点写进 `GET /nodes/<share-token>`。**
消费者就能直接看到 41 条。否决: 共享入口契约就是发布一条可直拨 mixed; vless/hysteria2 不能当浏览器 `--proxy-server`。改契约会破坏 hx-consumer-api。

**关掉共享入口, 给每个节点单独端口。**
成员会各自出现在订阅里。否决: 本机明确开了 shared inbound, 一个客户端覆盖全部组; 改模式等于另起一套数据面。

## Consequences

- 打开 `#/routing` 时, 「良心云」下一眼能看到成员列表。
- 用户收起后再刷新, 不会被强制重新展开。
