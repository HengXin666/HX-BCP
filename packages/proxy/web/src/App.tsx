import { lazy, Suspense, useCallback, useEffect, useState } from "react"
import {
  Archive,
  BellRing,
  CheckCircle2,
  CircleX,
  Globe,
  Info,
  LayoutDashboard,
  LogOut,
  ListFilter,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  Radio,
  Route,
  Settings2,
  TerminalSquare,
  X,
} from "lucide-react"

import { ThemeToggle } from "@/components/theme-toggle"
// AuthPage is the only page kept in the eager graph: it is the first
// interactive screen when the control plane is unauthenticated, so lazily
// loading it would flash a fallback on top of the login form. Everything else
// is split so first paint only pays for the shell.
import { AuthPage } from "@/pages/auth-page"
import { api, setCsrfToken, setUnauthenticatedHandler } from "@/lib/api"
import { cn } from "@/lib/utils"

type Page = "overview" | "subscriptions" | "routing" | "residential" | "rules" | "settings" | "alerts" | "artifacts" | "terminal" | "about"
type Notice = { id: number; message: string; tone: "success" | "error" }
const sidebarStorageKey = "hx-pxy.sidebar-collapsed"

// One loader per page, shared by React.lazy (render path) and preloadPage
// (hover/focus prefetch). A second import() of the same specifier is free: the
// module registry returns the already-resolved promise, so the browser never
// downloads a chunk twice.
const pageLoaders = {
  overview: () => import("@/pages/overview-page"),
  subscriptions: () => import("@/pages/inventory-page"),
  routing: () => import("@/pages/routing-page"),
  residential: () => import("@/pages/residential-page"),
  rules: () => import("@/pages/rules-page"),
  settings: () => import("@/pages/settings-page"),
  alerts: () => import("@/pages/alerts-page"),
  artifacts: () => import("@/pages/artifacts-page"),
  terminal: () => import("@/pages/terminal-page"),
  about: () => import("@/pages/about-page"),
}

// The terminal bundles xterm (~200KB), so it is split into its own chunk:
// residential, proxy-service and node pages never pay that download on load.
const TerminalPage = lazy(() => pageLoaders.terminal().then((module) => ({ default: module.TerminalPage })))
const OverviewPage = lazy(() => pageLoaders.overview().then((module) => ({ default: module.OverviewPage })))
const InventoryPage = lazy(() => pageLoaders.subscriptions().then((module) => ({ default: module.InventoryPage })))
const RoutingPage = lazy(() => pageLoaders.routing().then((module) => ({ default: module.RoutingPage })))
const ResidentialPage = lazy(() => pageLoaders.residential().then((module) => ({ default: module.ResidentialPage })))
const RulesPage = lazy(() => pageLoaders.rules().then((module) => ({ default: module.RulesPage })))
const SettingsPage = lazy(() => pageLoaders.settings().then((module) => ({ default: module.SettingsPage })))
const AlertsPage = lazy(() => pageLoaders.alerts().then((module) => ({ default: module.AlertsPage })))
const ArtifactsPage = lazy(() => pageLoaders.artifacts().then((module) => ({ default: module.ArtifactsPage })))
const AboutPage = lazy(() => pageLoaders.about().then((module) => ({ default: module.AboutPage })))

const preloadedPages = new Set<Page>()
// Starts downloading a page chunk without rendering it. Called from sidebar
// hover/focus so the click usually resolves against an already-loaded module.
function preloadPage(id: Page) {
  if (preloadedPages.has(id)) return
  preloadedPages.add(id)
  pageLoaders[id]().catch(() => {
    // A failed prefetch must stay silent and be retryable: the real
    // navigation issues its own import() and surfaces the error there.
    preloadedPages.delete(id)
  })
}

