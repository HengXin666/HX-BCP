import { useCallback, useEffect, useState, type FormEvent } from "react"
import {
  FlaskConical,
  ListRestart,
  Pencil,
  Plus,
  RefreshCw,
  Settings2,
  Trash2,
  Wrench,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { ResidentialSessionsDialog } from "@/components/residential-sessions-dialog"
import { CFAccountGroup } from "@/components/cf-account-group"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ApiError, api } from "@/lib/api"
import type {
  CreateResidentialChannelRequest,
  CreateResidentialProviderRequest,
  ResidentialChannel,
  ResidentialChannelMode,
  ResidentialPreset,
  ResidentialProvider,
  ResidentialProtocol,
  ResidentialRegionMode,
  ResidentialRotationMode,
  UpdateResidentialChannelRequest,
  ResidentialSessionExpiryPolicy,
  ResidentialTestResult,
  ProxyGroup,
  TrafficSummary,
} from "@/lib/types"
import { cn, formatBytes } from "@/lib/utils"

type ProviderForm = {
  name: string
  vendor: string
  protocol: ResidentialProtocol
  rotationMode: ResidentialRotationMode
  gatewayHost: string
  gatewayPort: string
  upstreamProxyGroupID: string
  apiProxyURL: string
  apiURL: string
  workerURL: string
  username: string
  password: string
  usernameTemplate: string
  sessionTTL: string
  maxSessions: string
  expiryPolicy: ResidentialSessionExpiryPolicy
  defaultRegion: string
  defaultRegionMode: ResidentialRegionMode
  defaultRandomRegions: string
  enabled: boolean
}

const emptyProviderForm: ProviderForm = {
  name: "",
  vendor: "bestproxy",
  protocol: "http",
  rotationMode: "session-template",
  gatewayHost: "",
  gatewayPort: "2312",
  upstreamProxyGroupID: "",
  apiProxyURL: "",
  apiURL: "",
  workerURL: "",
  username: "",
  password: "",
  usernameTemplate: "{user}-session-{session}",
  sessionTTL: "600",
  maxSessions: "64",
  expiryPolicy: "rotate",
  defaultRegion: "",
  defaultRegionMode: "fixed",
  defaultRandomRegions: "",
  enabled: true,
}

type ChannelForm = {
  name: string
  providerID: string
  providerIDs: string[]
  mode: ResidentialChannelMode
  protocol: "vless" | "vmess" | "trojan"
  regionMode: ResidentialRegionMode
  region: string
  randomRegions: string
  publicHost: string
  sessionCount: string
  idleReleaseSeconds: string
  preallocate: boolean
  enabled: boolean
}

const emptyChannelForm: ChannelForm = {
  name: "",
  providerID: "",
  providerIDs: [],
  mode: "sticky",
  protocol: "vless",
  regionMode: "fixed",
  region: "",
  randomRegions: "",
  publicHost: "",
  sessionCount: "3",
  idleReleaseSeconds: "0",
  preallocate: false,
  enabled: true,
}

