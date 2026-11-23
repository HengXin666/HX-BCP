import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react"
import {
  Activity,
  ArrowDownToLine,
  ArrowUpFromLine,
  Cable,
  CircleAlert,
  CircleCheck,
  LoaderCircle,
  RefreshCw,
  Route,
  ShieldBan,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { api } from "@/lib/api"
import { HostResources } from "@/components/host-resources"
import type { DataPlaneStatus, ListenerRecord, OverviewSample, ProxyGroup, RoutingRuleSet, TrafficSummary } from "@/lib/types"
import { cn } from "@/lib/utils"

interface OverviewPageProps {
  onNotice: (message: string, tone?: "success" | "error") => void
}

type OverviewData = {
  status: DataPlaneStatus
  groups: ProxyGroup[]
  listeners: ListenerRecord[]
  ruleSets: RoutingRuleSet[]
  listenerTotals: Map<string, TrafficSummary>
  listenerToday: Map<string, TrafficSummary>
}

type ChartTransition = {
  key: string
  samples: OverviewSample[]
  step: number
}

const windows = [30, 60, 120] as const

export function OverviewPage({ onNotice }: OverviewPageProps) {
  const [data, setData] = useState<OverviewData | null>(null)
  const [samples, setSamples] = useState<OverviewSample[]>([])
  const [windowSeconds, setWindowSeconds] = useState<(typeof windows)[number]>(60)
  const [loading, setLoading] = useState(true)
  const [streaming, setStreaming] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [status, groups, listeners, rules] = await Promise.all([
        api.dataPlaneStatus(),
        api.listProxyGroups(),
        api.listListeners(),
        api.routingRules(),
      ])
      const todayStart = new Date()
      todayStart.setHours(0, 0, 0, 0)
      const [totalResult, todayResult] = await Promise.allSettled([
        api.trafficSummaries("listener"),
        api.trafficSummaries("listener", todayStart, new Date()),
      ])
      setData({
        status,
        groups: groups.items,
        listeners: listeners.items,
        ruleSets: rules.rule_sets,
        listenerTotals: totalResult.status === "fulfilled" ? indexSummaries(totalResult.value.items) : new Map(),
        listenerToday: todayResult.status === "fulfilled" ? indexSummaries(todayResult.value.items) : new Map(),
      })
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "加载总览失败", "error")
    } finally {
      setLoading(false)
    }
  }, [onNotice])

  useEffect(() => { void load() }, [load])

  useEffect(() => {
    const source = new EventSource(api.overviewStreamURL())
    const appendSamples = (incoming: OverviewSample[]) => {
      setSamples((current) => {
        const byTimestamp = new Map(current.map((sample) => [sample.timestamp, sample]))
        for (const sample of incoming) byTimestamp.set(sample.timestamp, sample)
        return Array.from(byTimestamp.values()).sort((left, right) => Date.parse(left.timestamp) - Date.parse(right.timestamp)).slice(-120)
      })
      setStreaming(true)
    }
    source.addEventListener("history", (event) => {
      try {
        const history = JSON.parse((event as MessageEvent<string>).data) as OverviewSample[]
        if (Array.isArray(history)) appendSamples(history)
      } catch {
        setStreaming(false)
      }
    })
    source.addEventListener("sample", (event) => {
      try {
        appendSamples([JSON.parse((event as MessageEvent<string>).data) as OverviewSample])
      } catch {
        setStreaming(false)
      }
    })
    source.onerror = () => setStreaming(false)
    return () => source.close()
  }, [])

  const visibleSamples = useMemo(() => samples.slice(-windowSeconds), [samples, windowSeconds])
  const latest = samples.at(-1)
  const routes = useMemo(() => (data?.listeners ?? []).map((listener) => ({
    listener,
    group: data?.groups.find((group) => group.id === listener.proxy_group_id),
    ruleSets: (data?.ruleSets ?? []).filter((set) => set.enabled && setAppliesToGroup(set, listener.proxy_group_id)),
  })), [data])

  return <div className="space-y-5">
    <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div><h1 className="text-xl font-semibold">总览</h1><p className="mt-1 text-sm text-muted-foreground">流量、入口与分流状态</p></div>
      <Button variant="outline" onClick={() => void load()} disabled={loading}>{loading ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}刷新</Button>
    </header>

    <StatusBand status={data?.status ?? null} streaming={streaming} loading={loading} />

    <section className="grid border-y bg-card sm:grid-cols-2 xl:grid-cols-4">
      <Metric icon={ArrowDownToLine} label="当前下载" value={formatRate(latest?.download_bytes_per_second ?? 0)} color="text-info" />
      <Metric icon={ArrowUpFromLine} label="当前上传" value={formatRate(latest?.upload_bytes_per_second ?? 0)} color="text-success" />
      <Metric icon={Activity} label="活动连接" value={String(latest?.active_connections ?? 0)} color="text-primary" />
      <Metric icon={Route} label="有效入口" value={String(routes.filter((item) => item.listener.enabled).length)} color="text-warning" />
    </section>

    <HostResources onNotice={onNotice} />

    <section className="border-y bg-card py-4">
      <div className="flex flex-col gap-3 px-4 sm:flex-row sm:items-center sm:justify-between">
        <div><h2 className="text-sm font-semibold">实时流量</h2><p className="mt-0.5 text-xs text-muted-foreground">{visibleSamples.length} 个秒级采样点</p></div>
        <div className="inline-flex w-fit rounded-md border bg-muted p-0.5" aria-label="图表时间窗口">
          {windows.map((seconds) => <button key={seconds} type="button" onClick={() => setWindowSeconds(seconds)} className={cn("h-7 rounded px-2.5 text-xs font-medium text-muted-foreground", windowSeconds === seconds && "bg-card text-foreground shadow-sm")}>{seconds} 秒</button>)}
        </div>
      </div>
      <TrafficChart samples={visibleSamples} />
    </section>

    <section>
      <div className="mb-3 flex items-end justify-between gap-3"><div><h2 className="text-sm font-semibold">路由拓扑</h2><p className="mt-0.5 text-xs text-muted-foreground">{routes.length} 个 Listener 入口</p></div><Badge variant="outline">{data?.ruleSets.filter((item) => item.enabled).length ?? 0} 个规则集</Badge></div>
      {loading ? <div className="flex min-h-36 items-center justify-center text-sm text-muted-foreground"><LoaderCircle className="mr-2 size-4 animate-spin" />正在加载路由</div> : routes.length === 0 ? <div className="border-y bg-card px-4 py-12 text-center text-sm text-muted-foreground">暂无 Listener 路由</div> : <div className="grid gap-2 lg:grid-cols-2">{routes.map(({ listener, group, ruleSets }) => <RouteRow key={listener.id} listener={listener} group={group} ruleSets={ruleSets} samples={samples} total={data?.listenerTotals.get(listener.id)} today={data?.listenerToday.get(listener.id)} />)}</div>}
    </section>
  </div>
}

