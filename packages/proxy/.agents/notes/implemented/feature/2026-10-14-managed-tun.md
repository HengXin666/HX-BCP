# Agent Note: Mihomo owns an explicitly enabled TUN

Status: implemented

## Problem

Operators need host-wide proxy routing without manually maintaining a second proxy client, but enabling it changes host routing and requires privileges.

## Decision

Settings carry an opt-in tun object; omitted updates preserve it. The compiler validates the chosen enabled group, requires DNS, generates hx-pxy-tun and an IN-TYPE,TUN default rule before MATCH. Private destinations are excluded; auto-redirect stays off. Apply readiness checks the actual interface and rolls back on failure. Only the optional data-plane systemd drop-in grants network capabilities; the installer does not activate it.

## Alternatives considered

- **Do nothing / reuse existing:** Keeping explicit listeners only has no host route risk, but cannot serve programs that ignore proxy environment variables.
- **Alternative:** Automatically granting capabilities and rewriting firewall rules would be turnkey, but silently enlarges privileges and can interrupt Docker and remote administration.

## Consequences

Host permissions remain an operator step. Site rules retain precedence. Other TUNs can still conflict; no privileged host mutation is part of tests. Parser and default/invalid-group tests are not proof of real host TUN connectivity.

## Testing

internal/systemsettings/tun_test.go; internal/dataplane/mihomo/compiler_tun_test.go; docs/NETWORK_OPERATIONS.md
