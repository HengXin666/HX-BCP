import { useCallback, useEffect, useMemo, useState } from "react"
import { ChevronDown, ChevronRight, CircleDot, FileText, Gauge, Globe2, LoaderCircle, Pencil, Plus, Radio, RefreshCw, Trash2 } from "lucide-react"

import { ConfirmDialog } from "@/components/confirm-dialog"
import { CreateSubscriptionForm } from "@/components/create-subscription-form"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { api } from "@/lib/api"
import { sourceClassLabel } from "@/lib/service-shape"
import type { NodeRecord, SourceType, Subscription, TrafficSummary } from "@/lib/types"
import { cn, compactId, formatBytes, formatDate } from "@/lib/utils"

interface SubscriptionsPageProps {
  onNotice: (message: string, tone?: "success" | "error") => void
  embedded?: boolean
}

const sourceMeta: Record<SourceType, { label: string; icon: typeof Globe2 }> = {
  remote: { label: "远程 URL", icon: Globe2 },
  inline: { label: "内联内容", icon: Radio },
  file: { label: "本地文件", icon: FileText },
}

export function SubscriptionsPage({ onNotice, embedded = false }: SubscriptionsPageProps) {
  const [items, setItems] = useState<Subscription[]>([])
  const [nodes, setNodes] = useState<NodeRecord[]>([])
  const [traffic, setTraffic] = useState<Map<string, TrafficSummary>>(new Map())
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [editTarget, setEditTarget] = useState<Subscription | null>(null)
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const [deleteTarget, setDeleteTarget] = useState<Subscription | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [subscriptions, nodeResult, trafficResult] = await Promise.all([api.listSubscriptions(), api.listNodes(), api.trafficSummaries("node")])
      setItems(subscriptions.items)
      setNodes(nodeResult.items)
      setTraffic(new Map(trafficResult.items.map((item) => [item.resource_id, item])))
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "加载订阅失败", "error")
    } finally {
      setLoading(false)
    }
  }, [onNotice])

  useEffect(() => {
    void load()
  }, [load])

  const metrics = useMemo(() => {
    const enabled = items.filter((item) => item.enabled).length
    const snapshots = items.filter((item) => item.last_success_snapshot_id).length
    const failing = items.filter((item) => item.consecutive_failures > 0).length
    const totalTraffic = Array.from(traffic.values()).reduce((total, item) => total + item.upload_bytes + item.download_bytes, 0)
    return { enabled, snapshots, failing, totalTraffic }
  }, [items, traffic])

  const nodesBySubscription = useMemo(() => {
    const grouped = new Map<string, NodeRecord[]>()
    for (const node of nodes) {
      for (const source of node.sources) {
        const current = grouped.get(source.subscription_id) || []
        if (!current.some((item) => item.id === node.id)) current.push(node)
        grouped.set(source.subscription_id, current)
      }
    }
    return grouped
  }, [nodes])

  async function checkNodes(subscription?: Subscription) {
    const key = subscription ? `test:${subscription.id}` : "test:all"
    const selected = subscription ? nodesBySubscription.get(subscription.id) || [] : nodes.filter((node) => node.lifecycle_state !== "disabled" && node.lifecycle_state !== "retired")
    if (selected.length === 0) {
      onNotice("当前范围没有可测试节点", "error")
      return
    }
    setBusy((current) => ({ ...current, [key]: true }))
    try {
      const result = await api.checkNodes(selected.map((node) => node.id))
      const success = result.items.filter((item) => item.success).length
      onNotice(`测试完成：${success}/${result.items.length} 个节点可用`, success === result.items.length ? "success" : "error")
      await load()
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "批量测试失败", "error")
    } finally {
      setBusy((current) => ({ ...current, [key]: false }))
    }
  }

  async function refresh(item: Subscription) {
    setBusy((current) => ({ ...current, [item.id]: true }))
    try {
      const result = await api.refreshSubscription(item.id)
      onNotice(
        result.changed
          ? `刷新成功：解析并写入 ${result.estimated_nodes} 个去重节点`
          : `内容未变化：已确认 ${result.estimated_nodes} 个节点关系`,
      )
      await load()
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "刷新订阅失败", "error")
      await load()
    } finally {
      setBusy((current) => ({ ...current, [item.id]: false }))
    }
  }

  async function refreshAll() {
	  setBusy((current) => ({ ...current, "refresh:all": true }))
	  try {
	    const result = await api.refreshSubscriptions(items.filter((item) => item.enabled).map((item) => item.id))
	    const success = result.items.filter((item) => item.success).length
	    onNotice(`批量刷新完成：${success}/${result.items.length} 个订阅成功`, success === result.items.length ? "success" : "error")
	    await load()
	  } catch (error) {
	    onNotice(error instanceof Error ? error.message : "批量刷新失败", "error")
	  } finally {
	    setBusy((current) => ({ ...current, "refresh:all": false }))
	  }
  }

  async function remove() {
    const item = deleteTarget
    if (!item) return
    setBusy((current) => ({ ...current, [item.id]: true }))
    try {
      await api.deleteSubscription(item.id, item.version)
      setItems((current) => current.filter((candidate) => candidate.id !== item.id))
      setDeleteTarget(null)
      onNotice("订阅已删除")
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "删除订阅失败", "error")
    } finally {
      setBusy((current) => ({ ...current, [item.id]: false }))
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        {!embedded && <div>
          <h1 className="text-xl font-semibold tracking-tight">订阅</h1>
          <p className="mt-1 max-w-3xl text-sm leading-5 text-muted-foreground">
            管理加密订阅来源、持久化刷新计划和最近成功快照。刷新成功后会解析并更新节点库存。
          </p>
        </div>}
        <div className={cn("grid w-full grid-cols-2 gap-2 sm:flex sm:w-auto sm:shrink-0", embedded && "sm:ml-auto")}>
          <Button variant="outline" onClick={() => void checkNodes()} disabled={loading || Boolean(busy["test:all"])}>
            {busy["test:all"] ? <LoaderCircle className="animate-spin" /> : <Gauge />}
            测试节点
          </Button>
          <Button variant="outline" onClick={() => void refreshAll()} disabled={loading || Boolean(busy["refresh:all"]) || metrics.enabled === 0}>
            {busy["refresh:all"] ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
            刷新订阅
          </Button>
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCw className={loading ? "animate-spin" : ""} />
            重新加载
          </Button>
          <Button onClick={() => setShowCreate(true)}>
            <Plus />
            新建订阅
          </Button>
        </div>
      </div>

      <div className="grid overflow-hidden rounded-lg border bg-card sm:grid-cols-2 xl:grid-cols-4">
        <Metric label="启用订阅" value={metrics.enabled} helper={`总计 ${items.length} 条`} />
        <Metric label="已有成功快照" value={metrics.snapshots} helper="失败刷新不会覆盖成功版本" border />
        <Metric label="连续失败" value={metrics.failing} helper={metrics.failing ? "按指数退避等待重试" : "当前没有失败订阅"} border warning={metrics.failing > 0} />
        <Metric label="节点累计流量" value={formatBytes(metrics.totalTraffic)} helper="所有去重节点的上传与下载总和" border />
      </div>

      <section className="overflow-hidden rounded-lg border bg-card">
        <div className="flex items-center justify-between border-b bg-muted/60 px-3 py-2.5">
          <div>
            <div className="text-sm font-semibold">订阅列表</div>
            <div className="mt-0.5 text-[11px] text-muted-foreground">来源配置已加密，页面与 API 均不回显原文</div>
          </div>
          <Badge variant="secondary">{items.length}</Badge>
        </div>

        {loading ? (
          <div className="flex min-h-48 items-center justify-center gap-2 text-sm text-muted-foreground">
            <LoaderCircle className="size-4 animate-spin" />
            正在加载订阅
          </div>
        ) : items.length === 0 ? (
          <div className="flex min-h-56 flex-col items-center justify-center px-6 text-center">
            <Radio className="mb-3 size-8 text-muted-foreground" />
            <div className="font-medium">还没有订阅</div>
            <p className="mt-1 max-w-md text-xs leading-5 text-muted-foreground">
              可添加远程 URL、内联 URI/Base64/YAML，或服务器上的普通文件。创建后执行刷新即可生成节点库存。
            </p>
            <Button className="mt-4" onClick={() => setShowCreate(true)}>
              <Plus />
              新建第一条订阅
            </Button>
          </div>
        ) : (
          <div className="divide-y">
            {items.map((item) => (
              <SubscriptionRow
                key={item.id}
                item={item}
                nodes={nodesBySubscription.get(item.id) || []}
                traffic={traffic}
                expanded={expanded.has(item.id)}
                busy={Boolean(busy[item.id])}
                testing={Boolean(busy[`test:${item.id}`])}
                onToggle={() => setExpanded((current) => {
                  const next = new Set(current)
                  if (next.has(item.id)) next.delete(item.id)
                  else next.add(item.id)
                  return next
                })}
                onCheck={() => void checkNodes(item)}
                onRefresh={() => void refresh(item)}
                onEdit={() => setEditTarget(item)}
                onDelete={() => setDeleteTarget(item)}
              />
            ))}
          </div>
        )}
      </section>

      {showCreate && (
        <CreateSubscriptionForm
          onCancel={() => setShowCreate(false)}
          onError={(message) => onNotice(message, "error")}
          onSaved={(created) => {
            setShowCreate(false)
            setItems((current) => [created, ...current])
            onNotice("订阅已创建，可立即手动刷新")
          }}
        />
      )}

      {editTarget && (
        <CreateSubscriptionForm
          subscription={editTarget}
          onCancel={() => setEditTarget(null)}
          onError={(message) => onNotice(message, "error")}
          onSaved={(updated) => {
            setEditTarget(null)
            setItems((current) => current.map((item) => item.id === updated.id ? updated : item))
            onNotice("订阅设置已保存")
          }}
        />
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除订阅"
        description={`将删除“${deleteTarget?.name ?? ""}”及其快照来源关系。仅被该订阅引用的节点会随最新活动快照变化进入退役状态。`}
        busy={deleteTarget ? Boolean(busy[deleteTarget.id]) : false}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => void remove()}
      />
    </div>
  )
}

function SubscriptionRow({
  item,
  nodes,
  traffic,
  expanded,
  busy,
  testing,
  onToggle,
  onCheck,
  onRefresh,
  onEdit,
  onDelete,
}: {
  item: Subscription
  nodes: NodeRecord[]
  traffic: Map<string, TrafficSummary>
  expanded: boolean
  busy: boolean
  testing: boolean
  onToggle: () => void
  onCheck: () => void
  onRefresh: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const source = sourceMeta[item.source_type]
  const SourceIcon = source.icon
  const totalTraffic = nodes.reduce((total, node) => {
    const value = traffic.get(node.id)
    return total + (value?.upload_bytes ?? 0) + (value?.download_bytes ?? 0)
  }, 0)
  return (
    <article>
      <div className="grid gap-3 px-3 py-3 transition-colors hover:bg-muted/40 lg:grid-cols-[minmax(220px,1.25fr)_minmax(180px,.8fr)_minmax(250px,1fr)_auto] lg:items-center">
        <div className="flex min-w-0 items-center gap-2.5">
          <Button variant="ghost" size="icon" onClick={onToggle} aria-label={`${expanded ? "收起" : "展开"} ${item.name}`} className="size-7 shrink-0">
            {expanded ? <ChevronDown /> : <ChevronRight />}
          </Button>
          <div className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-card text-muted-foreground"><SourceIcon className="size-4" /></div>
          <button type="button" className="min-w-0 text-left" onClick={onEdit} title="查看与编辑订阅">
            <span className="block truncate font-medium text-foreground hover:text-primary">{item.name}</span>
            <span className="mt-0.5 block truncate font-mono text-[11px] text-muted-foreground" title={item.id}>{compactId(item.id)} · v{item.version}</span>
          </button>
        </div>

        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline"><SourceIcon className="mr-1 size-3" />{source.label}</Badge>
          {/* 来源类别决定了这类节点失败后怎么处理, 所以它是行上的主要信息之一:
              一个池里掉几个节点是零代价, 一个住宅源掉了要人修。 */}
          <Badge variant={sourceClassLabel[item.source_class].variant} title={sourceClassLabel[item.source_class].detail}>{sourceClassLabel[item.source_class].label}</Badge>
          <Badge variant={item.enabled ? "success" : "secondary"}>{item.enabled ? "已启用" : "已禁用"}</Badge>
          {item.consecutive_failures > 0 && <Badge variant="warning">失败 {item.consecutive_failures}</Badge>}
          <span className="w-full text-[11px] text-muted-foreground">{nodes.length} 个节点 · {formatBytes(totalTraffic)}</span>
        </div>

        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-[11px] sm:grid-cols-4 lg:grid-cols-2">
          <SubscriptionDetail label="最近刷新" value={formatDate(item.last_refresh_attempt_at)} />
          <SubscriptionDetail label="下次刷新" value={formatDate(item.next_refresh_at)} />
          <SubscriptionDetail label="刷新计划" value={item.refresh_cron ? `Cron ${item.refresh_cron}` : `${item.refresh_interval_seconds}s`} />
          <SubscriptionDetail label="成功快照" value={item.last_success_snapshot_id ? compactId(item.last_success_snapshot_id) : "暂无"} mono />
        </dl>

        <div className="flex flex-wrap items-center gap-1.5 lg:justify-end">
          <Button variant="ghost" size="icon" onClick={onEdit} aria-label={`编辑 ${item.name}`} title="编辑订阅"><Pencil /></Button>
          <Button variant="outline" size="sm" onClick={onCheck} disabled={busy || testing || nodes.length === 0}>{testing ? <LoaderCircle className="animate-spin" /> : <Gauge />}测试</Button>
          <Button variant="outline" size="sm" onClick={onRefresh} disabled={busy || !item.enabled}>{busy ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}刷新</Button>
          <Button variant="ghost" size="icon" onClick={onDelete} disabled={busy} aria-label={`删除 ${item.name}`} title="删除订阅"><Trash2 className="text-destructive" /></Button>
        </div>
      </div>
      {expanded && <div className="border-t bg-muted/40 px-3 py-3 sm:pl-14">
        {nodes.length === 0 ? <div className="py-3 text-center text-xs text-muted-foreground">该订阅尚无活动节点，请先刷新订阅。</div> : (
          <div className="divide-y overflow-hidden rounded-md border bg-card">
            {nodes.map((node) => <SubscriptionNode key={node.id} node={node} traffic={traffic.get(node.id)} />)}
          </div>
        )}
      </div>}
    </article>
  )
}

function SubscriptionDetail({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="min-w-0"><dt className="text-muted-foreground">{label}</dt><dd className={cn("truncate text-foreground", mono && "font-mono")} title={value}>{value}</dd></div>
}

function SubscriptionNode({ node, traffic }: { node: NodeRecord; traffic?: TrafficSummary }) {
  return (
    <div className="grid gap-2 px-3 py-2.5 text-xs sm:grid-cols-[minmax(180px,1fr)_auto_auto] lg:grid-cols-[minmax(200px,1fr)_auto_auto_minmax(180px,1fr)_auto] lg:items-center">
      <div className="flex min-w-0 items-center gap-2">
        <CircleDot className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate font-medium text-foreground" title={node.display_name}>{node.display_name}</span>
      </div>
      <Badge variant="outline">{node.protocol.toUpperCase()}</Badge>
      <Badge variant={node.lifecycle_state === "healthy" ? "success" : node.lifecycle_state === "quarantined" ? "warning" : "secondary"}>{node.lifecycle_state}</Badge>
      <div className="flex flex-wrap gap-1 sm:col-span-3 lg:col-span-1">{node.health_checks.length ? node.health_checks.map((check) => <Badge key={check.target_id} variant={check.success ? "success" : "destructive"}>{check.target_name}{check.latency_ms == null ? "" : ` ${check.latency_ms}ms`}</Badge>) : <span className="text-muted-foreground">未测试站点</span>}</div>
      <span className="text-right tabular-nums text-muted-foreground sm:col-start-3 lg:col-auto">{node.last_latency_ms == null ? "未测试" : `${node.last_latency_ms} ms`} · {formatBytes((traffic?.upload_bytes ?? 0) + (traffic?.download_bytes ?? 0))}</span>
    </div>
  )
}

function Metric({ label, value, helper, border = false, warning = false }: { label: string; value: number | string; helper: string; border?: boolean; warning?: boolean }) {
  return (
    <div className={cn("px-4 py-3", border && "border-t sm:border-l sm:border-t-0")}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={cn("mt-0.5 text-xl font-semibold tabular-nums", warning && "text-warning")}>{value}</div>
      <div className="mt-0.5 text-[11px] text-muted-foreground">{helper}</div>
    </div>
  )
}
