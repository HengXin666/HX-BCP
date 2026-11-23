// Session-local, bounded reads only; never persist credentials to browser storage.
// See .agents/notes/implemented/feature/2026-10-14-session-query-cache.md.
export class QueryCache {
  private entries = new Map<string, { expires: number; value: Promise<unknown> }>()

  private ttl: number
  private capacity: number
  private now: () => number
  constructor(ttl = 5_000, capacity = 128, now = Date.now) {
    this.ttl = ttl; this.capacity = capacity; this.now = now
  }

  clear() { this.entries.clear() }

  async read<T>(key: string, load: () => Promise<T>): Promise<T> {
    const existing = this.entries.get(key)
    if (existing && existing.expires > this.now()) return structuredClone(await existing.value) as T
    const entry = { expires: this.now() + this.ttl, value: load() as Promise<unknown> }
    this.entries.delete(key)
    this.entries.set(key, entry)
    while (this.entries.size > this.capacity) this.entries.delete(this.entries.keys().next().value!)
    try { return structuredClone(await entry.value) as T }
    catch (error) {
      if (this.entries.get(key) === entry) this.entries.delete(key)
      throw error
    }
  }
}

export function cacheableQuery(path: string): boolean {
  return /^\/api\/v1\/(nodes|subscriptions|proxy-groups|listeners|residential\/channels|routing-rules)(\?|$)/.test(path)
}