const pages: Array<{
  id: Page
  label: string
  description: string
  icon: typeof Radio
}> = [
  { id: "overview", label: "总览", description: "流量与路由", icon: LayoutDashboard },
  { id: "subscriptions", label: "订阅与节点", description: "来源、刷新与质量", icon: Radio },
  { id: "routing", label: "代理服务", description: "代理组与 Listener", icon: Route },
  { id: "residential", label: "住宅代理", description: "动态住宅 IP 渠道", icon: Globe },
  { id: "rules", label: "站点别名", description: "可复用网页组", icon: ListFilter },
  { id: "settings", label: "全局配置", description: "测速、DNS 与性能", icon: Settings2 },
  { id: "alerts", label: "告警", description: "状态与邮件通知", icon: BellRing },
  { id: "artifacts", label: "备份", description: "Backup 与 Export", icon: Archive },
  { id: "terminal", label: "终端", description: "服务器 Shell", icon: TerminalSquare },
  // 「接入 AI」页已整体删除: 它复制一段提示词给外部 AI, 而那份提示词是
  // .agents/skills/hx-consumer-api/SKILL.md 的劣化副本。接入说明只有 skill
  // 这一个正规形态, 不再有前端入口, 也不再注册路由。
  // 见 .agents/notes/implemented/simplification/2026-09-26-drop-ai-entry-prompt-ui.md。
  { id: "about", label: "关于", description: "版本与更新", icon: Info },
]

