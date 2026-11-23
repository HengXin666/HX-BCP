import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Activity, ArrowDownToLine, ArrowUpFromLine, CirclePause, CirclePlay, Eraser, LoaderCircle, RefreshCw, ScrollText, Unplug } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api"
import type { NodeRecord, ProxyLogEvent, TrafficPoint, TrafficSeries, TrafficSummary } from "@/lib/types"
import { cn, formatBytes, formatDate } from "@/lib/utils"

export function TrafficPanel({ listenerId, groupId, nodes }: { listenerId: string; groupId?: string; nodes: NodeRecord[] }) {
  const [listener, setListener] = useState<TrafficSeries | null>(null)
  const [group, setGroup] = useState<TrafficSeries | null>(null)
  const [nodeTotals, setNodeTotals] = useState<Map<string, TrafficSummary>>(new Map())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true)
    try {
      const [listenerResult, groupResult, nodesResult] = await Promise.all([
        api.trafficSeries("listener", listenerId),
        groupId ? api.trafficSeries("proxy_group", groupId) : Promise.resolve(null),
        api.trafficSummaries("node"),
      ])
      setListener(listenerResult)
      setGroup(groupResult)
      setNodeTotals(new Map(nodesResult.items.map((item) => [item.resource_id, item])))
      setError("")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "流量统计加载失败")
    } finally {
      setLoading(false)
    }
  }, [groupId, listenerId])

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(true), 15_000)
    return () => window.clearInterval(timer)
  }, [load])

  if (loading) return <PanelLoading label="正在加载流量统计" />
  if (error) return <PanelError message={error} onRetry={() => void load()} />

  const summary = listener?.summary
  return <div className="space-y-3">
    <div className="grid overflow-hidden rounded-md border bg-card sm:grid-cols-4">
      <TrafficMetric icon={ArrowDownToLine} label="下载" value={formatBytes(summary?.download_bytes ?? 0)} />
      <TrafficMetric icon={ArrowUpFromLine} label="上传" value={formatBytes(summary?.upload_bytes ?? 0)} border />
      <TrafficMetric icon={Activity} label="累计连接" value={String(summary?.connection_count ?? 0)} border />
      <TrafficMetric icon={CirclePlay} label="活动连接" value={String(summary?.active_connections ?? 0)} border />
    </div>
    <div className="rounded-md border bg-card px-3 py-3">
      <div className="mb-2 flex items-center justify-between gap-3">
        <div><div className="text-xs font-semibold">最近 24 小时</div><div className="mt-0.5 text-[11px] text-muted-foreground">每分钟聚合，15 秒自动刷新</div></div>
        <Button variant="ghost" size="icon" onClick={() => void load()} aria-label="刷新流量统计"><RefreshCw className="size-3.5" /></Button>
      </div>
      <TrafficBars points={listener?.points ?? []} />
      <div className="mt-2 text-[11px] text-muted-foreground">最近持久化：{formatDate(summary?.updated_at)}{group && ` · 代理组累计 ${formatBytes(group.summary.upload_bytes + group.summary.download_bytes)}`}</div>
    </div>
    <div className="overflow-x-auto rounded-md border bg-card">
      <table className="w-full min-w-[680px] text-left text-xs">
        <thead className="bg-muted text-muted-foreground"><tr><Th>节点</Th><Th>下载</Th><Th>上传</Th><Th>连接</Th><Th>最后统计</Th></tr></thead>
        <tbody className="divide-y">{nodes.length ? nodes.map((node) => {
          const total = nodeTotals.get(node.id)
          return <tr key={node.id} className="hover:bg-muted/60"><Td><span className="block max-w-[280px] truncate font-medium text-foreground" title={node.display_name}>{node.display_name}</span></Td><Td>{formatBytes(total?.download_bytes ?? 0)}</Td><Td>{formatBytes(total?.upload_bytes ?? 0)}</Td><Td>{total?.connection_count ?? 0}</Td><Td>{formatDate(total?.updated_at)}</Td></tr>
        }) : <tr><td colSpan={5} className="px-3 py-6 text-center text-muted-foreground">该服务当前没有节点统计</td></tr>}</tbody>
      </table>
    </div>
    <p className="text-[11px] leading-4 text-muted-foreground">统计来自 Mihomo 连接快照，用于运行观测，不作为计费依据；资源在本地删除时对应历史一并清除。</p>
  </div>
}

function TrafficBars({ points }: { points: TrafficPoint[] }) {
  const recent = points.slice(-48)
  const max = Math.max(1, ...recent.map((point) => point.upload_bytes + point.download_bytes))
  if (!recent.length) return <div className="flex h-24 items-center justify-center rounded bg-muted/60 px-3 text-center text-xs text-muted-foreground">暂无已建立连接的可计量流量；拨号失败请查看实时日志</div>
  return <div className="flex h-24 items-end gap-px overflow-hidden rounded bg-muted/50 px-1 pt-2" aria-label="最近 24 小时流量趋势">
    {recent.map((point) => {
      const bytes = point.upload_bytes + point.download_bytes
      return <div key={point.time} className="min-w-1 flex-1 rounded-t-sm bg-primary/75 transition-[height] duration-300" style={{ height: `${Math.max(bytes ? 5 : 1, bytes / max * 100)}%` }} title={`${formatDate(point.time)} · ${formatBytes(bytes)}`} />
    })}
  </div>
}

