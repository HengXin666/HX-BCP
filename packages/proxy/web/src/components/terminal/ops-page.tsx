import { useEffect, useRef, useState } from "react"
import { Cpu, Database, Gauge, HardDrive, HardDriveDownload, HardDriveUpload, LoaderCircle, MemoryStick, RefreshCw, Server } from "lucide-react"

import { api, type DiskUsage, type SystemResourceSample } from "@/lib/api"
import { cn, formatBytes } from "@/lib/utils"

const POLL_INTERVAL_MS = 3000

// OpsPage is the "运维" sub-tab of the terminal page: a read-only live view of
// the host — CPU/memory/load/network from /api/v1/system/resources plus disk
// usage from /api/v1/system/disk. It polls on an interval while mounted and
// carries no shell authority.
export function OpsPage({ onNotice }: { onNotice: (message: string, tone?: "success" | "error") => void }) {
  const [sample, setSample] = useState<SystemResourceSample | null>(null)
  const [disks, setDisks] = useState<DiskUsage[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [history, setHistory] = useState<SystemResourceSample[]>([])
  const aliveRef = useRef(true)

  useEffect(() => {
    aliveRef.current = true
    let timer: number | null = null

    const load = async () => {
      try {
        const [resources, disk] = await Promise.all([api.systemResources(), api.systemDisk()])
        if (!aliveRef.current) return
        setSample(resources)
        setDisks(disk.filesystems)
        setError(null)
        setHistory((current) => [...current, resources].slice(-120))
      } catch (cause) {
        if (!aliveRef.current) return
        setError(cause instanceof Error ? cause.message : "系统数据获取失败")
      } finally {
        if (aliveRef.current) setLoading(false)
      }
    }

    void load()
    timer = window.setInterval(() => void load(), POLL_INTERVAL_MS)
    return () => {
      aliveRef.current = false
      if (timer !== null) window.clearInterval(timer)
    }
  }, [onNotice])

  if (loading && !sample) {
    return (
      <div className="flex h-full min-h-40 items-center justify-center gap-2 text-xs text-muted-foreground">
        <LoaderCircle className="size-4 animate-spin" />正在读取系统状态…
      </div>
    )
  }

  const cpuShare = sample ? sample.cpu_usage_pct / Math.max(1, sample.cpu_count) : 0
  const memPct = sample && sample.memory_total_bytes > 0 ? (sample.memory_used_bytes / sample.memory_total_bytes) * 100 : 0
  const swapPct = sample && sample.swap_total_bytes > 0 ? (sample.swap_used_bytes / sample.swap_total_bytes) * 100 : 0

  return (
    // 与 Docker 子页同因: 终端页的 main 在 lg 断点是 lg:h-full lg:overflow-hidden,
    // 所以子页必须自己滚动, 否则磁盘表与容器清单被裁在视口外且无法滚动。
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
      <div className="flex shrink-0 items-center justify-between">
        <div className="flex items-center gap-1.5 text-sm font-semibold">
          <Server className="size-4 text-muted-foreground" />主机实时状态
        </div>
        <div className="flex items-center gap-2">
          {error && <span className="text-xs text-destructive">{error}</span>}
          <button
            type="button"
            onClick={() => {
              setLoading(true)
              void Promise.all([api.systemResources(), api.systemDisk()])
                .then(([resources, disk]) => {
                  setSample(resources)
                  setDisks(disk.filesystems)
                  setError(null)
                })
                .catch((cause) => setError(cause instanceof Error ? cause.message : "刷新失败"))
                .finally(() => setLoading(false))
            }}
            className="inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs hover:bg-muted"
          >
            <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />刷新
          </button>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <section className="rounded-md border bg-card p-3">
          <MetricHeader icon={<Cpu className="size-4 text-info" />} label="CPU" value={sample ? `${(cpuShare).toFixed(1)}%` : "—"} />
          <Bar ratio={sample ? Math.min(1, cpuShare / 100) : 0} tone="info" />
          <div className="mt-1.5 text-[11px] text-muted-foreground">
            核心数 {sample?.cpu_count ?? "—"} · 系统总占用 {sample ? `${sample.cpu_usage_pct.toFixed(1)}%` : "—"}
          </div>
          <Sparkline values={history.map((h) => h.cpu_usage_pct)} tone="info" />
        </section>

        <section className="rounded-md border bg-card p-3">
          <MetricHeader icon={<MemoryStick className="size-4 text-success" />} label="内存" value={sample ? `${formatBytes(sample.memory_used_bytes)} / ${formatBytes(sample.memory_total_bytes)}` : "—"} />
          <Bar ratio={memPct / 100} tone="success" />
          <div className="mt-1.5 text-[11px] text-muted-foreground">
            已用 {sample ? formatBytes(sample.memory_used_bytes) : "—"} · 页缓存 {sample ? formatBytes(sample.memory_cached_bytes) : "—"} · 占用 {memPct.toFixed(1)}%
          </div>
          <Sparkline values={history.map((h) => h.memory_used_bytes)} tone="success" />
        </section>

        <section className="rounded-md border bg-card p-3">
          <MetricHeader icon={<Gauge className="size-4 text-warning" />} label="负载 1 / 5 / 15" value={sample ? `${sample.load1.toFixed(2)} / ${sample.load5.toFixed(2)} / ${sample.load15.toFixed(2)}` : "—"} />
          <div className="mt-2 grid grid-cols-2 gap-2">
            <MiniStat icon={<HardDriveDownload className="size-3 text-info" />} label="下行速率" value={sample ? formatRate(sample.net_rx_bytes_per_sec) : "—"} />
            <MiniStat icon={<HardDriveUpload className="size-3 text-success" />} label="上行速率" value={sample ? formatRate(sample.net_tx_bytes_per_sec) : "—"} />
          </div>
        </section>

        <section className="rounded-md border bg-card p-3">
          <MetricHeader icon={<Database className="size-4 text-purple-400" />} label="Swap" value={sample ? `${formatBytes(sample.swap_used_bytes)} / ${formatBytes(sample.swap_total_bytes)}` : "—"} />
          <Bar ratio={swapPct / 100} tone="warning" />
          <div className="mt-1.5 text-[11px] text-muted-foreground">
            {sample && sample.swap_total_bytes > 0 ? `占用 ${swapPct.toFixed(1)}%` : "未启用 Swap"}
          </div>
        </section>
      </div>

      <section className="rounded-md border bg-card">
        <div className="flex items-center gap-1.5 border-b px-3 py-2 text-xs font-semibold text-muted-foreground">
          <HardDrive className="size-3.5" />磁盘占用
        </div>
        {disks.length === 0 ? (
          <div className="px-3 py-4 text-xs text-muted-foreground">未读取到磁盘信息</div>
        ) : (
          <div className="divide-y">
            {disks.map((disk) => (
              <div key={`${disk.filesystem}-${disk.mounted_on}`} className="px-3 py-2">
                <div className="flex items-center justify-between gap-2 text-xs">
                  <span className="truncate font-medium">{disk.mounted_on}</span>
                  <span className="font-mono tabular-nums text-muted-foreground">
                    {formatBytes(disk.used_bytes)} / {formatBytes(disk.size_bytes)} · {disk.use_percent}%
                  </span>
                </div>
                <Bar ratio={disk.use_percent / 100} tone={disk.use_percent > 90 ? "danger" : "warning"} className="mt-1.5" />
                <div className="mt-1 truncate text-[10px] text-muted-foreground">
                  {disk.filesystem} · 可用 {formatBytes(disk.avail_bytes)}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      {sample && sample.processes.length > 0 && (
        <section className="rounded-md border bg-card">
          <div className="flex items-center gap-1.5 border-b px-3 py-2 text-xs font-semibold text-muted-foreground">
            <Cpu className="size-3.5" />受管进程
          </div>
          <ul className="divide-y">
            {sample.processes.map((proc) => (
              <li key={proc.pid} className="flex items-center justify-between gap-2 px-3 py-1.5 text-xs">
                <span className="truncate">{proc.name}<span className="text-muted-foreground"> · {proc.pid}</span></span>
                <span className="font-mono tabular-nums text-muted-foreground">{proc.cpu_usage_pct.toFixed(1)}% · {formatBytes(proc.memory_rss_bytes)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function MetricHeader({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground">{icon}{label}</span>
      <span className="font-mono text-xs tabular-nums">{value}</span>
    </div>
  )
}

function Bar({ ratio, tone, className }: { ratio: number; tone: "info" | "success" | "warning" | "danger"; className?: string }) {
  const clamped = Math.max(0, Math.min(1, ratio))
  const toneClass = { info: "bg-info", success: "bg-success", warning: "bg-warning", danger: "bg-destructive" }[tone]
  return (
    <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)}>
      <div className={cn("h-full rounded-full transition-all duration-500", toneClass)} style={{ width: `${clamped * 100}%` }} />
    </div>
  )
}

function MiniStat({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/40 px-2 py-1">
      <div className="flex items-center gap-1 text-[10px] text-muted-foreground">{icon}{label}</div>
      <div className="mt-0.5 font-mono text-xs tabular-nums">{value}</div>
    </div>
  )
}

function Sparkline({ values, tone }: { values: number[]; tone: "info" | "success" }) {
  const width = 160
  const height = 24
  if (values.length < 2) return null
  const max = Math.max(1, ...values)
  const path = values.map((value, index) => {
    const x = (index / (values.length - 1)) * width
    const y = height - (value / max) * (height - 2) - 1
    return `${index === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`
  }).join(" ")
  return (
    <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" className="mt-2 h-6 w-full">
      <path d={path} fill="none" stroke={tone === "info" ? "var(--info)" : "var(--success)"} strokeWidth="1" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

function formatRate(bytes: number): string {
  if (bytes < 1024) return `${Math.round(bytes)} B/s`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB/s`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB/s`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB/s`
}
