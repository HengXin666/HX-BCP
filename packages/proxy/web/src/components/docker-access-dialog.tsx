import { useCallback, useEffect, useRef, useState } from "react"
import { LoaderCircle, Undo2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import { dockerProxyURIs } from "@/lib/docker-endpoint"
import type { DockerAccessAssessment, DockerAccessReason, ListenerRecord } from "@/lib/types"

// The dialog never ends at a refusal. The server assesses the service
// (internal/dockeraccess) and returns either a dialable entry, a previewed fix
// the operator can confirm in one click, or the one prerequisite only the
// operator can provide. Copy buttons exist only for a verified-dialable entry.
// See .agents/notes/implemented/feature/2026-10-15-docker-access-entry.md.

const reasonText: Record<DockerAccessReason, string> = {
  direct: "",
  companion_entry: "",
  service_disabled: "该服务已停用。启用服务后再打开此窗口即可获取容器连接。",
  residential_channel: "",
  websocket_protocol: "VLESS / VMess / Trojan 走反向代理的 WebSocket，容器不能把它直接当作 HTTP_PROXY。",
  loopback_bind: "该服务只监听本机环回地址，容器里的 127.0.0.1 指向容器自己，连不到它。",
  missing_auth: "该服务没有账号认证，不能对容器网络开放。",
  no_share_export: "该服务没有可读取的认证信息。",
  no_reachable_address: "本机没有可供容器连接的地址。",
  no_docker_bridge: "本机未检测到 Docker 网桥（docker0 或 compose 的 br-*）。请先在本机启动 Docker 或创建 compose 网络，再重新打开此窗口。",
  bridge_scan_failed: "控制面读不到本机的网络接口列表（systemd 沙箱缺少 AF_NETLINK，或控制面运行在容器网络命名空间里），所以无法判断有没有 Docker 网桥。请检查控制面的部署配置，而不是 Docker 是否在运行。",
  entry_disabled: "该服务的 Docker 入口已停用。撤销后可重新创建。",
  entry_unreachable: "该服务的 Docker 入口已无法从容器访问（网桥被删除、地址变化或共享入口已关闭）。撤销后可按当前网络重新创建。",
  entry_pending: "Docker 网桥已就绪，但数据面尚未加载该入口（启动时网桥还不存在）。约 30 秒内会自动生效，之后重新打开此窗口即可复制连接。",
  no_free_port: "所选网桥上 17890–17989 的端口都已被占用。请释放其中一个端口，或切换到其他网桥后重试。",
  shared_entry_disabled: "共享入口已关闭，该服务当前不可连接。",
}

type ProxyURI = { scheme: string; uri: string }

export function DockerAccessDialog({ listener, subscriptionURL, onClose, onChanged }: {
  listener: ListenerRecord
  subscriptionURL?: string
  onClose: () => void
  onChanged?: () => void
}) {
  const [assessment, setAssessment] = useState<DockerAccessAssessment | null>(null)
  const [gateway, setGateway] = useState("")
  const [uris, setURIs] = useState<ProxyURI[]>([])
  const [status, setStatus] = useState("正在检查容器能否连接此服务…")
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  // Every load gets a sequence number; only the newest one may update the
  // dialog, so a slow answer for a previously selected bridge can never
  // overwrite (or auto-copy) the URI of the bridge picked after it.
  const sequence = useRef(0)

  const show = useCallback(async (result: DockerAccessAssessment, cancelled: () => boolean) => {
    setAssessment(result)
    setURIs([])
    if (result.state !== "ready" || !result.entry) {
      setStatus(result.state === "needs_entry" ? "" : reasonText[result.reason] || "")
      return
    }
    const content = await api.listenerShareContent(result.entry.share_path)
    if (cancelled()) return
    const built = dockerProxyURIs(result.entry, content, true)
    setURIs(built)
    const first = built[0]
    if (!first) return
    try {
      await navigator.clipboard.writeText(first.uri)
      if (!cancelled()) setStatus(`已复制 ${first.scheme} 认证代理 URI。`)
    } catch {
      if (!cancelled()) setStatus("浏览器未允许自动复制，请点击对应的复制按钮，或选中文本手动复制。")
    }
  }, [])

  const load = useCallback(async (selected: string, unmounted: () => boolean = () => false) => {
    const ticket = ++sequence.current
    const cancelled = () => unmounted() || ticket !== sequence.current
    setFailed(false)
    setStatus("正在检查容器能否连接此服务…")
    try {
      const result = await api.dockerAccess(listener.id, selected || undefined)
      if (cancelled()) return
      if (!selected && result.gateway) setGateway(result.gateway)
      await show(result, cancelled)
    } catch (error) {
      if (cancelled()) return
      setFailed(true)
      setStatus(error instanceof Error ? error.message : "读取 Docker 连接信息失败")
    }
  }, [listener.id, show])

  useEffect(() => {
    let cancelled = false
    void load("", () => cancelled)
    return () => { cancelled = true }
  }, [load])

  async function confirmPlan() {
    if (!assessment?.plan) return
    setBusy(true)
    try {
      const ticket = ++sequence.current
      const result = await api.applyDockerAccess(listener.id, { plan_id: assessment.plan.id, gateway: gateway || undefined, confirm: true })
      onChanged?.()
      // A bridge picked while the entry was being created owns the dialog
      // now; the late result must not overwrite its assessment.
      if (ticket === sequence.current) await show(result, () => ticket !== sequence.current)
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "创建 Docker 入口失败")
      await load(gateway)
    } finally { setBusy(false) }
  }

  async function revoke() {
    setBusy(true)
    try {
      await api.revokeDockerAccess(listener.id)
      onChanged?.()
      await load(gateway)
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "撤销 Docker 入口失败")
    } finally { setBusy(false) }
  }

  async function copy(value: string, label: string) {
    try { await navigator.clipboard.writeText(value); setStatus(`已复制${label}；其中含凭据，请勿公开。`) }
    catch { setStatus("复制未获授权，请选中文本后按 Ctrl+C / Command+C。") }
  }

  function changeGateway(value: string) {
    setGateway(value)
    void load(value)
  }

  const state = assessment?.state
  const bridges = assessment?.bridges ?? []
  const selectedBridge = bridges.find(bridge => bridge.address === (assessment?.gateway || gateway))
  const port = assessment?.entry?.port ?? assessment?.plan?.port
  const showGatewayPicker = bridges.length > 1 && (state === "needs_entry" || (state === "ready" && assessment?.entry?.host === assessment?.gateway))

  return <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
    <DialogContent>
      <DialogHeader>
        <DialogTitle>Docker 使用方式</DialogTitle>
        <DialogDescription>需要新入口时只绑定 Docker 网桥网关地址，不会新增 0.0.0.0 监听，也不会修改宿主防火墙。</DialogDescription>
      </DialogHeader>
      <div className="space-y-4 p-5 text-sm">
        {!assessment && !failed && <p role="status" className="flex items-center gap-2"><LoaderCircle className="size-4 animate-spin" />{status}</p>}
        {failed && <div className="space-y-2"><p role="status" className="text-destructive">{status}</p><Button variant="outline" onClick={() => void load(gateway)}>重新检查</Button></div>}

        {showGatewayPicker && <label className="block space-y-1 text-xs">
          <span className="text-muted-foreground">容器所在网络（网桥网关）</span>
          <Select value={assessment?.gateway || gateway} onValueChange={changeGateway}>
            <SelectTrigger className="h-8" aria-label="选择 Docker 网桥"><SelectValue /></SelectTrigger>
            <SelectContent>{bridges.map(bridge => <SelectItem key={bridge.address} value={bridge.address}>{bridge.interface} · {bridge.address}（{bridge.subnet}）</SelectItem>)}</SelectContent>
          </Select>
        </label>}

        {state === "ready" && <ReadyView assessment={assessment!} uris={uris} status={status} busy={busy} onCopy={copy} onRevoke={() => void revoke()} />}

        {state === "needs_entry" && assessment?.plan && <div className="space-y-3">
          <p>{reasonText[assessment.reason]}可以为它创建一个专供容器使用的入口，出口与本服务完全相同：</p>
          <ul className="list-disc space-y-1 pl-5 text-xs leading-5" aria-label="变更预览">
            <li>新建 Mixed 入口「{assessment.plan.name}」，同时支持 HTTP 与 SOCKS5。</li>
            {assessment.plan.placement === "shared"
              ? <li>复用现有共享 Mixed 入口 {assessment.plan.bind_address}:{assessment.plan.port}，按用户名分流，<strong>不新开端口</strong>。</li>
              : <li>仅监听 {selectedBridge?.interface ?? "网桥"} 网关 <span className="font-mono">{assessment.plan.bind_address}:{assessment.plan.port}</span>（新端口，非 0.0.0.0，只有该 Docker 网络与本机可达）。</li>}
            <li>出口沿用本服务的代理组 / 固定节点，修改本服务的节点策略会同时生效。</li>
            <li>自动生成随机用户名与密码；容器连接地址为 <span className="font-mono">{assessment.plan.dial_host}:{assessment.plan.port}</span>。</li>
            <li>创建后可在此窗口一键撤销。</li>
          </ul>
          <Button onClick={() => void confirmPlan()} disabled={busy}>{busy && <LoaderCircle className="animate-spin" />}确认创建 Docker 入口</Button>
          {status && <p role="status" className="text-xs text-destructive">{status}</p>}
        </div>}

        {state === "blocked" && <div className="space-y-2">
          <p role="status">{status}</p>
          {assessment?.revocable && <Button variant="outline" onClick={() => void revoke()} disabled={busy}><Undo2 />撤销 Docker 入口</Button>}
        </div>}

        {(state === "client_landing" || (assessment?.client_landing && state !== "ready")) && <ClientLanding listener={listener} subscriptionURL={subscriptionURL} primary={state === "client_landing"} onCopy={copy} />}

        {port !== undefined && state === "ready" && selectedBridge && assessment?.entry?.host === selectedBridge.address && <p className="text-xs leading-5 text-muted-foreground">
          若容器内连接超时，多半是宿主防火墙拦截了 {selectedBridge.subnet} → {port}。可由管理员按需放行（示例，不会自动执行）：<span className="mt-1 block break-all font-mono">sudo iptables -I INPUT -s {selectedBridge.subnet} -d {selectedBridge.address} -p tcp --dport {port} -j ACCEPT</span>
        </p>}
      </div>
    </DialogContent>
  </Dialog>
}

