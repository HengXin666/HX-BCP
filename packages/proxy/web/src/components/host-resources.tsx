import { useEffect, useRef, useState } from "react"
import { Cpu, MemoryStick, Info } from "lucide-react"

import { ApiError, api, type SystemResourceSample } from "@/lib/api"
import { cn, formatBytes } from "@/lib/utils"

// HostResources queries the control-plane host resource endpoint at a coarse
// 10s cadence. The server collector keeps state, so each call is a handful of
// /proc reads — negligible overhead. Remote proxy nodes do not expose CPU/RAM,
// so this only reports the local control plane + data plane processes, the
// only servers we actually manage.
export function HostResources({ onNotice }: { onNotice: (m: string, tone?: "success" | "error") => void }) {
  const [sample, setSample] = useState<SystemResourceSample | null>(null)
  const [error, setError] = useState(false)
  const timerRef = useRef<number | null>(null)

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const result = await api.systemResources()
        if (!cancelled) {
          setSample(result)
          setError(false)
        }
      } catch (cause) {
        if (!cancelled && cause instanceof ApiError) {
          if (cause.status === 404) {
            // Resource monitoring not available on this build; stop polling.
            cancelled = true
            if (timerRef.current !== null) window.clearInterval(timerRef.current)
            timerRef.current = null
          } else {
            setError(true)
          }
        } else if (cause instanceof ApiError) {
          setError(true)
        }
      }
    }
    void poll()
    timerRef.current = window.setInterval(poll, 10_000)
    return () => {
      cancelled = true
      if (timerRef.current !== null) window.clearInterval(timerRef.current)
      timerRef.current = null
    }
  }, [onNotice])

  if (error) return null
  const controlPlane = sample?.processes?.find((p) => p.name === "hx-pxyd")
  const dataplane = sample?.processes?.find((p) => p.name === "mihomo")
  const processes = [controlPlane, dataplane].filter(Boolean) as NonNullable<typeof controlPlane>[]
  if (!sample && !error) return null

  const cpuShare = sample ? sample.cpu_usage_pct / Math.max(1, sample.cpu_count) : 0

  return (
    <section className="border-y bg-card py-3">
      <div className="mb-2 flex items-center justify-between gap-2 px-4">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Cpu className="size-4 text-muted-foreground" />
          主机资源
          <span className="inline-flex items-center gap-1 text-[11px] font-normal text-muted-foreground">
            <Info className="size-3" />控制面 + 数据面（远程代理节点 CPU/内存不可读）
          </span>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-2 px-4 lg:grid-cols-4">
        <HostMetric
          icon={<Cpu className="size-3.5 text-info" />}
          label="系统 CPU"
          value={sample ? `${cpuShare.toFixed(0)}%` : "—"}
          ratio={Math.min(1, cpuShare / 100)}
          tone="bg-info"
        />
        <HostMetric
          icon={<MemoryStick className="size-3.5 text-success" />}
          label="内存"
          value={sample ? `${formatBytes(sample.memory_used_bytes)} / ${formatBytes(sample.memory_total_bytes)}` : "—"}
          ratio={sample && sample.memory_total_bytes ? sample.memory_used_bytes / sample.memory_total_bytes : 0}
          tone="bg-success"
        />
        {processes.length > 0 ? (
          <div className="col-span-2 grid grid-cols-2 gap-2 lg:col-span-2">
            {processes.map((proc) => (
              <HostMetric
                key={proc.pid}
                icon={<span className="size-3.5 rounded-full bg-primary/70" />}
                label={proc.name === "mihomo" ? "数据面 (mihomo)" : "控制面"}
                value={`${proc.cpu_usage_pct.toFixed(1)}% · ${formatBytes(proc.memory_rss_bytes)}`}
                ratio={Math.min(1, proc.cpu_usage_pct / 100)}
                tone="bg-primary"
              />
            ))}
          </div>
        ) : (
          <HostMetric
            icon={<span className="size-3.5 rounded-full bg-muted-foreground" />}
            label="进程"
            value={sample ? `负载 ${sample.load1.toFixed(2)}` : "—"}
            ratio={0}
            tone="bg-muted-foreground"
          />
        )}
      </div>
    </section>
  )
}

function HostMetric({
  icon,
  label,
  value,
  ratio,
  tone,
}: {
  icon: React.ReactNode
  label: string
  value: string
  ratio: number
  tone: string
}) {
  const clamped = Math.max(0, Math.min(1, ratio))
  return (
    <div className="rounded-md border bg-card px-2.5 py-2">
      <div className="flex items-center justify-between text-[11px]">
        <span className="inline-flex items-center gap-1 text-muted-foreground">{icon}{label}</span>
        <span className="font-mono tabular-nums">{value}</span>
      </div>
      <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div className={cn("h-full rounded-full transition-all duration-500", tone)} style={{ width: `${clamped * 100}%` }} />
      </div>
    </div>
  )
}
