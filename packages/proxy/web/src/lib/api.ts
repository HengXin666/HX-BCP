import type {
  ApiErrorPayload,
  ArtifactKind,
  ArtifactList,
  ArtifactRecord,
  BatchRefreshList,
  CreateListenerRequest,
  CreateProxyGroupRequest,
  CreateProxyServiceRequest,
  CreateSubscriptionRequest,
  UpdateSubscriptionRequest,
  DataPlaneStatus,
  GlobalSettings,
  ListenerList,
  ListenerRecord,
  DockerAccessAssessment,
  DockerAccessLink,
  ProxyGroupType,
  NetworkAddresses,
  NodeCheckResult,
  NodeCheckList,
  NodeCheckProgress,
  NodeList,
  NodeRecord,
  ProxyGroup,
  ProxyGroupList,
  ProxyServiceRecord,
  RoutingRulesConfig,
  RefreshResult,
  CreateResidentialChannelRequest,
  CreateResidentialProviderRequest,
  ResidentialChannel,
  ResidentialChannelList,
  ResidentialPresetCatalog,
  ResidentialProvider,
  ResidentialProviderList,
  ResidentialRotationResult,
  ResidentialChannelSession,
  ResidentialTestResult,
  UpdateResidentialChannelRequest,
  UpdateResidentialProviderRequest,
  Subscription,
  SubscriptionList,
	SystemInfo,
  TrafficResourceType,
  TrafficSeries,
  TrafficSummaryList,
  UpdateListenerRequest,
  UpdateProxyGroupRequest,
  UpdateProxyServiceRequest,
  VerifyResult,
  AddFleetAccountRequest,
  FleetAccount,
  FleetStatus,
  FleetWorker,
} from "@/lib/types"

import { cacheableQuery, QueryCache } from "./query-cache"

const queryCache = new QueryCache()
export function invalidateQueries() { queryCache.clear() }

export class ApiError extends Error {
  readonly status: number
  readonly code?: string
  readonly requestId?: string

  constructor(message: string, status: number, code?: string, requestId?: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
    this.requestId = requestId
  }
}

export type NetworkSnapshot = {
  interfaces: Array<{ name: string; addresses: string[]; up: boolean; kind: string }>
  ipv4_forwarding: boolean
  tun_available: boolean
}
export type ActiveConnectionSnapshot = {
  items: Array<{ id: string; source_ip: string; source_port: string; destination: string; destination_port: string; network: string; inbound: string; origin: string; upload: number; download: number }>
  total: number
  sampled_at: string
}

let csrfToken = ""
let onUnauthenticated: (() => void) | null = null

export function setCsrfToken(token: string) {
  if (token !== csrfToken || !token) invalidateQueries()
  csrfToken = token
}

/** 供其它客户端模块复用同一份会话状态。 */
export function getCsrfToken(): string {
  return csrfToken
}

export function setUnauthenticatedHandler(handler: (() => void) | null) {
  onUnauthenticated = handler
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? "GET").toUpperCase()
  if (method === "GET") {
    if (!init && cacheableQuery(path)) return queryCache.read(path, () => fetchJSON<T>(path))
    return fetchJSON<T>(path, init)
  }
  invalidateQueries()
  try { return await fetchJSON<T>(path, init) }
  finally { invalidateQueries() }
}