export function ResidentialPage({
  onNotice,
}: {
  onNotice: (message: string, tone?: "success" | "error") => void
}) {
  const [providers, setProviders] = useState<ResidentialProvider[]>([])
  const [channels, setChannels] = useState<ResidentialChannel[]>([])
  const [presets, setPresets] = useState<ResidentialPreset[]>([])
  const [workerProtocols, setWorkerProtocols] = useState<ResidentialProtocol[]>([])
  const [proxyGroups, setProxyGroups] = useState<ProxyGroup[]>([])
  const [traffic, setTraffic] = useState<Map<string, TrafficSummary>>(new Map())
  const [loading, setLoading] = useState(true)
  const [providerDialogOpen, setProviderDialogOpen] = useState(false)
  const [channelDialogOpen, setChannelDialogOpen] = useState(false)
  const [endpointChannel, setEndpointChannel] = useState<ResidentialChannel | null>(null)
  const [sessionChannel, setSessionChannel] = useState<ResidentialChannel | null>(null)
  const [editingProvider, setEditingProvider] = useState<ResidentialProvider | null>(null)
  const [editingChannel, setEditingChannel] = useState<ResidentialChannel | null>(null)
  const [testResult, setTestResult] = useState<ResidentialTestResult | null>(null)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const [providerList, channelList, catalog, groupList, trafficList] = await Promise.all([
        api.listResidentialProviders(),
        api.listResidentialChannels(),
        api.residentialPresets(),
        api.listProxyGroups(),
        api.trafficSummaries("residential_channel"),
      ])
      setProviders(providerList.items)
      setChannels(channelList.items)
      setPresets(catalog.items)
      setWorkerProtocols(catalog.worker_protocols ?? [])
      setProxyGroups(groupList.items)
      setTraffic(new Map(trafficList.items.map((item) => [item.resource_id, item])))
      setSessionChannel((current) => current
        ? channelList.items.find((item) => item.id === current.id) ?? null
        : null)
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
    } finally {
      setLoading(false)
    }
  }, [onNotice])

  useEffect(() => {
    void reload()
  }, [reload])

  async function runAction(action: () => Promise<unknown>, success: string) {
    try {
      await action()
      onNotice(success)
      await reload()
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
      else onNotice(String(cause), "error")
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">住宅代理</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            将稳定客户端节点映射到服务端轮换的住宅出口，并统一统计渠道流量。
          </p>
        </div>
        <button
          type="button"
          onClick={() => void reload()}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-muted"
        >
          <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
          刷新
        </button>
      </div>

      <Tabs defaultValue="channels">
        <TabsList>
          <TabsTrigger value="channels">渠道 {channels.length > 0 && `(${channels.length})`}</TabsTrigger>
          <TabsTrigger value="providers">供应商 {providers.length > 0 && `(${providers.length})`}</TabsTrigger>
        </TabsList>

        <TabsContent value="channels" className="space-y-4">
          <div className="flex items-center justify-end">
            <Button size="sm" onClick={() => { setEditingChannel(null); setChannelDialogOpen(true) }}>
              <Plus className="mr-1 size-3.5" />
              新建渠道
            </Button>
          </div>
          <section className="rounded-md border bg-card">
            {channels.length === 0 ? (
              <div className="px-4 py-12 text-center text-sm text-muted-foreground">
                还没有住宅渠道。先创建一个供应商，再新建渠道即可获得客户端入口。
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead className="border-b bg-muted/50 text-muted-foreground">
                    <tr>
                      <th className="px-3 py-2 font-medium">名称</th>
                      <th className="px-3 py-2 font-medium">供应商</th>
                      <th className="px-3 py-2 font-medium">模式</th>
                      <th className="px-3 py-2 font-medium">地区</th>
                      <th className="px-3 py-2 font-medium">客户端入口</th>
                      <th className="px-3 py-2 font-medium">节点</th>
                      <th className="px-3 py-2 font-medium">出口 IP</th>
                      <th className="px-3 py-2 font-medium">累计流量</th>
                      <th className="px-3 py-2 text-right font-medium">操作</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y">
                    {channels.map((channel) => (
                      <tr key={channel.id}>
                        <td className="px-3 py-2 font-medium">{channel.name}</td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {channel.providers && channel.providers.length > 0
                            ? channel.providers.map((provider) => provider.name).join(", ")
                            : (channel.provider_name ?? channel.provider_id)}
                        </td>
                        <td className="px-3 py-2"><Badge variant={channel.mode === "sticky" ? "default" : "outline"}>{channel.mode === "sticky" ? "粘滞" : "透传"}</Badge></td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {regionModeLabel(channel.region_mode)}
                          {channel.region_mode === "fixed" && channel.region ? ` · ${channel.region}` : ""}
                          {channel.region_mode === "application-random" && channel.random_regions?.length
                            ? ` · ${channel.random_regions.join(",")}`
                            : ""}
                        </td>
                        <td className="px-3 py-2">
                          <div className="space-y-1.5">
                            <Badge variant="outline">
                              {isResidentialWebSocketKind(channel.endpoint.kind)
                                ? `${channel.endpoint.kind.toUpperCase()} · WebSocket · TLS`
                                : `${channel.endpoint.kind.toUpperCase()} · 旧版直连`}
                            </Badge>
                            <div className="flex items-center gap-1.5">
                              <code className={cn("rounded px-1.5 py-0.5", hasPublicEndpoint(channel) ? "bg-success-muted" : "bg-warning-muted text-warning")}>
                                {formatPublicEndpoint(channel)}
                              </code>
                              {isResidentialWebSocketKind(channel.endpoint.kind) && (
                                <button type="button" title="配置公网域名" onClick={() => setEndpointChannel(channel)} className="text-muted-foreground hover:text-foreground"><Pencil className="size-3" /></button>
                              )}
                            </div>
                            {!isResidentialWebSocketKind(channel.endpoint.kind) && (
                              <span className="text-[11px] text-warning">旧版明文入口，仅为兼容保留；请迁移到托管 WebSocket 渠道。</span>
                            )}
                            {isResidentialWebSocketKind(channel.endpoint.kind) && channel.mode === "sticky" && (
                              <span className="text-[11px] text-muted-foreground">节点名称和凭据稳定，住宅出口由服务端内部轮换。</span>
                            )}
                            {!hasPublicEndpoint(channel) && <span className="text-[11px] text-warning">未配置公网端点，禁止复制本机地址</span>}
                          </div>
                        </td>
                        <td className="px-3 py-2">
                          {channel.session_count > 0 ? (
                            <button type="button" className="font-medium text-primary hover:underline" onClick={() => setSessionChannel(channel)}>
                              {channel.active_session_count}/{channel.session_count} 已分配
                            </button>
                          ) : (
                            <span className="text-muted-foreground">{channel.active_session_count} 个按需分配</span>
                          )}
                          {channel.direct_endpoint && (
                            <div className="mt-1 text-[11px] text-warning">
                              存在旧版直连入口，请尽快停用
                            </div>
                          )}
                        </td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {channel.sessions?.some((session) => session.exit_ip)
                            ? `${channel.sessions.filter((session) => session.exit_ip).length} 个已记录`
                            : "由服务端会话维护"}
                        </td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {formatBytes((traffic.get(channel.id)?.upload_bytes ?? 0) + (traffic.get(channel.id)?.download_bytes ?? 0))}
                        </td>
                        <td className="px-3 py-2">
                          <div className="flex items-center justify-end gap-1">
                            <button
                              type="button"
                              title="编辑渠道"
                              onClick={() => { setEditingChannel(channel); setChannelDialogOpen(true) }}
                              className="inline-flex size-7 items-center justify-center rounded-md border hover:bg-muted"
                            >
                              <Settings2 className="size-3.5" />
                            </button>
                            <button
                              type="button"
                              title={isResidentialWebSocketKind(channel.endpoint.kind) ? "配置公网域名" : "旧版入口只读"}
                              disabled={!isResidentialWebSocketKind(channel.endpoint.kind)}
                              onClick={() => setEndpointChannel(channel)}
                              className="inline-flex size-7 items-center justify-center rounded-md border hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                            >
                              <Pencil className="size-3.5" />
                            </button>
                            <button
                              type="button"
                              title="管理住宅节点"
                              disabled={channel.session_count < 1}
                              onClick={() => setSessionChannel(channel)}
                              className="inline-flex size-7 items-center justify-center rounded-md border hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                            >
                              <ListRestart className="size-3.5" />
                            </button>
                            <button
                              type="button"
                              title="删除渠道"
                              onClick={() => void runAction(() => api.deleteResidentialChannel(channel.id, channel.version), "渠道已删除")}
                              className="inline-flex size-7 items-center justify-center rounded-md border text-destructive hover:bg-destructive/10"
                            >
                              <Trash2 className="size-3.5" />
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </TabsContent>

        <TabsContent value="providers" className="space-y-4">
          <CFAccountGroup onNotice={onNotice} />
          <div className="flex items-center justify-end">
            <Button size="sm" onClick={() => { setEditingProvider(null); setProviderDialogOpen(true) }}>
              <Plus className="mr-1 size-3.5" />
              新建供应商
            </Button>
          </div>
          <section className="rounded-md border bg-card">
            {providers.length === 0 ? (
              <div className="px-4 py-12 text-center text-sm text-muted-foreground">
                还没有住宅供应商。选一个预设（如 BestProxy 账密网关或 BestProxy API 提取）即可开始。
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead className="border-b bg-muted/50 text-muted-foreground">
                    <tr>
                      <th className="px-3 py-2 font-medium">名称</th>
                      <th className="px-3 py-2 font-medium">厂商</th>
                      <th className="px-3 py-2 font-medium">协议</th>
                      <th className="px-3 py-2 font-medium">轮换模式</th>
                      <th className="px-3 py-2 font-medium">默认地区</th>
                      <th className="px-3 py-2 font-medium">网关 / API</th>
                      <th className="px-3 py-2 font-medium">上游组</th>
                      <th className="px-3 py-2 font-medium">会话策略</th>
                      <th className="px-3 py-2 text-right font-medium">操作</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y">
                    {providers.map((provider) => (
                      <tr key={provider.id}>
                        <td className="px-3 py-2 font-medium">{provider.name}</td>
                        <td className="px-3 py-2 text-muted-foreground">{provider.vendor}</td>
                        <td className="px-3 py-2"><code className="rounded bg-muted px-1.5 py-0.5">{provider.protocol}</code></td>
                        <td className="px-3 py-2">{rotationModeLabel(provider.rotation_mode)}</td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {regionModeLabel(provider.default_region_mode)}
                          {provider.default_region_mode === "fixed" && provider.default_region ? ` · ${provider.default_region}` : ""}
                          {provider.default_region_mode === "application-random" && provider.default_random_regions?.length
                            ? ` · ${provider.default_random_regions.join(",")}`
                            : ""}
                        </td>
                        <td className="px-3 py-2">
                          {provider.rotation_mode === "cf-worker" ? (
                            <div className="space-y-1">
                              <code className="rounded bg-muted px-1.5 py-0.5">
                                {provider.worker_url_configured ? "CF Worker 已配置（不会回显）" : "CF Worker 未配置"}
                              </code>
                              {provider.api_proxy_configured && <div className="text-[11px] text-muted-foreground">API 上游代理已配置</div>}
                            </div>
                          ) : provider.rotation_mode === "hx-cf-wspxy" ? (
                            <div className="space-y-1">
                              <code className="rounded bg-muted px-1.5 py-0.5">
                                {provider.api_url_configured ? "WsPxy 控制面已配置（不会回显）" : "WsPxy 控制面未配置"}
                              </code>
                            </div>
                          ) : provider.rotation_mode === "api-list" ? (
                            <div className="space-y-1">
                              <code className="rounded bg-muted px-1.5 py-0.5">
                                {provider.api_url_configured ? "API 已配置（不会回显）" : "API 未配置"}
                              </code>
                              {provider.api_proxy_configured && <div className="text-[11px] text-muted-foreground">API 上游代理已配置</div>}
                            </div>
                          ) : (
                            <div className="space-y-1">
                              <code className="rounded bg-muted px-1.5 py-0.5">{provider.gateway_host}:{provider.gateway_port}</code>
                              {provider.api_proxy_configured && <div className="text-[11px] text-muted-foreground">API 上游代理已配置</div>}
                            </div>
                          )}
                        </td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {provider.upstream_proxy_group_id
                            ? proxyGroups.find((group) => group.id === provider.upstream_proxy_group_id)?.name ?? "上游组已配置"
                            : "直连住宅网关"}
                        </td>
                        <td className="px-3 py-2 text-muted-foreground">
                          最多 {provider.max_concurrent_sessions} · {provider.session_expiry_policy === "rotate" ? "到期换 IP" : "到期终止"}
                        </td>
                        <td className="px-3 py-2">
                          <div className="flex items-center justify-end gap-1">
                            <button
                              type="button"
                              title="编辑供应商"
                              onClick={() => { setEditingProvider(provider); setProviderDialogOpen(true) }}
                              className="inline-flex size-7 items-center justify-center rounded-md border hover:bg-muted"
                            >
                              <Pencil className="size-3.5" />
                            </button>
                            <button
                              type="button"
                              title="测试连接：观察出口 IP"
                              onClick={() => void testProvider(provider.id, setTestResult, onNotice)}
                              className="inline-flex size-7 items-center justify-center rounded-md border hover:bg-muted"
                            >
                              <FlaskConical className="size-3.5" />
                            </button>
                            <button
                              type="button"
                              title="删除供应商"
                              onClick={() => void runAction(() => api.deleteResidentialProvider(provider.id, provider.version), "供应商已删除")}
                              className="inline-flex size-7 items-center justify-center rounded-md border text-destructive hover:bg-destructive/10"
                            >
                              <Trash2 className="size-3.5" />
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </TabsContent>
      </Tabs>

      {providerDialogOpen && (
        <ProviderDialog
          presets={presets}
          workerProtocols={workerProtocols}
          proxyGroups={proxyGroups}
          initial={editingProvider ?? undefined}
          onClose={() => setProviderDialogOpen(false)}
          onSaved={async () => {
            setProviderDialogOpen(false)
            setEditingProvider(null)
            onNotice("供应商已保存")
            await reload()
          }}
          onNotice={onNotice}
        />
      )}

      {channelDialogOpen && (
        <ChannelDialog
          providers={providers}
          initial={editingChannel ?? undefined}
          onClose={() => setChannelDialogOpen(false)}
          onSaved={async () => {
            setChannelDialogOpen(false)
            onNotice(editingChannel ? "渠道已更新" : "渠道已创建，客户端入口可用")
            await reload()
          }}
          onNotice={onNotice}
        />
      )}

      {endpointChannel && (
        <ChannelEndpointDialog
          channel={endpointChannel}
          onClose={() => setEndpointChannel(null)}
          onSaved={async () => {
            setEndpointChannel(null)
            onNotice("住宅公网端点已保存")
            await reload()
          }}
          onNotice={onNotice}
        />
      )}

      {sessionChannel && (
        <ResidentialSessionsDialog
          channel={sessionChannel}
          onClose={() => setSessionChannel(null)}
          onReload={reload}
          onNotice={onNotice}
        />
      )}

      {testResult && (
        <Dialog open onOpenChange={() => setTestResult(null)}>
          <DialogContent className="max-w-md">
            <DialogHeader>
              <DialogTitle>测试连接结果</DialogTitle>
            </DialogHeader>
            <div className="px-5 py-4 text-sm">
              {testResult.success ? (
                <div className="space-y-1.5">
                  <div className="flex items-center gap-2 text-success"><Badge>可用</Badge>出口 IP <code className="font-mono">{testResult.exit_ip || "由渠道数据面验证"}</code></div>
                  <div className="text-xs text-muted-foreground">延迟 {testResult.latency_ms} ms</div>
                  {testResult.detail && <div className="text-xs text-muted-foreground">{testResult.detail}</div>}
                </div>
              ) : (
                <div className="space-y-1.5 text-destructive">
                  <div className="font-medium">连接失败</div>
                  <div className="whitespace-pre-wrap text-xs">{testResult.error}</div>
                  {testResult.rendered_username_preview && (
                    <div className="text-xs text-muted-foreground">生成用户名：{testResult.rendered_username_preview}</div>
                  )}
                </div>
              )}
            </div>
          </DialogContent>
        </Dialog>
      )}
    </div>
  )
}

function rotationModeLabel(mode: string) {
  switch (mode) {
    case "session-template":
      return "粘滞会话（会话 ID 轮换）"
    case "per-request":
      return "每请求轮换"
    case "api-list":
      return "API 提取（刷新节点）"
    case "cf-worker":
      return "CF Worker 面板（主动刷新）"
    case "hx-cf-wspxy":
      return "HX-CF-WsPxy（colo 出口）"
    default:
      return mode
  }
}

function parseRegionList(value: string): string[] {
  return Array.from(new Set(value.split(/[,\s]+/).map((item) => item.trim()).filter(Boolean)))
}

function regionModeLabel(mode: ResidentialRegionMode) {
  return mode === "application-random" ? "应用层随机地区" : "固定地区"
}

async function testProvider(
  id: string,
  setResult: (value: ResidentialTestResult) => void,
  onNotice: (message: string, tone?: "success" | "error") => void,
) {
  try {
    setResult(await api.testResidentialProvider(id))
  } catch (cause) {
    if (cause instanceof ApiError) onNotice(cause.message, "error")
  }
}

function ProviderDialog({
  presets,
  workerProtocols,
  proxyGroups,
  initial,
  onClose,
  onSaved,
  onNotice,
}: {
  presets: ResidentialPreset[]
  workerProtocols: ResidentialProtocol[]
  proxyGroups: ProxyGroup[]
  initial?: ResidentialProvider
  onClose: () => void
  onSaved: () => Promise<void>
  onNotice: (message: string, tone?: "success" | "error") => void
}) {
  const [form, setForm] = useState<ProviderForm>(() =>
    initial
      ? {
          name: initial.name,
          vendor: initial.vendor,
          protocol: initial.protocol,
          rotationMode: initial.rotation_mode,
          gatewayHost: initial.gateway_host,
          gatewayPort: String(initial.gateway_port),
          upstreamProxyGroupID: initial.upstream_proxy_group_id ?? "",
          apiProxyURL: "",
          // Extraction URLs may contain an app_key and are write-only. An
          // empty value on update tells the backend to keep the current URL.
          apiURL: "",
          // Cloudflare Worker panel links are write-only too: an empty value
          // on update keeps the configured link on the backend.
          workerURL: "",
          username: "",
          password: "",
          usernameTemplate: initial.username_template,
          sessionTTL: String(initial.session_ttl_seconds),
          maxSessions: String(initial.max_concurrent_sessions),
          expiryPolicy: initial.session_expiry_policy,
          defaultRegion: initial.default_region ?? "",
          defaultRegionMode: initial.default_region_mode ?? "fixed",
          defaultRandomRegions: (initial.default_random_regions ?? []).join(", "),
          enabled: initial.enabled,
        }
      : emptyProviderForm,
  )
  const [saving, setSaving] = useState(false)
  const [presetApplied, setPresetApplied] = useState(false)

  useEffect(() => {
    if (!initial && !presetApplied && presets.length > 0) {
      applyPreset("bestproxy")
      setPresetApplied(true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [presets, initial])

  function applyPreset(vendor: string) {
    if (vendor === "custom") {
      setForm((current) => ({ ...current, vendor: "custom" }))
      return
    }
    const preset = presets.find((item) => item.vendor === vendor)
    if (!preset) return
    setForm({
      ...emptyProviderForm,
      name: "",
      vendor: preset.vendor,
      protocol: preset.protocol,
      rotationMode: preset.rotation_mode,
      // cf-worker presets carry no gateway login: the panel subscription
      // link is the only secret. Other presets fall back to the BestProxy
      // gateway defaults as before.
      gatewayHost: (preset.rotation_mode === "cf-worker" || preset.rotation_mode === "hx-cf-wspxy") ? preset.gateway_host : (preset.gateway_host || "proxy.bestproxy.com"),
      gatewayPort: String(preset.gateway_port || ((preset.rotation_mode === "cf-worker" || preset.rotation_mode === "hx-cf-wspxy") ? 1 : 2312)),
      upstreamProxyGroupID: "",
      apiProxyURL: "",
      usernameTemplate: preset.username_template,
      sessionTTL: String(preset.session_ttl_seconds),
      maxSessions: "64",
      expiryPolicy: "rotate",
      defaultRegion: preset.vendor === "bestproxy" ? "US" : (preset.vendor === "rapidproxy" ? "GLOBAL" : ""),
      defaultRegionMode: "fixed",
      defaultRandomRegions: "",
    })
  }

  function update<K extends keyof ProviderForm>(key: K, value: ProviderForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    try {
      // api-list and cf-worker providers are fetched by the control plane:
      // their gateway columns hold placeholders and no gateway login exists.
      const fetchMode = form.rotationMode === "api-list" || form.rotationMode === "cf-worker" || form.rotationMode === "hx-cf-wspxy"
      const gatewayPlaceholder = form.rotationMode === "api-list"
        ? "api-list.invalid"
        : form.rotationMode === "hx-cf-wspxy"
          ? "hx-cf-wspxy.invalid"
          : "cf-worker.invalid"
      const base = {
        name: form.name.trim(),
        vendor: form.vendor.trim() || "custom",
        protocol: form.protocol,
        gateway_host: fetchMode ? gatewayPlaceholder : form.gatewayHost.trim(),
        gateway_port: fetchMode ? 1 : Number(form.gatewayPort),
        upstream_proxy_group_id: form.upstreamProxyGroupID === "none" ? undefined : form.upstreamProxyGroupID || undefined,
        api_url: (form.rotationMode === "api-list" || form.rotationMode === "hx-cf-wspxy") ? form.apiURL.trim() : undefined,
        worker_url: form.rotationMode === "cf-worker" ? form.workerURL.trim() : undefined,
        api_proxy_url: form.apiProxyURL.trim() || undefined,
        username_template: fetchMode ? "" : form.usernameTemplate.trim(),
        rotation_mode: form.rotationMode,
        session_ttl_seconds: Number(form.sessionTTL),
        max_concurrent_sessions: Number(form.maxSessions),
        session_expiry_policy: form.expiryPolicy,
        default_region: form.defaultRegionMode === "fixed" ? form.defaultRegion.trim() || undefined : undefined,
        default_region_mode: form.defaultRegionMode,
        default_random_regions: form.defaultRegionMode === "application-random"
          ? parseRegionList(form.defaultRandomRegions)
          : undefined,
        enabled: form.enabled,
      }
      const credentials = form.username && form.password ? { username: form.username, password: form.password } : undefined
      if (!credentials && !fetchMode && !initial) {
        throw new Error("账密网关模式需要填写用户名和密码")
      }
      if (initial) {
        await api.updateResidentialProvider(initial.id, { ...base, version: initial.version, credentials })
      } else {
        await api.createResidentialProvider({ ...base, credentials } as CreateResidentialProviderRequest)
      }
      await onSaved()
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
      else onNotice(String(cause), "error")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-2xl">
        <form onSubmit={(event) => void submit(event)}>
          <DialogHeader>
            <DialogTitle>{initial ? "编辑供应商" : "新建供应商"}</DialogTitle>
            <DialogDescription>
              {initial
                ? "新配置在下一次会话分配或到期换 IP 时生效；账密留空则保留原值。"
                : "选择预设会自动填入网关与用户名模板；保存后用「测试连接」确认出口 IP。"}
            </DialogDescription>
          </DialogHeader>

          <div className="grid gap-3 px-5 py-4 sm:grid-cols-2">
            <label className="grid gap-1 text-xs">
              预设
              <Select value={form.vendor} onValueChange={applyPreset}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {presets.map((preset) => (
                    <SelectItem key={preset.vendor} value={preset.vendor}>{preset.label}{preset.verified ? "" : "（未校验）"}</SelectItem>
                  ))}
                  <SelectItem value="custom">自定义</SelectItem>
                </SelectContent>
              </Select>
            </label>
            <label className="grid gap-1 text-xs">
              名称
              <Input value={form.name} onChange={(event) => update("name", event.target.value)} placeholder="例如 bestproxy-主账号" required />
            </label>
            <label className="grid gap-1 text-xs">
              轮换模式
              <Select value={form.rotationMode} onValueChange={(value) => {
                const mode = value as ResidentialRotationMode
                setForm((current) => ({
                  ...current,
                  rotationMode: mode,
                  protocol: mode === "hx-cf-wspxy" ? "http" : current.protocol,
                }))
              }}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="session-template">粘滞会话（会话 ID 轮换）</SelectItem>
                  <SelectItem value="per-request">每请求轮换</SelectItem>
                  <SelectItem value="api-list">API 提取（获取/刷新节点）</SelectItem>
                  <SelectItem value="cf-worker">CF Worker 面板（BPB）</SelectItem>
                  <SelectItem value="hx-cf-wspxy">HX-CF-WsPxy（CF colo 出口）</SelectItem>
                </SelectContent>
              </Select>
            </label>
            <label className="grid gap-1 text-xs">
              协议
              <Select value={form.protocol} onValueChange={(value) => update("protocol", value as ResidentialProtocol)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {form.rotationMode === "cf-worker" ? (
                    workerProtocols.map((protocol) => (
                      <SelectItem key={protocol} value={protocol}>{protocol.toUpperCase()}</SelectItem>
                    ))
                  ) : form.rotationMode === "hx-cf-wspxy" ? (
                    <SelectItem value="http">HTTP</SelectItem>
                  ) : (
                    <>
                      <SelectItem value="http">HTTP</SelectItem>
                      <SelectItem value="socks5">SOCKS5</SelectItem>
                      <SelectItem value="https">HTTPS</SelectItem>
                    </>
                  )}
                </SelectContent>
              </Select>
              {form.rotationMode !== "cf-worker" && (form.vendor === "bestproxy" || form.vendor === "bestproxy-api" || form.vendor === "rapidproxy") && form.protocol !== "http" && (
                <span className="text-[11px] text-amber-600">{form.vendor === "rapidproxy" ? "RapidProxy" : "BestProxy"} 出口节点按 HTTP 代理使用，请选回 HTTP</span>
              )}
            </label>

            {form.rotationMode === "hx-cf-wspxy" ? (
              <label className="grid gap-1 text-xs sm:col-span-2">
                HX-CF-WsPxy 控制面（只写入，不回显）
                <Input
                  value={form.apiURL}
                  onChange={(event) => update("apiURL", event.target.value)}
                  placeholder={initial?.api_url_configured
                    ? "已配置，留空保持当前地址；粘贴新 origin 可替换"
                    : "http://127.0.0.1:2470"}
                  required={!initial?.api_url_configured}
                />
                <span className="text-[11px] text-muted-foreground">
                  填本机 SessionPlane origin，例如 http://127.0.0.1:2470。控制面 POST /session 开会话，Mihomo 拨返回的 127.0.0.1 CONNECT 端口；next 调用 /rotate 换 colo pin。WSP1 只跑在 WsPxy 与 Worker 之间。
                </span>
              </label>
            ) : form.rotationMode === "api-list" ? (
              <label className="grid gap-1 text-xs sm:col-span-2">
                API 提取链接（只写入，不回显）
                <Input
                  value={form.apiURL}
                  onChange={(event) => update("apiURL", event.target.value)}
                  placeholder={initial?.api_url_configured
                    ? "已配置，留空保持当前链接；粘贴新链接可替换"
                    : "https://bestproxy.com/api/v2/...?app_key=...&num=8&cc=US&life=60&format=json"}
                  required={!initial?.api_url_configured}
                />
                  <span className="text-[11px] text-muted-foreground">
                  把供应商面板的完整 API 链接粘贴到这里；服务端会加密保存，不会在列表或响应中回显。客户端建立会话或换 IP 时才实时请求新的 IP:port 节点。
                  </span>
              </label>
            ) : form.rotationMode === "cf-worker" ? (
              <label className="grid gap-1 text-xs sm:col-span-2">
                CF Worker 面板链接（只写入，不回显）
                <Input
                  value={form.workerURL}
                  onChange={(event) => update("workerURL", event.target.value)}
                  placeholder={initial?.worker_url_configured
                    ? "已配置，留空保持当前链接；粘贴新链接可替换"
                    : "https://<worker-domain>/<securePath>/sub/raw?app=xray"}
                  required={!initial?.worker_url_configured}
                />
                <span className="text-[11px] text-muted-foreground">
                  把 BPB-Worker-Panel 部署后的面板链接或 raw 订阅链接粘贴到这里；服务端会加密保存，不会回显。客户端主动 next 时才重新请求该链接并轮换出口地址（TTL 为 0，不自动刷新）。
                </span>
              </label>
            ) : (
              <>
                <label className="grid gap-1 text-xs">
                  网关地址
                  <Input value={form.gatewayHost} onChange={(event) => update("gatewayHost", event.target.value)} placeholder="proxy.bestproxy.com" required />
                </label>
                <label className="grid gap-1 text-xs">
                  网关端口
                  <Input value={form.gatewayPort} onChange={(event) => update("gatewayPort", event.target.value)} inputMode="numeric" required />
                </label>
                <label className="grid gap-1 text-xs">
                  账号
                  <Input value={form.username} onChange={(event) => update("username", event.target.value)} placeholder="子用户账号" autoComplete="off" />
                </label>
                <label className="grid gap-1 text-xs">
                  密码
                  <Input value={form.password} onChange={(event) => update("password", event.target.value)} type="password" placeholder="子用户密码" autoComplete="new-password" />
                </label>
                <label className="grid gap-1 text-xs sm:col-span-2">
                  用户名模板
                  <Input value={form.usernameTemplate} onChange={(event) => update("usernameTemplate", event.target.value)} className="font-mono" />
                  <span className="text-[11px] text-muted-foreground">
                    支持 {"{user}"} {"{session}"} {"{region}"} {"{country}"} {"{city}"} {"{ttl}"}；BestProxy 预设为 {"{user}_area-{region}_life-{ttl}_session-{session}"}，RapidProxy 预设为 {"{user}-residential-{region}-session-{session}-stime-{ttl}"}，默认地区可按需修改。
                  </span>
                </label>
              </>
            )}

            <label className="grid gap-1 text-xs sm:col-span-2">
              上游海外 Proxy Group（链式代理）
              <Select value={form.upstreamProxyGroupID || "none"} onValueChange={(value) => update("upstreamProxyGroupID", value === "none" ? "" : value)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">不使用上游，直接连接住宅网关</SelectItem>
                  {proxyGroups.filter((group) => group.enabled).map((group) => (
                    <SelectItem key={group.id} value={group.id}>{group.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <span className="text-[11px] text-muted-foreground">实际链路为 listener → 住宅节点 → 上游组 → BestProxy；上游组请选可访问海外网络的已启用组。</span>
            </label>

            <label className="grid gap-1 text-xs sm:col-span-2">
              API 上游代理（可选）
              <Input
                value={form.apiProxyURL}
                onChange={(event) => update("apiProxyURL", event.target.value)}
                placeholder={initial?.api_proxy_configured ? "已配置，留空保持当前代理；支持 http://、https://、socks5://" : "例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080"}
                autoComplete="off"
              />
              <span className="text-[11px] text-muted-foreground">只用于 BestProxy API 提取和服务端测试连接；服务端加密保存，不会回显。</span>
            </label>

            <label className="grid gap-1 text-xs">
              {form.vendor === "bestproxy" ? "life（分钟）" : form.vendor === "rapidproxy" ? "stime（分钟）" : "会话 TTL（秒）"}
              <Input value={form.sessionTTL} onChange={(event) => update("sessionTTL", event.target.value)} inputMode="numeric" />
            </label>
            <label className="grid gap-1 text-xs">
              最大并发住宅会话
              <Input value={form.maxSessions} onChange={(event) => update("maxSessions", event.target.value)} inputMode="numeric" />
            </label>
            <label className="grid gap-1 text-xs">
              IP 到期处理
              <Select value={form.expiryPolicy} onValueChange={(value) => update("expiryPolicy", value as ResidentialSessionExpiryPolicy)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="rotate">保留客户端会话并换一个 IP</SelectItem>
                  <SelectItem value="expire">终止客户端会话</SelectItem>
                </SelectContent>
              </Select>
            </label>
            <label className="grid gap-1 text-xs">
              默认地区策略
              <Select value={form.defaultRegionMode} onValueChange={(value) => update("defaultRegionMode", value as ResidentialRegionMode)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="fixed">固定地区</SelectItem>
                  <SelectItem value="application-random">应用层随机地区</SelectItem>
                </SelectContent>
              </Select>
            </label>
            {form.defaultRegionMode === "fixed" ? (
              <label className="grid gap-1 text-xs">
                默认地区 / area
                <Input value={form.defaultRegion} onChange={(event) => update("defaultRegion", event.target.value)} placeholder="如 US（留空使用提取链接）" />
              </label>
            ) : (
              <label className="grid gap-1 text-xs">
                随机候选地区
                <Input value={form.defaultRandomRegions} onChange={(event) => update("defaultRandomRegions", event.target.value)} placeholder="如 US, JP, GB" />
              </label>
            )}
            <span className="text-[11px] text-muted-foreground sm:col-span-2">
              应用层随机会在每次获取住宅 IP 前使用密码学安全随机数从候选地区中选择，并覆盖提取链接中的 cc/country/region 参数。
            </span>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={saving}><Wrench className="mr-1 size-3.5" />{saving ? "保存中…" : "保存"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function ChannelDialog({
  providers,
  initial,
  onClose,
  onSaved,
  onNotice,
}: {
  providers: ResidentialProvider[]
  initial?: ResidentialChannel
  onClose: () => void
  onSaved: () => Promise<void>
  onNotice: (message: string, tone?: "success" | "error") => void
}) {
  const [form, setForm] = useState<ChannelForm>(() => {
    if (initial) {
      return {
        name: initial.name,
        providerID: initial.provider_id,
        providerIDs: (initial.providers ?? []).map((provider) => provider.id),
        mode: initial.mode,
        protocol: (isResidentialWebSocketKind(initial.endpoint.kind) ? initial.endpoint.kind : "vless") as ChannelForm["protocol"],
        regionMode: initial.region_mode ?? "fixed",
        region: initial.region ?? "",
        randomRegions: (initial.random_regions ?? []).join(", "),
        publicHost: initial.public_endpoint?.host ?? "",
        sessionCount: String(initial.session_count ?? 0),
        idleReleaseSeconds: String(initial.idle_release_seconds ?? 0),
        preallocate: initial.preallocate ?? false,
        enabled: initial.enabled,
      }
    }
    const provider = providers.find((item) => item.enabled) ?? providers[0]
    return {
      ...emptyChannelForm,
      providerID: provider?.id ?? "",
      providerIDs: [],
      regionMode: provider?.default_region_mode ?? "fixed",
      region: provider?.default_region ?? "",
      randomRegions: (provider?.default_random_regions ?? []).join(", "),
    }
  })
  const [saving, setSaving] = useState(false)

  function update<K extends keyof ChannelForm>(key: K, value: ChannelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
  }

  function selectProvider(providerID: string) {
    const provider = providers.find((item) => item.id === providerID)
    setForm((current) => ({
      ...current,
      providerID,
      regionMode: provider?.default_region_mode ?? "fixed",
      region: provider?.default_region ?? "",
      randomRegions: (provider?.default_random_regions ?? []).join(", "),
    }))
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!form.providerID) {
      onNotice("请先选择供应商", "error")
      return
    }
    if (form.mode === "sticky" && Number(form.sessionCount) < 1) {
      onNotice("粘滞渠道至少需要 1 个客户端节点", "error")
      return
    }
    setSaving(true)
    try {
      // A WebSocket channel always publishes through the reverse proxy, so its
      // public host is required. A legacy direct channel may keep the endpoint
      // it already has when the field is left blank.
      const publicEndpoint = form.publicHost.trim()
        ? { host: form.publicHost.trim(), port: 443, tls: true }
        : undefined
      if (initial) {
        const payload: UpdateResidentialChannelRequest = {
          version: initial.version,
          name: form.name.trim(),
          region_mode: form.regionMode,
          region: form.regionMode === "fixed" ? form.region.trim() || undefined : undefined,
          random_regions: form.regionMode === "application-random" ? parseRegionList(form.randomRegions) : undefined,
          session_count: initial.mode === "sticky" ? Number(form.sessionCount) : undefined,
          idle_release_seconds: initial.mode === "sticky" ? Number(form.idleReleaseSeconds) : undefined,
          preallocate: initial.mode === "sticky" ? form.preallocate : undefined,
          public_endpoint: publicEndpoint,
          enabled: form.enabled,
        }
        await api.updateResidentialChannel(initial.id, payload)
      } else {
        const payload: CreateResidentialChannelRequest = {
          name: form.name.trim(),
          provider_id: form.providerID,
          provider_ids: form.providerIDs,
          mode: form.mode,
          protocol: form.protocol,
          region_mode: form.regionMode,
          region: form.regionMode === "fixed" ? form.region.trim() || undefined : undefined,
          random_regions: form.regionMode === "application-random" ? parseRegionList(form.randomRegions) : undefined,
          session_count: form.mode === "sticky" ? Number(form.sessionCount) : 0,
          idle_release_seconds: form.mode === "sticky" ? Number(form.idleReleaseSeconds) : 0,
          preallocate: form.mode === "sticky" ? form.preallocate : false,
          public_endpoint: publicEndpoint ?? { host: form.publicHost.trim(), port: 443, tls: true },
          enabled: form.enabled,
        }
        await api.createResidentialChannel(payload)
      }
      await onSaved()
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
      else onNotice(String(cause), "error")
    } finally {
      setSaving(false)
    }
  }

  const editing = Boolean(initial)

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-2xl">
        <form onSubmit={(event) => void submit(event)}>
          <DialogHeader>
            <DialogTitle>{editing ? "编辑渠道" : "新建渠道"}</DialogTitle>
            <DialogDescription>
              {editing
                ? "修改渠道的地区、节点数量或空闲释放策略；节点凭据与已分配出口保持不变。"
                : "粘滞渠道发布固定客户端节点；出口 IP 由服务端按 TTL、空闲策略和 next 请求轮换。"}
            </DialogDescription>
          </DialogHeader>

          <div className="grid gap-3 px-5 py-4 sm:grid-cols-2">
            <label className="grid gap-1 text-xs">
              名称
              <Input value={form.name} onChange={(event) => update("name", event.target.value)} placeholder="例如 outlook-渠道1" required />
            </label>
            <label className="grid gap-1 text-xs">
              供应商
              {editing ? (
                <Input value={initial?.provider_name ?? form.providerID} disabled readOnly />
              ) : (
                <Select value={form.providerID} onValueChange={selectProvider}>
                  <SelectTrigger><SelectValue placeholder="选择供应商" /></SelectTrigger>
                  <SelectContent>
                    {providers.map((provider) => (
                      <SelectItem key={provider.id} value={provider.id}>{provider.name}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </label>
            <label className="grid gap-1 text-xs">
              聚合节点（CF-渠道 挂多个 CF-Node，可选）
              <div className="max-h-32 overflow-y-auto rounded-md border border-border p-1.5">
                {providers
                  .filter((provider) => provider.id !== form.providerID)
                  .map((provider) => (
                    <label key={provider.id} className="flex cursor-pointer items-center gap-1.5 px-1 py-0.5 text-xs hover:bg-surface">
                      <input
                        type="checkbox"
                        className="h-3 w-3"
                        checked={form.providerIDs.includes(provider.id)}
                        onChange={(event) => {
                          const next = event.target.checked
                            ? [...form.providerIDs, provider.id]
                            : form.providerIDs.filter((id) => id !== provider.id)
                          update("providerIDs", next)
                        }}
                      />
                      {provider.name}
                    </label>
                  ))}
              </div>
            </label>
            <label className="grid gap-1 text-xs">
              模式
              <Select value={form.mode} disabled={editing} onValueChange={(value) => update("mode", value as ResidentialChannelMode)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="sticky">粘滞（可轮换 IP）</SelectItem>
                  <SelectItem value="passthrough">透传（供应商自行轮换）</SelectItem>
                </SelectContent>
              </Select>
            </label>
            <label className="grid gap-1 text-xs">
              客户端协议
              <Select value={form.protocol} disabled={editing} onValueChange={(value) => update("protocol", value as ChannelForm["protocol"])}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="vless">VLESS over WebSocket</SelectItem>
                  <SelectItem value="vmess">VMess over WebSocket</SelectItem>
                  <SelectItem value="trojan">Trojan over WebSocket</SelectItem>
                </SelectContent>
              </Select>
            </label>
            {form.mode === "sticky" && (
              <>
                <label className="grid gap-1 text-xs">
                  节点数量
                  <Input value={form.sessionCount} onChange={(event) => update("sessionCount", event.target.value)} inputMode="numeric" min={1} required />
                  <span className="text-[11px] text-muted-foreground">每个逻辑节点拥有独立且稳定的客户端凭据；减少数量会从尾部释放，已有节点凭据保持不变。</span>
                </label>
                <label className="grid gap-1 text-xs">
                  空闲释放（秒）
                  <Input value={form.idleReleaseSeconds} onChange={(event) => update("idleReleaseSeconds", event.target.value)} inputMode="numeric" min={0} />
                  <span className="text-[11px] text-muted-foreground">0 表示保持分配；换 IP 不改变节点凭据。</span>
                </label>
                <label className="flex items-center gap-2 text-xs">
                  <input type="checkbox" checked={form.preallocate} onChange={(event) => update("preallocate", event.target.checked)} />
                  创建时立即预分配全部出口 IP
                  <span className="text-[11px] text-muted-foreground">默认关闭：懒分配——客户端首次请求到达时才分配，避免一次性分配 N 个 IP 阻塞创建（20260821）。</span>
                </label>
              </>
            )}
            <label className="grid gap-1 text-xs">
              地区策略
              <Select value={form.regionMode} onValueChange={(value) => update("regionMode", value as ResidentialRegionMode)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="fixed">固定地区</SelectItem>
                  <SelectItem value="application-random">应用层随机地区</SelectItem>
                </SelectContent>
              </Select>
            </label>
            {form.regionMode === "fixed" ? (
              <label className="grid gap-1 text-xs">
                地区 / area
                <Input value={form.region} onChange={(event) => update("region", event.target.value)} placeholder="如 US（留空使用供应商默认）" />
              </label>
            ) : (
              <label className="grid gap-1 text-xs">
                随机候选地区
                <Input value={form.randomRegions} onChange={(event) => update("randomRegions", event.target.value)} placeholder="如 US, JP, GB" />
              </label>
            )}
            <div className="rounded-md border bg-muted/50 px-3 py-2 text-xs sm:col-span-2">
              <div className="font-medium">{form.protocol.toUpperCase()} over WebSocket · TLS</div>
              <div className="mt-1 text-[11px] text-muted-foreground">内部环回端口、WebSocket 路径和节点凭据由服务端自动生成，不对客户端暴露。</div>
            </div>
            <label className="grid gap-1 text-xs sm:col-span-2">
              Cloudflare / 雷池域名
              <Input
                value={form.publicHost}
                onChange={(event) => update("publicHost", event.target.value)}
                placeholder="proxy.example.com"
                required={!editing || isResidentialWebSocketKind(initial?.endpoint.kind ?? "")}
              />
              <span className="text-[11px] text-muted-foreground">渠道订阅只发布该 HTTPS 443 域名；源站内部端口不会进入 API 或客户端配置。</span>
            </label>
            <label className="grid gap-1 text-xs sm:col-span-2">
              启用
              <Select value={form.enabled ? "enabled" : "disabled"} onValueChange={(value) => update("enabled", value === "enabled")}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="enabled">启用</SelectItem>
                  <SelectItem value="disabled">停用</SelectItem>
                </SelectContent>
              </Select>
            </label>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={saving}><Wrench className="mr-1 size-3.5" />{saving ? "保存中…" : editing ? "保存修改" : "创建"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function hasPublicEndpoint(channel: ResidentialChannel): boolean {
  return Boolean(channel.public_endpoint?.host?.trim()) && channel.public_endpoint.port > 0
}

function publicHostPort(channel: ResidentialChannel): string {
  const endpoint = channel.public_endpoint
  if (!hasPublicEndpoint(channel)) return "未配置公网端点"
  const host = endpoint.host.includes(":") && !endpoint.host.startsWith("[") ? `[${endpoint.host}]` : endpoint.host
  const defaultPort = endpoint.tls ? 443 : 80
  return endpoint.port === defaultPort ? host : `${host}:${endpoint.port}`
}

function formatPublicEndpoint(channel: ResidentialChannel): string {
  return hasPublicEndpoint(channel) ? `公网 ${publicHostPort(channel)}` : "未配置公网端点"
}

function isResidentialWebSocketKind(kind: string): boolean {
  return kind === "vless" || kind === "vmess" || kind === "trojan"
}

function ChannelEndpointDialog({
  channel,
  onClose,
  onSaved,
  onNotice,
}: {
  channel: ResidentialChannel
  onClose: () => void
  onSaved: () => Promise<void>
  onNotice: (message: string, tone?: "success" | "error") => void
}) {
  const [host, setHost] = useState(channel.public_endpoint?.host ?? "")
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    try {
      await api.updateResidentialChannel(channel.id, {
        version: channel.version,
        name: channel.name,
        region: channel.region,
        region_mode: channel.region_mode,
        random_regions: channel.random_regions,
        public_endpoint: {
          host: host.trim(),
          port: 443,
          tls: true,
        },
        enabled: channel.enabled,
      })
      await onSaved()
    } catch (cause) {
      if (cause instanceof ApiError) onNotice(cause.message, "error")
      else onNotice(String(cause), "error")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-lg">
        <form onSubmit={(event) => void submit(event)}>
          <DialogHeader>
            <DialogTitle>配置住宅公网端点</DialogTitle>
            <DialogDescription>这里是客户端实际连接的地址，不是 Mihomo 的本机监听地址。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 px-5 py-4 sm:grid-cols-2">
            <label className="grid gap-1 text-xs sm:col-span-2">
              公网主机名 / IP
              <Input value={host} onChange={(event) => setHost(event.target.value)} placeholder="proxy.example.com 或 VPS 公网 IP" autoComplete="off" required />
            </label>
            <div className="rounded-md border bg-muted/50 px-3 py-2 text-[11px] text-muted-foreground sm:col-span-2">
              公网路径固定走 {channel.endpoint.kind.toUpperCase()} over WebSocket、HTTPS 443 和 Edge Relay；渠道订阅不会暴露 Mihomo 内部端口。
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={saving}>{saving ? "保存中…" : "保存公网端点"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