function pageFromHash(): Page {
  const value = window.location.hash.replace(/^#\/?/, "")
  if (value === "nodes") return "subscriptions"
  return pages.some((item) => item.id === value) ? (value as Page) : "overview"
}

type AuthGate =
  | { phase: "checking" }
  | { phase: "required"; configured: boolean }
  | { phase: "ready"; username?: string }

export default function App() {
  const [page, setPage] = useState<Page>(pageFromHash)
  const [healthy, setHealthy] = useState<boolean | null>(null)
  const [notice, setNotice] = useState<Notice | null>(null)
  const [authGate, setAuthGate] = useState<AuthGate>({ phase: "checking" })
  const [terminalMounted, setTerminalMounted] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => window.localStorage.getItem(sidebarStorageKey) === "1")

  useEffect(() => {
    window.localStorage.setItem(sidebarStorageKey, sidebarCollapsed ? "1" : "0")
  }, [sidebarCollapsed])

  const refreshAuth = useCallback(async () => {
    try {
      const status = await api.authStatus()
      if (!status.configured) {
        setAuthGate({ phase: "required", configured: false })
        return
      }
      if (!status.authenticated) {
        setCsrfToken("")
        setAuthGate({ phase: "required", configured: true })
        return
      }
      if (status.csrf_token) setCsrfToken(status.csrf_token)
      setAuthGate({ phase: "ready", username: status.username })
    } catch {
      // 后端不可达或未启用认证模块时不阻塞页面；健康横幅会提示离线。
      setAuthGate({ phase: "ready" })
    }
  }, [])

  useEffect(() => {
    setUnauthenticatedHandler(() => {
      setCsrfToken("")
      setAuthGate({ phase: "required", configured: true })
    })
    void refreshAuth()
    return () => setUnauthenticatedHandler(null)
  }, [refreshAuth])

  useEffect(() => {
    function onHashChange() {
      setPage(pageFromHash())
    }
    window.addEventListener("hashchange", onHashChange)
    return () => window.removeEventListener("hashchange", onHashChange)
  }, [])

  // The terminal chunk is downloaded lazily on first visit; afterwards the
  // page stays mounted across tab switches so the WebSocket session survives.
  useEffect(() => {
    if (page === "terminal") setTerminalMounted(true)
  }, [page])

  useEffect(() => {
    let cancelled = false
    async function check() {
      const ready = await api.health()
      if (!cancelled) setHealthy(ready)
    }
    void check()
    const timer = window.setInterval(() => void check(), 15_000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [])

  const navigate = useCallback((next: Page) => {
    window.location.hash = `/${next}`
    setPage(next)
  }, [])

  const showNotice = useCallback((message: string, tone: "success" | "error" = "success") => {
    const next = { id: Date.now(), message, tone }
    setNotice(next)
    window.setTimeout(() => {
      setNotice((current) => (current?.id === next.id ? null : current))
    }, 4_500)
  }, [])

  const handleLogout = useCallback(async () => {
    try {
      await api.logout()
    } finally {
      setCsrfToken("")
      setAuthGate({ phase: "required", configured: true })
    }
  }, [])

  const requireLogin = useCallback(() => {
    setCsrfToken("")
    setAuthGate({ phase: "required", configured: true })
  }, [])

  if (authGate.phase === "checking") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background text-sm text-muted-foreground">
        正在检查登录状态…
      </div>
    )
  }

  if (authGate.phase === "required") {
    // AuthPage is eager, so this Suspense boundary only exists to keep the
    // gate structurally uniform; the fallback mirrors the checking screen.
    return (
      <Suspense fallback={<AuthFallback />}>
        <AuthPage configured={authGate.configured} onAuthenticated={() => void refreshAuth()} />
      </Suspense>
    )
  }

  return (
    <div className={cn(
      "min-h-screen bg-background lg:grid lg:h-screen lg:overflow-hidden lg:transition-[grid-template-columns] lg:duration-200",
      sidebarCollapsed ? "lg:grid-cols-[68px_minmax(0,1fr)]" : "lg:grid-cols-[228px_minmax(0,1fr)]",
    )}>
      <aside data-sidebar data-collapsed={sidebarCollapsed} className="relative z-50 hidden min-h-screen border-r bg-card lg:sticky lg:top-0 lg:flex lg:h-screen lg:flex-col">
        <div className={cn("flex h-14 items-center border-b", sidebarCollapsed ? "justify-center px-2" : "gap-2.5 px-3")}>
          {!sidebarCollapsed && <>
            <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-[#24292f] text-white"><Network className="size-4" /></div>
            <div className="min-w-0 flex-1 truncate text-sm font-semibold">HX-Pxy</div>
          </>}
          <div className="group relative flex">
            <button
              type="button"
              className="flex size-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              onClick={() => setSidebarCollapsed((current) => !current)}
              aria-label={sidebarCollapsed ? "展开侧栏" : "折叠侧栏"}
              aria-expanded={!sidebarCollapsed}
            >
              {sidebarCollapsed ? <PanelLeftOpen className="size-4" /> : <PanelLeftClose className="size-4" />}
            </button>
            {sidebarCollapsed && <SidebarTooltip>展开侧栏</SidebarTooltip>}
          </div>
        </div>

        <nav className="space-y-1 p-2" aria-label="主导航">
          {pages.map((item) => (
            <SidebarItem
              key={item.id}
              item={item}
              active={item.id === page}
              onClick={() => navigate(item.id)}
              onPreload={() => preloadPage(item.id)}
              collapsed={sidebarCollapsed}
            />
          ))}
        </nav>

        <div className={cn("mt-auto border-t", sidebarCollapsed ? "space-y-1 p-2" : "p-3")}>
          {sidebarCollapsed ? <>
            <SidebarIconSlot tooltip={`后端状态：${healthy === null ? "检测中" : healthy ? "就绪" : "离线"}`}><StatusDot healthy={healthy} compact /></SidebarIconSlot>
            <SidebarIconSlot tooltip="切换主题"><ThemeToggle /></SidebarIconSlot>
            {authGate.username && <SidebarIconSlot tooltip={`退出登录（${authGate.username}）`}>
              <button type="button" onClick={() => void handleLogout()} className="flex size-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground" aria-label={`退出登录（${authGate.username}）`}><LogOut className="size-4" /></button>
            </SidebarIconSlot>}
          </> : <>
            <div className="rounded-md border bg-muted/60 px-3 py-2.5">
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs font-medium">后端状态</span>
              <StatusDot healthy={healthy} />
            </div>
            <div className="mt-1 text-[11px] leading-4 text-muted-foreground">
              Mihomo 数据面、节点检测、持久化流量统计、实时日志与告警均已接入。
            </div>
            </div>
            <div className="mt-2 flex h-8 items-center justify-between rounded-md px-2.5 text-xs text-muted-foreground"><span>界面主题</span><ThemeToggle /></div>
          {authGate.username && (
            <button
              type="button"
              onClick={() => void handleLogout()}
              className="mt-2 flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              <LogOut className="size-3.5 shrink-0" />
              <span className="min-w-0 flex-1 truncate">退出登录（{authGate.username}）</span>
            </button>
          )}
          </>}
        </div>
      </aside>

      <div className={cn("min-w-0 lg:h-screen", page === "routing" ? "lg:overflow-y-auto 2xl:overflow-hidden" : "lg:overflow-y-auto")}>
        <header className="sticky top-0 z-40 border-b bg-card lg:hidden">
          <div className="flex h-13 items-center gap-2 px-3">
            <div className="flex size-7 items-center justify-center rounded-md bg-[#24292f] text-white">
              <Network className="size-4" />
            </div>
            <span className="font-semibold">HX-Pxy</span>
            <div className="ml-auto flex items-center gap-1"><ThemeToggle /><StatusDot healthy={healthy} /></div>
          </div>
          <nav className="scrollbar-none flex gap-1 overflow-x-auto border-t px-2 py-1.5">
            {pages.map((item) => {
              const Icon = item.icon
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => navigate(item.id)}
                  onMouseEnter={() => preloadPage(item.id)}
                  onFocus={() => preloadPage(item.id)}
                  className={cn(
                    "inline-flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium text-muted-foreground",
                    page === item.id && "bg-accent text-accent-foreground",
                  )}
                >
                  <Icon className="size-3.5" />
                  {item.label}
                </button>
              )
            })}
          </nav>
        </header>

        {/*
          终端页单独一套容器类: 它有 lg:h-full + lg:overflow-hidden, 如果同时带
          px/py padding, 子页的 h-full 就会超出可用高度, 溢出部分被 overflow-hidden
          直接裁掉 —— 而且没有任何元素成为滚动容器, 结果是"看不到也滚不动"。
          所以终端页把 padding 交给 TerminalPage 自己承担, 由它内部的 flex 列分配高度。
        */}
        <main className={cn(
          "mx-auto w-full",
          page === "terminal"
            ? "lg:h-full lg:max-w-none lg:overflow-hidden"
            : "max-w-[1440px] px-4 py-5 sm:px-6 lg:px-8 lg:py-7",
          page === "routing" && "2xl:h-full 2xl:max-w-none 2xl:overflow-hidden",
        )}>
          {healthy === false && (
            <div className="mb-4 flex items-start gap-2.5 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2.5 text-sm text-destructive">
              <CircleX className="mt-0.5 size-4 shrink-0" />
              <div>
                <div className="font-medium">无法连接 Go 控制面</div>
                <div className="mt-0.5 text-xs opacity-80">
                  请从项目根目录运行 bash run.sh，并确认 19090 端口未被占用。
                </div>
              </div>
            </div>
          )}

          {page !== "terminal" && (
            <div key={page} className={cn("page-enter", page === "routing" && "2xl:h-full")}>
              <Suspense fallback={<PageFallback />}>
                {page === "overview" && <OverviewPage onNotice={showNotice} />}
                {page === "subscriptions" && <InventoryPage initialView={window.location.hash.includes("nodes") ? "nodes" : "subscriptions"} onNotice={showNotice} />}
                {page === "routing" && <RoutingPage onNotice={showNotice} />}
                {page === "residential" && <ResidentialPage onNotice={showNotice} />}
                {page === "rules" && <RulesPage onNotice={showNotice} />}
                {page === "settings" && <SettingsPage onNotice={showNotice} username={authGate.username} onSignedOut={requireLogin} />}
                {page === "alerts" && <AlertsPage onNotice={showNotice} />}
                {page === "artifacts" && <ArtifactsPage onNotice={showNotice} />}
                {page === "about" && <AboutPage onNotice={showNotice} />}
              </Suspense>
            </div>
          )}
          {/* TerminalPage stays mounted across page switches so the WebSocket
              session survives: switching sidebar tabs only hides it. The chunk
              is downloaded on first visit (see terminalMounted). */}
          {terminalMounted && <div className={page === "terminal" ? "contents" : "hidden"}><Suspense fallback={<div className="flex min-h-40 items-center justify-center text-sm text-muted-foreground">正在加载终端…</div>}><TerminalPage onNotice={showNotice} /></Suspense></div>}
        </main>
      </div>

      {notice && (
        <div className="toast-enter fixed bottom-4 right-4 z-[90] flex max-w-sm items-start gap-2.5 rounded-md border bg-card px-3 py-2.5 shadow-[0_12px_32px_rgba(23,33,31,0.16)]">
          {notice.tone === "success" ? (
            <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" />
          ) : (
            <CircleX className="mt-0.5 size-4 shrink-0 text-destructive" />
          )}
          <div className="min-w-0 text-sm leading-5">{notice.message}</div>
          <button
            type="button"
            onClick={() => setNotice(null)}
            className="ml-1 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label="关闭提示"
          >
            <X className="size-3.5" />
          </button>
        </div>
      )}
    </div>
  )
}

