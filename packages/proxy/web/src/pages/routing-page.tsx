import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Activity, BookOpen, Cable, CheckCircle2, ChevronDown, ChevronRight, CircleAlert, Link2, ListTree, LoaderCircle, Network, Pencil, Plus, RefreshCw, Save, ScrollText, Server } from "lucide-react"

import { ConfirmDialog } from "@/components/confirm-dialog"
import { ActiveConnectionsPanel } from "@/components/active-connections"
import { CreateProxyServiceForm } from "@/components/create-proxy-service-form"
import { EditProxyServiceForm } from "@/components/edit-proxy-service-form"
import { LiveLogsPanel, TrafficPanel } from "@/components/service-observability"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api, invalidateQueries } from "@/lib/api"
import { matchesRegion, regionLabel, strategyLabel } from "@/lib/proxy-groups"
import { serviceShape, shapeMeta, type ServiceShape } from "@/lib/service-shape"
import type { DataPlaneStatus, ListenerRecord, NodeRecord, ProxyGroup, ResidentialChannel, RoutingAction, RoutingRulesConfig, RoutingRuleSet, SharedInboundSettings, Subscription } from "@/lib/types"
import { cn, formatDate } from "@/lib/utils"
import { DockerAccessDialog } from "@/components/docker-access-dialog"

interface RoutingPageProps {
  onNotice: (message: string, tone?: "success" | "error") => void
}