function StatusBand({ status, streaming, loading }: { status: DataPlaneStatus | null; streaming: boolean; loading: boolean }) {
  const running = status?.running === true
  const Icon = loading ? LoaderCircle : running ? CircleCheck : CircleAlert
  return <section className="flex flex-col border-y bg-card sm:flex-row sm:items-center">
    <div className={cn("flex min-w-56 items-center gap-3 px-4 py-3", running ? "bg-success-muted" : "bg-warning-muted")}><Icon className={cn("size-5", loading && "animate-spin", running ? "text-success" : "text-warning")} /><div><div className="text-sm font-semibold">{loading ? "读取状态" : running ? "Mihomo 运行中" : "Mihomo 未运行"}</div><div className="mt-0.5 text-[11px] text-muted-foreground">{status?.listener_count ?? 0} 入口 · {status?.proxy_count ?? 0} 代理</div></div></div>
    <div className="flex flex-1 items-center justify-between gap-3 px-4 py-3 text-xs"><span className="text-muted-foreground">秒级数据流</span><span className="inline-flex items-center gap-1.5 font-medium"><span className={cn("size-2 rounded-full", streaming ? "bg-success" : "bg-destructive")} />{streaming ? "已连接" : "正在重连"}</span></div>
  </section>
}

function Metric({ icon: Icon, label, value, color }: { icon: typeof Activity; label: string; value: string; color: string }) {
  return <div className="flex min-h-24 items-center gap-3 border-b px-4 py-3 last:border-b-0 sm:border-b-0 sm:border-r sm:last:border-r-0"><Icon className={cn("size-5 shrink-0", color)} /><div className="min-w-0"><div className="text-xs text-muted-foreground">{label}</div><div className="mt-1 truncate font-mono text-lg font-semibold tabular-nums">{value}</div></div></div>
}

