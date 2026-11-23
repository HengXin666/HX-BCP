import { useEffect, useState } from "react"
import { api, type ActiveConnectionSnapshot } from "@/lib/api"
import { Button } from "@/components/ui/button"

const origins: Record<string, string> = { local: "本机", docker: "Docker", external: "外网 / 局域网", unknown: "未知" }
const filters: Array<[string, string]> = [["", "全部"], ...Object.entries(origins)]

export function ActiveConnectionsPanel() {
  const [open, setOpen] = useState(false)
  const [snapshot, setSnapshot] = useState<ActiveConnectionSnapshot | null>(null)
  const [error, setError] = useState("")
  const [filter, setFilter] = useState("")
  useEffect(() => {
    if (!open) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    async function poll() {
      try { const value = await api.activeConnections(); if (!cancelled) { setSnapshot(value); setError("") } }
      catch (error) { if (!cancelled) setError(error instanceof Error ? error.message : "连接采样失败") }
      finally { if (!cancelled) timer = setTimeout(poll, 3_000) }
    }
    void poll()
    return () => { cancelled = true; clearTimeout(timer) }
  }, [open])
  const stale = snapshot && Date.now() - Date.parse(snapshot.sampled_at) > 10_000
  return <section className="shrink-0 rounded-lg border bg-card p-3">
    <Button variant="ghost" onClick={() => setOpen(!open)} aria-expanded={open}>活跃连接 {open ? "收起" : "展开"}</Button>
    {open && <div className="mt-3 space-y-3">
      <p className="text-xs text-muted-foreground">最多显示 500 条当前连接，不保存访问历史。按数据面可见源 IP 分类；host 网络、NAT 和反向代理后的真实来源可能无法区分。</p>
      <div className="flex flex-wrap gap-2">{filters.map(([key, label]) => <Button key={key} size="sm" variant={filter === key ? "default" : "outline"} onClick={() => setFilter(key)}>{label}</Button>)}<span className="self-center text-xs">总计 {snapshot?.total ?? "…"} · {snapshot ? new Date(snapshot.sampled_at).toLocaleTimeString() : "等待采样"}</span></div>
      {(error || stale) && <p role="alert" className="text-xs text-destructive">{error || "采样已过期：数据面可能不可用，以下不是实时状态"}</p>}
      <div className="max-h-80 overflow-auto"><table className="w-full min-w-[640px] text-left text-xs"><thead><tr>{["来源", "源地址", "目标", "协议 / 入口", "上传 / 下载"].map((label) => <th key={label} className="p-2">{label}</th>)}</tr></thead><tbody>{snapshot?.items.filter((item) => !filter || item.origin === filter).map((item) => <tr key={item.id} className="border-t"><td className="p-2">{origins[item.origin] ?? "未知"}</td><td className="p-2 font-mono">{item.source_ip}:{item.source_port}</td><td className="p-2 font-mono">{item.destination}:{item.destination_port}</td><td className="p-2">{item.network} / {item.inbound}</td><td className="p-2">{item.upload.toLocaleString()} / {item.download.toLocaleString()} B</td></tr>)}</tbody></table></div>
      {snapshot?.items.length === 0 && <p className="text-xs text-muted-foreground">当前没有采样到的活跃连接</p>}
    </div>}
  </section>
}
