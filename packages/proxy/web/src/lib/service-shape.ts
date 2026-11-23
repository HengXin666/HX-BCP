import type { ListenerRecord, NodeRecord, ProxyGroup, ProxyGroupStrategy, Subscription } from "@/lib/types"

/**
 * A service's SHAPE — what it is, derived from data — as opposed to how it is
 * named. Three rows used to look identical (`1 个订阅 · Top 全部` + `自动测速`)
 * while one was a 41-node paid subscription, one a 1305-member disposable pool
 * and one a pinned single exit, so an operator could not tell them apart.
 *
 * The shape is derived from the source class the operator declared
 * (`subscriptions.source_class`), never from a name: names are operator-chosen
 * text and cannot be a judgement.
 *
 * See .agents/notes/implemented/simplification/2026-10-01-service-shape-follows-source-class.md
 * for the three measured defects this replaces, the class semantics
 * (feature/2026-09-26-source-class-drives-probe-policy.md), and the pin
 * semantics (feature/2026-09-26-listener-pins-a-node.md).
 */
export type SourceClass = "pool" | "subscription" | "residential"
/**
 * `pinned` and `manual` are deliberately distinct, because they are different
 * statements that were being conflated:
 *
 * - `pinned`: `listeners.node_id`. ONE node carries the traffic; the rule
 *   targets that node's name and the group is only a schema anchor. Nothing is
 *   selected, so no strategy applies.
 * - `manual`: `source_spec.node_ids`. A group whose members are an explicit
 *   list of nodes. Selection still happens (the strategy picks among them), so
 *   the strategy is a real choice and only the filters that assume a dynamic
 *   source are irrelevant.
 */
export type ServiceShape = "direct" | "pinned" | "manual" | "pool" | "residential" | "subscription" | "mixed"

export interface ShapeMeta {
  label: string
  /** What this thing *is*, in one line. */
  detail: string
  /** Badge variant from the shared theme (components/ui/badge.tsx). */
  variant: "default" | "secondary" | "success" | "warning" | "destructive" | "outline"
  /**
   * Which selection knobs a service of this shape actually has. A knob that is
   * false must also be *unsent* by the forms — the inapplicable ones are not
   * harmless, see the pool row below.
   */
  knobs: {
    limit: boolean
    regions: boolean
    maxLatency: boolean
    healthCheck: boolean
    strategies: ProxyGroupStrategy[]
  }
}

/**
 * The single table behind the row badge, the row summary, the create/edit form
 * options and the capability hint. Splitting any of those into its own copy is
 * how the UI starts telling a different story than the probe policy it claims
 * to describe.
 *
 * Every `false` knob has a measured reason, not a stylistic one:
 *
 * - `pool.regions`: region matching reads name fragments. All 1305 pooled
 *   members are named `65.111.26.216:3129` — 0 match any region alias.
 * - `pool.maxLatency`: the cap rejects members with no latency sample, and a
 *   pooled member that has never answered has none. Measured against a copy of
 *   the production database: the pool group resolves **436 members unfiltered
 *   and 0 with the create form's default 2000 ms cap**. A silently empty group.
 * - `pinned.*`: a pinned service has exactly one node by definition, so
 *   selection is not a question it can answer.
 * - `direct.*`: the server's own egress is not a node list.
 */
