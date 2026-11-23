// See .agents/notes/implemented/feature/2026-10-14-docker-reachable-address.md.
export function dockerEndpoint(listener: { kind: string; bind_address: string; port: number }, gateway: string): string | null {
  if (!["http", "socks5", "socks", "mixed"].includes(listener.kind)) return null
  const bind = listener.bind_address.trim()
  if (!bind || bind === "localhost" || bind === "::1" || bind.startsWith("127.")) return null
  const host = bind === "0.0.0.0" || bind === "::" ? gateway.trim() : bind
  if (!host || !Number.isInteger(listener.port) || listener.port < 1 || listener.port > 65535) return null
  return `${host.includes(":") ? `[${host}]` : host}:${listener.port}`
}

// Reuse the authenticated export, but dial the actual plain Mihomo socket,
// not its public TLS reverse proxy. Never return credentials in a notice.
export function dockerProxyURI(address: string, kind: string, content: string, authRequired: boolean): string {
  const scheme = kind === "socks" || kind === "socks5" ? "socks5h" : "http"
  const allowed = scheme === "http" ? ["http:", "https:"] : ["socks5:", "socks5h:"]
  const source = content.trim().split(/\r?\n/).map(line => new URL(line)).find(url => allowed.includes(url.protocol))
  if (!source || (authRequired && (!source.username || !source.password))) throw new Error("入口认证信息不完整，请检查服务凭据后重试")
  const credentials = source.username || source.password ? `${source.username}:${source.password}@` : ""
  return `${scheme}://${credentials}${address}`
}

// Builds every container URI an assessed entry supports (Mixed offers both).
// socks5h (not socks5) keeps DNS resolution on the proxy side, which is what
// a container behind a bridge usually needs.
export function dockerProxyURIs(endpoint: { host: string; port: number; schemes: string[] }, content: string, authRequired = true): Array<{ scheme: string; uri: string }> {
  const host = endpoint.host.includes(":") ? `[${endpoint.host}]` : endpoint.host
  const address = `${host}:${endpoint.port}`
  return endpoint.schemes.map(scheme => ({ scheme, uri: dockerProxyURI(address, scheme === "socks5h" ? "socks" : "http", content, authRequired) }))
}
