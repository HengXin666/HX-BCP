import { useEffect, useState } from "react"
import { api, type NetworkSnapshot } from "@/lib/api"
import type { GlobalSettings, ProxyGroup } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

export function NetworkSettings({ settings, onChange }: { settings: GlobalSettings; onChange: (settings: GlobalSettings) => void }) {
  const [snapshot, setSnapshot] = useState<NetworkSnapshot | null>(null)
  const [groups, setGroups] = useState<ProxyGroup[]>([])
  const [container, setContainer] = useState("")
  const [busy, setBusy] = useState(false)
  const [output, setOutput] = useState("")
  const tun = settings.tun ?? { enabled: false, proxy_group_id: "" }
  useEffect(() => {
    let cancelled = false
    Promise.all([api.networkInspect(), api.listProxyGroups()]).then(([network, groups]) => {
      if (!cancelled) { setSnapshot(network); setGroups(groups.items.filter((group) => group.enabled)) }
    }).catch((error) => { if (!cancelled) setOutput(String(error)) })
    return () => { cancelled = true }
  }, [])
  async function run(action: string) {
    const repair = action !== "diagnose"
    if (repair && !window.confirm(`这会${action === "enable-forwarding" ? "开启" : "关闭"}整台宿主机的 IPv4 转发，影响所有 Docker 网络。仅修改运行态，重启后由系统配置决定。确认继续？`)) return
    setBusy(true)
    try {
      const result = await api.networkOperation(action, repair ? "" : container.trim(), repair)
      setOutput(`退出码 ${result.exit_code}\n${result.stdout}\n${result.stderr}`)
      setSnapshot(await api.networkInspect())
    } catch (error) { setOutput(error instanceof Error ? error.message : "网络操作失败") }
    finally { setBusy(false) }
  }
  return <section className="mb-4 space-y-4 rounded-lg border bg-card p-4">
    <h2 className="font-semibold">TUN 与 Docker 网络</h2>
    <p className="text-xs leading-5 text-muted-foreground">TUN 默认关闭，启用后由 Mihomo 接管当前环境的公网流量；内网与 Docker 网桥地址排除。需要先按 docs/NETWORK_OPERATIONS.md 配置数据面的 /dev/net/tun 与 CAP_NET_ADMIN；缺少权限时应用失败并回滚。不会自动关闭其他代理。</p>
    <label className="flex items-center gap-2 text-sm"><Checkbox checked={tun.enabled} onCheckedChange={(value) => onChange({ ...settings, tun: { ...tun, enabled: value === true } })} />启用当前环境 TUN（保存并应用后生效）</label>
    <Select value={tun.proxy_group_id} onValueChange={(proxy_group_id) => onChange({ ...settings, tun: { ...tun, proxy_group_id } })}><SelectTrigger className="max-w-md"><SelectValue placeholder="选择 TUN 公网出口代理组" /></SelectTrigger><SelectContent>{groups.map((group) => <SelectItem key={group.id} value={group.id}>{group.name}</SelectItem>)}</SelectContent></Select>
    <div className="flex flex-wrap items-center gap-2 text-xs"><span>TUN 设备权限可见：{snapshot ? snapshot.tun_available ? "是（仍需验证 capability）" : "否" : "检测中"}</span><span>IPv4 转发：{snapshot ? snapshot.ipv4_forwarding ? "开启" : "关闭" : "检测中"}</span><Button size="sm" variant="outline" disabled={busy} onClick={() => { setBusy(true); api.networkInspect().then(setSnapshot).catch((error) => setOutput(String(error))).finally(() => setBusy(false)) }}>重新探测</Button></div>
    <div className="max-h-44 overflow-auto text-xs">{snapshot?.interfaces.filter((device) => device.kind !== "host").map((device) => <p className="py-1 font-mono" key={device.name}>{device.kind} · {device.name} · {device.up ? "UP" : "DOWN"} · {device.addresses.join(", ")}</p>)}</div>
    <div className="flex flex-wrap gap-2"><Input className="max-w-sm" placeholder="容器 ID（12–64 位十六进制；留空诊断宿主机）" value={container} onChange={(event) => setContainer(event.target.value)} /><Button variant="outline" disabled={busy} onClick={() => void run("diagnose")}>网络诊断</Button></div>
    <p className="text-xs text-muted-foreground">容器诊断读取容器内部 DNS、路由并测试 example.com 解析；不读取环境变量。修复仅适用于转发被关闭的情况，不重启 Docker、不清空防火墙、不保证解决所有网络故障。修复要求最近验证的 2FA。</p>
    <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy || !snapshot || snapshot.ipv4_forwarding} onClick={() => void run("enable-forwarding")}>修复：开启 IPv4 转发</Button><Button variant="outline" disabled={busy || !snapshot?.ipv4_forwarding} onClick={() => void run("disable-forwarding")}>关闭 IPv4 转发</Button></div>
    {output && <pre role="status" className="max-h-72 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-3 text-xs">{output}</pre>}
  </section>
}