function TrafficChart({ samples }: { samples: OverviewSample[] }) {
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null)
  const clipID = `overview-chart-clip-${useId().replace(/:/g, "")}`
  const width = 960
  const height = 260
  const inset = { top: 16, right: 18, bottom: 34, left: 64 }
  const plotWidth = width - inset.left - inset.right
  const plotHeight = height - inset.top - inset.bottom
  const transition = useChartTransition(samples, plotWidth / Math.max(1, samples.length - 1))
  const renderedSamples = transition?.samples ?? samples
  const maximum = Math.max(1, ...samples.flatMap((sample) => [sample.upload_bytes_per_second, sample.download_bytes_per_second]))
  const upload = chartPath(renderedSamples, "upload_bytes_per_second", maximum, width, height, inset, samples.length)
  const download = chartPath(renderedSamples, "download_bytes_per_second", maximum, width, height, inset, samples.length)
  const hovered = hoveredIndex == null ? undefined : samples[hoveredIndex]
  const hoveredX = hoveredIndex == null ? 0 : chartX(hoveredIndex, samples.length, plotWidth, inset.left)
  const tooltipLeft = Math.min(88, Math.max(12, (hoveredX / width) * 100))
  const xIndexes = samples.length < 2 ? [0] : [0, Math.floor((samples.length - 1) / 2), samples.length - 1].filter((value, index, values) => values.indexOf(value) === index)
  return <div className="mt-3 px-2 sm:px-4">
    <div className="relative h-64 w-full overflow-hidden">
      <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" className="size-full" role="img" aria-label="上传和下载速率滑动窗口图表">
        <defs><clipPath id={clipID}><rect x={inset.left} y={inset.top} width={plotWidth} height={plotHeight} /></clipPath></defs>
        {[0, 0.25, 0.5, 0.75, 1].map((ratio) => {
          const y = inset.top + plotHeight * (1 - ratio)
          return <g key={ratio}><line x1={inset.left} x2={width - inset.right} y1={y} y2={y} stroke="var(--border)" strokeWidth="1" vectorEffect="non-scaling-stroke" /><text x={inset.left - 8} y={y + 4} textAnchor="end" fill="var(--muted-foreground)" fontSize="11">{formatChartRate(maximum * ratio)}</text></g>
        })}
        <line x1={inset.left} x2={width - inset.right} y1={height - inset.bottom} y2={height - inset.bottom} stroke="var(--border)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
        <text x="10" y={inset.top + 4} fill="var(--muted-foreground)" fontSize="11">传输量</text>
        <text x={width - inset.right} y={height - 5} textAnchor="end" fill="var(--muted-foreground)" fontSize="11">时间</text>
        <g key={transition?.key ?? "static"} clipPath={`url(#${clipID})`}>
          <path d={download} fill="none" stroke="var(--info)" strokeWidth="2" vectorEffect="non-scaling-stroke" />
          <path d={upload} fill="none" stroke="var(--success)" strokeWidth="2" vectorEffect="non-scaling-stroke" />
          {transition && <animateTransform attributeName="transform" type="translate" from="0 0" to={`${-transition.step} 0`} dur="700ms" calcMode="spline" keySplines="0.22 1 0.36 1" fill="freeze" />}
        </g>
        {xIndexes.map((index) => <text key={index} x={chartX(index, samples.length, plotWidth, inset.left)} y={height - 10} textAnchor={index === 0 ? "start" : index === samples.length - 1 ? "end" : "middle"} fill="var(--muted-foreground)" fontSize="11">{samples[index] ? formatTimestamp(samples[index].timestamp) : "-"}</text>)}
        {hovered && <g pointerEvents="none"><line x1={hoveredX} x2={hoveredX} y1={inset.top} y2={height - inset.bottom} stroke="var(--foreground)" strokeOpacity=".35" strokeDasharray="4 4" vectorEffect="non-scaling-stroke" /><circle cx={hoveredX} cy={chartY(hovered.download_bytes_per_second, maximum, height, inset)} r="4" fill="var(--info)" /><circle cx={hoveredX} cy={chartY(hovered.upload_bytes_per_second, maximum, height, inset)} r="4" fill="var(--success)" /></g>}
        {samples.map((sample, index) => <rect key={sample.timestamp} x={chartX(index, samples.length, plotWidth, inset.left) - Math.max(8, plotWidth / Math.max(1, samples.length - 1) / 2)} y={inset.top} width={Math.max(16, plotWidth / Math.max(1, samples.length - 1))} height={plotHeight} fill="transparent" tabIndex={0} role="button" aria-label={`${formatTimestamp(sample.timestamp)} 下载 ${formatRate(sample.download_bytes_per_second)}，上传 ${formatRate(sample.upload_bytes_per_second)}`} onMouseEnter={() => setHoveredIndex(index)} onFocus={() => setHoveredIndex(index)} onMouseLeave={() => setHoveredIndex(null)} onBlur={() => setHoveredIndex(null)} />)}
      </svg>
      {samples.length === 0 && <div className="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground">等待首个采样点</div>}
      {hovered && <div className="pointer-events-none absolute top-2 z-10 w-48 -translate-x-1/2 rounded-md border bg-card px-2.5 py-2 text-[11px] shadow-sm" style={{ left: `${tooltipLeft}%` }}><div className="font-medium">{formatDateTime(hovered.timestamp)}</div><div className="mt-1 grid grid-cols-2 gap-x-3 gap-y-0.5 tabular-nums"><span className="text-info">下载</span><span className="text-right">{formatRate(hovered.download_bytes_per_second)}</span><span className="text-success">上传</span><span className="text-right">{formatRate(hovered.upload_bytes_per_second)}</span></div></div>}
    </div>
    <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-2 text-[11px] text-muted-foreground"><div className="flex gap-4"><span className="inline-flex items-center gap-1.5"><span className="h-0.5 w-4 bg-info" />下载</span><span className="inline-flex items-center gap-1.5"><span className="h-0.5 w-4 bg-success" />上传</span></div><span>峰值 {formatRate(maximum)} · 最近 {samples.length} 秒</span></div>
  </div>
}