export const shapeMeta: Record<ServiceShape, ShapeMeta> = {
  direct: {
    label: "服务器直连",
    detail: "不经过任何节点，使用本机网络出口",
    variant: "secondary",
    knobs: { limit: false, regions: false, maxLatency: false, healthCheck: false, strategies: ["manual"] },
  },
  pinned: {
    label: "指定节点",
    detail: "出口钉死在单个节点上，不随测速漂移",
    variant: "success",
    knobs: { limit: false, regions: false, maxLatency: false, healthCheck: false, strategies: ["manual"] },
  },
  manual: {
    label: "固定节点组",
    detail: "成员是手工列出的一批节点，仍按策略在其中选择",
    variant: "success",
    knobs: {
      limit: false,
      regions: false,
      // The member list is explicit, so a filter could only ever remove
      // something the operator typed in on purpose.
      maxLatency: false,
      healthCheck: true,
      strategies: ["url-test", "fallback", "round-robin", "manual"],
    },
  },
  pool: {
    label: "代理池",
    detail: "成员是消耗品：活着就用，失效即弃",
    variant: "warning",
    knobs: { limit: true, regions: false, maxLatency: false, healthCheck: true, strategies: ["url-test", "round-robin", "fallback"] },
  },
  residential: {
    label: "住宅出口",
    detail: "少量不可替代的出口，坏了要人修",
    variant: "default",
    knobs: { limit: true, regions: false, maxLatency: true, healthCheck: true, strategies: ["manual", "fallback", "url-test"] },
  },
  subscription: {
    label: "订阅节点",
    detail: "按地区 / 延迟从订阅里挑可用成员",
    variant: "outline",
    knobs: { limit: true, regions: true, maxLatency: true, healthCheck: true, strategies: ["url-test", "fallback", "round-robin", "consistent-hashing", "sticky-sessions", "manual"] },
  },
  mixed: {
    label: "混合来源",
    detail: "同时引用多类来源，按最保守的一类处理",
    variant: "warning",
    knobs: {
      limit: true,
      // Region aliases only match the named sources; a pool half contributes
      // nothing but is filtered just as hard, so the option stays off until the
      // group drops to one class.
      regions: false,
      // Same measurement as pool: a member with no sample is dropped, not kept.
      maxLatency: false,
      healthCheck: true,
      // The union minus the shapes needing a stable member identity: with a
      // disposable half in the group, sticky sessions pin to a member that may
      // be gone before the next request.
      strategies: ["url-test", "fallback", "round-robin", "manual"],
    },
  },
}

/** Badge text and tone for a source class, used wherever sources are listed. */
export const sourceClassLabel: Record<SourceClass, { label: string; variant: ShapeMeta["variant"]; detail: string }> = {
  pool: { label: "代理池", variant: "warning", detail: "海量一次性成员：失败即弃，靠刷新补新" },
  subscription: { label: "订阅", variant: "outline", detail: "长期订阅资产：失败多为上游抖动，退避重试" },
  residential: { label: "住宅出口", variant: "default", detail: "少量不可替代出口：永不自动隔离或退役" },
}

/** The declared class of every source a group draws from. */
export function sourceClasses(listener: ListenerRecord, group: ProxyGroup | undefined, nodes: NodeRecord[], subscriptions: Subscription[]): SourceClass[] {
  const classByID = new Map(subscriptions.map((item) => [item.id, item.source_class]))
  const subscriptionIDs = new Set<string>()
  if (listener.node_id) {
    // A pinned service is classified by the source that owns the pinned node.
    // Reading the anchor group instead would classify every pinned service as
    // whatever the operator's main subscription happens to be — the group on a
    // pinned listener is a schema anchor, not its policy.
    for (const node of nodes) {
      if (node.id !== listener.node_id) continue
      for (const source of node.sources) subscriptionIDs.add(source.subscription_id)
    }
  } else {
    for (const id of group?.source_spec.subscription_ids ?? []) subscriptionIDs.add(id)
  }
  const classes = new Set<SourceClass>()
  for (const id of subscriptionIDs) {
    const value = classByID.get(id)
    if (value) classes.add(value)
  }
  return [...classes].sort()
}

/** The one shape a service is rendered as. */
export function serviceShape(listener: ListenerRecord, group: ProxyGroup | undefined, nodes: NodeRecord[], subscriptions: Subscription[]): ServiceShape {
  if (listener.node_id) return "pinned"
  const spec = group?.source_spec
  if (!spec) return "direct"
  const classes = sourceClasses(listener, group, nodes, subscriptions)
  if (classes.length > 1) return "mixed"
  const only = classes[0]
  if (only) return only
  // No class came back because no source is referenced: the group either names
  // its members directly, or falls back to the server's own egress.
  return spec.node_ids.length > 0 ? "manual" : "direct"
}

/** Shape of a service that does not exist yet — used by the create form. */
export function shapeForSelection(subscriptionIDs: string[], nodeIDs: string[], direct: boolean, subscriptions: Subscription[]): ServiceShape {
  if (direct) return "direct"
  const classes = new Set<SourceClass>()
  for (const id of subscriptionIDs) {
    const value = subscriptions.find((item) => item.id === id)?.source_class
    if (value) classes.add(value)
  }
  if (classes.size > 1) return "mixed"
  const only = [...classes][0]
  if (only) return only
  return nodeIDs.length > 0 ? "manual" : "pool"
}
