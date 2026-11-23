import { test } from "node:test"
import assert from "node:assert/strict"
import { QueryCache, cacheableQuery } from "../src/lib/query-cache.ts"

test("deduplicates, isolates consumers, expires and invalidates", async () => {
  let now = 0, calls = 0
  const cache = new QueryCache(5, 2, () => now)
  const load = async () => ({ count: ++calls })
  const [a, b] = await Promise.all([cache.read("a", load), cache.read("a", load)])
  a.count = 99
  assert.equal(b.count, 1)
  now = 6
  assert.equal((await cache.read("a", load)).count, 2)
  cache.clear()
  assert.equal((await cache.read("a", load)).count, 3)
  await cache.read("b", load); await cache.read("c", load)
  assert.equal((await cache.read("a", load)).count, 6)
})

test("errors and invalidated in-flight reads cannot repopulate cache", async () => {
  const cache = new QueryCache()
  await assert.rejects(cache.read("a", async () => { throw new Error("offline") }))
  let finish!: (value: number) => void
  const old = cache.read("a", () => new Promise<number>((resolve) => { finish = resolve }))
  cache.clear()
  await cache.read("a", async () => 2)
  finish(1); await old
  assert.equal(await cache.read("a", async () => 3), 2)
})

test("only resource lists are cached", () => {
  assert.ok(cacheableQuery("/api/v1/nodes?limit=1000"))
  for (const path of ["/api/v1/auth/status", "/api/v1/settings", "/api/v1/listeners/id/credentials", "/api/v1/system/network", "/api/v1/connections"]) assert.equal(cacheableQuery(path), false)
})
