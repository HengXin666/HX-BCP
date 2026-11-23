import { useMemo, useState } from "react"
import { Check, LoaderCircle, Save } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api"
import { strategyChoices } from "@/lib/proxy-groups"
import { shapeForSelection, shapeMeta, type ServiceShape } from "@/lib/service-shape"
import type { ListenerKind, ListenerRecord, NodeRecord, ProxyGroup, ProxyGroupStrategy, SharedInboundSettings, Subscription } from "@/lib/types"

type Notice = (message: string, tone?: "success" | "error") => void

export function EditProxyServiceForm({ listener, sharedInbound, group, nodes, subscriptions, onSaved, onCancel, onNotice }: { listener: ListenerRecord; sharedInbound: SharedInboundSettings | null; group: ProxyGroup; nodes: NodeRecord[]; subscriptions: Subscription[]; onSaved: () => Promise<void>; onCancel: () => void; onNotice: Notice }) {
  // A pinned service is edited as a pinned service. Its group is a schema
  // anchor shared with every other pinned service, so editing "the group" here
  // would edit someone else's policy — and the strategy/limit/regions controls
  // describe a selection this service does not perform.
  const pinned = Boolean(listener.node_id)
  const currentPin = listener.node_id ?? ""
  const [pin, setPin] = useState(currentPin)
  const initialMode = pinned ? "manual" : group.source_spec.include_direct ? "direct" : group.source_spec.subscription_ids?.length ? "rule" : "manual"
  const [name, setName] = useState(group.name)
  const [mode, setMode] = useState<"direct" | "rule" | "manual">(initialMode)
  const [strategy, setStrategy] = useState<ProxyGroupStrategy>(group.strategy)
  const [subscriptionIDs, setSubscriptionIDs] = useState(group.source_spec.subscription_ids ?? [])
  const [nodeIDs, setNodeIDs] = useState(group.source_spec.node_ids)
  const [limit, setLimit] = useState(group.source_spec.limit ?? 0)
  const [maxLatency, setMaxLatency] = useState(group.source_spec.max_latency_ms ?? 2000)
  const [kind, setKind] = useState<ListenerKind>(listener.kind)
  const [bindAddress, setBindAddress] = useState(listener.bind_address)
  const [enabled, setEnabled] = useState(listener.enabled)
  const [publicHost, setPublicHost] = useState(listener.public_endpoint?.host ?? "")
  const [publicTLS, setPublicTLS] = useState(listener.public_endpoint?.tls ?? false)
  const [wsPath, setWSPath] = useState(listener.transport?.ws_path ?? "/__hx-proxy__/hx-pxy")
  const [replaceAuth, setReplaceAuth] = useState(false)
  const [clearAuth, setClearAuth] = useState(false)
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [saving, setSaving] = useState(false)
  const advanced = ["vless", "vmess", "trojan"].includes(kind)
  // See .agents/notes/implemented/bug-fix/2026-10-15-fixed-ingress-port-controls.md.
  const shared = sharedInbound?.mode === "shared"
  const port = shared ? (advanced ? sharedInbound.ws_port || 7891 : sharedInbound.mixed_port || 7890) : listener.port
  const publicPort = shared ? (advanced ? sharedInbound.ws_public_port || 443 : sharedInbound.mixed_public_port || port) : listener.public_endpoint?.port || port
  const boundAddress = advanced ? "127.0.0.1" : shared ? sharedInbound.mixed_bind_address || "0.0.0.0" : bindAddress.trim()
  const nonLoopback = boundAddress !== "" && !["127.0.0.1", "::1", "localhost"].includes(boundAddress)
  const authConfigured = (listener.auth_configured && !clearAuth) || replaceAuth
  // A pinned service always has a valid source: the pin *is* the source. It
  // used to fall through to the multi-node branch, so the form refused to save
  // until the operator picked nodes that the update then ignored.
  // 组是「在一批节点里选一个」, 不是指向单个节点的句柄。历史上已经存在的单节点组
  // 仍然要能改名/停用/换策略 (否则连收敛都做不到), 所以「成员没变」放行; 把一个多
  // 节点组缩成一个才是要被拦下的动作。服务端有同样的判据, 这里只是提前说清楚。
  const legacySingleNode = (group.source_spec.node_ids?.length ?? 0) === 1
    && nodeIDs.length === 1 && nodeIDs[0] === group.source_spec.node_ids[0]
  const validSource = pinned ? pin !== ""
    : mode === "direct" || (mode === "rule" ? subscriptionIDs.length > 0 : nodeIDs.length >= 2 || legacySingleNode)
  const valid = name.trim() && port > 0 && port < 65536 && validSource && (!advanced || (publicHost.trim() && wsPath.startsWith("/"))) && (!replaceAuth || (username.trim() && password)) && (!nonLoopback || authConfigured)

  // 形态与可选项从同一张表取, 与创建表单和列表行同源。见 lib/service-shape.ts。
  const editShape: ServiceShape = pinned ? "pinned" : shapeForSelection(subscriptionIDs, nodeIDs, mode === "direct", subscriptions)
  const editKnobs = shapeMeta[editShape].knobs
  const serviceShapeLabel = shapeMeta[editShape].label
  const serviceShapeDetail = shapeMeta[editShape].detail
  const selectedSummary = useMemo(() => pinned ? `钉死节点：${nodes.find((item) => item.id === currentPin)?.display_name ?? "节点已不在库存"}` : mode === "direct" ? "当前服务器 DIRECT" : mode === "rule" ? `${subscriptionIDs.length} 个订阅，Top ${editKnobs.limit ? (limit || "全部") : "全部"}` : `${nodeIDs.length} 个固定节点`, [currentPin, editKnobs.limit, limit, mode, nodeIDs.length, nodes, pinned, subscriptionIDs.length])

  async function save() {
    setSaving(true)
    try {
      await api.updateProxyService({
        group_id: group.id,
        group_version: group.version,
        name: name.trim(),
        strategy: pinned || mode === "direct" ? "manual" : strategy,
        empty_behavior: mode === "direct" ? "direct" : "fail-closed",
        // A pinned service carries no group policy: its group is a shared
        // anchor, so writing this form's group fields would overwrite another
        // service's selection. Only the pin itself is sent.
        source_spec: pinned ? group.source_spec : (mode === "direct" ? { node_ids: [], include_direct: true } : mode === "rule" ? { ...group.source_spec, node_ids: [], subscription_ids: subscriptionIDs, max_latency_ms: maxLatency, limit: limit || undefined, sort_by: "latency", include_direct: false } : { node_ids: nodeIDs, include_direct: false }),
        enabled: group.enabled,
        fallback_target_id: group.fallback_target_id,
        // Carried through unchanged: this form does not edit the egress chain, and
        // omitting it would silently drop the chain on an unrelated save.
        dialer_proxy_group_id: group.dialer_proxy_group_id,
        listener_id: listener.id,
        listener_version: listener.version,
        listener: {
          name: `${name.trim()} 入口`,
          kind,
          bind_address: boundAddress,
          port,
          auth: replaceAuth ? { username: username.trim(), password } : undefined,
          clear_auth: clearAuth,
          transport: advanced ? { type: "ws", ws_path: wsPath.trim() } : undefined,
          public_endpoint: publicHost.trim() ? { host: publicHost.trim(), port: publicPort, tls: advanced || publicTLS } : undefined,
          enabled,
          // Omitted means "keep the stored pin"; an empty string would mean
          // "clear it", and the two intentions must not share a representation.
          node_id: pinned && pin !== currentPin ? pin : undefined,
        },
      })
      await onSaved()
      onNotice("代理服务已校验、保存并重新应用")
    } catch (error) {
      onNotice(error instanceof Error ? error.message : "保存代理服务失败", "error")
      // A failed composite save bumps the group/listener versions during the
      // rollback; reload so the form is rebuilt with current versions and a
      // retry does not hit a stale optimistic-lock conflict.
      await onSaved()
    } finally { setSaving(false) }
  }

  return <div className="space-y-4 p-4">
    <div className="flex flex-wrap items-center justify-between gap-2"><div><div className="text-sm font-semibold">实时编辑服务</div><div className="mt-0.5 text-[11px] text-muted-foreground">{selectedSummary}</div></div><div className="flex gap-2"><Button variant="outline" onClick={onCancel} disabled={saving}>取消</Button><Button onClick={() => void save()} disabled={saving || !valid}>{saving ? <LoaderCircle className="animate-spin" /> : <Save />}{saving ? "正在校验并应用" : "保存并应用"}</Button></div></div>
    <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/60 px-3 py-2 text-[11px]">
      <span className="font-medium">{serviceShapeLabel}</span>
      <span className="text-muted-foreground">{serviceShapeDetail}</span>
    </div>
    <div className="grid gap-3 md:grid-cols-3">
      <Field label="服务名称"><Input value={name} onChange={(event) => setName(event.target.value)} /></Field>
      {pinned ? <>
        <Field label="钉住的节点">
          <Select value={pin} onValueChange={setPin}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{nodes.map((item) => <SelectItem key={item.id} value={item.id}>{item.display_name}</SelectItem>)}</SelectContent></Select>
        </Field>
        {/* 去掉"出站策略"这一格: 钉节点的服务不做选择, 原来那格显示的 strategyMeta 是
            锚组的策略, 与这个服务的实际出口无关。 */}
      </> : <>
        <Field label="节点来源"><Select value={mode} onValueChange={(value) => setMode(value as typeof mode)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="direct">服务器直连</SelectItem><SelectItem value="rule">跨订阅规则</SelectItem><SelectItem value="manual">固定节点</SelectItem></SelectContent></Select></Field>
        <Field label="出站策略"><Select value={strategy} onValueChange={(value) => setStrategy(value as ProxyGroupStrategy)} disabled={mode === "direct"}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{strategyChoicesForShape(editShape).map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select></Field>
      </>}
    </div>
    {pinned && <p className="text-[11px] leading-4 text-muted-foreground">该服务把出口钉死在这一个节点上，不参与任何组内选择；改名不会改变出口。清空节点会把它交回代理组选择。</p>}
    {!pinned && mode === "rule" && <div className="grid gap-3 md:grid-cols-[1fr_120px_140px]"><CheckGrid title="订阅" items={subscriptions.map((item) => ({ id: item.id, label: item.name }))} selected={subscriptionIDs} onChange={setSubscriptionIDs} />{editKnobs.limit && <Field label="最多节点"><Input type="number" min={0} max={500} value={limit} onChange={(event) => setLimit(Number(event.target.value))} /></Field>}{editKnobs.maxLatency && <Field label="最大延迟 ms"><Input type="number" min={0} max={60000} value={maxLatency} onChange={(event) => setMaxLatency(Number(event.target.value))} /></Field>}</div>}
    {!pinned && mode === "manual" && <>
      <CheckGrid title="固定节点" items={nodes.map((item) => ({ id: item.id, label: `${item.display_name} · ${item.last_latency_ms == null ? "未测试" : `${item.last_latency_ms}ms`}` }))} selected={nodeIDs} onChange={setNodeIDs} />
      {nodeIDs.length < 2 && !legacySingleNode && <p className="text-[11px] leading-4 text-muted-foreground">代理组至少要选 2 个节点。只用一个节点请别建组：改用上面的「钉死节点」（listener.node_id），不需要代理组；也不要为每个节点各建一个组。</p>}
      {legacySingleNode && <p className="text-[11px] leading-4 text-muted-foreground">这是一个历史遗留的单节点组，现在仍可改名或停用，但不能再把别的组缩成一个节点。要固定单个出口请用「钉死节点」。</p>}
    </>}
    <div className="border-t pt-4"><div className="mb-3 text-xs font-semibold">入口配置</div><div className="grid gap-3 md:grid-cols-4"><Field label="协议"><Select value={kind} onValueChange={(value) => setKind(value as ListenerKind)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{["mixed", "http", "socks", "vless", "vmess", "trojan"].map((value) => <SelectItem key={value} value={value}>{value.toUpperCase()}{["vless", "vmess", "trojan"].includes(value) ? " WS" : ""}</SelectItem>)}</SelectContent></Select></Field><Field label="绑定 IP"><Input value={boundAddress} onChange={(event) => setBindAddress(event.target.value)} disabled={advanced || shared} />{nonLoopback && !authConfigured && <span className="block text-xs text-destructive">非环回监听地址（如 0.0.0.0）必须设置用户名/密码认证</span>}{nonLoopback && authConfigured && <span className="block text-xs text-muted-foreground">非环回地址监听 0.0.0.0，内网/Docker 容器可直接访问，公网由防火墙保护</span>}</Field><Field label="端口"><output className="block py-2 font-mono text-xs">{port}（固定）</output></Field><label className="flex h-8 items-center gap-2 self-end rounded-md border bg-card px-3 text-xs"><Checkbox checked={enabled} onCheckedChange={(value) => setEnabled(value === true)} />启用入口</label></div></div>
    {advanced && <div className="grid gap-3 md:grid-cols-2"><Field label="Cloudflare / 雷池域名"><Input value={publicHost} onChange={(event) => setPublicHost(event.target.value)} /></Field><Field label="WebSocket Path（固定前缀 /__hx-proxy__/）"><Input value={wsPath} onChange={(event) => setWSPath(event.target.value)} /></Field></div>}
    {!advanced && <div className="grid gap-3 md:grid-cols-2"><Field label="公网主机名 / IP（可选）"><Input value={publicHost} onChange={(event) => setPublicHost(event.target.value)} placeholder="VPS 公网地址" /></Field><Field label="公网端口"><output className="block py-2 font-mono text-xs">{publicPort}（固定）</output></Field><label className="flex items-center gap-2 rounded-md border bg-card px-3 py-2 text-xs md:col-span-2"><Checkbox checked={publicTLS} onCheckedChange={(value) => setPublicTLS(value === true)} />公网 HTTP 端点使用 TLS</label></div>}
    <div className="grid gap-3 md:grid-cols-2"><label className="flex items-center gap-2 rounded-md border bg-card px-3 py-2 text-xs"><Checkbox checked={replaceAuth} onCheckedChange={(value) => { setReplaceAuth(value === true); if (value) setClearAuth(false) }} />设置新凭据</label><label className="flex items-center gap-2 rounded-md border bg-card px-3 py-2 text-xs"><Checkbox checked={clearAuth} onCheckedChange={(value) => { setClearAuth(value === true); if (value) setReplaceAuth(false) }} disabled={advanced} />清除现有认证</label></div>
    {replaceAuth && <div className="grid gap-3 md:grid-cols-2"><Field label={advanced ? "用户备注" : "用户名"}><Input value={username} onChange={(event) => setUsername(event.target.value)} /></Field><Field label={kind === "vless" || kind === "vmess" ? "UUID" : "密码"}><Input type="password" value={password} onChange={(event) => setPassword(event.target.value)} /></Field></div>}
  </div>
}

/** 形态只列出它真正拥有的策略: 池里的成员可能下一次请求就没了, 不能承诺会话粘滞。 */
function strategyChoicesForShape(shape: ServiceShape) {
  const allowed = shapeMeta[shape].knobs.strategies
  return strategyChoices.filter((item) => allowed.includes(item.value))
}

function Field({ label, children }: { label: string; children: React.ReactNode }) { return <label className="block space-y-1.5"><span className="text-xs font-medium">{label}</span>{children}</label> }
function CheckGrid({ title, items, selected, onChange }: { title: string; items: Array<{ id: string; label: string }>; selected: string[]; onChange: (ids: string[]) => void }) { return <div><div className="mb-1.5 text-xs font-medium">{title}</div><div className="grid max-h-36 gap-px overflow-y-auto rounded-md border bg-border sm:grid-cols-2">{items.map((item) => { const checked = selected.includes(item.id); return <label key={item.id} className="flex items-center gap-2 bg-card px-3 py-2 text-xs"><Checkbox checked={checked} onCheckedChange={(value) => onChange(value === true ? [...selected, item.id] : selected.filter((id) => id !== item.id))} /><span className="min-w-0 flex-1 truncate">{item.label}</span>{checked && <Check className="size-3 text-primary" />}</label> })}</div></div> }
