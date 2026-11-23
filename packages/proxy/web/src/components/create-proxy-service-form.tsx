import { useMemo, useState, type FormEvent } from "react"
import { Cable, Gauge, Globe, LoaderCircle, Plus, Server } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { api } from "@/lib/api"
import { listenerKinds, matchesRegion, regionChoices, strategyChoices } from "@/lib/proxy-groups"
import { shapeForSelection, shapeMeta, sourceClassLabel, type ServiceShape } from "@/lib/service-shape"
import type { CreateListenerRequest, NodeRecord, ProxyGroupStrategy, ResidentialChannel, SharedInboundSettings, Subscription } from "@/lib/types"
import { cn } from "@/lib/utils"

interface CreateProxyServiceFormProps {
  nodes: NodeRecord[]
  subscriptions: Subscription[]
  /**
   * Residential channels are not subscriptions: each one already owns a proxy
   * group (provider + channel), so a new service can reuse it instead of
   * building a second group for the same exits.
   */
  residentialChannels: ResidentialChannel[]
  sharedInbound: SharedInboundSettings | null
  onCreated: () => Promise<void>
  onCancel: () => void
  onNotice: (message: string, tone?: "success" | "error") => void
}

/**
 * 共享入口按用户名分流: 同一族内重名会让整份配置编译失败, 所以默认用户名必须
 * 每次唯一 (与密码一样随机), 否则第二个用默认值新建的服务必然撞上第一个。
 * 用户仍可以改成自己想要的名字。
 * 见 .agents/notes/implemented/bug-fix/2026-10-17-shared-inbound-username-unique-at-write.md。
 */
function uniqueUsername() {
  return `hx-${crypto.randomUUID().slice(0, 8)}`
}

/**
 * 新建代理服务。
 *
 * 弹窗而非页面内的常驻表单: 创建是一次性动作, 而这张表单有十几项 (名称/来源/
 * 策略/协议/绑定/端口/公网端点/凭据)。常驻时它要么把服务列表挤到一侧变窄,
 * 要么自己占满一屏 —— 两者都要为"偶尔用一次"的表单长期付布局代价。
 * 它现在的形态与「新建订阅」「新建渠道」一致。见
 * .agents/notes/implemented/simplification/2026-09-29-proxy-service-create-dialog.md。
 */
