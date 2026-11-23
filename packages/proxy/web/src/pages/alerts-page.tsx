import { useCallback, useEffect, useState, type FormEvent } from "react"
import { BellRing, CheckCheck, History, Mail, RefreshCw, Send } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import {
  ApiError,
  api,
  type AlertRecord,
  type AlertSettings,
} from "@/lib/api"
import { cn } from "@/lib/utils"

export function AlertsPage({
  onNotice,
}: {
  onNotice: (message: string, tone?: "success" | "error") => void
}) {
  const [firing, setFiring] = useState<AlertRecord[]>([])
  const [history, setHistory] = useState<AlertRecord[]>([])
  const [settings, setSettings] = useState<AlertSettings | null>(null)
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const [firingList, resolvedList, currentSettings] = await Promise.all([
        api.listAlerts("firing"),
        api.listAlerts("resolved"),
        api.alertSettings(),
      ])
      setFiring(firingList.items)
      setHistory(resolvedList.items)
      setSettings(currentSettings)
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
    } finally {
      setLoading(false)
    }
  }, [onNotice])

  useEffect(() => {
    void reload()
    const timer = window.setInterval(() => void reload(), 30_000)
    return () => window.clearInterval(timer)
  }, [reload])

  async function acknowledge(id: string) {
    try {
      await api.acknowledgeAlert(id)
      onNotice("已确认告警，后续将不再重复通知")
      await reload()
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">告警</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            订阅刷新失败、空代理组与数据面异常每分钟自动评估，恢复后发送一次恢复通知。
          </p>
        </div>
        <button
          type="button"
          onClick={() => void reload()}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-muted"
        >
          <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
          刷新
        </button>
      </div>

      <section className="rounded-md border bg-card">
        <header className="flex items-center gap-2 border-b px-4 py-2.5 text-sm font-medium">
          <BellRing className="size-4 text-destructive" />
          当前告警（{firing.length}）
        </header>
        {firing.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-muted-foreground">
            没有正在触发的告警。
          </div>
        ) : (
          <ul className="divide-y">
            {firing.map((item) => (
              <li key={item.id} className="flex items-start gap-3 px-4 py-3">
                <SeverityBadge severity={item.severity} />
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">
                    {item.rule}
                    <span className="ml-2 font-normal text-muted-foreground">{item.target_name}</span>
                  </div>
                  <div className="mt-0.5 text-xs text-muted-foreground">{item.message}</div>
                  <div className="mt-1 text-[11px] text-muted-foreground">
                    触发于 {new Date(item.fired_at).toLocaleString()}
                    {item.notify_count > 0 && ` · 已通知 ${item.notify_count} 次`}
                    {item.acknowledged && " · 已确认"}
                  </div>
                </div>
                {!item.acknowledged && (
                  <button
                    type="button"
                    onClick={() => void acknowledge(item.id)}
                    className="inline-flex shrink-0 items-center gap-1 rounded-md border px-2.5 py-1 text-xs hover:bg-muted"
                  >
                    <CheckCheck className="size-3.5" />
                    确认
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="rounded-md border bg-card">
        <header className="flex items-center gap-2 border-b px-4 py-2.5 text-sm font-medium">
          <History className="size-4 text-muted-foreground" />
          历史（最近 {history.length} 条已恢复）
        </header>
        {history.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-muted-foreground">暂无历史记录。</div>
        ) : (
          <ul className="max-h-72 divide-y overflow-y-auto">
            {history.map((item) => (
              <li key={item.id} className="flex items-start gap-3 px-4 py-2.5">
                <SeverityBadge severity={item.severity} muted />
                <div className="min-w-0 flex-1">
                  <div className="text-sm">
                    {item.rule}
                    <span className="ml-2 text-muted-foreground">{item.target_name}</span>
                  </div>
                  <div className="mt-0.5 text-[11px] text-muted-foreground">
                    {new Date(item.fired_at).toLocaleString()} 触发
                    {item.resolved_at && ` · ${new Date(item.resolved_at).toLocaleString()} 恢复`}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      {settings && <SettingsCard settings={settings} onNotice={onNotice} onSaved={reload} />}
    </div>
  )
}

function SettingsCard({
  settings,
  onNotice,
  onSaved,
}: {
  settings: AlertSettings
  onNotice: (message: string, tone?: "success" | "error") => void
  onSaved: () => Promise<void>
}) {
  const [enabled, setEnabled] = useState(settings.enabled)
  const [host, setHost] = useState(settings.host ?? "")
  const [port, setPort] = useState(settings.port ?? 587)
  const [security, setSecurity] = useState(settings.security ?? "starttls")
  const [username, setUsername] = useState(settings.username ?? "")
  const [password, setPassword] = useState("")
  const [from, setFrom] = useState(settings.from ?? "")
  const [to, setTo] = useState((settings.to ?? []).join(", "))
  const [busy, setBusy] = useState(false)

  async function save(event: FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    try {
      await api.updateAlertSettings({
        enabled,
        host: host.trim(),
        port,
        security,
        username: username.trim(),
        password,
        from: from.trim(),
        to: to.split(",").map((value) => value.trim()).filter(Boolean),
      })
      onNotice("SMTP 告警设置已保存")
      setPassword("")
      await onSaved()
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
    } finally {
      setBusy(false)
    }
  }

  async function sendTest() {
    if (busy) return
    setBusy(true)
    try {
      await api.testAlertSettings()
      onNotice("测试邮件已发送，请检查收件箱")
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="rounded-md border bg-card">
      <header className="flex items-center gap-2 border-b px-4 py-2.5 text-sm font-medium">
        <Mail className="size-4 text-info" />
        邮件通道（SMTP）
      </header>
      <form onSubmit={save} className="grid gap-3 p-4 sm:grid-cols-2">
        <label className="flex items-center gap-2 text-sm sm:col-span-2">
          <Checkbox checked={enabled} onCheckedChange={(value) => setEnabled(value === true)} />
          启用邮件通知
        </label>
        <label className="block text-xs font-medium">
          SMTP 服务器
          <Input value={host} onChange={(e) => setHost(e.target.value)} required className="mt-1" placeholder="smtp.example.com" />
        </label>
        <div className="grid grid-cols-2 gap-3">
          <label className="block text-xs font-medium">
            端口
            <Input
              type="number"
              min={1}
              max={65535}
              value={port}
              onChange={(e) => setPort(Number(e.target.value))}
              required
              className="mt-1"
            />
          </label>
          <label className="block text-xs font-medium">
            加密
            <Select value={security} onValueChange={setSecurity}><SelectTrigger className="mt-1"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="starttls">STARTTLS</SelectItem><SelectItem value="tls">SMTPS（隐式 TLS）</SelectItem><SelectItem value="none">无（仅内网）</SelectItem></SelectContent></Select>
          </label>
        </div>
        <label className="block text-xs font-medium">
          用户名
          <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" className="mt-1" />
        </label>
        <label className="block text-xs font-medium">
          密码
          <Input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            placeholder={settings.has_password ? "留空保持不变" : ""}
            className="mt-1"
          />
        </label>
        <label className="block text-xs font-medium">
          发件人
          <Input value={from} onChange={(e) => setFrom(e.target.value)} required className="mt-1" placeholder="alerts@example.com" />
        </label>
        <label className="block text-xs font-medium">
          收件人（逗号分隔）
          <Input value={to} onChange={(e) => setTo(e.target.value)} required className="mt-1" placeholder="admin@example.com" />
        </label>
        <div className="flex items-center gap-2 sm:col-span-2">
          <Button
            type="submit"
            disabled={busy}
          >
            保存设置
          </Button>
          <Button
            type="button"
            onClick={() => void sendTest()}
            disabled={busy || !settings.configured}
            variant="outline"
          >
            <Send className="size-3.5" />
            发送测试邮件
          </Button>
        </div>
      </form>
    </section>
  )
}

function SeverityBadge({ severity, muted }: { severity: "warning" | "critical"; muted?: boolean }) {
  return (
    <span
      className={cn(
        "mt-0.5 inline-flex shrink-0 rounded-full border px-2 py-0.5 text-[11px] font-medium",
        muted
          ? "border-border bg-muted/60 text-muted-foreground"
          : severity === "critical"
            ? "border-destructive/40 bg-destructive/10 text-destructive"
            : "border-warning-border bg-warning-muted text-warning-foreground",
      )}
    >
      {severity === "critical" ? "严重" : "警告"}
    </span>
  )
}
