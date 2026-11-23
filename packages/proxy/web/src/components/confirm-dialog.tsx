import { AlertTriangle, X } from "lucide-react"
import { createPortal } from "react-dom"

import { Button } from "@/components/ui/button"

interface ConfirmDialogProps {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = "确认删除",
  busy = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  if (!open) return null

  // Portal 而非就地渲染。调用方可能在 `.page-enter` 内部, 那个类带
  // `animation-fill-mode: both`, 会把 `transform: translateY(0)` 永久留在元素上;
  // transform 不是 none 就成为 position:fixed 的包含块, 于是一个"全屏"遮罩会
  // 退化成那一小块, 弹窗被裁在里面。
  return createPortal(
    <div className="fixed inset-0 z-[80] flex items-center justify-center bg-foreground/35 p-4">
      <div className="w-full max-w-md rounded-lg border bg-card shadow-[0_12px_36px_rgba(31,35,40,0.18)]">
        <div className="flex items-start justify-between border-b px-4 py-3">
          <div className="flex items-center gap-2 font-semibold">
            <AlertTriangle className="size-4 text-warning" />
            {title}
          </div>
          <Button variant="ghost" size="icon" onClick={onCancel} disabled={busy} aria-label="关闭">
            <X />
          </Button>
        </div>
        <div className="px-4 py-4 text-sm leading-6 text-muted-foreground">{description}</div>
        <div className="flex justify-end gap-2 border-t bg-muted/60 px-4 py-3">
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            取消
          </Button>
          <Button variant="destructive" onClick={onConfirm} disabled={busy}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