export function CreateProxyServiceForm({ nodes, subscriptions, residentialChannels, sharedInbound, onCreated, onCancel, onNotice }: CreateProxyServiceFormProps) {
  // In shared mode the bind address and port belong to the aggregate listener,
  // so the form shows them read-only and the backend derives the real endpoint.
  const shared = sharedInbound?.mode === "shared"
  const [name, setName] = useState("")
  const [strategy, setStrategy] = useState<ProxyGroupStrategy>("manual")
  // "pinned" is a first-class source mode, not a hint: a group around one node
  // is refused by the server, so the form has to be able to express the shape
  // the server recommends. Without this the only reachable path for "one
  // service, one node" would be the API.
  const [sourceMode, setSourceMode] = useState<"direct" | "rule" | "manual" | "pinned" | "residential">("direct")
  const [selectedSubscriptions, setSelectedSubscriptions] = useState<string[]>([])
  const [selectedNodes, setSelectedNodes] = useState<string[]>([])
  // 钉住的节点是单选: 一个服务只有一个出口节点。与「固定节点」的组内多选不同。
  const [pinnedNode, setPinnedNode] = useState("")
  // 住宅渠道是单选: 一个服务复用一个渠道已建的代理组。
  const [selectedChannelID, setSelectedChannelID] = useState("")
  // 默认**不选地区** = 不限地区。
  // See .agents/notes/implemented/feature/2026-10-03-region-filter-defaults-to-none.md
  // 旧默认 ["jp"] 等于替用户加了条件: 选一个不含日本节点的订阅 (实测 shadowmere
  // 全是 🇨🇦 Canada) 会静默命中 0 个节点, 建成空组。
  const [selectedRegions, setSelectedRegions] = useState<string[]>([])
  const [limit, setLimit] = useState(5)
  const [maxLatency, setMaxLatency] = useState(2000)
  const [kind, setKind] = useState<"mixed" | "http" | "socks" | "vless" | "vmess" | "trojan">("mixed")
  const [bindAddress, setBindAddress] = useState(sharedInbound?.mixed_bind_address || "0.0.0.0")
  const [authEnabled, setAuthEnabled] = useState(true)
  const [username, setUsername] = useState<string>(uniqueUsername)
  const [password, setPassword] = useState<string>(crypto.randomUUID())
  const [publicHost, setPublicHost] = useState("")
  const [publicTLS, setPublicTLS] = useState(false)
  const [wsPath, setWSPath] = useState("/__hx-proxy__/hx-pxy")
  const [submitting, setSubmitting] = useState(false)

  // The shape decides which knobs exist, and therefore which of them the
  // estimate may honour. Applying a cap the service will never carry would make
  // the number wrong in exactly the way the form was already wrong: see the
  // maxLatency note in lib/service-shape.ts.
  const shape = useMemo(
    () => sourceMode === "pinned" ? "pinned" : shapeForSelection(selectedSubscriptions, selectedNodes, sourceMode === "direct", subscriptions),
    [selectedNodes, selectedSubscriptions, sourceMode, subscriptions],
  )
  const knobs = shapeMeta[shape].knobs
  const effectiveStrategy: ProxyGroupStrategy = knobs.strategies.includes(strategy) ? strategy : (knobs.strategies[0] ?? "manual")
  // 停用的渠道不是候选: 它的组在数据面也被停用, 服务端会以 422 拒绝 (判据只有
  // enabled —— 前端不另立一套更严或更松的判据)。选中项在渠道被停用/删除后立即
  // 失效, 而不是留着一个提交必失败的选中态。
  const selectedChannel = useMemo(
    () => residentialChannels.find((item) => item.id === selectedChannelID && item.enabled),
    [residentialChannels, selectedChannelID],
  )
  const estimated = useMemo(() => sourceMode === "direct" ? 1 : sourceMode === "pinned" ? (pinnedNode ? 1 : 0) : sourceMode === "manual" ? selectedNodes.length : nodes.filter((node) => {
    if (!node.sources.some((source) => selectedSubscriptions.includes(source.subscription_id))) return false
    if (knobs.maxLatency && (node.last_latency_ms == null || node.last_latency_ms > maxLatency)) return false
    if (knobs.regions && selectedRegions.length > 0 && !selectedRegions.some((region) => matchesRegion(node.display_name, region))) return false
    return true
  }).slice(0, knobs.limit ? limit : undefined).length, [knobs.limit, knobs.maxLatency, knobs.regions, limit, maxLatency, nodes, pinnedNode, selectedNodes.length, selectedRegions, selectedSubscriptions, sourceMode])

  // See .agents/notes/implemented/bug-fix/2026-10-15-fixed-ingress-port-controls.md.
  const advanced = kind === "vless" || kind === "vmess" || kind === "trojan"
  const port = advanced ? (sharedInbound?.ws_port || 7891) : (sharedInbound?.mixed_port || 7890)
  const publicPort = advanced ? (sharedInbound?.ws_public_port || 443) : (sharedInbound?.mixed_public_port || port)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const nonLoopback = bindAddress.trim() !== "" && !["127.0.0.1", "::1", "localhost"].includes(bindAddress.trim())
    if (nonLoopback && !authEnabled) {
      onNotice("非环回监听地址（如 0.0.0.0）必须启用用户名密码认证", "error")
      return
    }
    setSubmitting(true)
    try {
      // 入口字段与来源无关: 住宅来源也只决定**出口是哪个组**, 协议/绑定/认证仍按
      // 共享入口或普通入口的既有规则处理, 不引入住宅专属字段。
      const listener: Omit<CreateListenerRequest, "proxy_group_id"> = {
        name: `${name.trim()} 入口`, kind, bind_address: bindAddress.trim(), port,
        auth: authEnabled ? { username: username.trim(), password } : undefined,
        transport: advanced ? { type: "ws", ws_path: wsPath.trim() } : undefined,
        public_endpoint: publicHost.trim() ? { host: publicHost.trim(), port: publicPort, tls: advanced || publicTLS } : undefined,
      }
      if (sourceMode === "residential") {
        // 复用渠道已建的代理组: **不下发** name/strategy/source_spec。组不是这次
        // 创建出来的, 它保留自己的策略与成员; 服务端也会拒绝一个"既复用又描述"
        // 的请求, 而不是安静地忽略那些字段。
        if (!selectedChannel) { onNotice("请选择一个可用的住宅渠道", "error"); return }
        await api.createProxyService({ listener: { ...listener, proxy_group_id: selectedChannel.proxy_group_id } })
        await onCreated()
        onNotice(`代理服务已创建，出口复用住宅渠道「${selectedChannel.name}」`)
        onCancel()
        return
      }
      if (sourceMode === "pinned") {
        // 一个节点 = 一个钉住的 listener, 不建组。服务端会拒绝为单节点建组, 而且
        // 同时下发 name/strategy/source_spec 会被 422 拒绝 (字段没有效果就不该被接受)。
        if (!pinnedNode) { onNotice("请选择要钉住的节点", "error"); return }
        await api.createProxyService({ listener: { ...listener, node_id: pinnedNode } })
        await onCreated()
        onNotice(`代理服务已创建，出口钉死在节点「${nodes.find((item) => item.id === pinnedNode)?.display_name ?? pinnedNode}」`)
        onCancel()
        return
      }
      // Only the knobs this shape has are sent. Sending an inapplicable one is
      // not a harmless no-op: `max_latency_ms` on a pool drops every member
      // that has never answered — i.e. the whole group. See lib/service-shape.ts.
      const ruleSpec = {
        node_ids: [],
        subscription_ids: selectedSubscriptions,
        states: ["candidate", "healthy", "degraded"] as Array<"candidate" | "healthy" | "degraded" | "quarantined">,
        sort_by: "latency" as "latency" | "name",
        include_direct: false,
        ...(knobs.regions && selectedRegions.length > 0 ? { regions: selectedRegions } : {}),
        ...(knobs.maxLatency ? { max_latency_ms: maxLatency } : {}),
        ...(knobs.limit && limit > 0 ? { limit } : {}),
      }
      await api.createProxyService({
        name: name.trim(), strategy: effectiveStrategy,
        source_spec: sourceMode === "direct" ? { node_ids: [], include_direct: true } : sourceMode === "rule" ? ruleSpec : { node_ids: selectedNodes, include_direct: false },
        empty_behavior: sourceMode === "direct" ? "direct" : "fail-closed",
        listener,
      })
      await onCreated()
      onNotice(shared ? `代理服务已加入共享入口：${username.trim()}` : `代理服务已启动：${bindAddress}:${port}`)
      onCancel()
    } catch (error) { onNotice(error instanceof Error ? error.message : "创建代理服务失败", "error") }
    finally { setSubmitting(false) }
  }

  // A group is a selection, so a rule-shaped source that resolves to exactly one
  // member is the same handle-in-disguise the server refuses. estimated === 0 is
  // allowed: a source that has not produced a snapshot yet is dormant, not a
  // one-node group. See internal/proxygroup/single_node_rule.go.
  const singleMemberRule = sourceMode === "rule" && selectedSubscriptions.length > 0 && estimated === 1
  const validSource = sourceMode === "direct"
    || (sourceMode === "pinned" ? pinnedNode !== ""
      : sourceMode === "rule" ? selectedSubscriptions.length > 0 && !singleMemberRule
        : sourceMode === "manual" ? selectedNodes.length >= 2
          : selectedChannel !== undefined)
  const nonLoopback = bindAddress.trim() !== "" && !["127.0.0.1", "::1", "localhost"].includes(bindAddress.trim())
  function changeKind(next: typeof kind) {
    setKind(next)
    const nextAdvanced = next === "vless" || next === "vmess" || next === "trojan"
    if (nextAdvanced) {
      setBindAddress("127.0.0.1")
      setAuthEnabled(true)
      if (!username) setUsername(uniqueUsername())
      if ((next === "vless" || next === "vmess") && !password) setPassword(crypto.randomUUID())
    } else {
      setBindAddress(sharedInbound?.mixed_bind_address || "0.0.0.0")
    }
  }
  function changeSourceMode(next: "direct" | "rule" | "manual" | "pinned" | "residential") {
    setSourceMode(next)
    if (next === "direct") setStrategy("manual")
  }
  const invalid = !name.trim() || !validSource || (nonLoopback && !authEnabled) || (authEnabled && (!username.trim() || !password)) || (advanced && (!publicHost.trim() || !wsPath.startsWith("/")))

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !submitting) onCancel() }}>
      <DialogContent className="max-w-3xl">
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>新建代理服务</DialogTitle>
            <DialogDescription>固定入口、登录账号与聚合规则一次创建；创建后立即编译并下发到 Mihomo。</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 px-5 py-4">
            <Field label="服务名称"><Input value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：日本低延迟出口" maxLength={128} autoFocus required /></Field>
            <Field label="节点来源"><ChipSet values={[{ value: "direct", label: "服务器直连" }, { value: "rule", label: "跨订阅规则" }, { value: "manual", label: "节点组" }, { value: "pinned", label: "单节点出口" }, { value: "residential", label: "住宅代理" }]} current={sourceMode} onChange={changeSourceMode} /></Field>
            {sourceMode === "direct" ? <div className="flex items-center gap-2 rounded-md border bg-muted/60 px-3 py-2 text-xs"><Server className="size-4 text-success" />流量使用当前服务器网络出口</div> : sourceMode === "residential" ? <>
              {/* 渠道不是订阅: 这里选的是一个**已经存在的代理组** (建渠道时一并创建),
                  不是一份节点快照, 所以没有地区/延迟/上限这些筛选。 */}
              <Field label="住宅渠道"><ResidentialChannelChoice items={residentialChannels} selected={selectedChannelID} onChange={setSelectedChannelID} /></Field>
              <a className="text-xs text-primary underline" href="#residential" onClick={onCancel}>配置住宅供应商账号、密码与渠道</a>
              {selectedChannel && <div className="flex items-start gap-2 rounded-md border bg-muted/60 px-3 py-2 text-xs" title={selectedChannel.proxy_group_id}>
                <Globe className="mt-0.5 size-4 shrink-0 text-info" />
                <span>复用渠道「{selectedChannel.name}」已建的代理组：出口、出站策略与成员由住宅渠道维护，本服务只新增一个入口，不会再建组。专属粘性身份请在住宅渠道页面创建。</span>
              </div>}
            </> : sourceMode === "rule" ? <>
              <Field label={`订阅（已选 ${selectedSubscriptions.length}）`}><SubscriptionCheckList items={subscriptions} selected={selectedSubscriptions} onChange={setSelectedSubscriptions} /></Field>
              {/* 勾选订阅之后才知道形态, 之前不猜。形态决定下面哪些指标存在。 */}
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border bg-muted/60 px-3 py-2 text-[11px]">
                <Badge variant={shapeMeta[shape].variant}>{shapeMeta[shape].label}</Badge>
                <span className="text-muted-foreground">{shapeMeta[shape].detail}</span>
              </div>
              {knobs.regions && <Field label="地区"><MultiChipSet values={regionChoices} current={selectedRegions} onChange={setSelectedRegions} /></Field>}
              <div className="grid gap-3 sm:grid-cols-2">
                {knobs.limit && <Field label="最多节点"><Input type="number" min={2} max={500} value={limit} onChange={(event) => setLimit(Number(event.target.value))} /></Field>}
                {knobs.maxLatency && <Field label="最大延迟 ms"><Input type="number" min={1} max={60000} value={maxLatency} onChange={(event) => setMaxLatency(Number(event.target.value))} /></Field>}
              </div>
              {/* 池的成员大多从未测速过: 说明"不可用"与"没测过"不是一回事。 */}
              {!knobs.maxLatency && <p className="text-[11px] leading-4 text-muted-foreground">该来源不按延迟筛选：未测速的成员同样会被选中，实际可用性由数据面健康检查决定。</p>}
              <div className="flex items-center gap-2 rounded-md border bg-muted/60 px-3 py-2 text-xs"><Gauge className="size-4 text-info" />当前指标预计命中 {estimated} 个节点</div>
              {/* 与服务端同一条判据: 订阅当前只有一个成员、或 limit/筛选择出只剩一个,
                  都不能建组。0 个是「还没刷新」的休眠态, 不拦。 */}
              {singleMemberRule && <p className="text-[11px] leading-4 text-destructive">这次规则当前只能解析出 1 个节点，不能建组。只让一个服务走一个节点请改用「单节点出口」（listener.node_id）；要给组留选择余地，请放宽筛选或多选订阅。</p>}
            </> : sourceMode === "pinned" ? <>
              {/* 单节点出口: 钉住一个节点, 不建组。这是服务端推荐的形态, 也是
                  「一个节点 = 一个组」被拒绝之后唯一还成立的一条路。 */}
              <Field label="钉住的节点"><CheckList items={nodes.map((item) => ({ id: item.id, label: `${item.display_name} · ${item.last_latency_ms == null ? "未测试" : `${item.last_latency_ms}ms`}` }))} selected={pinnedNode ? [pinnedNode] : []} onChange={(ids) => setPinnedNode(ids.find((id) => id !== pinnedNode) ?? "")} empty="暂无活动节点" /></Field>
              <div className="flex items-start gap-2 rounded-md border bg-muted/60 px-3 py-2 text-xs"><Server className="mt-0.5 size-4 shrink-0 text-success" /><span>这个服务固定从所选节点出网，<b>不会创建代理组</b>。节点消失或被停用时整份配置会编译失败并指名这个服务，不会静默换出口。</span></div>
            </> : <>
              <Field label={`固定节点（已选 ${selectedNodes.length}）`}><CheckList items={nodes.map((item) => ({ id: item.id, label: `${item.display_name} · ${item.last_latency_ms == null ? "未测试" : `${item.last_latency_ms}ms`}` }))} selected={selectedNodes} onChange={setSelectedNodes} empty="暂无活动节点" /></Field>
              {/* 组是「在一批节点里选一个」, 不是「指向某个节点」的句柄: 单节点组会
                  随服务数量线性膨胀 (2026-09-26 实测 57/61 个组都是单节点组)。要钉一个
                  节点就不该建组, 服务端会用 listener.node_id 直接钉。 */}
              {selectedNodes.length < 2 && <p className="text-[11px] leading-4 text-muted-foreground">节点组至少要选 2 个节点。只用一个节点请改用上面的「单节点出口」：它把服务钉到那个节点（listener.node_id），不创建任何代理组。不要为每个节点各建一个组。</p>}
            </>}
            {/* 复用组时出站策略属于那个组, 不属于本服务: 一个改不了的选择器只会骗人。
                表单因此既不显示它, 也不下发 strategy (见下方 submit)。 */}
            {sourceMode !== "residential" && sourceMode !== "pinned" && <Field label="出站策略"><ChipSet values={strategyChoicesForShape(shape)} current={effectiveStrategy} onChange={setStrategy} /></Field>}
            <div className="border-t pt-4">
              <div className="mb-3 text-xs font-semibold">入口与登录</div>
              <div className="space-y-3">
                <Field label="入口协议"><ChipSet values={listenerKinds} current={kind} onChange={changeKind} /></Field>
                {shared ? <div className="flex items-start gap-2 rounded-md border border-info-border bg-info-muted px-3 py-2 text-xs text-info-foreground"><Cable className="mt-0.5 size-3.5 shrink-0" /><span>共享入口模式：本服务将复用现有入口（{advanced ? `VLESS / VMess / Trojan 内部端口 ${sharedInbound?.ws_port || 7891}` : `Mixed 端口 ${sharedInbound?.mixed_port || 7890}`}），按用户名自动分流，不会再占用新的端口。</span></div> : null}
                <div className="grid gap-3 sm:grid-cols-[1fr_110px]"><Field label="绑定 IP"><Input value={bindAddress} onChange={(event) => setBindAddress(event.target.value)} disabled={advanced || shared} required />{!advanced && nonLoopback && !authEnabled && <span className="block text-xs text-destructive">非环回监听地址（如 0.0.0.0）必须启用用户名密码认证</span>}{!advanced && nonLoopback && authEnabled && <span className="block text-xs text-muted-foreground">监听 0.0.0.0 供内网 / Docker 容器访问，公网由防火墙保护</span>}</Field><Field label="本地端口"><output className="block py-2 font-mono text-xs">{port}（固定）</output></Field></div>
                {advanced && <div className="grid gap-3 sm:grid-cols-[1fr_1fr]"><Field label="Cloudflare 域名"><Input value={publicHost} onChange={(event) => setPublicHost(event.target.value)} placeholder="proxy.example.com" required /></Field><Field label="WebSocket Path（固定前缀 /__hx-proxy__/）"><Input value={wsPath} onChange={(event) => setWSPath(event.target.value)} placeholder="/__hx-proxy__/hx-pxy" required /></Field></div>}
                {!advanced && <div className="grid gap-3 sm:grid-cols-[1fr_110px]"><Field label="公网主机名 / IP（可选）"><Input value={publicHost} onChange={(event) => setPublicHost(event.target.value)} placeholder="VPS 公网地址" /></Field><Field label="公网端口"><output className="block py-2 font-mono text-xs">{publicPort}（固定）</output></Field></div>}
                {!advanced && publicHost.trim() && <label className="flex items-center gap-2 rounded-md border bg-muted/60 px-3 py-2 text-xs"><Checkbox checked={publicTLS} onCheckedChange={(value) => setPublicTLS(value === true)} />公网 HTTP 端点使用 TLS</label>}
                {!advanced && <>
                  <label className="flex items-center gap-2 rounded-md border bg-muted/60 px-3 py-2 text-xs"><Checkbox checked={authEnabled || shared} disabled={shared} onCheckedChange={(value) => setAuthEnabled(value === true)} />启用账号认证</label>
                  {shared && <p className="text-xs text-muted-foreground">共享入口必须启用账号认证：Mihomo 依靠登录名把每个服务分流到它自己的代理组。</p>}
                </>}
                {authEnabled && <div className="grid gap-3 sm:grid-cols-2"><Field label={advanced ? "用户备注" : "用户名"}><Input value={username} onChange={(event) => setUsername(event.target.value)} required /></Field><Field label={kind === "vless" || kind === "vmess" ? "UUID" : "密码"}><Input type="password" value={password} onChange={(event) => setPassword(event.target.value)} required /></Field></div>}
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel} disabled={submitting}>取消</Button>
            <Button type="submit" disabled={submitting || invalid}>{submitting ? <LoaderCircle className="animate-spin" /> : <Plus />}创建并启动</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * 订阅勾选列表。每行标出来源的类别, 因为"选哪个订阅"实际上是在选**服务的形态**:
 * 勾一个池和一个住宅源, 得到的服务在失败策略上完全不同。
 */
