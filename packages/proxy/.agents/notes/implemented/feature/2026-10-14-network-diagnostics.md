# Agent Note: Bounded and explicit host network operations

Status: implemented

## Problem

Administrators cannot explain Docker DNS/routing failures from listener configuration alone. A blind repair can destroy existing host firewall policy.

## Decision

The hostnetwork service owns interface inspection and three fixed commands: diagnose, enable-forwarding and disable-forwarding. A container selector is a 12-64-character hex ID. Execution uses the existing privileged helper, a single-flight guard, a 20-second command budget and cancellation. Repairs require confirmation and browser 2FA step-up (or an authenticated machine key); results are audited without command output.

## Alternatives considered

- **Do nothing / reuse existing:** Reusing the terminal alone is flexible and has no new endpoint, but requires users to assemble diagnostics and identify safe repairs themselves.
- **Alternative:** Automatically restarting Docker or flushing firewall rules can clear some faults, but disrupts unrelated workloads and loses administrator policy.

## Consequences

Repair only changes runtime IPv4 forwarding. It is not an all-purpose network optimizer; images without a shell or DNS tools produce an explicit diagnostic failure. Interface names recognize standard Docker bridges and sysfs TUN devices, not arbitrary CNI networks.

## Testing

internal/hostnetwork/service_test.go; internal/api/network_connections_test.go; web/e2e/network-routing-regression.mjs