export function RoutingPage({ onNotice }: RoutingPageProps) {
  const [nodes, setNodes] = useState<NodeRecord[]>([])
  const [subscriptions, setSubscriptions] = useState<Subscription[]>([])
  const [groups, setGroups] = useState<ProxyGroup[]>([])
  const [listeners, setListeners] = useState<ListenerRecord[]>([])
  const [residentialChannels, setResidentialChannels] = useState<ResidentialChannel[]>([])
  const [status, setStatus] = useState<DataPlaneStatus | null>(null)
  const [routingRules, setRoutingRules] = useState<RoutingRulesConfig>({ rule_sets: [] })
  // Shared-inbound settings decide whether a new service opens its own port or
  // joins the one-client entry points; the banner and the create form read it.
  const [sharedInbound, setSharedInbound] = useState<SharedInboundSettings | null>(null)
  const loadVersion = useRef(0)
  const [membersLoading, setMembersLoading] = useState(true)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ listener: ListenerRecord; group?: ProxyGroup } | null>(null)
  // Collapsed by default; a row expands on click, on the chevron, or when it
  // is opened for editing.
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [editing, setEditing] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)

  const load = useCallback(async () => {
    const version = ++loadVersion.current
    setLoading(true)
    setMembersLoading(true)
    // Inventory and runtime health must not hold the service list hostage.
    const members = api.listNodes(undefined, { health: false }).then((result) => {
      if (version === loadVersion.current) setNodes(result.items.filter((item) => !["disabled", "retired"].includes(item.lifecycle_state)))
    }).catch((error) => {
      if (version === loadVersion.current) onNotice(error instanceof Error ? error.message : "节点加载失败", "error")
    }).finally(() => { if (version === loadVersion.current) setMembersLoading(false) })
    void api.dataPlaneStatus().then((result) => { if (version === loadVersion.current) setStatus(result) }).catch(() => {})
    try {
      const [subscriptionResult, groupResult, listenerResult, ruleResult, residentialResult, settingsResult] = await Promise.all([
        api.listSubscriptions(), api.listProxyGroups(), api.listListeners(), api.routingRules(), api.listResidentialChannels(), api.globalSettings(),
      ])
      if (version !== loadVersion.current) return
      setSubscriptions(subscriptionResult.items.filter((item) => item.enabled))
      setGroups(groupResult.items)
      // Members of a shared entry point keep their own row (their credential
      // and group live there) and are listed as services like any other. The
      // aggregate carrier row is filtered out: it holds no credential and no
      // group of its own, so it must never be editable or deletable.
      const visibleListeners = listenerResult.items.filter((item) => !item.shared_inbound_aggregate)
      setListeners(visibleListeners)
      // Rows keep whatever expansion the user left them with: they start
      // collapsed and a reload must not re-open them.
      // 见 .agents/notes/implemented/simplification/2026-09-29-routing-rows-start-collapsed.md
      setResidentialChannels(residentialResult.items)
      setRoutingRules(ruleResult)
      setSharedInbound(settingsResult.shared_inbound ?? null)
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "加载代理服务失败", "error")
    } finally {
      if (version === loadVersion.current) setLoading(false)
    }
    await members
  }, [onNotice])

  useEffect(() => { void load(); return () => { loadVersion.current++ } }, [load])

  // Resolve each service's member list once per data load instead of on every
  // render; with thousands of nodes this filter chain is the page's hot path.
  //
  // A pinned service (listener.node_id) is resolved to that one node and never
  // through its group. The group on a pinned listener is a schema anchor shared
  // by every pinned service (see egressAnchorGroup), so resolving through it
  // listed the anchor's members instead: "ioa-vm-mixed" — pinned to the single
  // ioa-vm node — rendered the 10 members of 良心云, and its member count was
  // that group's. Read the pin first.
  const serviceNodes = useMemo(() => {
    const byListener = new Map<string, NodeRecord[]>()
    for (const listener of listeners) {
      if (listener.node_id) {
        byListener.set(listener.id, nodes.filter((node) => node.id === listener.node_id))
        continue
      }
      const group = groups.find((item) => item.id === listener.proxy_group_id)
      byListener.set(listener.id, resolveServiceNodes(group, nodes))
    }
    return byListener
  }, [groups, listeners, nodes])

  async function apply() {
    setBusy(true)
    try {
      setStatus(await api.applyDataPlane())
      onNotice("Mihomo 配置已重新编译并应用")
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "应用数据面失败", "error")
      setStatus(await api.dataPlaneStatus().catch(() => null))
    } finally { setBusy(false) }
  }

  async function removeService() {
    if (!deleteTarget) return
    setBusy(true)
    const target = deleteTarget
    try {
      // The server revokes this service's Docker entry before deleting it, so
      // the entry row (marked docker_access_for) no longer shares the group.
      await api.deleteListener(target.listener.id, target.listener.version)
      // The group is only removed when it belongs to this service alone.
      //
      // 2026-10-01: this used to be "if there is a group, delete it". That was
      // written when every service owned a group built for it. It stopped
      // being true once a listener could name its node directly: a pinned
      // listener still carries a non-null proxy_group_id, but that group is a
      // shared schema anchor (egressAnchorGroup), not this service's policy.
      // Deleting a service therefore tried to delete the operator's main
      // subscription group and surfaced the refusal as a failure of the whole
      // action, so the row stayed and the operator retried into a confusing
      // 404. Only a group this listener is the last user of may go.
      const group = target.group
      const remaining = listeners.filter((item) => item.docker_access_for !== target.listener.id)
      if (group && isSoleOwnerOfGroup(remaining, target.listener.id, group.id)) {
        await api.deleteProxyGroup(group.id, group.version)
      }
      onNotice("代理服务及其入口已删除")
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "删除代理服务失败", "error")
    } finally {
      // Cleared on both paths: leaving it set kept the dialog armed, so a
      // second click replayed a delete for a service that was already gone.
      setDeleteTarget(null)
      setBusy(false)
      await load()
    }
  }


  return (
    <div className="space-y-4 2xl:flex 2xl:h-full 2xl:min-h-0 2xl:flex-col 2xl:gap-4 2xl:space-y-0">
      <div className="flex shrink-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold">代理服务</h1>
          <p className="mt-1 max-w-3xl text-sm leading-5 text-muted-foreground">每项服务同时定义入口 IP、端口、登录账号，以及它可以使用的跨订阅节点规则。</p>
        </div>
        <div className="grid w-full grid-cols-3 gap-2 sm:flex sm:w-auto sm:shrink-0">
          <Button variant="outline" onClick={() => { invalidateQueries(); void load() }} disabled={loading || membersLoading || busy}><RefreshCw className={loading ? "animate-spin" : ""} />刷新</Button>
          <Button variant="outline" onClick={() => void apply()} disabled={busy}>{busy ? <LoaderCircle className="animate-spin" /> : <Server />}重新应用</Button>
          <Button disabled={membersLoading || loading} onClick={() => setShowCreate(true)}><Plus />新建代理服务</Button>
        </div>
      </div>

      {membersLoading && <p role="status" className="text-xs text-muted-foreground">正在加载节点成员，服务入口已可查看…</p>}
      <div className="shrink-0"><DataPlaneBand status={status} loading={loading} /></div>
      <ActiveConnectionsPanel />

      {sharedInbound?.mode === "shared" && (
        <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-info-border bg-info-muted px-3 py-2 text-xs text-info-foreground">
          <Cable className="size-3.5 shrink-0" />
          <span className="font-medium">共享入口已启用</span>
          <span className="text-muted-foreground">{`全部 HTTP / SOCKS5 / Mixed 服务共用本机 ${sharedInbound.mixed_bind_address || "0.0.0.0"}:${sharedInbound.mixed_port || 7890}，全部 VLESS / VMess / Trojan 服务共用内部端口 ${sharedInbound.ws_port || 7891}`}{sharedInbound.ws_public_host ? `（公网 ${sharedInbound.ws_public_host}:${sharedInbound.ws_public_port || 443}）` : "（未配置公网域名，订阅暂不可用）"}。</span>
          <span className="text-muted-foreground">1 个客户端进程即可覆盖所有代理组与协议；在「全局配置 → 性能与运行」可关闭。</span>
        </div>
      )}

      {/*
        列表独占整行。它在宽度上本来就有硬需求: 每行要塞下服务名 + 4-5 个徽章
        + 地址 + 策略 + 操作下拉, 542px 以下的列宽就会让徽章溢出到策略列上 (实测)。
        新建表单改为弹窗后, 这里不再需要为它留一列, 于是这个约束也一并消失。
        高度锁与 2xl 断点的来历见
        .agents/notes/implemented/bug-fix/2026-09-18-proxy-service-list-collapses.md。
      */}
      <div className="2xl:min-h-0 2xl:flex-1">
        <section className="flex h-full min-h-0 flex-col overflow-hidden rounded-lg border bg-card">
          <PanelHeader title="服务列表" description="入口与节点策略保持一一对应" count={listeners.length} />
          {listeners.length === 0 ? <EmptyState onCreate={() => setShowCreate(true)} /> : <div className="min-h-48 divide-y overflow-y-auto 2xl:min-h-0 2xl:flex-1">{listeners.map((listener) => {
            const group = groups.find((item) => item.id === listener.proxy_group_id)
            const residential = residentialChannels.find((channel) => channel.listener_id === listener.id)
            // Docker entries are marked by the server (docker_access_for).
            const dockerEntryFor = listener.docker_access_for ? (listeners.find((item) => item.id === listener.docker_access_for)?.name ?? "") : undefined
            const hasDockerEntry = listeners.some((item) => item.docker_access_for === listener.id)
            return <ServiceRow key={listener.id} listener={listener} dockerEntryFor={dockerEntryFor} hasDockerEntry={hasDockerEntry} group={group} groups={groups} routingRules={routingRules} nodes={serviceNodes.get(listener.id) ?? []} subscriptions={subscriptions} allNodes={nodes} residential={residential} sharedInbound={sharedInbound} expanded={expanded.has(listener.id)} editing={editing === listener.id} onToggle={() => setExpanded((current) => { const next = new Set(current); if (next.has(listener.id)) next.delete(listener.id); else next.add(listener.id); return next })} onEdit={() => { setExpanded((current) => new Set(current).add(listener.id)); setEditing(listener.id) }} onCloseEdit={() => setEditing(null)} onRoutingChanged={setRoutingRules} onChanged={async () => { setEditing(null); await load() }} onDelete={() => setDeleteTarget({ listener, group })} onNotice={onNotice} />
          })}</div>}
        </section>
      </div>

      {showCreate && (
        <CreateProxyServiceForm nodes={nodes} subscriptions={subscriptions} residentialChannels={residentialChannels} sharedInbound={sharedInbound} onCreated={load} onCancel={() => setShowCreate(false)} onNotice={onNotice} />
      )}

      <ConfirmDialog open={deleteTarget !== null} title="删除代理服务" description={deleteDescription(deleteTarget, listeners)} busy={busy} onCancel={() => setDeleteTarget(null)} onConfirm={() => void removeService()} />
    </div>
  )
}

