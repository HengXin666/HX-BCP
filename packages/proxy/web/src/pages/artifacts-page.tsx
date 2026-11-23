import { useCallback, useEffect, useState } from "react"
import { Archive, CheckCircle2, Download, FileArchive, LoaderCircle, Plus, RefreshCw, Trash2 } from "lucide-react"

import { ConfirmDialog } from "@/components/confirm-dialog"
import { Badge } from "@/components/ui/badge"
import { Button, buttonVariants } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import type { ArtifactKind, ArtifactRecord } from "@/lib/types"
import { cn, compactId, formatBytes, formatDate } from "@/lib/utils"

interface ArtifactsPageProps {
  onNotice: (message: string, tone?: "success" | "error") => void
}

export function ArtifactsPage({ onNotice }: ArtifactsPageProps) {
  const [kind, setKind] = useState<ArtifactKind>("backup")
  const [items, setItems] = useState<ArtifactRecord[]>([])
  const [description, setDescription] = useState("")
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const [deleteTarget, setDeleteTarget] = useState<ArtifactRecord | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const result = await api.listArtifacts(kind)
      setItems(result.items)
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "加载归档失败", "error")
    } finally {
      setLoading(false)
    }
  }, [kind, onNotice])

  useEffect(() => {
    void load()
  }, [load])

  async function createArtifact() {
    setCreating(true)
    try {
      const created = await api.createArtifact(kind, description.trim())
      setItems((current) => [created, ...current])
      setDescription("")
      onNotice(kind === "backup" ? "数据库一致性 Backup 已创建" : "Portable Export 已创建")
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "创建归档失败", "error")
    } finally {
      setCreating(false)
    }
  }

  async function verify(item: ArtifactRecord) {
    setBusy((current) => ({ ...current, [item.id]: true }))
    try {
      const result = await api.verifyArtifact(kind, item.id)
      onNotice(`完整性校验通过：${result.files_checked} 个文件，Manifest v${result.manifest_version}`)
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "完整性校验失败", "error")
    } finally {
      setBusy((current) => ({ ...current, [item.id]: false }))
    }
  }

  async function remove() {
    const item = deleteTarget
    if (!item) return
    setBusy((current) => ({ ...current, [item.id]: true }))
    try {
      await api.deleteArtifact(kind, item.id)
      setItems((current) => current.filter((candidate) => candidate.id !== item.id))
      setDeleteTarget(null)
      onNotice("归档已删除")
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "删除归档失败", "error")
    } finally {
      setBusy((current) => ({ ...current, [item.id]: false }))
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">备份</h1>
          <p className="mt-1 max-w-3xl text-sm leading-5 text-muted-foreground">
            Backup 包含 SQLite Online Backup 数据库快照；Portable Export 仅包含可迁移的非敏感控制面状态。
          </p>
        </div>
        <Button variant="outline" onClick={() => void load()} disabled={loading}>
          <RefreshCw className={loading ? "animate-spin" : ""} />
          重新加载
        </Button>
      </div>

      <div className="inline-flex overflow-hidden rounded-md border bg-card">
        <KindTab active={kind === "backup"} onClick={() => setKind("backup")} icon={Archive}>Backup</KindTab>
        <KindTab active={kind === "export"} onClick={() => setKind("export")} icon={FileArchive} border>Portable Export</KindTab>
      </div>

      <section className="rounded-lg border bg-card">
        <div className="border-b bg-muted/60 px-3 py-2.5">
          <div className="text-sm font-semibold">创建 {kind === "backup" ? "Backup" : "Portable Export"}</div>
          <div className="mt-0.5 text-[11px] text-muted-foreground">
            {kind === "backup"
              ? "包含事务一致数据库副本，但不包含主密钥；用于同实例状态保护，不是完整跨机灾难恢复包。"
              : "不包含数据库、订阅秘密、节点凭据、运行时配置或主密钥。"}
          </div>
        </div>
        <div className="flex flex-col gap-2 p-3 sm:flex-row">
          <Input
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            placeholder="可选描述，例如：配置变更前"
            maxLength={256}
          />
          <Button onClick={() => void createArtifact()} disabled={creating} className="shrink-0">
            {creating ? <LoaderCircle className="animate-spin" /> : <Plus />}
            创建 {kind === "backup" ? "Backup" : "Export"}
          </Button>
        </div>
      </section>

      <section className="overflow-hidden rounded-lg border bg-card">
        <div className="flex items-center justify-between border-b bg-muted/60 px-3 py-2.5">
          <div>
            <div className="text-sm font-semibold">归档列表</div>
            <div className="mt-0.5 text-[11px] text-muted-foreground">SHA-256 元数据、内嵌 Manifest、原子发布</div>
          </div>
          <Badge variant="secondary">{items.length}</Badge>
        </div>

        {loading ? (
          <div className="flex min-h-48 items-center justify-center gap-2 text-sm text-muted-foreground">
            <LoaderCircle className="size-4 animate-spin" />
            正在加载归档
          </div>
        ) : items.length === 0 ? (
          <div className="flex min-h-52 flex-col items-center justify-center px-6 text-center">
            {kind === "backup" ? <Archive className="mb-3 size-8 text-muted-foreground" /> : <FileArchive className="mb-3 size-8 text-muted-foreground" />}
            <div className="font-medium">当前没有 {kind === "backup" ? "Backup" : "Portable Export"}</div>
            <p className="mt-1 text-xs text-muted-foreground">创建后可下载、执行完整性校验或删除。</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[940px] border-collapse text-left">
              <thead className="bg-muted/60 text-xs text-muted-foreground">
                <tr>
                  <Th>归档</Th>
                  <Th>创建时间</Th>
                  <Th>大小</Th>
                  <Th>SHA-256</Th>
                  <Th>敏感内容</Th>
                  <Th align="right">操作</Th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {items.map((item) => (
                  <ArtifactRow
                    key={item.id}
                    item={item}
                    kind={kind}
                    busy={Boolean(busy[item.id])}
                    onVerify={() => void verify(item)}
                    onDelete={() => setDeleteTarget(item)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除归档"
        description={`将永久删除“${deleteTarget?.filename ?? ""}”。此操作不会修改当前数据库或运行状态。`}
        busy={deleteTarget ? Boolean(busy[deleteTarget.id]) : false}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={() => void remove()}
      />
    </div>
  )
}

function ArtifactRow({ item, kind, busy, onVerify, onDelete }: { item: ArtifactRecord; kind: ArtifactKind; busy: boolean; onVerify: () => void; onDelete: () => void }) {
  return (
    <tr className="hover:bg-muted/60">
      <Td>
        <div className="max-w-[360px] truncate font-medium text-foreground" title={item.filename}>{item.filename}</div>
        <div className="mt-0.5 max-w-[360px] truncate text-[11px] text-muted-foreground" title={item.description || item.id}>
          {item.description || compactId(item.id)}
        </div>
      </Td>
      <Td>{formatDate(item.created_at)}</Td>
      <Td>{formatBytes(item.size)}</Td>
      <Td><span className="font-mono text-[11px]" title={item.sha256}>{compactId(item.sha256)}</span></Td>
      <Td><Badge variant={item.includes_secrets ? "warning" : "success"}>{item.includes_secrets ? "包含" : "不包含"}</Badge></Td>
      <Td align="right">
        <div className="flex justify-end gap-1.5">
          <Button variant="outline" size="sm" onClick={onVerify} disabled={busy}>
            {busy ? <LoaderCircle className="animate-spin" /> : <CheckCircle2 />}
            校验
          </Button>
          <a
            href={api.artifactDownloadURL(kind, item.id)}
            download={item.filename}
            className={buttonVariants({ variant: "outline", size: "icon" })}
            aria-label={`下载 ${item.filename}`}
          >
            <Download />
          </a>
          <Button variant="ghost" size="icon" onClick={onDelete} disabled={busy} aria-label={`删除 ${item.filename}`}>
            <Trash2 className="text-destructive" />
          </Button>
        </div>
      </Td>
    </tr>
  )
}

function KindTab({ active, onClick, icon: Icon, border = false, children }: { active: boolean; onClick: () => void; icon: typeof Archive; border?: boolean; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground hover:bg-muted/60",
        border && "border-l",
        active && "bg-accent text-accent-foreground hover:bg-accent",
      )}
    >
      <Icon className="size-3.5" />
      {children}
    </button>
  )
}

function Th({ children, align = "left" }: { children: React.ReactNode; align?: "left" | "right" }) {
  return <th className={cn("whitespace-nowrap border-b px-3 py-2 font-medium", align === "right" && "text-right")}>{children}</th>
}

function Td({ children, align = "left" }: { children: React.ReactNode; align?: "left" | "right" }) {
  return <td className={cn("whitespace-nowrap px-3 py-2.5 text-xs text-muted-foreground", align === "right" && "text-right")}>{children}</td>
}