function SubscriptionCheckList({ items, selected, onChange }: { items: Subscription[]; selected: string[]; onChange: (ids: string[]) => void }) {
  if (items.length === 0) return <div className="rounded-md border px-3 py-4 text-center text-xs text-muted-foreground">暂无已启用订阅</div>
  return <div className="max-h-40 overflow-y-auto rounded-md border">{items.map((item) => {
    const checked = selected.includes(item.id)
    const meta = sourceClassLabel[item.source_class]
    return <label key={item.id} className="flex cursor-pointer items-center gap-2 border-b px-3 py-2 text-xs last:border-b-0 hover:bg-muted/60">
      <Checkbox checked={checked} onCheckedChange={(value) => onChange(value === true ? [...selected, item.id] : selected.filter((id) => id !== item.id))} />
      <span className="min-w-0 flex-1 truncate">{item.name}</span>
      <Badge variant={meta.variant} className="shrink-0">{meta.label}</Badge>
    </label>
  })}</div>
}

/**
 * 住宅渠道单选列表。
 *
 * 渠道不是订阅: 建渠道时控制面就一并建好了它的代理组 (provider + channel),
 * 所以这里选的是**一个已经存在的组**, 而不是一份会随刷新变化的节点快照。
 * 停用的渠道仍然列出但不可选 —— 它的组在数据面同样被停用, 服务端也会以 422
 * 拒绝; 直接藏起来会让操作员以为自己的渠道不见了。
 *
 * 见 .agents/notes/implemented/feature/2026-10-14-reuse-existing-group-for-an-entry.md。
 */