// Keeps the same muted, centered treatment as the terminal loader so route
// chunks resolve without a visible layout jump.
function PageFallback() {
  return (
    <div className="flex min-h-40 items-center justify-center text-sm text-muted-foreground">
      正在加载页面…
    </div>
  )
}

function AuthFallback() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background text-sm text-muted-foreground">
      正在加载登录…
    </div>
  )
}

function SidebarItem({
  item,
  active,
  onClick,
  onPreload,
  collapsed,
}: {
  item: (typeof pages)[number]
  active: boolean
  onClick: () => void
  onPreload: () => void
  collapsed: boolean
}) {
  const Icon = item.icon
  return (
    <button
      type="button"
      onClick={onClick}
      onMouseEnter={onPreload}
      onFocus={onPreload}
      aria-label={collapsed ? item.label : undefined}
      className={cn(
        "group relative flex w-full items-center rounded-md text-left text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
        collapsed ? "h-10 justify-center px-0" : "gap-2.5 px-2.5 py-2",
        active && "bg-accent font-medium text-accent-foreground",
      )}
    >
      <Icon className="size-4 shrink-0" />
      {!collapsed && <span className="min-w-0 flex-1">
        <span className="block truncate">{item.label}</span>
        <span className={cn("block truncate text-[11px] font-normal", active ? "text-accent-foreground/80" : "text-muted-foreground")}>{item.description}</span>
      </span>}
      {collapsed && <SidebarTooltip><span className="font-medium">{item.label}</span><span className="ml-1.5 text-muted-foreground">{item.description}</span></SidebarTooltip>}
    </button>
  )
}