async function copyText(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value)
    return true
  } catch {
    return false
  }
}

/**
 * 该 listener 是否是「这个组的唯一使用者」——只有这种情况删服务才附带删组。
 *
 * 为什么必须判: listener 的 proxy_group_id 是 NOT NULL 列, 所以**未独占的** listener
 * (共享入口成员、被钉到节点的服务) 也会指向某个组。那个组通常是别的服务的策略,
 * 或者只是一个 schema 锚组, 被很多 listener 共用 (见 egressAnchorGroup)。
 * 旧代码只看「有没有组」, 于是删一个服务会顺手动到别的服务赖以出网的组。
 *
 * 判据是"还有没有别的 listener 指向它" —— 用整个列表数, 不用"组名像不像专属"这类猜测。
 */
/**
 * 删除确认的文案必须与真正会发生的事一致: 共享组时明说「组会保留」, 否则操作员要么以为
 * 自己的节点策略跟着没了, 要么以为删干净了却在原处又看到它。
 */
function deleteDescription(target: { listener: ListenerRecord; group?: ProxyGroup } | null, listeners: ListenerRecord[]): string {
  const endpoint = String(target?.listener.bind_address ?? "") + ":" + String(target?.listener.port ?? "")
  if (!target) return "将关闭 " + endpoint + "。"
  const group = target.group
  if (group && isSoleOwnerOfGroup(listeners, target.listener.id, group.id)) {
    return "将关闭 " + endpoint + "，并删除只被它使用的代理组「" + group.name + "」。"
  }
  if (group) {
    return "将关闭 " + endpoint + "。代理组「" + group.name + "」仍被其他服务使用，会被保留。"
  }
  return "将关闭 " + endpoint + "。"
}

function isSoleOwnerOfGroup(listeners: ListenerRecord[], listenerID: string, groupID: string): boolean {
  if (!groupID) return false
  return listeners.filter((item) => item.proxy_group_id === groupID).length === 1
    && listeners.some((item) => item.id === listenerID && item.proxy_group_id === groupID)
}

/**
 * 来源摘要必须按**形态**说, 而不是按 source_spec 的形状说。
 *
 * 旧写法只数 subscription_ids, 于是三种完全不同的服务渲染出同一句话
 * 「1 个订阅 · Top 全部」: 41 节点的付费订阅、1305 成员的一次性池、以及被钉死
 * 到单节点的服务。操作员无法从行上判断哪一个是池、哪一个掉了要人修。
 */
