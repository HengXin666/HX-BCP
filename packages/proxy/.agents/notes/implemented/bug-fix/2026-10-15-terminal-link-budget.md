# Agent Note: Terminal input and heartbeats tolerate transient stalls

Status: implemented

## Problem

A transient tunnel stall or a large paste can close an otherwise usable terminal WebSocket. Unlimited tolerance would instead leak abandoned sessions.

## Decision

Heartbeats run serially at 15-second intervals with a 30-second probe budget and require two consecutive failures. Successful probes reset the streak. Budgets are immutable per server, so tests do not race with active sessions by rewriting global defaults. The socket read limit is 256 KiB. Browser input batches keystrokes, splits pastes into bounded messages and clears pending input on disconnect, never replaying it into a replacement shell.

## Alternatives considered

- **Do nothing / keep immediate disconnect:** it releases dead peers promptly, but treats a single transient delay as a lost terminal.
- **Disable heartbeat and message limits:** this avoids false disconnects, but leaves dead sessions and input memory unbounded.

## Consequences

Dead connections take longer to release. A reconnect still creates a new shell; this is not session resumption. Input queued at disconnect is deliberately lost rather than unexpectedly executed later.

## Testing

internal/api/terminal_heartbeat_test.go and web/e2e/terminal-input.test.ts cover streak resets, cancellation, chunking and cleanup. Real lossy WAN stability is environment-dependent.