async function fetchJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body && !headers.has("Content-Type") && !(init.body instanceof FormData)) {
    headers.set("Content-Type", "application/json")
  }
  const method = (init?.method ?? "GET").toUpperCase()
  if (csrfToken && method !== "GET" && method !== "HEAD") {
    headers.set("X-CSRF-Token", csrfToken)
  }

  const response = await fetch(path, {
    ...init,
    headers,
  })
  if (response.status === 401 && !path.startsWith("/api/v1/auth/")) {
    invalidateQueries()
    onUnauthenticated?.()
  }
  if (!response.ok) {
    let payload: ApiErrorPayload | undefined
    try {
      payload = (await response.json()) as ApiErrorPayload
    } catch {
      payload = undefined
    }
    throw new ApiError(
      payload?.error?.message || `请求失败：HTTP ${response.status}`,
      response.status,
      payload?.error?.code,
      payload?.error?.request_id,
    )
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

export type AlertRecord = {
  id: string
  rule: string
  target_id: string
  target_name: string
  severity: "warning" | "critical"
  status: "firing" | "resolved"
  message: string
  fired_at: string
  resolved_at?: string
  last_notified_at?: string
  notify_count: number
  acknowledged: boolean
}

export type AlertList = { items: AlertRecord[] }

export type AlertSettings = {
  enabled: boolean
  configured: boolean
  host?: string
  port?: number
  security?: string
  username?: string
  has_password: boolean
  from?: string
  to?: string[]
}

export type UpdateAlertSettingsRequest = {
  enabled: boolean
  host: string
  port: number
  security: string
  username: string
  password: string
  from: string
  to: string[]
}

export type TerminalStatus = {
  enabled: boolean
  active_sessions: number
  max_sessions: number
  idle_timeout_seconds: number
  max_lifetime_seconds: number
  privileged: boolean
  two_factor_configured: boolean
  two_factor_enabled: boolean
  two_factor_verified: boolean
  two_factor_verification_ttl_seconds: number
}

export type TerminalHostSample = {
  timestamp: string
  cpu_usage_pct: number
  cpu_count: number
  load1: number
  load5: number
  load15: number
  memory_total_bytes: number
  memory_used_bytes: number
  memory_cached_bytes: number
  swap_total_bytes: number
  swap_used_bytes: number
  net_rx_bytes_per_sec: number
  net_tx_bytes_per_sec: number
  processes: TerminalProcessResource[]
}

export type TerminalProcessResource = {
  name: string
  pid: number
  cpu_usage_pct: number
  memory_rss_bytes: number
}

export type SystemResourceSample = TerminalHostSample

export type DiskUsage = {
  filesystem: string
  size_bytes: number
  used_bytes: number
  avail_bytes: number
  use_percent: number
  mounted_on: string
}

export type DockerContainer = {
  id: string
  name: string
  image: string
  state: string
  status: string
  ports: string
  created: string
  cpu_perc?: string
  mem_usage?: string
  mem_perc?: string
  net_io?: string
  block_io?: string
}

export type TerminalFileEntry = {
  name: string
  size: number
  mode: string
  is_dir: boolean
  modified: string
}

export type TerminalFileList = {
  path: string
  parent: string
  entries: TerminalFileEntry[]
}

export type TwoFactorStatus = {
  configured: boolean
  enabled: boolean
  verified: boolean
  verification_ttl_seconds: number
}

export type TwoFactorSetup = {
  secret: string
  otpauth_url: string
}

export type NodeQualitySettings = {
  check_interval_seconds: number
  timeout_seconds: number
  batch_size: number
  probe_concurrency: number
  test_url: string
  health_targets: GlobalSettings["quality"]["health_targets"]
}

export type AuthStatus = {
  configured: boolean
  authenticated: boolean
  username?: string
  csrf_token?: string
}

export type APIKeyItem = {
  id: string
  name: string
  created_at: string
  last_used_at: string
  key?: string
}

export type LoginResult = {
  username: string
  csrf_token: string
  expires_at: string
}

export const api = {
  authStatus(): Promise<AuthStatus> {
    return request("/api/v1/auth/status")
  },

  authSetup(setupToken: string, username: string, password: string): Promise<void> {
    return request("/api/v1/auth/setup", {
      method: "POST",
      body: JSON.stringify({ setup_token: setupToken, username, password }),
    })
  },

  async login(username: string, password: string): Promise<LoginResult> {
    const result = await request<LoginResult>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    })
    setCsrfToken(result.csrf_token)
    return result
  },

  async logout(): Promise<void> {
    await request("/api/v1/auth/logout", { method: "POST" })
    setCsrfToken("")
  },

  async logoutAll(): Promise<void> {
    await request("/api/v1/auth/logout-all", { method: "POST" })
    setCsrfToken("")
  },

  changePassword(currentPassword: string, newPassword: string): Promise<void> {
    return request("/api/v1/auth/password", {
      method: "PUT",
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    })
  },

  changeUsername(currentPassword: string, newUsername: string): Promise<void> {
    return request("/api/v1/auth/username", {
      method: "PUT",
      body: JSON.stringify({ current_password: currentPassword, new_username: newUsername }),
    })
  },

  twoFactorStatus(): Promise<TwoFactorStatus> {
    return request("/api/v1/auth/2fa/status")
  },

  setupTwoFactor(): Promise<TwoFactorSetup> {
    return request("/api/v1/auth/2fa/setup", { method: "POST" })
  },

  enableTwoFactor(code: string): Promise<void> {
    return request("/api/v1/auth/2fa/enable", {
      method: "POST",
      body: JSON.stringify({ code }),
    })
  },

  disableTwoFactor(code: string): Promise<void> {
    return request("/api/v1/auth/2fa/disable", {
      method: "POST",
      body: JSON.stringify({ code }),
    })
  },

  verifyTwoFactor(code: string): Promise<void> {
    return request("/api/v1/auth/2fa/verify", {
      method: "POST",
      body: JSON.stringify({ code }),
    })
  },

  apiKeys(): Promise<{ items: APIKeyItem[] }> {
    return request("/api/v1/auth/api-keys")
  },

  apiKeyCreate(name: string): Promise<APIKeyItem> {
    return request("/api/v1/auth/api-keys", {
      method: "POST",
      body: JSON.stringify({ name }),
    })
  },

  apiKeyRevoke(id: string): Promise<void> {
    return request(`/api/v1/auth/api-keys/${encodeURIComponent(id)}`, { method: "DELETE" })
  },

  listAlerts(status?: "firing" | "resolved"): Promise<AlertList> {
    const query = new URLSearchParams({ limit: "200" })
    if (status) query.set("status", status)
    return request(`/api/v1/alerts?${query.toString()}`)
  },

  acknowledgeAlert(id: string): Promise<void> {
    return request(`/api/v1/alerts/${encodeURIComponent(id)}/ack`, { method: "POST" })
  },

  alertSettings(): Promise<AlertSettings> {
    return request("/api/v1/alerts/settings")
  },

  updateAlertSettings(payload: UpdateAlertSettingsRequest): Promise<AlertSettings> {
    return request("/api/v1/alerts/settings", {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  testAlertSettings(): Promise<void> {
    return request("/api/v1/alerts/settings/test", { method: "POST" })
  },

  terminalStatus(): Promise<TerminalStatus> {
    return request("/api/v1/terminal/status")
  },

  terminalSocketURL(): string {
    const scheme = window.location.protocol === "https:" ? "wss" : "ws"
    return `${scheme}://${window.location.host}/api/v1/terminal/ws`
  },
  terminalMetricsURL(): string {
    return "/api/v1/terminal/metrics"
  },
  listTerminalFiles(path: string): Promise<TerminalFileList> {
    const query = new URLSearchParams({ path })
    return request(`/api/v1/terminal/files?${query.toString()}`)
  },
  terminalFileDownloadURL(path: string): string {
    const query = new URLSearchParams({ path, op: "download" })
    return `/api/v1/terminal/files?${query.toString()}`
  },
  uploadTerminalFile(directory: string, file: File): Promise<{ saved: string[] }> {
    const form = new FormData()
    form.append("file", file, file.name)
    const query = new URLSearchParams({ path: directory })
    return request(`/api/v1/terminal/files?${query.toString()}`, {
      method: "POST",
      body: form,
    })
  },
  removeTerminalFile(path: string): Promise<{ removed: string }> {
    return request("/api/v1/terminal/files/remove", {
      method: "POST",
      body: JSON.stringify({ path }),
    })
  },
  mkdirTerminalFile(path: string): Promise<{ path: string }> {
    return request("/api/v1/terminal/files/mkdir", {
      method: "POST",
      body: JSON.stringify({ path }),
    })
  },

  async health(): Promise<boolean> {
    try {
      const result = await request<{ status: string }>("/health/ready")
      return result.status === "ready"
    } catch {
      return false
    }
  },

  systemInfo(): Promise<SystemInfo> {
    return request("/api/v1/system/info")
  },
  systemResources(): Promise<SystemResourceSample> {
    return request("/api/v1/system/resources")
  },
  systemDisk(): Promise<{ filesystems: DiskUsage[] }> {
    return request("/api/v1/system/disk")
  },
  dockerContainers(): Promise<{ containers: DockerContainer[] }> {
    return request("/api/v1/docker/containers")
  },

  triggerSystemUpdate(): Promise<{ accepted: boolean }> {
    return request("/api/v1/system/update", { method: "POST" })
  },

  /**
   * Lists every subscription. The server caps a page at 1000 rows and the list
   * endpoint takes no filters, so one page is enough for any real deployment;
   * the loop is a correctness guard, not a regular cost.
   */
  async listSubscriptions(): Promise<SubscriptionList> {
    // The subscriptions endpoint caps a page at 500 (nodes allows 1000); asking
    // for more is a 400, not a quietly smaller page.
    const pageSize = 500
    const items: Subscription[] = []
    for (let offset = 0; ; offset += pageSize) {
      const page = await request<SubscriptionList>(`/api/v1/subscriptions?limit=${pageSize}&offset=${offset}`)
      items.push(...page.items)
      if (page.items.length < pageSize) return { items, limit: pageSize, offset: 0 }
    }
  },

  createSubscription(payload: CreateSubscriptionRequest): Promise<Subscription> {
    return request("/api/v1/subscriptions", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  updateSubscription(id: string, payload: UpdateSubscriptionRequest): Promise<Subscription> {
    return request(`/api/v1/subscriptions/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  refreshSubscription(id: string): Promise<RefreshResult> {
    return request(`/api/v1/subscriptions/${encodeURIComponent(id)}/refresh`, {
      method: "POST",
    })
  },

  refreshSubscriptions(subscriptionIds: string[]): Promise<BatchRefreshList> {
    return request("/api/v1/subscriptions?action=refresh", {
      method: "POST",
      body: JSON.stringify({ subscription_ids: subscriptionIds }),
    })
  },

  /**
   * Lists nodes, paging until exhausted.
   *
   * This used to be a single `limit=1000` request. That silently truncated the
   * inventory: with 1349 nodes the page returned exactly 1000, so every count
   * derived from it — the `N 可用` badge on a service row, the "healthy" metric,
   * the per-subscription node counts — was computed over a partial set. The
   * pool service showed `0/493 可用` when it held 1305 members. The server caps
   * a page at 1000, so the only correct shape is to keep asking.
   *
   * Two knobs keep that cheap, both measured on a 2750-node inventory:
   *
   * - `health: false` asks for the summary projection. Attaching the per-node
   *   health-target samples costs ~240ms of a ~390ms page for 1000 nodes; the
   *   proxy-service member lists never render them, so they must not pay for
   *   them. The node inventory page keeps the full projection.
   * - `with_total` lets the remaining pages be fetched in parallel. Walking
   *   offsets serially costs one round trip per page (~0.44s + 0.19s + 0.23s
   *   measured); with the total from the first page the rest are concurrent.
   */
  async listNodes(
    filters?: { search?: string; protocol?: string; state?: string },
    options?: { health?: boolean },
  ): Promise<NodeList> {
    const pageSize = 1000
    const url = (offset: number, withTotal: boolean) => {
      const query = new URLSearchParams({ limit: String(pageSize), offset: String(offset) })
      if (filters?.search) query.set("search", filters.search)
      if (filters?.protocol) query.set("protocol", filters.protocol)
      if (filters?.state) query.set("state", filters.state)
      if (options?.health === false) query.set("view", "summary")
      if (withTotal) query.set("with_total", "1")
      return `/api/v1/nodes?${query.toString()}`
    }
    const first = await request<NodeList>(url(0, true))
    const items: NodeRecord[] = [...first.items]
    if (first.items.length < pageSize) return { items, limit: pageSize, offset: 0, total: items.length }
    if (typeof first.total !== "number") {
      // A server that does not answer `total` (an older control plane, or one
      // that ignored with_total) cannot be paged in parallel. Fall back to the
      // serial walk rather than treating the first page as the whole set: doing
      // the latter is exactly the silent truncation that made every derived
      // count wrong before 2026-10-01.
      for (let offset = pageSize; ; offset += pageSize) {
        const page = await request<NodeList>(url(offset, false))
        items.push(...page.items)
        if (page.items.length < pageSize) return { items, limit: pageSize, offset: 0, total: items.length }
      }
    }
    if (items.length >= first.total) return { items, limit: pageSize, offset: 0, total: first.total }
    const offsets: number[] = []
    for (let offset = pageSize; offset < first.total; offset += pageSize) offsets.push(offset)
    for (let index = 0; index < offsets.length; index += 4) {
      const pages = await Promise.all(offsets.slice(index, index + 4).map((offset) => request<NodeList>(url(offset, false))))
      for (const page of pages) items.push(...page.items)
    }
    return { items, limit: pageSize, offset: 0, total: first.total }
  },

  checkNode(id: string): Promise<NodeCheckResult> {
    return request(`/api/v1/nodes/${encodeURIComponent(id)}/check`, { method: "POST" })
  },

  disableNode(id: string): Promise<NodeRecord> {
    return request(`/api/v1/nodes/${encodeURIComponent(id)}/disable`, { method: "POST" })
  },

  enableNode(id: string): Promise<NodeRecord> {
    return request(`/api/v1/nodes/${encodeURIComponent(id)}/enable`, { method: "POST" })
  },

  nodeQualitySettings(): Promise<NodeQualitySettings> {
    return request("/api/v1/node-settings")
  },

  updateNodeQualitySettings(payload: NodeQualitySettings): Promise<NodeQualitySettings> {
    return request("/api/v1/node-settings", {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  globalSettings(): Promise<GlobalSettings> {
    return request("/api/v1/settings")
  },

  /**
   * Host addresses a container can use to reach a bound listener.
   *
   * A container's 127.0.0.1 is the container itself and the public endpoint is
   * normally a Cloudflare domain pointing at an edge, so neither can be copied
   * into a docker compose/env file. The docker bridge gateway (172.17.0.1 by
   * default) is reachable from every bridge network because the host routes
   * between its bridges; lan_addresses is the fallback when no bridge exists.
   */
  systemNetwork(): Promise<NetworkAddresses> {
    return request("/api/v1/system/network")
  },

  networkInspect(): Promise<NetworkSnapshot> { return request("/api/v1/system/network/inspect") },
  networkOperation(action: string, container_id = "", confirm = false): Promise<{ exit_code: number; stdout: string; stderr: string }> {
    return request("/api/v1/system/network/operations", { method: "POST", body: JSON.stringify({ action, container_id, confirm }) })
  },
  activeConnections(): Promise<ActiveConnectionSnapshot> { return request("/api/v1/connections") },

  updateGlobalSettings(payload: GlobalSettings): Promise<GlobalSettings> {
    return request("/api/v1/settings", {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  routingRules(): Promise<RoutingRulesConfig> {
    return request("/api/v1/routing-rules")
  },

  updateRoutingRules(payload: RoutingRulesConfig): Promise<RoutingRulesConfig> {
    return request("/api/v1/routing-rules", {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  checkNodes(nodeIds: string[]): Promise<NodeCheckList> {
    return request("/api/v1/nodes", {
      method: "POST",
      body: JSON.stringify({ node_ids: nodeIds }),
    })
  },

  async streamNodeChecks(
    nodeIds: string[],
    onProgress: (progress: NodeCheckProgress) => void,
    signal?: AbortSignal,
  ): Promise<void> {
    const headers = new Headers({ "Content-Type": "application/json", Accept: "application/x-ndjson" })
    if (csrfToken) headers.set("X-CSRF-Token", csrfToken)
    const response = await fetch("/api/v1/nodes?stream=1", {
      method: "POST",
      headers,
      body: JSON.stringify({ node_ids: nodeIds }),
      signal,
    })
    if (response.status === 401) onUnauthenticated?.()
    if (!response.ok) {
      let payload: ApiErrorPayload | undefined
      try {
        payload = (await response.json()) as ApiErrorPayload
      } catch {
        payload = undefined
      }
      throw new ApiError(
        payload?.error?.message || `请求失败：HTTP ${response.status}`,
        response.status,
        payload?.error?.code,
        payload?.error?.request_id,
      )
    }
    if (!response.body) throw new ApiError("测速进度响应不可读", response.status)

    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
    let buffer = ""
    for (;;) {
      const { value, done } = await reader.read()
      buffer += value ?? ""
      const lines = buffer.split("\n")
      buffer = lines.pop() ?? ""
      for (const line of lines) {
        if (line.trim()) onProgress(JSON.parse(line) as NodeCheckProgress)
      }
      if (done) break
    }
    if (buffer.trim()) onProgress(JSON.parse(buffer) as NodeCheckProgress)
  },

  deleteSubscription(id: string, version: number): Promise<void> {
    return request(
      `/api/v1/subscriptions/${encodeURIComponent(id)}?version=${encodeURIComponent(version)}`,
      { method: "DELETE" },
    )
  },

  listProxyGroups(types?: ProxyGroupType[]): Promise<ProxyGroupList> {
    const query = types && types.length > 0 ? `?type=${types.map(encodeURIComponent).join(",")}` : ""
    return request(`/api/v1/proxy-groups${query}`)
  },

  createProxyGroup(payload: CreateProxyGroupRequest): Promise<ProxyGroup> {
    return request("/api/v1/proxy-groups", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  updateProxyGroup(id: string, payload: UpdateProxyGroupRequest): Promise<ProxyGroup> {
    return request(`/api/v1/proxy-groups/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  deleteProxyGroup(id: string, version: number): Promise<void> {
    return request(`/api/v1/proxy-groups/${encodeURIComponent(id)}?version=${version}`, {
      method: "DELETE",
    })
  },

  listListeners(): Promise<ListenerList> {
    return request("/api/v1/listeners")
  },

  createListener(payload: CreateListenerRequest): Promise<ListenerRecord> {
    return request("/api/v1/listeners", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  createProxyService(payload: CreateProxyServiceRequest): Promise<ProxyServiceRecord> {
    return request("/api/v1/proxy-services", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  updateProxyService(payload: UpdateProxyServiceRequest): Promise<ProxyServiceRecord> {
    return request(`/api/v1/proxy-services/${encodeURIComponent(payload.group_id)}`, {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  updateListener(id: string, payload: UpdateListenerRequest): Promise<ListenerRecord> {
    return request(`/api/v1/listeners/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  rotateListenerShare(id: string): Promise<ListenerRecord> {
    return request(`/api/v1/listeners/${encodeURIComponent(id)}/rotate-share`, {
      method: "POST",
    })
  },

  listenerShareURL(sharePath: string, format?: "v2rayn" | "clash" | "sing-box" | "uri"): string {
    const suffix = format ? `?format=${encodeURIComponent(format)}` : ""
    return `${window.location.protocol}//${window.location.host}${sharePath}${suffix}`
  },

  async listenerShareContent(sharePath: string): Promise<string> {
    const response = await fetch(api.listenerShareURL(sharePath, "uri"))
    if (!response.ok) {
      let payload: ApiErrorPayload | undefined
      try {
        payload = (await response.json()) as ApiErrorPayload
      } catch {
        payload = undefined
      }
      throw new ApiError(
        payload?.error?.message || `请求失败：HTTP ${response.status}`,
        response.status,
        payload?.error?.code,
        payload?.error?.request_id,
      )
    }
    return response.text()
  },

  dockerAccess(id: string, gateway?: string): Promise<DockerAccessAssessment> {
    const query = gateway ? `?gateway=${encodeURIComponent(gateway)}` : ""
    return request(`/api/v1/listeners/${encodeURIComponent(id)}/docker-access${query}`, { method: "GET" })
  },

  applyDockerAccess(id: string, payload: { plan_id: string; gateway?: string; confirm: true }): Promise<DockerAccessAssessment> {
    return request(`/api/v1/listeners/${encodeURIComponent(id)}/docker-access`, {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  revokeDockerAccess(id: string): Promise<void> {
    return request(`/api/v1/listeners/${encodeURIComponent(id)}/docker-access`, { method: "DELETE" })
  },

  dockerAccessLinks(): Promise<{ items: DockerAccessLink[] }> {
    return request("/api/v1/docker-access", { method: "GET" })
  },

  deleteListener(id: string, version: number): Promise<void> {
    return request(`/api/v1/listeners/${encodeURIComponent(id)}?version=${version}`, {
      method: "DELETE",
    })
  },

  dataPlaneStatus(): Promise<DataPlaneStatus> {
    return request("/api/v1/dataplane/status")
  },

  applyDataPlane(): Promise<DataPlaneStatus> {
    return request("/api/v1/dataplane/apply", { method: "POST" })
  },

  overviewStreamURL(): string {
    return "/api/v1/overview/stream"
  },

  trafficSeries(resourceType: TrafficResourceType, resourceId: string, hours = 24): Promise<TrafficSeries> {
    const to = new Date()
    const from = new Date(to.getTime() - hours * 60 * 60 * 1000)
    const query = new URLSearchParams({
      resource_type: resourceType,
      resource_id: resourceId,
      from: from.toISOString(),
      to: to.toISOString(),
      max_points: "96",
    })
    return request(`/api/v1/traffic?${query.toString()}`)
  },

  async trafficSummaries(resourceType: TrafficResourceType, from?: Date, to?: Date): Promise<TrafficSummaryList> {
    // The server caps summaries at 1000 rows, so a single request returns the
    // full set for typical deployments; keep a paging loop as a safe fallback
    // for larger inventories instead of always paying 5 sequential round trips.
    const items: TrafficSummaryList["items"] = []
    const pageSize = 1000
    for (let offset = 0; offset < pageSize; offset += pageSize) {
      const query = new URLSearchParams({ resource_type: resourceType, limit: String(pageSize), offset: String(offset) })
      if (from) query.set("from", from.toISOString())
      if (to) query.set("to", to.toISOString())
      const page = await request<TrafficSummaryList>(`/api/v1/traffic?${query.toString()}`)
      items.push(...page.items)
      if (page.items.length < pageSize) break
    }
    return { items, limit: pageSize, offset: 0 }
  },

  proxyLogURL(filters: { listenerId?: string; proxyGroupId?: string; nodeId?: string; level?: string }): string {
    const query = new URLSearchParams()
    if (filters.listenerId) query.set("listener_id", filters.listenerId)
    if (filters.proxyGroupId) query.set("proxy_group_id", filters.proxyGroupId)
    if (filters.nodeId) query.set("node_id", filters.nodeId)
    if (filters.level) query.set("level", filters.level)
    return `/api/v1/logs/stream?${query.toString()}`
  },

  listArtifacts(kind: ArtifactKind): Promise<ArtifactList> {
    return request(`/api/v1/${kind === "backup" ? "backups" : "exports"}`)
  },

  createArtifact(kind: ArtifactKind, description: string): Promise<ArtifactRecord> {
    return request(`/api/v1/${kind === "backup" ? "backups" : "exports"}`, {
      method: "POST",
      body: JSON.stringify({ description, include_secrets: false }),
    })
  },

  verifyArtifact(kind: ArtifactKind, id: string): Promise<VerifyResult> {
    const collection = kind === "backup" ? "backups" : "exports"
    return request(`/api/v1/${collection}/${encodeURIComponent(id)}/verify`, {
      method: "POST",
    })
  },

  deleteArtifact(kind: ArtifactKind, id: string): Promise<void> {
    const collection = kind === "backup" ? "backups" : "exports"
    return request(`/api/v1/${collection}/${encodeURIComponent(id)}`, {
      method: "DELETE",
    })
  },

  artifactDownloadURL(kind: ArtifactKind, id: string): string {
    const collection = kind === "backup" ? "backups" : "exports"
    return `/api/v1/${collection}/${encodeURIComponent(id)}/download`
  },

  residentialPresets(): Promise<ResidentialPresetCatalog> {
    return request("/api/v1/residential/presets")
  },

  listResidentialProviders(): Promise<ResidentialProviderList> {
    return request("/api/v1/residential/providers")
  },

  createResidentialProvider(payload: CreateResidentialProviderRequest): Promise<ResidentialProvider> {
    return request("/api/v1/residential/providers", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  updateResidentialProvider(
    id: string,
    payload: UpdateResidentialProviderRequest,
  ): Promise<ResidentialProvider> {
    return request(`/api/v1/residential/providers/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  deleteResidentialProvider(id: string, version: number): Promise<void> {
    return request(`/api/v1/residential/providers/${encodeURIComponent(id)}?version=${version}`, {
      method: "DELETE",
    })
  },

  testResidentialProvider(id: string, exitIPEndpoint?: string): Promise<ResidentialTestResult> {
    return request(`/api/v1/residential/providers/${encodeURIComponent(id)}/test`, {
      method: "POST",
      body: JSON.stringify(exitIPEndpoint ? { exit_ip_endpoint: exitIPEndpoint } : {}),
    })
  },

  listResidentialChannels(): Promise<ResidentialChannelList> {
    return request("/api/v1/residential/channels")
  },

  createResidentialChannel(payload: CreateResidentialChannelRequest): Promise<ResidentialChannel> {
    return request("/api/v1/residential/channels", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  updateResidentialChannel(
    id: string,
    payload: UpdateResidentialChannelRequest,
  ): Promise<ResidentialChannel> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  deleteResidentialChannel(id: string, version: number): Promise<void> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}?version=${version}`, {
      method: "DELETE",
    })
  },

  rotateResidentialChannel(id: string): Promise<ResidentialRotationResult> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}/rotate`, {
      method: "POST",
    })
  },

  rotateResidentialChannelToken(id: string): Promise<ResidentialChannel> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}/rotate-token`, {
      method: "POST",
    })
  },

  rotateResidentialShareToken(id: string): Promise<ResidentialChannel> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}/rotate-share`, {
      method: "POST",
    })
  },

  rotateResidentialControlToken(id: string): Promise<ResidentialChannel> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}/rotate-control`, {
      method: "POST",
    })
  },

  rotateResidentialSession(id: string, index: number): Promise<ResidentialChannelSession> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}/sessions/${index}/next`, {
      method: "POST",
    })
  },

  refreshResidentialChannelPool(id: string): Promise<ResidentialChannel> {
    return request(`/api/v1/residential/channels/${encodeURIComponent(id)}/refresh-pool`, {
      method: "POST",
    })
  },

  // ---- Fleet（CF 渠道账号）----
  fleetStatus(): Promise<FleetStatus> {
    return request("/api/v1/fleet/status")
  },

  fleetAccounts(): Promise<{ accounts: FleetAccount[] }> {
    return request("/api/v1/fleet/accounts")
  },

  fleetAddAccount(payload: AddFleetAccountRequest): Promise<FleetAccount> {
    return request("/api/v1/fleet/accounts", {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  fleetDeleteAccount(id: string): Promise<{ deleted: string }> {
    return request(`/api/v1/fleet/accounts/${encodeURIComponent(id)}`, {
      method: "DELETE",
    })
  },

  fleetSweep(): Promise<{ started: boolean }> {
    return request("/api/v1/fleet/sweep", { method: "POST" })
  },
}