function serviceSourceText(shape: ServiceShape, spec: ProxyGroup["source_spec"] | undefined, listener: ListenerRecord, subscriptions: Subscription[], nodes: NodeRecord[]): string {
  if (shape === "pinned") {
    const pinned = nodes.find((node) => node.id === listener.node_id)
    return pinned ? `钉死节点 ${pinned.display_name}` : "钉死节点（该节点已不在库存中）"
  }
  if (shape === "manual") return `${spec?.node_ids.length ?? 0} 个手工指定的节点`
  if (shape === "direct" || !spec) return "当前服务器 DIRECT 出口"
  const names = (spec.subscription_ids ?? []).map((id) => subscriptions.find((item) => item.id === id)?.name).filter((name): name is string => Boolean(name))
  const listed = names.length > 2 ? `${names.slice(0, 2).join("、")} 等 ${names.length} 个源` : names.join("、")
  if (shape === "pool") return `${listed}${spec.limit ? ` · 取前 ${spec.limit}` : " · 全量"}`
  if (shape === "residential") return `${listed || "住宅源"} · 不参与自动淘汰`
  if (shape === "mixed") return `${listed || "多来源"} · 混合来源`
  return `${listed}${spec.regions?.length ? ` · ${spec.regions.map(regionLabel).join("/")}` : ""} · Top ${spec.limit || "全部"}`
}

function endpointAddress(listener: ListenerRecord, endpoint: ListenerRecord["public_endpoint"]): string {
  const configuredHost = endpoint?.host?.trim()
  const bindHost = listener.bind_address.trim()
  const host = configuredHost || (isPublicBindAddress(bindHost) ? bindHost : "")
  if (!host) return ""
  const formattedHost = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host
  return `${formattedHost}:${endpoint?.port || listener.port}`
}

function isPublicBindAddress(value: string): boolean {
  return value !== "127.0.0.1" && value !== "::1" && value !== "0.0.0.0" && value !== "::" && !value.startsWith("127.")
}

