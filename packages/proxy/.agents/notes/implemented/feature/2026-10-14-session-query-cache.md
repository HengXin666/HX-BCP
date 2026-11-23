# Agent Note: Bounded session query reuse and independent routing inventory

Status: implemented

## Problem

Routing cannot show configured entries while waiting for every node health sample. Repeated navigation duplicates identical inventory reads.

## Decision

The API wrapper caches only six explicit inventory GET endpoints in memory for five seconds, caps entries at 128, deduplicates requests and clones returned values. Mutations and authentication transitions invalidate it. Node pagination has four concurrent requests at most. Routing loads inventory and runtime health independently from service rows.

## Alternatives considered

- **Do nothing / reuse existing:** Keeping every read fresh is simple and avoids staleness, but repeats identical reads during navigation.
- **Alternative:** Persisting full responses across sessions removes more requests, but risks retaining credentials and stale administrative state.

## Consequences

Other clients can change inventory during the five-second window. Refresh invalidates the cache; live connections and settings are never cached. There is no measured end-to-end latency claim.

## Testing

web/e2e/query-cache.test.ts; web/e2e/network-routing-regression.mjs

The release verification job runs npm run test:runtime to enforce cache, Docker copy and terminal-input contracts before packaging.