function ReadyView({ assessment, uris, status, busy, onCopy, onRevoke }: {
  assessment: DockerAccessAssessment; uris: ProxyURI[]; status: string; busy: boolean
  onCopy: (value: string, label: string) => void; onRevoke: () => void
}) {
  const entry = assessment.entry!
  return <div className="space-y-3">
    {entry.companion && <p className="text-xs text-muted-foreground">通过本服务的 Docker 入口连接（{entry.host}:{entry.port}），出口与本服务相同。</p>}
    <p role="status">{status}</p>
    {uris.map(item => {
      const label = item.scheme === "http" ? "HTTP_PROXY / HTTPS_PROXY" : "ALL_PROXY（SOCKS5，远端解析 DNS）"
      return <div key={item.scheme} className="space-y-1.5">
        <span className="block text-xs font-medium">{label}</span>
        <Textarea aria-label={label} readOnly value={item.uri} onFocus={event => event.target.select()} className="min-h-0 break-all font-mono text-xs" rows={2} />
        <Button size="sm" variant="outline" onClick={() => onCopy(item.uri, ` ${item.scheme} URI`)}>复制 {item.scheme === "http" ? "HTTP" : "SOCKS5h"} URI</Button>
      </div>
    })}
    <div className="space-y-1 text-xs leading-5 text-muted-foreground">
      <p>同一个 http:// URI 同时填入 HTTP_PROXY 与 HTTPS_PROXY（HTTPS 目标走 CONNECT，不要改成 https://）。只支持 SOCKS 的应用使用 socks5h://，由代理解析域名。</p>
      <p>容器内验证：curl --proxy "$HTTP_PROXY" https://example.com</p>
    </div>
    {assessment.revocable && <Button variant="outline" size="sm" onClick={onRevoke} disabled={busy}><Undo2 />撤销 Docker 入口</Button>}
  </div>
}