function RouteRow({ listener, group, ruleSets, samples, total, today }: { listener: ListenerRecord; group?: ProxyGroup; ruleSets: RoutingRuleSet[]; samples: OverviewSample[]; total?: TrafficSummary; today?: TrafficSummary }) {
  const live = resourceSample(samples.at(-1), listener.id)
  return <article className="min-w-0 border bg-card p-3">
    <div className="flex items-start gap-2.5"><div className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted"><Cable className="size-4" /></div><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-1.5"><span className="truncate text-sm font-medium">{listener.name}</span><Badge variant={listener.enabled ? "success" : "secondary"}>{listener.enabled ? "启用" : "停用"}</Badge></div><div className="mt-1 truncate font-mono text-[11px] text-muted-foreground">{listener.bind_address}:{listener.port} · {listener.kind.toUpperCase()}</div></div></div>
    <div className="my-2 border-t" />
    <div className="grid gap-2 text-xs sm:grid-cols-2"><div><div className="text-[11px] text-muted-foreground">代理组</div><div className="mt-0.5 truncate font-medium">{group?.name ?? "代理组缺失"}</div></div><div><div className="text-[11px] text-muted-foreground">策略</div><div className="mt-0.5 truncate font-medium">{group?.strategy ?? "-"}</div></div></div>
    <div className="mt-2 flex min-h-6 flex-wrap gap-1">{ruleSets.length === 0 ? <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground"><ShieldBan className="size-3" />仅默认规则</span> : ruleSets.slice(0, 4).map((set) => <Badge key={set.id} variant="outline">{set.name} · {actionLabel(set, listener.proxy_group_id)}</Badge>)}{ruleSets.length > 4 && <Badge variant="secondary">+{ruleSets.length - 4}</Badge>}</div>
    <div className="mt-3 grid gap-2 border-t pt-3 text-xs sm:grid-cols-3"><RouteMetric label="实时速率" value={`↓ ${formatRate(live?.download_bytes_per_second ?? 0)} · ↑ ${formatRate(live?.upload_bytes_per_second ?? 0)}`} helper={`${live?.active_connections ?? 0} 个活动连接`} /><RouteMetric label="累计流量" value={formatBytes((total?.upload_bytes ?? 0) + (total?.download_bytes ?? 0))} helper="历史总量" /><RouteMetric label="今日流量" value={formatBytes((today?.upload_bytes ?? 0) + (today?.download_bytes ?? 0))} helper="本地今日 00:00 起" /></div>
    <MiniTrafficChart samples={samples} resourceID={listener.id} />
  </article>
}

function RouteMetric({ label, value, helper }: { label: string; value: string; helper: string }) {
  return <div className="min-w-0"><div className="text-[11px] text-muted-foreground">{label}</div><div className="mt-0.5 truncate font-mono text-[11px] font-medium tabular-nums" title={value}>{value}</div><div className="mt-0.5 text-[10px] text-muted-foreground">{helper}</div></div>
}

function MiniTrafficChart({ samples, resourceID }: { samples: OverviewSample[]; resourceID: string }) {
  const clipID = `mini-chart-clip-${useId().replace(/:/g, "")}`
  const width = 320
  const height = 56
  const inset = 4
  const transition = useChartTransition(samples, (width - inset * 2) / Math.max(1, samples.length - 1))
  const renderedSamples = transition?.samples ?? samples
  const maximum = Math.max(1, ...samples.map((sample) => {
    const resource = resourceSample(sample, resourceID)
    return Math.max(resource?.upload_bytes_per_second ?? 0, resource?.download_bytes_per_second ?? 0)
  }))
  const download = resourcePath(renderedSamples, resourceID, "download_bytes_per_second", maximum, width, height, inset, samples.length)
  const upload = resourcePath(renderedSamples, resourceID, "upload_bytes_per_second", maximum, width, height, inset, samples.length)
  return <div className="mt-3 border-t pt-2"><div className="flex items-center justify-between text-[10px] text-muted-foreground"><span>入口实时趋势</span><span>峰值 {formatRate(maximum)}</span></div><div className="mt-1 h-12">{samples.length === 0 ? <div className="flex h-full items-center text-[10px] text-muted-foreground">等待采样</div> : <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" className="size-full" role="img" aria-label="入口实时流量趋势"><defs><clipPath id={clipID}><rect x={inset} y={inset} width={width - inset * 2} height={height - inset * 2} /></clipPath></defs><g key={transition?.key ?? "static"} clipPath={`url(#${clipID})`}><path d={download} fill="none" stroke="var(--info)" strokeWidth="2" vectorEffect="non-scaling-stroke" /><path d={upload} fill="none" stroke="var(--success)" strokeWidth="2" vectorEffect="non-scaling-stroke" />{transition && <animateTransform attributeName="transform" type="translate" from="0 0" to={`${-transition.step} 0`} dur="700ms" calcMode="spline" keySplines="0.22 1 0.36 1" fill="freeze" />}</g></svg>}</div></div>
}

function chartPath(samples: OverviewSample[], key: "upload_bytes_per_second" | "download_bytes_per_second", maximum: number, width: number, height: number, inset: { top: number; right: number; bottom: number; left: number }, coordinateCount = samples.length): string {
  if (samples.length === 0) return ""
  return samples.map((sample, index) => {
    const x = chartX(index, coordinateCount, width - inset.left - inset.right, inset.left)
    const y = chartY(sample[key], maximum, height, inset)
    return `${index === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`
  }).join(" ")
}

function chartX(index: number, count: number, width: number, left: number): number {
  return left + (index / Math.max(1, count - 1)) * width
}

function chartY(value: number, maximum: number, height: number, inset: { top: number; bottom: number }): number {
  return height - inset.bottom - (value / maximum) * (height - inset.top - inset.bottom)
}

function resourceSample(sample: OverviewSample | undefined, resourceID: string) {
  return sample?.resources?.find((resource) => resource.resource_type === "listener" && resource.resource_id === resourceID)
}

function resourcePath(samples: OverviewSample[], resourceID: string, key: "upload_bytes_per_second" | "download_bytes_per_second", maximum: number, width: number, height: number, inset: number, coordinateCount = samples.length): string {
  if (samples.length === 0) return ""
  const span = Math.max(1, coordinateCount - 1)
  return samples.map((sample, index) => {
    const resource = resourceSample(sample, resourceID)
    const x = inset + (index / span) * (width - inset * 2)
    const y = height - inset - ((resource?.[key] ?? 0) / maximum) * (height - inset * 2)
    return `${index === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`
  }).join(" ")
}

function useChartTransition(samples: OverviewSample[], step: number): ChartTransition | null {
  const previousSamples = useRef<OverviewSample[] | undefined>(undefined)
  const [transition, setTransition] = useState<ChartTransition | null>(null)
  const reducedMotion = usePrefersReducedMotion()

  useLayoutEffect(() => {
    const previous = previousSamples.current
    previousSamples.current = samples
    const latest = samples.at(-1)
    if (!previous || !latest || !canSlideChart(previous, samples) || step <= 0) {
      setTransition(null)
      return
    }
    setTransition({ key: latest.timestamp, samples: [...previous, latest], step })
  }, [samples, step])

  if (reducedMotion || transition?.key !== samples.at(-1)?.timestamp) return null
  return transition
}

function canSlideChart(previous: OverviewSample[], current: OverviewSample[]): boolean {
  if (previous.length < 2 || current.length !== previous.length) return false
  if (current.at(-1)?.timestamp === previous.at(-1)?.timestamp) return false
  return current.slice(0, -1).every((sample, index) => sample.timestamp === previous[index + 1]?.timestamp)
}

function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(false)

  useEffect(() => {
    const media = window.matchMedia("(prefers-reduced-motion: reduce)")
    const update = () => setReduced(media.matches)
    update()
    media.addEventListener("change", update)
    return () => media.removeEventListener("change", update)
  }, [])

  return reduced
}

function indexSummaries(items: TrafficSummary[]): Map<string, TrafficSummary> {
  return new Map(items.map((item) => [item.resource_id, item]))
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${Math.round(bytes)} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`
}

function formatChartRate(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)}M`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)}K`
  return `${Math.round(bytes)}`
}

function formatTimestamp(value: string): string {
  return new Date(value).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" })
}

function formatDateTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" })
}

function setAppliesToGroup(set: RoutingRuleSet, groupID: string): boolean {
  if (set.routes?.length) return set.routes.some((route) => route.proxy_group_id === groupID)
  return Boolean(set.action?.type) && (!set.applied_group_ids?.length || set.applied_group_ids.includes(groupID))
}

function actionLabel(set: RoutingRuleSet, groupID: string): string {
  const action = set.routes?.find((route) => route.proxy_group_id === groupID)?.action ?? set.action
  if (action?.type === "reject") return "拒绝"
  if (action?.type === "direct") return "直连"
  return "代理"
}

function formatRate(bytes: number): string {
  if (bytes < 1024) return `${Math.round(bytes)} B/s`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB/s`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB/s`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB/s`
}