function ResidentialChannelChoice({ items, selected, onChange }: { items: ResidentialChannel[]; selected: string; onChange: (id: string) => void }) {
  if (items.length === 0) return <div className="rounded-md border px-3 py-4 text-center text-xs text-muted-foreground">暂无住宅渠道。先到「住宅代理」页面创建供应商与渠道，再回到这里复用它的出口。</div>
  return <div className="max-h-40 overflow-y-auto rounded-md border">{items.map((item) => {
    const exits = Math.max(item.pool_size ?? 0, item.active_session_count)
    return <label key={item.id} title={`代理组 ${item.proxy_group_id}`} className={cn("flex items-center gap-2 border-b px-3 py-2 text-xs last:border-b-0", item.enabled ? "cursor-pointer hover:bg-muted/60" : "cursor-not-allowed opacity-60")}>
      <Checkbox checked={selected === item.id} disabled={!item.enabled} onCheckedChange={(value) => { if (value === true) onChange(item.id) }} />
      <span className="min-w-0 flex-1 truncate">{item.name}</span>
      <span className="shrink-0 text-muted-foreground">{item.region_mode === "fixed" && item.region ? item.region : "不限地区"}</span>
      {/* 出口数是信息而不是闸门: 服务端只校验渠道是否启用, 前端不另立更严的判据。
          0 个出口的渠道仍可选, 但它今天会以 fail-closed 拒绝, 所以说清楚。 */}
      <span className={cn("shrink-0 tabular-nums", exits === 0 && "text-warning")}>{item.session_count > 0 ? `${item.active_session_count}/${item.session_count} 会话` : `${exits} 个出口`}</span>
      <Badge variant={item.enabled ? "outline" : "secondary"} className="shrink-0">{item.enabled ? "可用" : "已停用"}</Badge>
    </label>
  })}</div>
}