function ServiceRow({ listener, dockerEntryFor, hasDockerEntry, group, groups, routingRules, nodes, subscriptions, allNodes, residential, sharedInbound, expanded, editing, onToggle, onEdit, onCloseEdit, onRoutingChanged, onChanged, onDelete, onNotice }: { listener: ListenerRecord; dockerEntryFor?: string; hasDockerEntry: boolean; group?: ProxyGroup; groups: ProxyGroup[]; routingRules: RoutingRulesConfig; nodes: NodeRecord[]; subscriptions: Subscription[]; allNodes: NodeRecord[]; residential?: ResidentialChannel; sharedInbound: SharedInboundSettings | null; expanded: boolean; editing: boolean; onToggle: () => void; onEdit: () => void; onCloseEdit: () => void; onRoutingChanged: (config: RoutingRulesConfig) => void; onChanged: () => Promise<void>; onDelete: () => void; onNotice: RoutingPageProps["onNotice"] }) {
  const [action, setAction] = useState("")
  const [dockerOpen, setDockerOpen] = useState(false)
  const [detailTab, setDetailTab] = useState("members")
  const spec = group?.source_spec
  // The row's shape is derived from the declared source class, so a pool, a
  // paid subscription and a pinned exit stop rendering as the same string.
  // See lib/service-shape.ts for the one table behind badge, summary and forms.
  const shape = serviceShape(listener, group, allNodes, subscriptions)
  const shapeInfo = shapeMeta[shape]
  // A pinned service must not advertise a strategy it cannot use: the compiler
  // targets the node name directly and the group is only a schema anchor.
  const strategyText = listener.node_id ? "固定出口" : group ? strategyLabel(group.strategy) : "组已缺失"
  const sourceText = serviceSourceText(shape, spec, listener, subscriptions, allNodes)

  async function copyShareLink(format: "v2rayn" | "clash" | "sing-box" | "uri", client: string) {
    if (!listener.share_path) return
    const ok = await copyText(api.listenerShareURL(listener.share_path, format))
    onNotice(ok ? `${client} 订阅链接已复制` : "复制失败，请手动复制", ok ? "success" : "error")
  }

  async function copyEndpoint() {
    // 复制入口地址时，公开端点缺省就直接用本机监听地址。
    // 不少场景（管理员本机、同主机容器等）连接 127.0.0.1 本就是通的，
    // 不再强制要求公网域名才能复制入口地址。
    const address = endpointAddress(listener, publicEndpoint) || `${listener.bind_address}:${listener.port}`
    const ok = await copyText(address)
    onNotice(ok ? "入口地址已复制" : "复制失败，请手动复制", ok ? "success" : "error")
  }

  async function copyResidentialURL(kind: "clash" | "control") {
    const value = kind === "clash" ? residential?.subscription_url : residential?.control_url
    if (!value) {
      onNotice(kind === "clash" ? "请先为住宅渠道配置声明节点和可用公网端点" : "该住宅渠道没有可用的自动化控制 URL", "error")
      return
    }
    const ok = await copyText(value)
    const label = kind === "clash" ? "Clash / Mihomo 订阅" : "自动化控制 URL"
    onNotice(ok ? `${label}已复制` : "复制失败，请手动复制", ok ? "success" : "error")
  }

  async function copyConnectionURI() {
    if (!listener.share_path) return
    try {
      const content = await api.listenerShareContent(listener.share_path)
      const ok = await copyText(content.trimEnd())
      onNotice(ok ? "认证连接 URI 已复制" : "复制失败，请手动复制", ok ? "success" : "error")
    } catch (cause) {
      onNotice(cause instanceof Error ? cause.message : "读取认证连接 URI 失败", "error")
    }
  }

  const directMember = group?.source_spec.include_direct ? 1 : 0
  const memberCount = nodes.length + directMember
  const healthyCount = nodes.filter((node) => node.lifecycle_state === "healthy").length + directMember
  // "Usable" is not "healthy": a quarantined member is still in the list but
  // the group never routes through it, so counting only healthy nodes made a
  // pool of hundreds read as `0/N`. The badge's denominator is the whole
  // inventory and its title says which part is routable.
  const routableCount = nodes.filter((node) => node.lifecycle_state === "healthy" || node.lifecycle_state === "degraded" || node.lifecycle_state === "candidate").length + directMember
  const publicEndpoint = residential?.public_endpoint ?? listener.public_endpoint
  function runAction(value: string) {
    setAction(value)
    if (value === "residential-clash") void copyResidentialURL("clash")
    else if (value === "residential-control") void copyResidentialURL("control")
    else if (value === "address") void copyEndpoint()
    else if (value === "docker") setDockerOpen(true)
    else if (value === "auth-uri") void copyConnectionURI()
    else if (value === "edit") { setDetailTab("edit"); onEdit() }
    else if (value === "delete") onDelete()
    else if (!residential) { const [format = "clash", client = format] = value.split(":"); void copyShareLink(format as "v2rayn" | "clash" | "sing-box" | "uri", client) }
    window.setTimeout(() => setAction(""), 0)
  }
  return (
    <div>
    <div
      className="grid cursor-pointer gap-x-4 gap-y-2 px-3 py-2.5 hover:bg-muted/60 lg:grid-cols-[minmax(0,1fr)_minmax(150px,190px)_190px] lg:items-center"
      onClick={onToggle}
      role="button"
      tabIndex={0}
      aria-expanded={expanded}
      onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onToggle() } }}
    >
      <div className="flex min-w-0 items-start gap-2.5">
        <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={(event) => { event.stopPropagation(); onToggle() }} aria-label={expanded ? "收起节点" : "展开节点"}>{expanded ? <ChevronDown /> : <ChevronRight />}</Button>
        <div className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-card text-muted-foreground"><Cable className="size-4" /></div>
        <div className="min-w-0 flex-1">
          {/* Small screens reserve a full line for the name instead of letting badges shrink it to zero. */}
          <div className="flex min-w-0 flex-wrap items-center gap-1.5 lg:flex-nowrap">
            {/* 标题必须是**这个服务**的名字, 不能借用组名。一个 listener 被钉到节点时, 它指向的组只是共享的 schema 锚组 (见 egressAnchorGroup): 借组名会让三个不同服务全都显示成同一个名字 (实测: ioa-vm / outlook / soloHK04 / hk-new-01 四行都显示「良心云」), 操作员无法分辨在删哪一个。 */}
            <span className="basis-full truncate font-medium lg:basis-auto" title={residential?.name || listener.name}>{residential?.name || listener.name}</span>
            {/* 形态徽章来自 source_class, 不是名字: 池 / 订阅 / 住宅 / 指定节点在行上必须一眼可分。 */}
            <Badge variant={shapeInfo.variant} className="shrink-0" title={shapeInfo.detail}>{shapeInfo.label}</Badge>
            <Badge variant="outline" className="shrink-0">{listener.kind.toUpperCase()}</Badge>
            {listener.shared_inbound && <Badge variant="outline" className="shrink-0">共享入口</Badge>}
            {dockerEntryFor !== undefined && <Badge variant="outline" className="shrink-0" title={dockerEntryFor ? `为「${dockerEntryFor}」创建的容器专用入口；在源服务的 Docker 使用方式中可撤销` : "容器专用入口"}>Docker 入口</Badge>}
            {residential && <Badge variant="outline" className="shrink-0">住宅会话</Badge>}
            <Badge variant={routableCount > 0 ? "success" : "warning"} className="shrink-0" title={`${routableCount} 个成员可参与路由（健康 ${healthyCount}，隔离的不参与）`}>{routableCount}/{memberCount} 可路由</Badge>
            <Badge variant={listener.auth_configured ? "warning" : "secondary"} className="shrink-0">{listener.auth_configured ? "账号认证" : "无认证"}</Badge>
          </div>
          {/* 地址截断而不是折行: 折行会撑高整行。完整值在 title 里。 */}
          <div
            className="mt-1 truncate font-mono text-[11px] text-muted-foreground"
            title={`本机 ${listener.bind_address}:${listener.port}${endpointAddress(listener, publicEndpoint) ? ` · 公网 ${endpointAddress(listener, publicEndpoint)}` : " · 未配置公网端点"}`}
          >
            本机 {listener.bind_address}:{listener.port}{endpointAddress(listener, publicEndpoint) ? ` · 公网 ${endpointAddress(listener, publicEndpoint)}` : " · 未配置公网端点"}
          </div>
        </div>
      </div>
      <div className="min-w-0"><div className="flex items-center gap-1.5 text-xs font-medium"><Network className="size-3.5 shrink-0" />{strategyText}</div><div className="mt-1 truncate text-[11px] text-muted-foreground" title={sourceText}>{sourceText}</div></div>
      <div className="w-full lg:w-[190px]">
        <Select value={action} onValueChange={runAction}><SelectTrigger onClick={(event) => event.stopPropagation()} className="h-8"><Link2 className="mr-1 size-3.5" /><SelectValue placeholder="服务操作" /></SelectTrigger><SelectContent><SelectItem value="edit">编辑服务</SelectItem>{!residential && listener.share_path && <><SelectItem value="clash:Clash / Mihomo">复制 Clash / Mihomo 订阅</SelectItem><SelectItem value="v2rayn:v2rayN / v2rayNG">复制 v2rayN / v2rayNG 订阅</SelectItem><SelectItem value="v2rayn:Shadowrocket">复制 Shadowrocket 订阅</SelectItem><SelectItem value="sing-box:sing-box / NekoBox">复制 sing-box / NekoBox 订阅</SelectItem><SelectItem value="auth-uri">复制认证连接 URI</SelectItem></>}{residential ? <><SelectItem value="residential-clash">复制 Clash / Mihomo 订阅</SelectItem><SelectItem value="residential-control">复制自动化控制 URL</SelectItem></> : <><SelectItem value="address">复制入口地址</SelectItem></>}{(listener.enabled || hasDockerEntry) && <SelectItem value="docker">Docker 使用方式</SelectItem>}<SelectItem value="delete" className="text-destructive">删除服务</SelectItem></SelectContent></Select>
      </div>
    </div>
    {dockerOpen && <DockerAccessDialog listener={listener} subscriptionURL={residential?.subscription_url} onClose={() => setDockerOpen(false)} onChanged={() => void onChanged()} />}
    {expanded && <div className="border-t bg-muted/70 px-3 py-3 sm:px-4">
      <Tabs value={editing ? "edit" : detailTab} onValueChange={(value) => { setDetailTab(value); if (value === "edit") onEdit(); else if (editing) onCloseEdit() }}>
        <TabsList className="max-w-full overflow-x-auto">
          <TabsTrigger value="members"><ListTree className="mr-1.5 size-3.5" />节点成员</TabsTrigger>
          <TabsTrigger value="traffic"><Activity className="mr-1.5 size-3.5" />流量统计</TabsTrigger>
          <TabsTrigger value="routes" disabled={!group}><BookOpen className="mr-1.5 size-3.5" />路由策略</TabsTrigger>
          <TabsTrigger value="logs"><ScrollText className="mr-1.5 size-3.5" />实时日志</TabsTrigger>
          <TabsTrigger value="edit" disabled={!group}><Pencil className="mr-1.5 size-3.5" />动态编辑</TabsTrigger>
        </TabsList>
        <TabsContent value="members"><div className="overflow-hidden rounded-md border bg-card">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b bg-muted/60 px-3 py-2 text-[11px] text-muted-foreground">
            <span className="font-medium text-foreground">{shapeInfo.label}</span>
            <span>{shapeInfo.detail}</span>
            <span className="ml-auto tabular-nums">共 {memberCount} 个，{routableCount} 个可路由</span>
          </div>
          {group?.source_spec.include_direct && <ServiceMember name="DIRECT（当前服务器出口）" protocol="DIRECT" state="可用" latency={null} source="本机" />}
          {nodes.map((node) => <ServiceMember key={node.id} name={node.display_name} protocol={node.protocol.toUpperCase()} state={node.lifecycle_state} latency={node.last_latency_ms} source={node.sources.map((item) => item.subscription_name).join("、") || "固定节点"} />)}
          {memberCount === 0 && <div className="px-3 py-4 text-center text-xs text-muted-foreground">当前规则没有命中可用节点</div>}
          {/* 池的成员是消耗品, 隔离的不会参与路由: 说清楚, 免得操作员以为它们还是候选。 */}
          {memberCount - routableCount > 0 && <div className="border-t px-3 py-2 text-[11px] text-muted-foreground">
            {shape === "pool"
              ? `另有 ${memberCount - routableCount} 个已隔离 / 退役成员：池内成员失败即被弃用，不会回到路由。`
              : `另有 ${memberCount - routableCount} 个已隔离 / 退役成员，当前不参与路由；${shape === "residential" ? "住宅源不会被自动退役，恢复后会自动回到列表。" : "恢复后会自动回到列表。"}`}
          </div>}
        </div></TabsContent>
        <TabsContent value="traffic"><TrafficPanel listenerId={listener.id} groupId={group?.id} nodes={nodes} /></TabsContent>
        <TabsContent value="routes">{group && <GroupRoutingPanel group={group} groups={groups} config={routingRules} onSaved={onRoutingChanged} onNotice={onNotice} />}</TabsContent>
        <TabsContent value="logs"><LiveLogsPanel listenerId={listener.id} /></TabsContent>
        <TabsContent value="edit">{group && <div className="overflow-hidden rounded-md border bg-card"><EditProxyServiceForm listener={listener} sharedInbound={sharedInbound} group={group} nodes={allNodes} subscriptions={subscriptions} onSaved={onChanged} onCancel={() => { setDetailTab("members"); onCloseEdit() }} onNotice={onNotice} /></div>}</TabsContent>
      </Tabs>
    </div>}
    </div>
  )
}