function ClientLanding({ listener, subscriptionURL, primary, onCopy }: {
  listener: ListenerRecord; subscriptionURL?: string; primary: boolean; onCopy: (value: string, label: string) => void
}) {
  const url = subscriptionURL || (listener.share_path ? api.listenerShareURL(listener.share_path, "clash") : "")
  const body = <div className="space-y-2 text-xs leading-5 text-muted-foreground">
    {url
      ? <><Textarea aria-label="Mihomo 订阅 URL" readOnly value={url} onFocus={event => event.target.select()} className="break-all font-mono text-xs" /><Button size="sm" variant="outline" onClick={() => onCopy(url, "订阅 URL")}>复制订阅 URL</Button></>
      : <p>该服务尚未发布订阅：请先在全局配置中设置公网域名。</p>}
    <p>1. 将订阅导入容器内或 sidecar 的 Mihomo，保存为 config.yaml；订阅 URL 是凭据，勿公开。</p>
    <p>2. 客户端启用 mixed-port: 7890。与 Mihomo 共享网络命名空间时用 http://127.0.0.1:7890；不同容器用 http://mihomo:7890，且仅在私有 Docker 网络中开启 allow-lan: true。</p>
    <p>3. 应用的 HTTP_PROXY / HTTPS_PROXY 指向该客户端入口。</p>
  </div>
  if (primary) return body
  return <details className="rounded-md border px-3 py-2 text-xs">
    <summary className="cursor-pointer select-none">或者：在容器里用 Mihomo 客户端落地订阅</summary>
    <div className="mt-2">{body}</div>
  </details>
}
