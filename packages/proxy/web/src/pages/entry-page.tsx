import { useCallback, useEffect, useState, type FormEvent } from "react"
import { Copy, KeyRound, Plus, RefreshCw, RotateCcw, Trash2, Unlink } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { ApiError } from "@/lib/api"
import {
  entryApi,
  entryUsername,
  formatBytes,
  type EntryPool,
  type EntrySlot,
  type EntryStatus,
  type EntryTenant,
} from "@/lib/entry-api"
import { cn } from "@/lib/utils"

const poolLabels: Record<EntryPool, string> = {
  resi: "真住宅 (按流量计费)",
  subresi: "订阅中的住宅节点",
  sub: "普通订阅节点",
  direct: "本机出口",
}
const allPools: EntryPool[] = ["resi", "subresi", "sub", "direct"]

type Notify = (message: string, tone?: "success" | "error") => void

export function EntryPage({ onNotice }: { onNotice: Notify }) {
  const [status, setStatus] = useState<EntryStatus | null>(null)
  const [tenants, setTenants] = useState<EntryTenant[]>([])
  const [slots, setSlots] = useState<EntrySlot[]>([])
  const [loading, setLoading] = useState(true)
  const [revealed, setRevealed] = useState<EntryTenant | null>(null)
  const tenantName = (id: string) => tenants.find((tenant) => tenant.id === id)?.name ?? id

  const fail = useCallback((cause: unknown) => {
    if (cause instanceof ApiError) onNotice(cause.message, "error")
  }, [onNotice])

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const [nextStatus, tenantList, slotList] = await Promise.all([entryApi.status(), entryApi.tenants(), entryApi.slots()])
      setStatus(nextStatus)
      setTenants(tenantList.items)
      setSlots([...slotList.nodes, ...slotList.residential])
    } catch (cause) {
      fail(cause)
    } finally {
      setLoading(false)
    }
  }, [fail])

  useEffect(() => {
    void reload()
    const timer = window.setInterval(() => void reload(), 15_000)
    return () => window.clearInterval(timer)
  }, [reload])

  async function act(work: () => Promise<unknown>, done: string) {
    try {
      await work()
      onNotice(done)
      await reload()
    } catch (cause) {
      fail(cause)
    }
  }

  async function copyShare(tenant: EntryTenant) {
    try {
      const { path } = await entryApi.sharePath(tenant.id)
      await navigator.clipboard.writeText(window.location.origin + path)
      onNotice("已复制 Clash 订阅链接, 可加 ?pool=subresi&cc=us,jp&slots=5")
    } catch (cause) {
      fail(cause)
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">统一入口</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            一个端口同时讲 HTTP 与 SOCKS5, 用户名选出口: 租户-pool-resi-cc-us-session-任务名-ttl-30。
          </p>
        </div>
        <button type="button" onClick={() => void reload()} className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-muted">
          <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
          刷新
        </button>
      </div>

      {status && <StatusPanel status={status} />}

      <CreateTenant onCreated={(tenant) => { setRevealed(tenant); void reload() }} onError={fail} />

      {revealed?.secret && <SecretPanel tenant={revealed} status={status} onClose={() => setRevealed(null)} onNotice={onNotice} />}

      <section className="rounded-md border bg-card">
        <header className="border-b px-4 py-2.5 text-sm font-medium">租户</header>
        {tenants.length === 0 ? (
          <p className="px-4 py-6 text-center text-sm text-muted-foreground">还没有租户。建一个, 拿到密码就能接入。</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-muted-foreground">
                <tr><th className="px-4 py-2">名称</th><th className="px-4 py-2">可用池</th><th className="px-4 py-2">住宅用量</th><th className="px-4 py-2">总流量</th><th className="px-4 py-2 text-right">操作</th></tr>
              </thead>
              <tbody>
                {tenants.map((tenant) => (
                  <tr key={tenant.id} className={cn("border-t", !tenant.enabled && "opacity-50")}>
                    <td className="px-4 py-2 font-medium">{tenant.name}{tenant.note && <span className="ml-2 text-xs text-muted-foreground">{tenant.note}</span>}</td>
                    <td className="px-4 py-2 text-xs">{tenant.pools.join(", ")}</td>
                    <td className="px-4 py-2 text-xs">
                      {formatBytes(tenant.resi_used_bytes)}{tenant.resi_quota_bytes > 0 && ` / ${formatBytes(tenant.resi_quota_bytes)}`}
                    </td>
                    <td className="px-4 py-2 text-xs">{formatBytes(tenant.total_bytes)}</td>
                    <td className="px-4 py-2">
                      <div className="flex justify-end gap-1">
                        <IconButton title="复制 Clash 订阅" onClick={() => void copyShare(tenant)}><Copy className="size-3.5" /></IconButton>
                        <IconButton title="轮换密码" onClick={() => void act(async () => setRevealed(await entryApi.updateTenant(tenant.id, { rotate_secret: true, version: tenant.version })), "密码已轮换, 旧密码立即失效")}><KeyRound className="size-3.5" /></IconButton>
                        <IconButton title="清零住宅用量" onClick={() => void act(() => entryApi.resetUsage(tenant.id), "住宅用量已清零")}><RotateCcw className="size-3.5" /></IconButton>
                        <Button size="sm" variant="outline" onClick={() => void act(() => entryApi.updateTenant(tenant.id, { enabled: !tenant.enabled, version: tenant.version }), tenant.enabled ? "已停用" : "已启用")}>
                          {tenant.enabled ? "停用" : "启用"}
                        </Button>
                        <IconButton title="删除" onClick={() => { if (window.confirm(`删除租户 ${tenant.name}? 用它的服务会立刻连不上。`)) void act(() => entryApi.deleteTenant(tenant.id), "已删除") }}><Trash2 className="size-3.5" /></IconButton>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="rounded-md border bg-card">
        <header className="border-b px-4 py-2.5 text-sm font-medium">槽位 (session 绑定)</header>
        {slots.length === 0 ? (
          <p className="px-4 py-6 text-center text-sm text-muted-foreground">没有活跃槽位。带 session 的连接会在这里出现。</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-muted-foreground">
                <tr><th className="px-4 py-2">租户</th><th className="px-4 py-2">槽位</th><th className="px-4 py-2">当前节点</th><th className="px-4 py-2">换绑次数</th><th className="px-4 py-2">到期</th><th className="px-4 py-2 text-right">操作</th></tr>
              </thead>
              <tbody>
                {slots.map((item) => (
                  <tr key={`${item.tenant}/${item.slot}/${item.binding.NodeID}`} className="border-t">
                    <td className="px-4 py-2">{tenantName(item.tenant)}</td>
                    <td className="px-4 py-2 font-mono text-xs">{item.slot}</td>
                    <td className="px-4 py-2 font-mono text-xs">{item.binding.NodeID}</td>
                    <td className="px-4 py-2 text-xs">{item.binding.Rebinds}</td>
                    <td className="px-4 py-2 text-xs">{new Date(item.binding.ExpiresAt).toLocaleString()}</td>
                    <td className="px-4 py-2 text-right">
                      <IconButton title="释放, 下次连接换新出口" onClick={() => void act(() => entryApi.releaseSlot(item.tenant, item.slot), "槽位已释放")}><Unlink className="size-3.5" /></IconButton>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}

function StatusPanel({ status }: { status: EntryStatus }) {
  const cells: Array<[string, string]> = [
    ["入口", status.listen || "未开启"],
    ["真住宅平台", String(status.pools.resi ?? 0)],
    ["订阅住宅节点", String(status.pools.subresi ?? 0)],
    ["订阅节点", String(status.pools.sub ?? 0)],
    ["活跃连接", String(status.connections.active)],
    ["失败连接", String(status.connections.failed)],
  ]
  return (
    <section className="space-y-2">
      {status.listen_error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          入口监听失败: {status.listen_error}。用 HX_PXY_ENTRY_LISTEN 换一个地址后重启。
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 md:grid-cols-3 xl:grid-cols-6">
        {cells.map(([label, value]) => (
          <div key={label} className="rounded-md border bg-card px-3 py-2">
            <div className="text-xs text-muted-foreground">{label}</div>
            <div className="mt-0.5 truncate font-mono text-sm">{value}</div>
          </div>
        ))}
      </div>
      {status.hub_error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          数据面 hub 未启用, 订阅与代理组不可达: {status.hub_error}。用 HX_PXY_ENTRY_HUB_PORT 换端口后重启。
        </div>
      )}
      {!status.exit_probe && <p className="text-xs text-muted-foreground">出口探测未开启 (HX_PXY_ENTRY_EXIT_PROBE=1), subresi 只包含声明为住宅的订阅源。</p>}
    </section>
  )
}

function CreateTenant({ onCreated, onError }: { onCreated: (tenant: EntryTenant) => void; onError: (cause: unknown) => void }) {
  const [name, setName] = useState("")
  const [note, setNote] = useState("")
  const [quotaGB, setQuotaGB] = useState("")
  const [pools, setPools] = useState<EntryPool[]>(["sub", "subresi"])
  const [groups, setGroups] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    try {
      const quota = Number(quotaGB)
      const created = await entryApi.createTenant({
        name: name.trim(), pools: [...pools, ...groupGrants(groups)], note,
        resi_quota_bytes: Number.isFinite(quota) && quota > 0 ? Math.round(quota * 1024 ** 3) : 0,
      })
      setName("")
      setNote("")
      setQuotaGB("")
      setGroups("")
      onCreated(created)
    } catch (cause) {
      onError(cause)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={(event) => void submit(event)} className="space-y-3 rounded-md border bg-card p-4">
      <div className="text-sm font-medium">新建租户</div>
      <div className="grid gap-2 md:grid-cols-4">
        <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="名称: 字母, 数字, 下划线" required pattern="[A-Za-z0-9_]{1,64}" />
        <Input value={note} onChange={(event) => setNote(event.target.value)} placeholder="备注 (可选)" />
        <Input value={quotaGB} onChange={(event) => setQuotaGB(event.target.value)} placeholder="住宅流量上限 GB, 空为不限" inputMode="decimal" />
        <Input value={groups} onChange={(event) => setGroups(event.target.value)} placeholder="可用代理组, 逗号分隔, * 为全部" />
      </div>
      <div className="flex flex-wrap gap-4">
        {allPools.map((pool) => (
          <label key={pool} className="flex items-center gap-2 text-sm">
            <Checkbox checked={pools.includes(pool)} onCheckedChange={(checked) => setPools((current) => checked ? [...current, pool] : current.filter((item) => item !== pool))} />
            <span className="font-mono text-xs">{pool}</span>
            <span className="text-xs text-muted-foreground">{poolLabels[pool]}</span>
          </label>
        ))}
      </div>
      <Button type="submit" size="sm" disabled={busy || !name.trim() || pools.length === 0}><Plus className="mr-1 size-3.5" />创建</Button>
    </form>
  )
}

function SecretPanel({ tenant, status, onClose, onNotice }: { tenant: EntryTenant; status: EntryStatus | null; onClose: () => void; onNotice: Notify }) {
  const address = status?.listen?.replace(/^0\.0\.0\.0:/, `${window.location.hostname}:`) ?? "127.0.0.1:7890"
  const pool = tenant.pools.find((item) => !item.startsWith("group")) ?? "sub"
  const username = entryUsername(tenant.name, { pool, cc: "us", session: "job1", ttl: 30 })
  const examples = [
    `socks5://${username}:${tenant.secret}@${address}`,
    `http://${username}:${tenant.secret}@${address}`,
    `hxpxy://${tenant.name}:${tenant.secret}@${address}?pool=${pool}&cc=us&session=job1&ttl=30`,
  ]
  return (
    <section className="space-y-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-4 text-sm">
      <div className="font-medium">租户 {tenant.name} 的密码只显示这一次</div>
      <code className="block break-all rounded bg-background px-2 py-1 font-mono text-xs">{tenant.secret}</code>
      <div className="text-xs text-muted-foreground">接入示例 (去掉 session 即每连接轮换):</div>
      {examples.map((example) => (
        <button key={example} type="button" className="block w-full break-all rounded bg-background px-2 py-1 text-left font-mono text-xs hover:bg-muted" onClick={() => void navigator.clipboard.writeText(example).then(() => onNotice("已复制"))}>
          {example}
        </button>
      ))}
      <Button size="sm" variant="outline" onClick={onClose}>我已保存</Button>
    </section>
  )
}

// groupGrants turns "a, b" into group:a and group:b, and "*" into every group.
function groupGrants(value: string): string[] {
  const names = value.split(",").map((item) => item.trim()).filter(Boolean)
  return names.includes("*") ? ["group"] : names.map((name) => `group:${name}`)
}

function IconButton({ title, onClick, children }: { title: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" title={title} aria-label={title} onClick={onClick} className="inline-flex size-7 items-center justify-center rounded-md border hover:bg-muted">
      {children}
    </button>
  )
}