function SidebarTooltip({ children }: { children: React.ReactNode }) {
  return <span data-sidebar-tooltip role="tooltip" className="pointer-events-none absolute left-[calc(100%+8px)] top-1/2 z-[70] -translate-y-1/2 whitespace-nowrap rounded-md border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground opacity-0 shadow-lg transition-opacity group-hover:opacity-100">{children}</span>
}

function SidebarIconSlot({ children, tooltip }: { children: React.ReactNode; tooltip: string }) {
  return <div className="group relative flex justify-center">{children}<SidebarTooltip>{tooltip}</SidebarTooltip></div>
}

function StatusDot({ healthy, compact = false }: { healthy: boolean | null; compact?: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border bg-card px-2 py-0.5 text-[11px] text-muted-foreground",
        compact && "size-8 justify-center p-0",
        healthy === true && "border-success-border bg-success-muted text-success-foreground",
        healthy === false && "border-destructive/40 bg-destructive/10 text-destructive",
      )}
    >
      <span
        className={cn(
          "size-1.5 rounded-full bg-muted-foreground",
          healthy === true && "bg-success",
          healthy === false && "bg-destructive",
        )}
      />
      {!compact && (healthy === null ? "检测中" : healthy ? "就绪" : "离线")}
    </span>
  )
}