function GroupRoutingPanel({ group, groups, config, onSaved, onNotice }: { group: ProxyGroup; groups: ProxyGroup[]; config: RoutingRulesConfig; onSaved: (config: RoutingRulesConfig) => void; onNotice: RoutingPageProps["onNotice"] }) {
  const [values, setValues] = useState<Record<string, string>>(() => routeValues(config, group.id, groups))
  const [saving, setSaving] = useState(false)

  useEffect(() => { setValues(routeValues(config, group.id, groups)) }, [config, group.id, groups])

  async function save() {
    setSaving(true)
    try {
      const ruleSets = config.rule_sets.map((set) => {
        const routes = expandedRoutes(set, groups).filter((route) => route.proxy_group_id !== group.id)
        const action = actionFromValue(values[set.id] ?? "")
        if (action) routes.push({ proxy_group_id: group.id, action })
        return { ...set, routes, applied_group_ids: undefined, action: undefined }
      })
      const saved = await api.updateRoutingRules({ rule_sets: ruleSets })
      onSaved(saved)
      onNotice(`${group.name} 的路由策略已应用`)
    } catch (error) { onNotice(error instanceof Error ? error.message : "应用路由策略失败", "error") }
    finally { setSaving(false) }
  }

  const aliases = config.rule_sets.filter((set) => set.enabled)
  return <div className="overflow-hidden rounded-md border bg-card">
    <div className="flex items-center justify-between border-b bg-muted/60 px-3 py-2.5"><div><div className="text-xs font-semibold">{group.name} 的独立路由</div><div className="mt-0.5 text-[11px] text-muted-foreground">仅影响绑定到此代理组的入口流量，按站点别名优先级匹配。</div></div><Button size="sm" onClick={() => void save()} disabled={saving}>{saving ? <LoaderCircle className="animate-spin" /> : <Save />}保存策略</Button></div>
    {aliases.length === 0 ? <div className="px-4 py-8 text-center text-xs text-muted-foreground">请先到“站点别名”页面创建网页组。</div> : <div className="divide-y">{aliases.map((set) => <div key={set.id} className="grid gap-2 px-3 py-3 sm:grid-cols-[minmax(180px,1fr)_minmax(220px,320px)] sm:items-center"><div className="min-w-0"><div className="flex items-center gap-2"><span className="truncate text-xs font-medium">{set.name}</span><Badge variant="secondary">{set.rules.length} 项</Badge></div><div className="mt-1 truncate font-mono text-[11px] text-muted-foreground">{set.id}</div></div><Select value={values[set.id] || "none"} onValueChange={(value) => setValues((current) => ({ ...current, [set.id]: value === "none" ? "" : value }))}><SelectTrigger aria-label={`${set.name} 路由动作`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">不在此组使用</SelectItem><SelectItem value="direct">直连 DIRECT</SelectItem><SelectItem value="reject">阻断 REJECT</SelectItem>{groups.filter((item) => item.enabled).map((item) => <SelectItem key={item.id} value={`group:${item.id}`}>转到 {item.name}</SelectItem>)}</SelectContent></Select></div>)}</div>}
  </div>
}

function expandedRoutes(set: RoutingRuleSet, groups: ProxyGroup[]) {
  if (set.routes?.length) return [...set.routes]
  if (!set.action?.type) return []
  const ids = set.applied_group_ids?.length ? set.applied_group_ids : groups.map((group) => group.id)
  return ids.map((proxy_group_id) => ({ proxy_group_id, action: set.action as RoutingAction }))
}

function routeValues(config: RoutingRulesConfig, groupID: string, groups: ProxyGroup[]): Record<string, string> {
  return Object.fromEntries(config.rule_sets.map((set) => {
    const action = expandedRoutes(set, groups).find((route) => route.proxy_group_id === groupID)?.action
    return [set.id, action?.type === "proxy_group" ? `group:${action.proxy_group_id}` : action?.type ?? ""]
  }))
}

function actionFromValue(value: string): RoutingAction | null {
  if (value === "direct" || value === "reject") return { type: value }
  if (value.startsWith("group:")) return { type: "proxy_group", proxy_group_id: value.slice(6) }
  return null
}

function ServiceMember({ name, protocol, state, latency, source }: { name: string; protocol: string; state: string; latency: number | null | undefined; source: string }) {
  return <div className="grid gap-2 border-b px-3 py-2 text-xs last:border-b-0 sm:grid-cols-[minmax(180px,1fr)_90px_110px_90px_minmax(100px,0.7fr)] sm:items-center"><span className="truncate font-medium" title={name}>{name}</span><Badge variant="outline">{protocol}</Badge><Badge variant={state === "healthy" || state === "可用" ? "success" : state === "quarantined" ? "warning" : "secondary"}>{state}</Badge><span className="tabular-nums text-muted-foreground">{latency == null ? "未测试" : `${latency} ms`}</span><span className="truncate text-muted-foreground" title={source}>{source}</span></div>
}

function DataPlaneBand({ status, loading }: { status: DataPlaneStatus | null; loading: boolean }) {
  const running = status?.running === true
  return <section className="grid overflow-hidden rounded-lg border bg-card md:grid-cols-[220px_1fr]"><div className={cn("flex items-center gap-3 px-4 py-4", running ? "bg-success-muted" : status?.state === "failed" ? "bg-destructive/10" : "bg-muted/60")}>{loading ? <LoaderCircle className="size-5 animate-spin" /> : running ? <CheckCircle2 className="size-5 text-success" /> : <CircleAlert className="size-5 text-warning" />}<div><div className="font-semibold">{loading ? "检测中" : running ? "Mihomo 运行中" : status?.available ? "Mihomo 空闲" : "Mihomo 不可用"}</div><div className="mt-0.5 text-[11px] text-muted-foreground">{status?.listener_count ?? 0} 个活动入口</div></div></div><div className="grid gap-2 px-4 py-3 text-xs sm:grid-cols-3"><Info label="进程" value={status?.pid ? `PID ${status.pid}` : "—"} /><Info label="最近应用" value={formatDate(status?.last_apply_at)} /><Info label="版本" value={status?.version || "—"} /></div></section>
}

function PanelHeader({ title, description, count }: { title: string; description: string; count?: number }) { return <div className="flex items-center justify-between border-b bg-muted/60 px-3 py-2.5"><div><div className="text-sm font-semibold">{title}</div><div className="mt-0.5 text-[11px] text-muted-foreground">{description}</div></div>{count != null && <Badge variant="secondary">{count}</Badge>}</div> }
function EmptyState({ onCreate }: { onCreate: () => void }) { return <div className="flex min-h-48 flex-col items-center justify-center px-4 text-center"><Cable className="mb-2 size-7 text-muted-foreground" /><div className="text-sm font-medium">还没有代理服务</div><div className="mt-1 text-xs text-muted-foreground">创建入口并绑定跨订阅节点规则。</div><Button size="sm" className="mt-3" onClick={onCreate}><Plus />新建代理服务</Button></div> }
function Info({ label, value }: { label: string; value: string }) { return <div className="min-w-0"><div className="text-[11px] text-muted-foreground">{label}</div><div className="mt-0.5 block truncate" title={value}>{value}</div></div> }
function resolveServiceNodes(group: ProxyGroup | undefined, nodes: NodeRecord[]): NodeRecord[] {
  if (!group) return []
  const spec = group.source_spec
  const byID = new Map(nodes.map((node) => [node.id, node]))
  const result = spec.node_ids.map((id) => byID.get(id)).filter((node): node is NodeRecord => Boolean(node))
  if (spec.subscription_ids?.length) {
    const dynamic = nodes.filter((node) => {
      if (!node.sources.some((source) => spec.subscription_ids?.includes(source.subscription_id))) return false
      if (spec.protocols?.length && !spec.protocols.includes(node.protocol.toLowerCase())) return false
      if (spec.states?.length && !spec.states.includes(node.lifecycle_state as typeof spec.states[number])) return false
      if (spec.name_keywords?.length && !spec.name_keywords.some((keyword) => node.display_name.toLowerCase().includes(keyword.toLowerCase()))) return false
      if (spec.regions?.length && !spec.regions.some((region) => matchesRegion(node.display_name, region))) return false
      if (spec.max_latency_ms && (node.last_latency_ms == null || node.last_latency_ms > spec.max_latency_ms)) return false
      return true
    }).sort((left, right) => spec.sort_by === "name"
      ? left.display_name.localeCompare(right.display_name)
      : (left.last_latency_ms ?? Number.MAX_SAFE_INTEGER) - (right.last_latency_ms ?? Number.MAX_SAFE_INTEGER) || left.id.localeCompare(right.id))
    for (const node of (spec.limit ? dynamic.slice(0, spec.limit) : dynamic)) {
      if (!result.some((item) => item.id === node.id)) result.push(node)
    }
  }
  return result
}
