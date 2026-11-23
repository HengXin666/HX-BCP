# Agent Note: 节点列表的 lean 投影 —— 不渲染健康目标就别付它的代价

Status: implemented

## Problem

代理服务页需要节点身份、来源与摘要状态，却等待每页完整健康目标样本及串行分页。节点数量增加时，这条不参与服务行渲染的查询也被重复执行，阻塞入口列表。

## Decision

给节点列表加一个**显式的 lean 投影**, 并让不需要健康目标的调用方 opt-in:

- `node.Filter.SkipHealthChecks` 跳过 `attachHealthChecks`(该字段返回空数组, 不是 null)。
- `GET /api/v1/nodes?view=summary` 映射到它; 默认仍是 `full`(既有调用方不会静默少字段),
  非法 view 是 400 而不是被忽略。
- 同时给 `with_total=1` 提供 `total`: 客户端拿第一页就知道总量, 可以**并行**取剩下的页,
  不必按 offset 串行走。Count 走同一个 WHERE 构造函数, 不会和分页答案不一致。

后续的前端改动(api.listNodes 透出 view/summary + with_total 并行翻页, 服务页用 summary)在
同一次改动里落地。

## Alternatives considered

- **什么都不做, 只把页面的 loading 拆开、先渲染骨架。** 它最强的理由是零后端风险, 且
  「感觉慢」有时确实只是首屏阻塞。否决: 实测慢在服务端序列化的 240ms/页, 不是渲染;
  骨架屏不会让 0.9s 变成 0.2s, 只是把等待藏起来。
- **复用现有接口, 让前端只取第一页(limit=1000)不再翻页。** 最强理由: 一行改动。
  否决: 这正是 2026-10-01 修掉的缺陷——分页截断会让成员计数与「可路由」徽章建立在残缺集合上,
  在 2750 节点的库存上会丢掉 1750 个节点。
- **默认就返回 lean, 需要健康目标时再传 view=full。** 最强理由: 让最快的路径成为默认。
  否决: 会静默改变既有消费者(nodes 页)拿到的字段, 违反「API 字段具有稳定语义」。默认必须
  是兼容的那一侧, 优化由调用方显式选择。
- **把 ListLatestNodeHealthResults 换成「每个 (node,url) 取最新」的相关子查询, 或新增一张
  latest 表。** 最强理由: 这是让**所有**调用方(包括节点页)变快的唯一办法。否决(本轮):
  原有实现正是从逐行 NOT EXISTS 改过来的, 再换回去是同一个坑; 新建 latest 表要迁移 + 双写 +
  一致性测试, 影响面远大于一个页面的加载时间。lean 投影把收益给到真正需要它的调用方,
  且不改变任何已有语义。
- **给服务页单独做一个「按 listener 返回成员」的端点。** 最强理由: 这才是根本解——页面不必
  下载整份库存来算几个组的成员。否决(本轮): 新端点要复刻 pipeline 的成员解析(筛选/排序/
  limit/去重)语义, 与服务端已有 ResolveNodeIDs 形成第二个事实来源的风险很高; 等 lean 投影
  落地后如果仍然慢, 再单独评估。

## Consequences

summary 请求不执行健康目标关联读取；默认 full 保持兼容。with_total 额外执行与分页共用 WHERE 的 COUNT，可能与并发刷新间存在快照变化。客户端最多四路分页，不能承诺所有环境的固定延迟收益。

## Testing

internal/api/nodes_test.go 覆盖 view=summary、total 与分页数量、非法 view=400；internal/store 的 nodeFilterWhere 被 ListNodes/CountNodes 共用。web/e2e/network-routing-regression.mjs 用阻塞节点请求证明服务行独立渲染。