export function LiveLogsPanel({ listenerId }: { listenerId: string }) {
  const [level, setLevel] = useState("all")
  const [events, setEvents] = useState<ProxyLogEvent[]>([])
  const [paused, setPaused] = useState(false)
  const [status, setStatus] = useState<"connecting" | "connected" | "reconnecting">("connecting")
  const pausedRef = useRef(paused)
  const bottomRef = useRef<HTMLDivElement | null>(null)
  pausedRef.current = paused

  useEffect(() => {
    setStatus("connecting")
    const stream = new EventSource(api.proxyLogURL({ listenerId, level: level === "all" ? undefined : level }))
    const ready = () => setStatus("connected")
    const receive = (message: MessageEvent<string>) => {
      if (pausedRef.current) return
      try {
        const event = JSON.parse(message.data) as ProxyLogEvent
        setEvents((current) => [...current.slice(-199), event])
      } catch { /* Ignore malformed transient frames. */ }
    }
    stream.addEventListener("ready", ready)
    stream.addEventListener("log", receive as EventListener)
    stream.onerror = () => setStatus("reconnecting")
    return () => stream.close()
  }, [level, listenerId])

  useEffect(() => {
    if (!paused) bottomRef.current?.scrollIntoView({ block: "nearest" })
  }, [events, paused])

  return <div className="overflow-hidden rounded-md border bg-[#101816] text-[#d8e5e1]">
    <div className="flex flex-wrap items-center gap-2 border-b border-white/10 bg-[#16211e] px-3 py-2">
      <Badge variant={status === "connected" ? "success" : "warning"}>{status === "connected" ? "实时连接" : status === "connecting" ? "正在连接" : "正在重连"}</Badge>
      <div className="w-32"><Select value={level} onValueChange={setLevel}><SelectTrigger className="border-white/15 bg-card/5 text-[#d8e5e1]"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">全部级别</SelectItem><SelectItem value="info">Info 及以上</SelectItem><SelectItem value="warning">Warning 及以上</SelectItem><SelectItem value="error">仅 Error</SelectItem></SelectContent></Select></div>
      <span className="text-[11px] text-[#8fa39d]">保留最近 {events.length}/200 条</span>
      <div className="ml-auto flex gap-1">
        <Button variant="ghost" size="sm" className="text-[#d8e5e1] hover:bg-card/10 hover:text-white" onClick={() => setPaused((value) => !value)}>{paused ? <CirclePlay /> : <CirclePause />}{paused ? "继续" : "暂停"}</Button>
        <Button variant="ghost" size="sm" className="text-[#d8e5e1] hover:bg-card/10 hover:text-white" onClick={() => setEvents([])}><Eraser />清空</Button>
      </div>
    </div>
    <div className="h-72 overflow-auto p-3 font-mono text-[11px] leading-5">
      {events.length === 0 ? <div className="flex h-full flex-col items-center justify-center text-[#8fa39d]"><ScrollText className="mb-2 size-6" />等待该服务产生代理日志</div> : events.map((event, index) => <div key={`${event.timestamp}-${index}`} className="grid grid-cols-[70px_58px_minmax(0,1fr)] gap-2 border-b border-white/5 py-1 last:border-0"><span className="text-[#8fa39d]">{new Date(event.timestamp).toLocaleTimeString("zh-CN", { hour12: false })}</span><span className={cn("uppercase", event.level === "error" ? "text-red-300" : event.level.startsWith("warn") ? "text-amber-300" : "text-cyan-300")}>{event.level}</span><span className="break-words">{event.node && <span className="mr-2 text-emerald-300">[{event.node}]</span>}{event.message}</span></div>)}
      <div ref={bottomRef} />
    </div>
  </div>
}

function TrafficMetric({ icon: Icon, label, value, border = false }: { icon: typeof Activity; label: string; value: string; border?: boolean }) { return <div className={cn("px-3 py-3", border && "border-t sm:border-l sm:border-t-0")}><div className="flex items-center gap-1.5 text-[11px] text-muted-foreground"><Icon className="size-3.5" />{label}</div><div className="mt-1 text-lg font-semibold tabular-nums">{value}</div></div> }
function PanelLoading({ label }: { label: string }) { return <div className="flex h-40 items-center justify-center gap-2 text-xs text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{label}</div> }
function PanelError({ message, onRetry }: { message: string; onRetry: () => void }) { return <div className="flex h-40 flex-col items-center justify-center gap-2 text-xs text-muted-foreground"><Unplug className="size-5" /><span>{message}</span><Button variant="outline" size="sm" onClick={onRetry}>重试</Button></div> }
function Th({ children }: { children: React.ReactNode }) { return <th className="whitespace-nowrap px-3 py-2 font-medium">{children}</th> }
function Td({ children }: { children: React.ReactNode }) { return <td className="whitespace-nowrap px-3 py-2 text-muted-foreground">{children}</td> }
