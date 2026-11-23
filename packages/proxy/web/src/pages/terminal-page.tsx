import { useCallback, useEffect, useRef, useState } from "react"
import { AlertTriangle, LoaderCircle, Maximize2, PanelLeft, Play, ShieldCheck, Square, TerminalSquare } from "lucide-react"
import { Terminal } from "@xterm/xterm"
import { FitAddon } from "@xterm/addon-fit"
import "@xterm/xterm/css/xterm.css"

import { ApiError, api, type TerminalStatus } from "@/lib/api"
import { DockerPage } from "@/components/terminal/docker-page"
import { FilePanel } from "@/components/terminal/file-panel"
import { HostMonitor } from "@/components/terminal/host-monitor"
import { OpsPage } from "@/components/terminal/ops-page"
import { Input } from "@/components/ui/input"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { createOsc7Scanner, quoteForShell } from "@/lib/terminal-cwd"
import { createInputFramer } from "@/lib/terminal-input"
import { subscribeTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"

type ConnectionState = "idle" | "connecting" | "connected" | "reconnecting" | "closed"
type TerminalTab = "shell" | "ops" | "docker"

// Reconnect backoff: 1s, 2s, 4s, 8s, 16s, then 30s capped, plus jitter. The
// terminal buffer survives reconnects (auto-reconnect never resets xterm), so
// scrollback and last command output remain visible while the link heals.
const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 30_000
// A pong must arrive at least this often while the socket looks open; browsers
// cannot send WebSocket ping frames, so the client pings and the server pongs.
// Missing the deadline for a full minute means the path is half-dead — force a
// close so the reconnect loop takes over instead of waiting for TCP to give up.
const PONG_TIMEOUT_MS = 60_000

export function TerminalPage({
  onNotice,
}: {
  onNotice: (message: string, tone?: "success" | "error") => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const keepAliveRef = useRef<number | null>(null)
  const reconnectTimerRef = useRef<number | null>(null)
  const reconnectAttemptRef = useRef(0)
  const lastPongRef = useRef(0)
  const cwdRef = useRef("/")
  // Rolling PTY-output buffer that recognizes the OSC 7 sequence the shell
  // integration emits after every prompt. It is a *read-only* signal: the page
  // never types a probe command into the session, so the shell history stays
  // free of lines the user did not write.
  const osc7Ref = useRef<ReturnType<typeof createOsc7Scanner> | null>(null)
  const inputDisposableRef = useRef<{ dispose: () => void } | null>(null)
  const [status, setStatus] = useState<TerminalStatus | null>(null)
  const [connection, setConnection] = useState<ConnectionState>("idle")
  const [twoFactorCode, setTwoFactorCode] = useState("")
  const [unlocking, setUnlocking] = useState(false)
  const [panelHidden, setPanelHidden] = useState(false)
  const [cwd, setCwd] = useState("/")
  const [tab, setTab] = useState<TerminalTab>("shell")
  // statusRef mirrors status for callbacks that outlive a render (reconnect
  // timers, keepalive) so they always check the latest 2FA state.
  const statusRef = useRef<TerminalStatus | null>(null)
  statusRef.current = status

  const updateCwd = useCallback((next: string) => {
    cwdRef.current = next
    setCwd(next)
    // Report the directory to the server so a reconnect or a later login
    // resumes here. The service validates and throttles persistence; the
    // message itself is tiny, so reporting on every confirmed change is fine.
    const socket = socketRef.current
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: "cwd", cwd: next }))
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    api
      .terminalStatus()
      .then((result) => {
        if (!cancelled) setStatus(result)
      })
      .catch((cause) => {
        if (!cancelled && cause instanceof ApiError) onNotice(cause.message, "error")
      })
    return () => {
      cancelled = true
    }
  }, [onNotice])

  const disconnect = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      window.clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
    reconnectAttemptRef.current = 0
    if (keepAliveRef.current !== null) {
      window.clearInterval(keepAliveRef.current)
      keepAliveRef.current = null
    }
    const socket = socketRef.current
    if (socket) {
      socket.close(1000, "user disconnect")
      // The connect() onclose guard ignores stale sockets (its whole purpose),
      // so a user-initiated disconnect must print the session-ended feedback
      // here instead of relying on the async close event.
      terminalRef.current?.write("\r\n\x1b[33m[会话已结束：user disconnect]\x1b[0m\r\n")
    }
    socketRef.current = null
    setConnection("closed")
  }, [])

  // Keep the 2FA/session state fresh while the page stays open. After the
  // 15-minute verification window lapses (e.g. the terminal is disconnected
  // but the page is still mounted), the UI must switch back to the 2FA unlock
  // prompt instead of failing to connect against stale status.
  useEffect(() => {
    const interval = window.setInterval(() => {
      api
        .terminalStatus()
        .then((result) => {
          setStatus(result)
          if (!result.two_factor_verified && socketRef.current?.readyState === WebSocket.OPEN) {
            // The server revokes an open socket on its next revalidation;
            // close it client-side so the unlock prompt is not shown while a
            // dead session lingers.
            disconnect()
          }
        })
        .catch(() => {})
    }, 30_000)
    return () => window.clearInterval(interval)
  }, [disconnect])

  async function unlockTerminal() {
    if (!/^\d{6}$/.test(twoFactorCode.trim())) {
      onNotice("请输入 6 位 2FA 验证码", "error")
      return
    }
    setUnlocking(true)
    try {
      await api.verifyTwoFactor(twoFactorCode.trim())
      setTwoFactorCode("")
      setStatus(await api.terminalStatus())
      onNotice("终端已解锁，连接期间 2FA 验证会自动续期")
    } catch (cause) {
      onNotice(cause instanceof Error ? cause.message : "2FA 验证失败", "error")
    } finally {
      setUnlocking(false)
    }
  }

  // Keep the session synced with server PTY clock even when the browser tab is
  // hidden: switching pages must NOT disconnect the user. A lightweight ping
  // frame every 20s keeps NAT/proxy keepalive alive in both directions (the
  // server replies pong) and lets the client detect a half-dead connection via
  // the pong deadline instead of waiting for TCP to give up.
  const startKeepalive = useCallback((socket: WebSocket) => {
    if (keepAliveRef.current !== null) window.clearInterval(keepAliveRef.current)
    lastPongRef.current = Date.now()
    keepAliveRef.current = window.setInterval(() => {
      if (socket.readyState !== WebSocket.OPEN) return
      socket.send(JSON.stringify({ type: "ping" }))
      if (Date.now() - lastPongRef.current > PONG_TIMEOUT_MS) {
        // No pong for a full minute on a connection we believe is open: the
        // path is half-dead. Force the close so the reconnect loop takes over.
        socket.close(4000, "pong timeout")
      }
    }, 20_000)
  }, [])

  // When returning to this tab, the terminal may have buffered output while we
  // were away. We do NOT reconnect — the socket is still open — but we
  // re-fit so the visible terminal matches the viewport again.
  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState === "visible") {
        fitRef.current?.fit()
        if (terminalRef.current) terminalRef.current.focus()
      }
    }
    document.addEventListener("visibilitychange", onVisible)
    return () => document.removeEventListener("visibilitychange", onVisible)
  }, [])

  // Debounced refit on container resize so the PTY window tracks layout changes
  // caused by expanding/collapsing the side panel.
  useEffect(() => {
    if (!containerRef.current) return
    const observer = new ResizeObserver(() => {
      fitRef.current?.fit()
      const term = terminalRef.current
      const socket = socketRef.current
      if (term && socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }))
      }
    })
    observer.observe(containerRef.current)
    return () => observer.disconnect()
  }, [])

  // connectRef lets timers created in an older render invoke the latest
  // connect without recreating the timer whenever connect's identity changes.
  const connectRef = useRef<((options?: { preserve?: boolean }) => void) | null>(null)

  // Schedule an automatic reconnect with exponential backoff + jitter. The
  // 2FA state is re-checked when the timer fires so a lapsed verification
  // simply stops the loop and lets the unlock prompt take over.
  const scheduleReconnect = useCallback(() => {
    if (reconnectTimerRef.current !== null) return
    const attempt = reconnectAttemptRef.current
    reconnectAttemptRef.current = attempt + 1
    const delay = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** Math.min(attempt, 5)) + Math.random() * 400
    setConnection("reconnecting")
    reconnectTimerRef.current = window.setTimeout(() => {
      reconnectTimerRef.current = null
      if (!statusRef.current?.two_factor_verified) return
      connectRef.current?.({ preserve: true })
    }, delay)
  }, [])

  const connect = useCallback((options?: { preserve?: boolean }) => {
    if (!containerRef.current || socketRef.current) return
    // A manual connect cancels any pending auto-reconnect (the user took over).
    if (reconnectTimerRef.current !== null) {
      window.clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
    if (!status?.two_factor_verified) {
      // The cached status may be stale if the 2FA window lapsed while the page
      // stayed open; refresh once so the unlock prompt appears immediately.
      api.terminalStatus().then((result) => setStatus(result)).catch(() => {})
      return
    }
    setConnection("connecting")

    if (!terminalRef.current) {
      const terminal = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
        theme: terminalTheme(),
      })
      const fit = new FitAddon()
      terminal.loadAddon(fit)
      terminal.open(containerRef.current)
      terminalRef.current = terminal
      fitRef.current = fit
      subscribeTheme(() => { terminalRef.current && (terminalRef.current.options.theme = terminalTheme()) })
    }
    const terminal = terminalRef.current
    const fit = fitRef.current
    // Auto-reconnects preserve the xterm buffer so scrollback and the last
    // command output survive the network blip; manual connects start fresh.
    const isReconnect = options?.preserve === true
    // Reconnecting must not stack onData listeners: each leaked listener
    // re-sends every keystroke, so a reconnect used to echo `d` as `dd`.
    inputDisposableRef.current?.dispose()
    inputDisposableRef.current = null
    // Keystrokes are batched into short frames and every frame goes through
    // chunkTerminalInput: a paste is one big onData callback, and a frame above
    // the server's read limit makes coder/websocket close the whole socket with
    // StatusMessageTooBig — which the page could only surface as an unstable
    // terminal. See web/src/lib/terminal-input.ts.
    const inputFramer = createInputFramer({
      send: (frame) => {
        const socket = socketRef.current
        if (socket && socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "input", data: frame }))
        }
      },
      schedule: (callback, delayMs) => window.setTimeout(callback, delayMs),
      cancel: (handle) => window.clearTimeout(handle as number),
    })
    if (!isReconnect) {
      terminal.reset()
    }
    fit?.fit()

    const socket = new WebSocket(api.terminalSocketURL())
    socket.binaryType = "arraybuffer"
    socketRef.current = socket
    let opened = false

    // A socket that has been superseded by a newer connection must never
    // mutate shared state. Its close/error events can fire AFTER a reconnect
    // has taken over (e.g. 断开 → 连接 quickly, or the 2FA poll forcing a
    // disconnect right before the user reconnects); without this guard the
    // stale onclose nulls socketRef.current, clears the new socket's
    // keepalive, flips the UI back to "closed" and silently drops every
    // keystroke until the page is reloaded.
    const isCurrentSocket = () => socketRef.current === socket

    // Streaming decoder lets us peek at PTY output for cwd tracking without
    // touching the raw bytes written to the terminal.
    const decoder = new TextDecoder()

    const sendResize = () => {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }))
      }
    }

    socket.onopen = () => {
      // A stale socket that was already replaced must not drive the UI.
      if (!isCurrentSocket()) return
      opened = true
      reconnectAttemptRef.current = 0
      setConnection("connected")
      fit?.fit()
      sendResize()
      terminal.focus()
      startKeepalive(socket)
      // The directory arrives from the server (kernel cwd of the shell) and
      // from the shell's own OSC 7 report; nothing is written to the PTY.
      if (isReconnect) {
        terminal.write("\r\n\x1b[32m[已重新连接]\x1b[0m\r\n")
      }
    }
    socket.onmessage = (event) => {
      // A stale socket must not write late output into the new session.
      if (!isCurrentSocket()) return
      if (event.data instanceof ArrayBuffer) {
        const bytes = new Uint8Array(event.data)
        terminal.write(bytes)
        // The shell integration emits OSC 7 after every prompt. Parsing it
        // here is what makes the file panel follow `cd` without the page ever
        // typing a probe command into the session.
        const text = decoder.decode(bytes, { stream: true })
        if (text.includes("\x1b]7;")) {
          const directory = osc7Ref.current?.feed(text) ?? null
          if (directory && directory !== cwdRef.current) updateCwd(directory)
        }
        return
      }
      if (typeof event.data === "string") {
        // Control frames from the server: "cwd" carries the kernel-reported
        // working directory (authoritative and immune to a custom rc that
        // rewrites PROMPT_COMMAND), "pong" refreshes the keepalive deadline.
        try {
          const message = JSON.parse(event.data) as { type?: string; cwd?: string }
          if (message.type === "pong") {
            lastPongRef.current = Date.now()
          } else if (message.type === "cwd" && typeof message.cwd === "string" && message.cwd !== "") {
            if (message.cwd !== cwdRef.current) updateCwd(message.cwd)
          }
        } catch {
          // Ignore non-JSON frames.
        }
      }
    }
    socket.onclose = (event) => {
      // Only the current socket may tear the session down: a stale socket's
      // close event racing a reconnect must not null out the new socket's
      // reference, clear its keepalive or flip the UI back to "closed".
      if (!isCurrentSocket()) return
      if (keepAliveRef.current !== null) {
        window.clearInterval(keepAliveRef.current)
        keepAliveRef.current = null
      }
      socketRef.current = null
      // Drop any half-batched keystrokes: the socket is gone, so flushing them
      // would only re-send into a closed connection on the next render.
      inputFramer.reset()
      osc7Ref.current?.reset()
      // Refresh 2FA/session state: when the server revoked the session (2FA
      // window lapsed, logout-all, ...) the UI must switch back to the unlock
      // prompt instead of letting the user retry against stale state.
      api.terminalStatus().then((result) => setStatus(result)).catch(() => {})
      if (event.code === 1000) {
        // Clean user-initiated close (disconnect/unmount already handled the
        // feedback); never auto-reconnect.
        setConnection("closed")
        return
      }
      if (event.code === 1008) {
        // Server revoked the session: stop reconnecting and prompt for a new
        // 2FA verification.
        setConnection("closed")
        terminal.write(`\r\n\x1b[33m[会话已结束${event.reason ? `：${event.reason}` : ""}]\x1b[0m\r\n`)
        onNotice("会话验证已失效，请重新验证后连接", "error")
        return
      }
      // Anything else — abnormal drop, server restart, pong timeout — is a
      // transient network failure on a lossy link: keep the scrollback and
      // reconnect with exponential backoff.
      setConnection("reconnecting")
      terminal.write("\r\n\x1b[33m[网络中断，正在自动重连…]\x1b[0m\r\n")
      if (reconnectAttemptRef.current === 0 && opened) {
        onNotice("网络中断，正在自动重连", "error")
      }
      scheduleReconnect()
    }
    socket.onerror = () => {
      // A stale socket's error must not surface a notice for the new session.
      if (!isCurrentSocket()) return
      // A failed WebSocket handshake (e.g. stale 2FA state) surfaces here; the
      // status refresh switches the page to the 2FA unlock prompt.
      api.terminalStatus().then((result) => setStatus(result)).catch(() => {})
      onNotice("终端连接失败，请确认已登录并完成 2FA 解锁", "error")
    }

    osc7Ref.current = createOsc7Scanner()
    const inputDisposable = terminal.onData((data) => {
      if (!socketRef.current || socketRef.current.readyState !== WebSocket.OPEN) return
      inputFramer.push(data)
    })

    inputDisposableRef.current = inputDisposable
  }, [onNotice, scheduleReconnect, startKeepalive, status?.two_factor_verified, updateCwd])
  connectRef.current = connect

  // Navigate from the file panel: update the shared cwd and drive the shell.
  //
  // The command carries a leading space on purpose. The shell integration sets
  // HISTCONTROL=ignorespace / HIST_IGNORE_SPACE, so panel navigation never
  // reaches the shell history: the user only sees the directories they typed,
  // exactly as if the panel were a native file browser. Bash and zsh both strip
  // the leading whitespace before executing, so the command itself is unchanged.
  const handlePanelPath = useCallback((next: string) => {
    // Ignore duplicate navigations (e.g. the second click of a double-click);
    // the shell already is where the panel wants to go.
    if (next === cwdRef.current) return
    updateCwd(next)
    const socket = socketRef.current
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: "input", data: ` cd ${quoteForShell(next)}\n` }))
    }
  }, [updateCwd])

  useEffect(() => {
    return () => {
      if (reconnectTimerRef.current !== null) {
        window.clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
      if (keepAliveRef.current !== null) window.clearInterval(keepAliveRef.current)
      socketRef.current?.close(1000, "unmount")
      socketRef.current = null
      inputDisposableRef.current?.dispose()
      inputDisposableRef.current = null
      terminalRef.current?.dispose()
      terminalRef.current = null
    }
  }, [])

  if (status && !status.two_factor_verified) {
    return (
      // 与已解锁分支同一套高度约定: 根占满可用高度并自带 padding(因为 App 的 main
      // 在终端页不再提供 padding), 子页用 flex-1 拿剩余空间并自己滚动。
      <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto px-4 py-5 sm:px-6 lg:px-8 lg:py-7">
        <PageHeader />
        <Tabs value={tab} onValueChange={(value) => setTab(value as TerminalTab)}>
          <TabsList>
            <TabsTrigger value="shell">终端</TabsTrigger>
            <TabsTrigger value="ops">运维</TabsTrigger>
            <TabsTrigger value="docker">Docker</TabsTrigger>
          </TabsList>
        </Tabs>
        {tab === "ops" ? (
          <OpsPage onNotice={onNotice} />
        ) : tab === "docker" ? (
          <DockerPage onNotice={onNotice} />
        ) : (
          <section className="rounded-md border bg-card p-4">
            <div className="flex items-center gap-2 text-sm font-medium"><ShieldCheck className="size-4 text-primary" />验证 2FA 后解锁终端</div>
            <p className="mt-2 text-xs leading-5 text-muted-foreground">输入验证器当前显示的 6 位验证码。验证成功后，当前登录会话可在 {Math.round(status.two_factor_verification_ttl_seconds / 60)} 分钟内建立终端连接；连接期间验证会自动续期，不会因验证超时中断。</p>
            <div className="mt-4 flex items-end gap-2"><label className="block min-w-0 flex-1 text-xs font-medium">一次性验证码<Input aria-label="终端 2FA 验证码" inputMode="numeric" maxLength={6} value={twoFactorCode} onChange={(event) => setTwoFactorCode(event.target.value.replace(/\D/g, "").slice(0, 6))} className="mt-1 font-mono tracking-[0.25em]" /></label><button type="button" onClick={() => void unlockTerminal()} disabled={unlocking} className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-60">{unlocking ? <LoaderCircle className="size-3.5 animate-spin" /> : <ShieldCheck className="size-3.5" />}解锁</button></div>
          </section>
        )}
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 px-4 py-5 sm:px-6 lg:px-8 lg:py-7">
      <div className="flex shrink-0 items-start justify-between gap-3">
        <PageHeader />
        <button
          type="button"
          onClick={() => setPanelHidden((v) => !v)}
          className="inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs hover:bg-muted"
        >
          <PanelLeft className="size-3.5" />
          {panelHidden ? "展开侧栏" : "收起侧栏"}
        </button>
      </div>

      <Tabs value={tab} onValueChange={(value) => setTab(value as TerminalTab)}>
        <TabsList>
          <TabsTrigger value="shell">终端</TabsTrigger>
          <TabsTrigger value="ops">运维</TabsTrigger>
          <TabsTrigger value="docker">Docker</TabsTrigger>
        </TabsList>
      </Tabs>

      {tab === "ops" ? (
        <OpsPage onNotice={onNotice} />
      ) : tab === "docker" ? (
        <DockerPage onNotice={onNotice} />
      ) : (
        <>
          <div className="flex items-start gap-2.5 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2.5 text-xs leading-5 text-destructive">
            <AlertTriangle className="mt-0.5 size-4 shrink-0" />
            <div>
              <span className="font-medium">高风险操作提示：</span>
              此终端以控制面进程用户身份在服务器上执行真实 Shell 命令。删除文件、修改系统配置、停止服务等操作立即生效且不可撤销。会话无空闲与寿命上限，全部会话都会写入审计日志。
            </div>
          </div>

          <div className={cn("grid min-h-0 flex-1 gap-3", panelHidden ? "grid-cols-1" : "grid-cols-1 lg:grid-cols-[1fr_320px]")}>
            <section className="flex min-h-0 flex-col overflow-hidden rounded-md border bg-card">
              <header className="flex items-center justify-between gap-2 border-b bg-muted/60 px-3 py-2">
                <div className="flex items-center gap-2 text-sm font-medium">
                  <TerminalSquare className="size-4 text-muted-foreground" />
                  服务器终端
                  {status?.privileged && <span className="rounded-full border border-warning-border bg-warning-muted px-2 py-0.5 text-[11px] text-warning-foreground">root PTY</span>}
                  <ConnectionBadge state={connection} />
                </div>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => fitRef.current?.fit()}
                    className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs hover:bg-card"
                    title="重新适配窗口尺寸"
                  >
                    <Maximize2 className="size-3.5" />
                  </button>
                  {connection === "connected" ? (
                    <button type="button" onClick={disconnect} className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1 text-xs hover:bg-card">
                      <Square className="size-3.5" /> 断开
                    </button>
                  ) : (
                    <button
                      type="button"
                      onClick={() => connect()}
                      disabled={connection === "connecting" || connection === "reconnecting" || !status?.two_factor_verified}
                      className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
                    >
                      <Play className="size-3.5" /> {connection === "connecting" ? "连接中…" : connection === "reconnecting" ? "重连中…" : "连接"}
                    </button>
                  )}
                </div>
              </header>
              <div className="relative min-h-0 flex-1 bg-background">
                {/* The mount point must not carry padding. FitAddon derives the row
                    count from this element's computed height and subtracts only the
                    .xterm child's own padding — which xterm.css never sets. Tailwind
                    preflight makes box-sizing border-box globally, so a padded
                    container reports a height that already includes its padding and
                    the subtraction is skipped: the grid renders one row taller than
                    the space available and the last row is clipped by the card's
                    overflow-hidden. Inset gives the same visual breathing room as
                    padding without corrupting the measurement. */}
                <div ref={containerRef} data-terminal-surface className="absolute inset-2" />
              </div>
            </section>

            {!panelHidden && (
              <aside className="hidden min-h-0 flex-col gap-3 rounded-md border bg-card lg:flex">
                {connection === "connected" || connection === "reconnecting" ? (
                  <>
                    <div className="overflow-auto p-2">
                      <HostMonitor enabled={connection === "connected"} />
                    </div>
                    <div className="mx-2 border-t" />
                    <div className="min-h-0 flex-1">
                      <FilePanel path={cwd} connected={connection === "connected"} onPathChange={handlePanelPath} onNotice={onNotice} />
                    </div>
                  </>
                ) : (
                  <div className="flex min-h-40 flex-1 flex-col items-center justify-center gap-1.5 p-4 text-center text-xs text-muted-foreground">
                    <TerminalSquare className="size-5 opacity-60" />
                    <div className="font-medium">连接后查看服务器数据</div>
                    <div className="text-[11px] opacity-70">系统监控与文件管理将在终端连接后显示</div>
                  </div>
                )}
              </aside>
            )}
          </div>
        </>
      )}
    </div>
  )
}

