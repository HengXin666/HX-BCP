# Agent Note: Active connections are a bounded transient snapshot

Status: implemented

## Problem

Aggregate byte counters do not identify currently active clients or distinguish local clients from containers. Persisting individual destinations creates unnecessary sensitive history.

## Decision

The existing metrics collector retains at most 500 credential-free connection details, with full count and sampling timestamp. Exact host/loopback IPs are local; Docker bridge CIDRs classify containers; remaining valid addresses are external. GET /connections is authenticated by the normal management middleware and no-store. The UI polls every three seconds only while expanded and reports stale samples.

## Alternatives considered

- **Do nothing / reuse existing:** Keeping only aggregate statistics minimizes metadata exposure, but cannot answer which clients are active.
- **Alternative:** Persisting all connections supports historical forensics, but adds sensitive destination storage and unbounded event volume to SQLite.

## Consequences

NAT, host networking and edge relays hide true source identity; external includes LAN clients. The display is a sample, not guaranteed live state, and only covers Mihomo-visible traffic.

## Testing

internal/metrics/connections_test.go; internal/api/network_connections_test.go; web/e2e/network-routing-regression.mjs