/** 形态只允许它真正拥有的策略: 给一个池选"会话粘滞"是在承诺一件做不到的事。 */
function strategyChoicesForShape(shape: ServiceShape) {
  const allowed = shapeMeta[shape].knobs.strategies
  return strategyChoices.filter((item) => allowed.includes(item.value))
}

function CheckList({ items, selected, onChange, empty }: { items: Array<{ id: string; label: string }>; selected: string[]; onChange: (ids: string[]) => void; empty: string }) {
  return <div className="max-h-40 overflow-y-auto rounded-md border">{items.length === 0 ? <div className="px-3 py-4 text-center text-xs text-muted-foreground">{empty}</div> : items.map((item) => { const checked = selected.includes(item.id); return <label key={item.id} className="flex cursor-pointer items-center gap-2 border-b px-3 py-2 text-xs last:border-b-0 hover:bg-muted/60"><Checkbox checked={checked} onCheckedChange={(value) => onChange(value === true ? [...selected, item.id] : selected.filter((id) => id !== item.id))} /><span className="min-w-0 flex-1 truncate">{item.label}</span></label> })}</div>
}

function Field({ label, children }: { label: string; children: React.ReactNode }) { return <label className="block space-y-1.5"><span className="text-xs font-medium">{label}</span>{children}</label> }
function Choice({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) { return <Button type="button" size="sm" variant={active ? "default" : "outline"} onClick={onClick}>{children}</Button> }
function ChipSet<T extends string>({ values, current, onChange }: { values: Array<{ value: T; label: string }>; current: T; onChange: (value: T) => void }) { return <div className="flex flex-wrap gap-1.5">{values.map((item) => <Choice key={item.value} active={current === item.value} onClick={() => onChange(item.value)}>{item.label}</Choice>)}</div> }
function MultiChipSet<T extends string>({ values, current, onChange }: { values: Array<{ value: T; label: string }>; current: T[]; onChange: (value: T[]) => void }) { return <div className="flex flex-wrap gap-1.5">{values.map((item) => { const active = current.includes(item.value); return <Choice key={item.value} active={active} onClick={() => onChange(active ? current.filter((value) => value !== item.value) : [...current, item.value])}>{item.label}</Choice> })}</div> }