function PageHeader() {
  return (
    <div>
      <h1 className="text-lg font-semibold">终端</h1>
      <p className="mt-0.5 text-sm text-muted-foreground">
        浏览器内服务器终端，切换页面不断连，集成系统监控与文件管理；文件目录与 Shell 实时同步。
      </p>
    </div>
  )
}

function ConnectionBadge({ state }: { state: ConnectionState }) {
  const meta: Record<ConnectionState, { label: string; className: string }> = {
    idle: { label: "未连接", className: "border-border bg-card text-muted-foreground" },
    connecting: { label: "连接中", className: "border-warning-border bg-warning-muted text-warning-foreground" },
    connected: { label: "已连接", className: "border-success-border bg-success-muted text-success-foreground" },
    reconnecting: { label: "重连中", className: "border-warning-border bg-warning-muted text-warning-foreground" },
    closed: { label: "已断开", className: "border-border bg-muted/60 text-muted-foreground" },
  }
  return (
    <span className={`inline-flex rounded-full border px-2 py-0.5 text-[11px] ${meta[state].className}`}>
      {meta[state].label}
    </span>
  )
}

function terminalTheme() {
  const styles = getComputedStyle(document.documentElement)
  const color = (name: string) => styles.getPropertyValue(name).trim()
  return {
    background: color("--background"),
    foreground: color("--foreground"),
    cursor: color("--foreground"),
    selectionBackground: color("--accent"),
  }
}
