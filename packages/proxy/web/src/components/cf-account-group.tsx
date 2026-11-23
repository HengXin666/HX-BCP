import { useCallback, useEffect, useState } from "react"
import { createPortal } from "react-dom"
import {
  Activity,
  ChevronDown,
  ChevronRight,
  Globe,
  KeyRound,
  LoaderCircle,
  Plus,
  RefreshCw,
  Server,
  ShieldAlert,
  Trash2,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import type { AddFleetAccountRequest, FleetAccount, FleetStatus, FleetWorker } from "@/lib/types"
import { formatDate } from "@/lib/utils"

interface CFAccountGroupProps {
  onNotice: (message: string, tone?: "success" | "error") => void
}

const statusMeta: Record<string, { label: string; variant: "success" | "destructive" | "warning" | "secondary" }> = {
  active: { label: "活跃", variant: "success" },
  banned: { label: "已封禁", variant: "destructive" },
  error: { label: "异常", variant: "warning" },
}

export function CFAccountGroup({ onNotice }: CFAccountGroupProps) {
  const [status, setStatus] = useState<FleetStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [showAdd, setShowAdd] = useState(false)
  const [addForm, setAddForm] = useState<AddFleetAccountRequest>({ email: "", cf_account_id: "", api_token: "" })
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const status = await api.fleetStatus()
      // Defensive: the fleet contract exposes workers as an array, but a stale
      // backend may serialize it as null; keep rendering robust either way.
      const normalized: FleetStatus = {
        ...status,
        accounts: (status.accounts ?? []).map((account) => ({
          ...account,
          workers: account.workers ?? [],
        })),
      }
      setStatus(normalized)
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "加载 CF 账号失败", "error")
    } finally {
      setLoading(false)
    }
  }, [onNotice])

  useEffect(() => {
    void load()
  }, [load])

  const accounts = status?.accounts ?? []
  const activeAccounts = accounts.filter((a) => a.status === "active").length
  const totalWorkers = status?.total_workers ?? 0
  const healthyWorkers = accounts.reduce(
    (acc: number, a: FleetAccount) => acc + a.workers.filter((w: FleetWorker) => w.failures === 0).length,
    0,
  )

  const handleAdd = async () => {
    if (!addForm.email.trim() || !addForm.cf_account_id.trim() || !addForm.api_token.trim()) {
      onNotice("请填写完整的邮箱 / 账号 ID / API Token", "error")
      return
    }
    setBusy("add")
    try {
      await api.fleetAddAccount(addForm)
      onNotice("CF 账号已接入，触发一次部署清扫", "success")
      setShowAdd(false)
      setAddForm({ email: "", cf_account_id: "", api_token: "" })
      await load()
      await api.fleetSweep()
      setTimeout(() => void load(), 3000)
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "接入 CF 账号失败", "error")
    } finally {
      setBusy(null)
    }
  }

  const handleDelete = async (account: FleetAccount) => {
    if (!window.confirm(`删除账号 ${account.email}？其下 worker 记录将一并移除。`)) return
    setBusy(`del-${account.id}`)
    try {
      await api.fleetDeleteAccount(account.id)
      onNotice("账号已删除", "success")
      await load()
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "删除失败", "error")
    } finally {
      setBusy(null)
    }
  }

  const handleSweep = async () => {
    setBusy("sweep")
    try {
      await api.fleetSweep()
      onNotice("已触发部署清扫（后台执行，稍候刷新）", "success")
      setTimeout(() => void load(), 4000)
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "触发清扫失败", "error")
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      {/* 统计条 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard icon={<Server className="size-4" />} label="CF 账号" value={String(accounts.length)} />
        <StatCard icon={<Activity className="size-4" />} label="活跃账号" value={String(activeAccounts)} />
        <StatCard icon={<Globe className="size-4" />} label="Worker 总数" value={String(totalWorkers)} />
        <StatCard icon={<ShieldAlert className="size-4" />} label="健康 Worker" value={String(healthyWorkers)} />
      </div>

      {/* 标题 + 操作条 */}
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold text-gh-text">CF 账号组</h3>
          <p className="text-xs text-gh-text-secondary">
            账号即供应商组：接入 CF 账号 → 自动部署 Worker（随机混淆）→ 作为 bpb 供应商挂到渠道
          </p>
        </div>
        <div className="flex gap-2">
          <Button size="sm" variant="secondary" onClick={() => void handleSweep()}>
            <RefreshCw className="size-3.5" /> 部署清扫
          </Button>
          <Button size="sm" variant="default" onClick={() => setShowAdd(true)}>
            <Plus className="size-3.5" /> 接入 CF 账号
          </Button>
        </div>
      </div>

      {/* 账号列表 */}
      {loading ? (
        <div className="flex items-center justify-center py-16 text-gh-text-secondary">
          <LoaderCircle className="mr-2 size-4 animate-spin" /> 加载 CF 账号...
        </div>
      ) : accounts.length === 0 ? (
        <div className="rounded-lg border border-dashed border-gh-border py-16 text-center text-sm text-gh-text-secondary">
          还没有接入 CF 账号。点「接入 CF 账号」开始。
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          {accounts.map((account) => {
            const isOpen = expanded[account.id]
            const meta = statusMeta[account.status] ?? { label: account.status, variant: "secondary" as const }
            const healthy = account.workers.filter((w) => w.failures === 0).length
            return (
              <div key={account.id} className="overflow-hidden rounded-lg border border-gh-border bg-gh-bg-secondary">
                {/* 卡片头部：账号形式 + 统计 + 折叠 */}
                <button
                  type="button"
                  className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-gh-bg-hover"
                  onClick={() => setExpanded((p) => ({ ...p, [account.id]: !p[account.id] }))}
                >
                  {isOpen ? <ChevronDown className="size-4 shrink-0 text-gh-text-secondary" /> : <ChevronRight className="size-4 shrink-0 text-gh-text-secondary" />}
                  <KeyRound className="size-4 shrink-0 text-gh-accent" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-semibold text-gh-text">{account.email}</span>
                      <Badge color={meta.variant === "success" ? "#3fb950" : meta.variant === "destructive" ? "#f85149" : meta.variant === "warning" ? "#d29922" : "#6e7681"}>
                        {meta.label}
                      </Badge>
                    </div>
                    <div className="mt-0.5 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gh-text-secondary">
                      <span>Worker {account.workers.length}</span>
                      <span>健康 {healthy}</span>
                      <span className="font-mono">{account.id.slice(0, 12)}…</span>
                    </div>
                  </div>
                  <span
                    role="button"
                    tabIndex={0}
                    onClick={(e) => {
                      e.stopPropagation()
                      void handleDelete(account)
                    }}
                    className="rounded p-1.5 text-gh-text-secondary transition-colors hover:bg-gh-danger/10 hover:text-gh-danger"
                    title="删除账号"
                  >
                    {busy === `del-${account.id}` ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                  </span>
                </button>

                {/* 折叠内容：worker 列表 */}
                {isOpen && (
                  <div className="border-t border-gh-border bg-gh-bg px-4 py-3">
                    {account.workers.length === 0 ? (
                      <p className="py-3 text-center text-xs text-gh-text-secondary">
                        {account.status === "active" ? "尚未部署 Worker（可点「部署清扫」）" : "账号不可用"}
                      </p>
                    ) : (
                      <div className="flex flex-col gap-2">
                        {account.workers.map((worker) => (
                          <div key={worker.worker_name} className="flex items-center justify-between gap-2 rounded-md border border-gh-border px-3 py-2">
                            <div className="min-w-0">
                              <div className="flex items-center gap-2">
                                <span className="text-sm font-medium text-gh-text">{worker.worker_name}</span>
                                <Badge color={worker.failures === 0 ? "#3fb950" : "#f85149"}>
                                  {worker.failures === 0 ? "健康" : `失败 ${worker.failures}`}
                                </Badge>
                              </div>
                              <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-gh-text-secondary">
                                <span className="truncate font-mono">{worker.canonical_url}</span>
                                <span className="font-mono">{worker.provider_id}</span>
                                {worker.last_check && <span>{formatDate(worker.last_check)}</span>}
                              </div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* 接入账号弹窗。走 Portal: 页面容器可能带 transform(例如页入场动画的
          `animation-fill-mode: both` 会永久保留 translateY(0)), 那会让 fixed 退化成
          相对该容器定位, 弹窗被裁在一小块里。 */}
      {showAdd && createPortal(
        <div className="fixed inset-0 z-50 overflow-y-auto bg-black/50 p-4" onClick={() => setShowAdd(false)}>
        <div className="grid min-h-full place-items-center">
          <div className="w-full max-w-md rounded-lg border border-gh-border bg-gh-bg p-5" onClick={(e) => e.stopPropagation()}>
            <h3 className="mb-3 text-sm font-semibold text-gh-text">接入 CF 账号</h3>
            <div className="flex flex-col gap-3">
              <label className="flex flex-col gap-1">
                <span className="text-xs text-gh-text-secondary">邮箱（账号标识）</span>
                <Input value={addForm.email} placeholder="xxx@outlook.com" onChange={(e) => setAddForm((f) => ({ ...f, email: e.target.value }))} />
              </label>
              <label className="flex flex-col gap-1">
                <span className="text-xs text-gh-text-secondary">CF 账号 ID</span>
                <Input value={addForm.cf_account_id} placeholder="32 位 hex account id" onChange={(e) => setAddForm((f) => ({ ...f, cf_account_id: e.target.value }))} />
              </label>
              <label className="flex flex-col gap-1">
                <span className="text-xs text-gh-text-secondary">API Token</span>
                <Input type="password" value={addForm.api_token} placeholder="cfut_… 或全局 token" onChange={(e) => setAddForm((f) => ({ ...f, api_token: e.target.value }))} />
              </label>
            </div>
            <div className="mt-4 flex justify-end gap-2">
              <Button size="sm" variant="ghost" disabled={busy === "add"} onClick={() => setShowAdd(false)}>取消</Button>
              <Button size="sm" variant="default" disabled={busy === "add"} onClick={() => void handleAdd()}>接入</Button>
            </div>
          </div>
        </div>
        </div>, document.body,
      )}
    </div>
  )
}

function StatCard({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-gh-border bg-gh-bg-secondary px-4 py-3">
      <span className="text-gh-accent">{icon}</span>
      <div>
        <div className="text-lg font-semibold leading-tight text-gh-text">{value}</div>
        <div className="text-xs text-gh-text-secondary">{label}</div>
      </div>
    </div>
  )
}
