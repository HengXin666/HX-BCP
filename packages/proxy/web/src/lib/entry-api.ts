import { apiRequest } from "@/lib/api"

export type EntryPool = "resi" | "subresi" | "sub" | "direct"

export type EntryStatus = {
  listen: string
  listen_error?: string
  hub_address: string
  hub_error?: string
  exit_probe: boolean
  pools: Record<string, number>
  groups: string[]
  gateways: string[]
  connections: { active: number; served: number; failed: number }
}

export type EntryTenant = {
  id: string
  name: string
  // pools holds pool names plus "group" (every proxy group) or "group:<name>".
  pools: string[]
  resi_quota_bytes: number
  resi_used_bytes: number
  total_bytes: number
  enabled: boolean
  note: string
  version: number
  created_at: string
  updated_at: string
  secret?: string
}

export type EntryTenantRequest = {
  name?: string
  pools?: string[]
  resi_quota_bytes?: number
  enabled?: boolean
  note?: string
  rotate_secret?: boolean
  version?: number
}

export type EntryNode = {
  id: string
  display_name: string
  protocol: string
  pools: string[]
  country: string
  city: string
  asn: number
  alive: boolean
  residential: boolean
  profile_at?: string
}

export type EntrySlot = {
  tenant: string
  slot: string
  binding: { NodeID: string; BoundAt: string; ExpiresAt: string; LostAt: string; Rebinds: number }
}

export const entryApi = {
  status(): Promise<EntryStatus> {
    return apiRequest("/api/v1/entry", { method: "GET" })
  },
  nodes(pool = ""): Promise<{ items: EntryNode[] }> {
    const query = pool ? `?pool=${encodeURIComponent(pool)}` : ""
    return apiRequest(`/api/v1/entry/nodes${query}`, { method: "GET" })
  },
  tenants(): Promise<{ items: EntryTenant[] }> {
    return apiRequest("/api/v1/entry/tenants", { method: "GET" })
  },
  createTenant(body: EntryTenantRequest): Promise<EntryTenant> {
    return apiRequest("/api/v1/entry/tenants", { method: "POST", body: JSON.stringify(body) })
  },
  updateTenant(id: string, body: EntryTenantRequest): Promise<EntryTenant> {
    return apiRequest(`/api/v1/entry/tenants/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(body) })
  },
  deleteTenant(id: string): Promise<void> {
    return apiRequest(`/api/v1/entry/tenants/${encodeURIComponent(id)}`, { method: "DELETE" })
  },
  resetUsage(id: string): Promise<void> {
    return apiRequest(`/api/v1/entry/tenants/${encodeURIComponent(id)}/reset-usage`, { method: "POST" })
  },
  sharePath(id: string): Promise<{ path: string }> {
    return apiRequest(`/api/v1/entry/tenants/${encodeURIComponent(id)}/share`, { method: "GET" })
  },
  slots(tenant = ""): Promise<{ nodes: EntrySlot[]; residential: EntrySlot[] }> {
    const query = tenant ? `?tenant=${encodeURIComponent(tenant)}` : ""
    return apiRequest(`/api/v1/entry/slots${query}`, { method: "GET" })
  },
  releaseSlot(tenant: string, slot: string): Promise<void> {
    return apiRequest(`/api/v1/entry/slots/${encodeURIComponent(tenant)}/${encodeURIComponent(slot)}`, { method: "DELETE" })
  },
}

// entryUsername renders the entry username dialect. It mirrors
// internal/entry/username.go, which stays the single authority on parsing.
export function entryUsername(tenant: string, parts: { pool?: string; cc?: string; session?: string; ttl?: number }): string {
  const segments = [tenant]
  if (parts.pool) segments.push("pool", parts.pool)
  if (parts.cc) segments.push("cc", parts.cc.toLowerCase())
  if (parts.session) segments.push("session", parts.session)
  if (parts.session && parts.ttl) segments.push("ttl", String(parts.ttl))
  return segments.join("-")
}

export function formatBytes(value: number): string {
  if (!value) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB"]
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}
