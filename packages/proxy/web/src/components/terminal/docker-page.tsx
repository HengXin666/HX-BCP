// See .agents/notes/implemented/bug-fix/2026-09-19-terminal-subpage-scroll-container.md.
import { useEffect, useRef, useState } from "react"
import { Container, LoaderCircle, RefreshCw } from "lucide-react"

import { api, type DockerContainer } from "@/lib/api"
import { cn } from "@/lib/utils"

const POLL_INTERVAL_MS = 5000

// DockerPage is the "Docker" sub-tab of the terminal page: a read-only list of
// containers (name, image, state, ports) merged with live `docker stats` where
// available. No start/stop/restart/remove operations are exposed.
export function DockerPage({ onNotice }: { onNotice: (message: string, tone?: "success" | "error") => void }) {
  const [containers, setContainers] = useState<DockerContainer[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const aliveRef = useRef(true)

  useEffect(() => {
    aliveRef.current = true
    let timer: number | null = null

    const load = async () => {
      try {
        const result = await api.dockerContainers()
        if (!aliveRef.current) return
        setContainers(result.containers)
        setError(null)
      } catch (cause) {
        if (!aliveRef.current) return
        setError(cause instanceof Error ? cause.message : "容器列表获取失败")
      } finally {
        if (aliveRef.current) setLoading(false)
      }
    }

    void load()
    timer = window.setInterval(() => void load(), POLL_INTERVAL_MS)
    return () => {
      aliveRef.current = false
      if (timer !== null) window.clearInterval(timer)
    }
  }, [onNotice])

  if (loading && containers.length === 0) {
    return (
      <div className="flex h-full min-h-40 items-center justify-center gap-2 text-xs text-muted-foreground">
        <LoaderCircle className="size-4 animate-spin" />正在读取容器列表…
      </div>
    )
  }

  const running = containers.filter((container) => container.state === "running").length

  return (
    // 终端页的 main 在 lg 断点是 lg:h-full lg:overflow-hidden, 所以这一页必须
    // 自己分配高度。滚动**只由表格区域承担** (min-h-0 flex-1 overflow-auto):
    //
    //   - 根节点不再 overflow-y-auto: 如果根和表格都能滚, 滚轮落在哪一层、
    //     列表是否还能滚到底就取决于当时的布局, 用户就会遇到 "docker 页面无法
    //     滚动列表"。
    //   - lg 以下 main 是内容高度(auto), flex 链没有确定高度, flex-1 的表格区域
    //     会被内容撑开而不产生滚动。max-lg:max-h 用视口高度给这一层一个确定上限,
    //     表格因此在 900x700 这类窄视口也仍然是唯一的滚动容器。
    //
    // 已知取舍: lg 以下整页会比视口高几十像素(900x700 实测 93px), 也就是文档级
    // 滚动条会同时存在。这是 App 壳在 lg 以下的既有行为(该高度的 flex 链没有确定
    // 高度, 只能靠内容撑), 不在本页可控范围内; 门禁 e2e/docker-scroll-guard.mjs
    // 因此只在 lg 及以上断死"整页不溢出", 窄视口只要求列表仍由表格区域滚动。
    // min-h-0 让 flex 子项在溢出时收缩而不是撑开父级。
    <div className="flex min-h-0 max-lg:max-h-[calc(100dvh-9.5rem)] flex-1 flex-col gap-3 overflow-hidden">
      <div className="flex shrink-0 items-center justify-between">
        <div className="flex items-center gap-1.5 text-sm font-semibold">
          <Container className="size-4 text-muted-foreground" />Docker 容器
          <span className="rounded-full border bg-muted/60 px-2 py-0.5 text-[11px] text-muted-foreground">
            {running} 运行 / {containers.length} 总计
          </span>
        </div>
        <div className="flex items-center gap-2">
          {error && <span className="text-xs text-destructive">{error}</span>}
          <button
            type="button"
            onClick={() => {
              setLoading(true)
              void api.dockerContainers()
                .then((result) => setContainers(result.containers))
                .catch((cause) => setError(cause instanceof Error ? cause.message : "刷新失败"))
                .finally(() => setLoading(false))
            }}
            className="inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs hover:bg-muted"
          >
            <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />刷新
          </button>
        </div>
      </div>

      {containers.length === 0 ? (
        <div className="shrink-0 rounded-md border bg-card px-4 py-8 text-center text-xs text-muted-foreground">没有可显示的容器</div>
      ) : (
        // 唯一的滚动容器: 纵向滚容器行, 横向滚 min-w-[860px] 的表格。
        // overscroll-contain 阻止滚到边界后把滚动传给外层页面。
        <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-md border bg-card">
          <table className="w-full min-w-[860px] text-left text-xs">
            <thead>
              <tr className="border-b bg-muted/60 text-muted-foreground">
                <th className="px-3 py-2 font-medium">名称</th>
                <th className="px-3 py-2 font-medium">镜像</th>
                <th className="px-3 py-2 font-medium">状态</th>
                <th className="px-3 py-2 font-medium">端口映射</th>
                <th className="px-3 py-2 font-medium">CPU</th>
                <th className="px-3 py-2 font-medium">内存</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {containers.map((container) => (
                <tr key={container.id} className="hover:bg-muted/30">
                  <td className="max-w-[180px] truncate px-3 py-2 font-medium" title={container.name}>{container.name}</td>
                  <td className="max-w-[200px] truncate px-3 py-2 text-muted-foreground" title={container.image}>{container.image}</td>
                  <td className="px-3 py-2">
                    <span
                      className={cn(
                        "inline-flex rounded-full border px-2 py-0.5 text-[10px]",
                        container.state === "running"
                          ? "border-success-border bg-success-muted text-success-foreground"
                          : "border-border bg-muted/60 text-muted-foreground",
                      )}
                    >
                      {container.state}
                    </span>
                  </td>
                  {/*
                    端口映射是这一页最常被查的字段, 所以不截断: docker ps 的端口串
                    可以有六七个映射, truncate 会把它变成 "0.0.0.0:8080->80/tcp, 0.0…",
                    用户根本读不到后半截。改成每个映射一行, 允许换行。
                  */}
                  <td className="px-3 py-2 align-top">
                    {container.ports
                      ? <div className="flex flex-col gap-0.5 font-mono text-[11px] text-muted-foreground">
                          {container.ports.split(", ").filter(Boolean).map((mapping) => (
                            <span key={mapping} className="whitespace-nowrap">{mapping}</span>
                          ))}
                        </div>
                      : <span className="text-muted-foreground">—</span>}
                  </td>
                  <td className="px-3 py-2 font-mono tabular-nums">{container.cpu_perc ?? "—"}</td>
                  <td className="px-3 py-2">
                    {container.mem_usage ? (
                      <span className="font-mono tabular-nums">{container.mem_usage}{container.mem_perc ? <span className="text-muted-foreground"> ({container.mem_perc})</span> : null}</span>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="text-[11px] leading-5 text-muted-foreground">
        当前为只读视图：仅展示容器、端口与运行占用，不提供启动/停止/删除操作。数据每 5 秒自动刷新。
      </p>
    </div>
  )
}
