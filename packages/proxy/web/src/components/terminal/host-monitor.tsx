import { useEffect, useRef, useState } from "react"
import { Cpu, MemoryStick, HardDriveDownload, HardDriveUpload, Gauge } from "lucide-react"

import { api, type TerminalHostSample } from "@/lib/api"
import { cn, formatBytes } from "@/lib/utils"

// HostMonitor subscribes to the server-side terminal metrics SSE and renders a
// compact FinalShell-style resource panel. It only runs while mounted and uses
// a single EventSource; the server samples /proc once per second, so the added
// load is negligible.
export function HostMonitor({ enabled }: { enabled: boolean }) {
  const [sample, setSample] = useState<TerminalHostSample | null>(null)
  const [history, setHistory] = useState<TerminalHostSample[]>([])
  const sourceRef = useRef<EventSource | null>(null)

  useEffect(() => {
    if (!enabled) {
      sourceRef.current?.close()
      sourceRef.current = null
      return
    }
    const source = new EventSource(api.terminalMetricsURL())
    sourceRef.current = source
    source.onmessage = (event) => {
      try {
        const next = JSON.parse(event.data) as TerminalHostSample
        setSample(next)
        setHistory((current) => [...current, next].slice(-120))
      } catch {
        // A malformed stream is a server bug; stop retrying it.
        source.close()
        sourceRef.current = null
      }
    }
    // Transient network drops (packet loss, NAT timeouts) surface here. Do NOT
    // close: EventSource reconnects automatically with its built-in backoff,
    // so the monitor keeps working when the link heals.
    source.onerror = () => {}
    return () => {
      source.close()
      sourceRef.current = null
    }
  }, [enabled])

  if (!enabled) return null
  const cpuPct = sample ? sample.cpu_usage_pct : 0
  const cpuShare = sample ? cpuPct / Math.max(1, sample.cpu_count) : 0
  const memPct = sample && sample.memory_total_bytes > 0
    ? (sample.memory_used_bytes / sample.memory_total_bytes) * 100
    : 0

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground">
        <Gauge className="size-3.5" />
        系统监控
      </div>
      <GaugeBar
        icon={<Cpu className="size-3.5" />}
        label="CPU"
        value={`${(cpuShare).toFixed(0)}%`}
        ratio={Math.min(1, cpuShare / 100)}
        tone="cpu"
        spark={history.map((s) => s.cpu_usage_pct)}
      />
      <GaugeBar
        icon={<MemoryStick className="size-3.5" />}
        label="内存"
        value={sample ? `${formatBytes(sample.memory_used_bytes)} / ${formatBytes(sample.memory_total_bytes)}` : "—"}
        ratio={memPct / 100}
        tone="mem"
        spark={history.map((s) => s.memory_used_bytes)}
      />
      <div className="grid grid-cols-2 gap-2">
        <MiniStat
          icon={<HardDriveDownload className="size-3 text-info" />}
          label="下行"
          value={sample ? `${formatRate(sample.net_rx_bytes_per_sec)}` : "—"}
        />
        <MiniStat
          icon={<HardDriveUpload className="size-3 text-success" />}
          label="上行"
          value={sample ? `${formatRate(sample.net_tx_bytes_per_sec)}` : "—"}
        />
      </div>
      <div className="rounded-md border bg-muted/40 px-2 py-1.5 text-[11px] text-muted-foreground">
        <div className="flex justify-between"><span>负载 1/5/15</span><span className="font-mono tabular-nums">{sample ? `${sample.load1.toFixed(2)} ${sample.load5.toFixed(2)} ${sample.load15.toFixed(2)}` : "—"}</span></div>
      </div>
      {sample && sample.processes.length > 0 && (
        <div className="rounded-md border bg-muted/40 px-2 py-1.5 text-[11px]">
          <div className="mb-1 font-medium text-muted-foreground">进程</div>
          <ul className="space-y-0.5">
            {sample.processes.map((proc) => (
              <li key={proc.pid} className="flex items-center justify-between gap-2">
                <span className="truncate">{proc.name}<span className="text-muted-foreground"> · {proc.pid}</span></span>
                <span className="font-mono tabular-nums text-muted-foreground">{proc.cpu_usage_pct.toFixed(1)}% · {formatBytes(proc.memory_rss_bytes)}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function GaugeBar({
  icon, label, value, ratio, tone, spark,
}: {
  icon: React.ReactNode
  label: string
  value: string
  ratio: number
  tone: "cpu" | "mem"
  spark: number[]
}) {
  const clamped = Math.max(0, Math.min(1, ratio))
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-[11px]">
        <span className="inline-flex items-center gap-1 text-muted-foreground">{icon}{label}</span>
        <span className="font-mono tabular-nums">{value}</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className={cn("h-full rounded-full transition-all duration-500", tone === "cpu" ? "bg-info" : "bg-success")}
          style={{ width: `${clamped * 100}%` }}
        />
      </div>
      {spark.length > 4 && <Sparkline values={spark} tone={tone} />}
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

function Sparkline({ values, tone }: { values: number[]; tone: "cpu" | "mem" }) {
  const width = 120
  const height = 22
  if (values.length < 2) return null
  const max = Math.max(1, ...values.map((v) => (tone === "mem" ? v : v)))
  const path = values.map((v, i) => {
    const x = (i / (values.length - 1)) * width
    const y = height - (v / max) * (height - 2) - 1
    return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`
  }).join(" ")
  return (
    <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" className="h-[22px] w-full">
      <path d={path} fill="none" stroke={tone === "cpu" ? "var(--info)" : "var(--success)"} strokeWidth="1" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

function formatRate(bytes: number): string {
  if (bytes < 1024) return `${Math.round(bytes)} B/s`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB/s`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB/s`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB/s`
}
