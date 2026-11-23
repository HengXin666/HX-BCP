import { useCallback, useEffect, useRef, useState } from "react"
import {
  ArrowUp,
  ChevronRight,
  Download,
  FolderUp,
  HardDriveUpload,
  HardDriveDownload,
  LoaderCircle,
  RefreshCw,
  Trash2,
} from "lucide-react"

import { ApiError, api, type TerminalFileEntry, type TerminalFileList } from "@/lib/api"
import { FileIcon } from "@/components/terminal/file-icons"
import { parentPath } from "@/lib/terminal-cwd"
import { cn, formatBytes } from "@/lib/utils"

export interface FilePanelProps {
  /** Absolute directory shown, owned by the parent (synced with the shell cwd). */
  path: string
  /** Only list files while the terminal is connected. */
  connected: boolean
  /** Called for every navigation: entering a directory, 上级, breadcrumb. */
  onPathChange: (path: string) => void
  onNotice: (message: string, tone?: "success" | "error") => void
}

// FilePanel renders a FinalShell-style file browser limited to the file
// system reachable by the control plane process. The path is controlled by
// the parent so the panel and the shell stay in sync; every navigation flows
// through onPathChange. Drag-and-drop upload and click-to-download are
// supported, and a request sequence number prevents stale responses from
// racing when navigating quickly.
export function FilePanel({ path, connected, onPathChange, onNotice }: FilePanelProps) {
  const [list, setList] = useState<TerminalFileList | null>(null)
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState<{ path: string; message: string } | null>(null)
  const [busyName, setBusyName] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const seqRef = useRef(0)
  const lastNavRef = useRef<{ name: string; at: number } | null>(null)
  const [dragOver, setDragOver] = useState(false)

  // load fetches one directory listing. Transient failures (network drops,
  // 5xx from a busy control plane) are retried with a small backoff, and the
  // previous successful listing stays visible while retrying; only a
  // definitive 403/404 or exhausted retries surface as a failure.
  const load = useCallback(async (target: string) => {
    const seq = ++seqRef.current
    setLoading(true)
    let lastError: unknown = null
    for (let attempt = 0; attempt < 3; attempt += 1) {
      try {
        const result = await api.listTerminalFiles(target === "" ? "/" : target)
        if (seqRef.current !== seq) return
        setFailed(null)
        setList(result)
        setLoading(false)
        return
      } catch (cause) {
        if (seqRef.current !== seq) return
        lastError = cause
        const retryable =
          cause instanceof ApiError ? cause.status >= 500 : true // network-level failure
        if (!retryable || attempt === 2) break
        await new Promise((resolve) => window.setTimeout(resolve, 500 * (attempt + 1)))
      }
    }
    if (seqRef.current !== seq) return
    const cause = lastError
    // Missing and permission-denied directories are normal shell states (the
    // root PTY helper starts in /root while the control plane cannot read
    // it). Render them inline instead of firing a page toast on every sync.
    if (
      cause instanceof ApiError &&
      (cause.code === "file_list_forbidden" || cause.code === "file_list_not_found")
    ) {
      setList(null)
      setFailed({ path: target === "" ? "/" : target, message: cause.message })
    } else {
      setFailed(null)
      onNotice(cause instanceof ApiError ? cause.message : "读取目录失败", "error")
    }
    setLoading(false)
  }, [onNotice])

  useEffect(() => {
    if (!connected) return
    void load(path)
  }, [connected, path, load])

  if (!connected) return null

  const enter = (entry: TerminalFileEntry) => {
    const target = `${path}/${entry.name}`.replace(/\/+/g, "/")
    // The second click of a double-click lands after the first navigation has
    // re-rendered, so re-entering the SAME entry would compute
    // `…/user/projects/projects` from the already-navigated path and list a
    // directory that does not exist. Suppress the same entry within 400ms.
    const now = Date.now()
    const last = lastNavRef.current
    if (last && last.name === entry.name && now - last.at < 400) return
    lastNavRef.current = { name: entry.name, at: now }
    onPathChange(target)
  }

  const goUp = () => {
    const target = failed ? parentPath(failed.path) : list?.parent
    if (target) onPathChange(target)
  }

  const currentSegment = path.split("/").filter(Boolean).at(-1)

  async function download(entry: TerminalFileEntry) {
    setBusyName(entry.name)
    try {
      const full = `${path}/${entry.name}`.replace(/\/+/g, "/")
      window.location.href = api.terminalFileDownloadURL(full)
    } finally {
      setTimeout(() => setBusyName(null), 600)
    }
  }

  async function remove(entry: TerminalFileEntry) {
    if (!confirm(`删除 ${entry.name}？该操作不可撤销。`)) return
    setBusyName(entry.name)
    try {
      const full = `${path}/${entry.name}`.replace(/\/+/g, "/")
      await api.removeTerminalFile(full)
      await load(path)
      onNotice(`已删除 ${entry.name}`)
    } catch (cause) {
      onNotice(cause instanceof ApiError ? cause.message : "删除失败", "error")
    } finally {
      setBusyName(null)
    }
  }

  async function onFiles(files: FileList) {
    setBusyName("upload")
    try {
      for (const file of Array.from(files)) {
        await api.uploadTerminalFile(list?.path ?? path, file)
      }
      await load(list?.path ?? path)
      onNotice(`已上传 ${files.length} 个文件`)
    } catch (cause) {
      onNotice(cause instanceof ApiError ? cause.message : "上传失败", "error")
    } finally {
      setBusyName(null)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1.5 border-b px-2 py-1.5 text-xs font-medium">
        <FolderUp className="size-3.5 text-muted-foreground" />
        <span className="text-muted-foreground">文件</span>
        <button
          type="button"
          onClick={goUp}
          disabled={!(list?.parent || (failed && parentPath(failed.path)))}
          className="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 disabled:opacity-50 hover:bg-muted"
        >
          <ArrowUp className="size-3" /> 上级
        </button>
        <div className="mx-1 flex min-w-0 flex-1 items-center gap-0.5 truncate text-muted-foreground">
          {path.split("/").filter(Boolean).map((segment, index, segments) => {
            const absolute = "/" + segments.slice(0, index + 1).join("/")
            return (
              <span key={absolute} className="inline-flex min-w-0 items-center">
                <button
                  type="button"
                  onClick={() => onPathChange(absolute)}
                  className="max-w-[6rem] truncate rounded px-1 hover:bg-muted"
                  title={absolute}
                >
                  {index === 0 ? "/" : segment}
                </button>
                {index < segments.length - 1 && <ChevronRight className="size-3 shrink-0 opacity-50" />}
              </span>
            )
          })}
        </div>
        <button
          type="button"
          onClick={() => void load(path)}
          className="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 hover:bg-muted"
          title="刷新"
        >
          {loading ? <LoaderCircle className="size-3 animate-spin" /> : <RefreshCw className="size-3" />}
        </button>
      </div>

      <div
        className={cn("relative flex min-h-0 flex-1 flex-col overflow-auto", dragOver && "bg-accent/20")}
        onDragOver={(e) => { e.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault()
          setDragOver(false)
          if (e.dataTransfer.files.length) void onFiles(e.dataTransfer.files)
        }}
      >
        {dragOver && (
          <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-accent/10 text-xs text-accent-foreground">
            <div className="rounded-md border bg-card px-3 py-2 shadow">松开以上传到 {list?.path ?? path}</div>
          </div>
        )}
        {!loading && failed && (
          <div className="flex h-full min-h-0 flex-col items-center justify-center gap-1.5 px-4 py-10 text-center text-xs">
            <FolderUp className="size-5 text-muted-foreground" />
            <div className="font-medium text-foreground">无法读取该目录</div>
            <div className="break-all font-mono text-muted-foreground">{failed.path}</div>
            <div className="text-muted-foreground">{failed.message}</div>
            <button
              type="button"
              onClick={goUp}
              disabled={!parentPath(failed.path)}
              className="mt-2 inline-flex items-center gap-1 rounded border px-2 py-1 hover:bg-muted disabled:opacity-50"
            >
              <ArrowUp className="size-3" /> 返回上级
            </button>
          </div>
        )}
        {!loading && !failed && list && list.entries.length === 0 && (
          <div className="flex items-center justify-center py-8 text-xs text-muted-foreground">空目录</div>
        )}
        {list?.entries.map((entry) => (
          <div
            key={entry.name}
            className="group flex items-center gap-2 border-b border-transparent px-2 py-1 text-xs hover:bg-muted/60"
          >
            <button
              type="button"
              onClick={() => entry.is_dir && enter(entry)}
              className="flex min-w-0 flex-1 items-center gap-1.5 text-left"
              title={entry.is_dir ? "进入目录" : entry.name}
            >
              <FileIcon
                name={entry.name}
                isDir={entry.is_dir}
                open={entry.is_dir && entry.name === currentSegment}
                className="size-4 shrink-0"
              />
              <span className={cn("min-w-0 truncate", entry.is_dir ? "font-medium" : "text-muted-foreground")}>
                {entry.name}
              </span>
              {!entry.is_dir && (
                <span className="ml-auto shrink-0 font-mono tabular-nums text-muted-foreground">{formatBytes(entry.size)}</span>
              )}
            </button>
            {!entry.is_dir && (
              <button
                type="button"
                onClick={() => void download(entry)}
                disabled={busyName === entry.name}
                className="opacity-0 transition-opacity group-hover:opacity-100"
                title="下载"
              >
                {busyName === entry.name ? <LoaderCircle className="size-3.5 animate-spin" /> : <Download className="size-3.5" />}
              </button>
            )}
            <button
              type="button"
              onClick={() => void remove(entry)}
              disabled={busyName === entry.name}
              className="opacity-0 transition-opacity group-hover:opacity-100 text-destructive hover:text-destructive/80"
              title="删除"
            >
              <Trash2 className="size-3.5" />
            </button>
          </div>
        ))}
      </div>

      <div className="flex items-center gap-2 border-t px-2 py-1.5 text-xs">
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          className="inline-flex items-center gap-1 rounded border px-2 py-1 hover:bg-muted disabled:opacity-50"
          disabled={busyName === "upload"}
        >
          {busyName === "upload" ? <LoaderCircle className="size-3.5 animate-spin" /> : <HardDriveUpload className="size-3.5" />}
          上传
        </button>
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          className="inline-flex items-center gap-1 rounded border px-2 py-1 hover:bg-muted"
          title="提示：也可直接把文件拖入目录列表"
        >
          <HardDriveDownload className="size-3.5" /> 拖拽区域
        </button>
        <input
          ref={fileInputRef}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => { if (e.target.files?.length) void onFiles(e.target.files); e.target.value = "" }}
        />
      </div>
    </div>
  )
}
