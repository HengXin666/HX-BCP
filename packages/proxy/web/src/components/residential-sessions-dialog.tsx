import { useState } from "react"
import { KeyRound, LoaderCircle, RefreshCw } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ApiError, api } from "@/lib/api"
import type { ResidentialChannel } from "@/lib/types"
import { formatDate } from "@/lib/utils"

export function ResidentialSessionsDialog({
  channel,
  onClose,
  onReload,
  onNotice,
}: {
  channel: ResidentialChannel
  onClose: () => void
  onReload: () => Promise<void>
  onNotice: (message: string, tone?: "success" | "error") => void
}) {
  const [busy, setBusy] = useState<number | "share" | "control" | null>(null)

  async function run(key: number | "share" | "control", action: () => Promise<unknown>, message: string) {
    setBusy(key)
    try {
      await action()
      await onReload()
      onNotice(message)
    } catch (error) {
      onNotice(error instanceof ApiError ? error.message : String(error), "error")
    } finally {
      setBusy(null)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{channel.name} · 住宅节点</DialogTitle>
          <DialogDescription>
            客户端节点名称和凭据保持稳定；“换 IP”仅替换服务端内部住宅出口。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 px-5 py-4">
          <div className="flex flex-col gap-2 rounded-md border bg-muted/40 p-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <div className="text-xs font-medium">自动化控制 Token</div>
              <div className="mt-1 text-[11px] text-muted-foreground">完整控制 URL 在“代理服务”页面复制。</div>
            </div>
            <div className="flex shrink-0 gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={busy !== null}
                onClick={() => void run("control", () => api.rotateResidentialControlToken(channel.id), "控制 Token 已轮换")}
              >
                {busy === "control" ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
                轮换
              </Button>
            </div>
          </div>

          <div className="overflow-x-auto rounded-md border">
            <table className="w-full min-w-[680px] text-left text-xs">
              <thead className="border-b bg-muted/50 text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">节点</th>
                  <th className="px-3 py-2 font-medium">路由</th>
                  <th className="px-3 py-2 font-medium">地区 / 出口</th>
                  <th className="px-3 py-2 font-medium">轮换</th>
                  <th className="px-3 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {(channel.sessions ?? []).map((session) => (
                  <tr key={session.index}>
                    <td className="px-3 py-2">
                      <div className="font-medium">{session.node_name}</div>
                      <code className="text-[11px] text-muted-foreground">{session.session_id}</code>
                    </td>
                    <td className="px-3 py-2">{session.route_mode}</td>
                    <td className="px-3 py-2 text-muted-foreground">
                      {session.country_code || "自动"}{session.exit_ip ? ` · ${session.exit_ip}` : ""}
                    </td>
                    <td className="px-3 py-2 text-muted-foreground">
                      {session.rotate_count} 次
                      {session.last_rotated_at ? <div className="text-[11px]">{formatDate(session.last_rotated_at)}</div> : null}
                    </td>
                    <td className="px-3 py-2 text-right">
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy !== null}
                        onClick={() => void run(session.index, () => api.rotateResidentialSession(channel.id, session.index), `${session.node_name} 已换 IP`)}
                      >
                        {busy === session.index ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
                        换 IP
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {(channel.sessions ?? []).length === 0 && (
              <div className="px-4 py-10 text-center text-sm text-muted-foreground">此渠道没有声明节点</div>
            )}
          </div>

          <div className="flex items-center justify-between gap-3 border-t pt-3">
            <span className="text-xs text-muted-foreground">轮换订阅 Token 后，已导入客户端的旧地址会失效。</span>
            <Button
              size="sm"
              variant="outline"
              disabled={busy !== null}
              onClick={() => void run("share", () => api.rotateResidentialShareToken(channel.id), "渠道订阅 Token 已轮换")}
            >
              {busy === "share" ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
              轮换订阅 Token
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
