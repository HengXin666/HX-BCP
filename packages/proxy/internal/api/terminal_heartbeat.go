package api

import (
	"context"
	"time"

	"github.com/coder/websocket"
)

// Terminal WebSocket link budget.
// See .agents/notes/implemented/bug-fix/2026-10-15-terminal-link-budget.md.
//
// These constants are tuned for a control plane that is reached over a lossy
// path (mobile networks, an HTTP/2 or Cloudflare tunnel in front of it). The
// previous values treated one dropped segment as a dead peer, which turned a
// single lost packet into a full terminal reconnect.
const (
	// terminalHeartbeatInterval is how often the server probes the browser.
	terminalHeartbeatInterval = 15 * time.Second
	// terminalHeartbeatTimeout bounds one probe's wait for the peer's pong. It
	// is deliberately larger than the interval: a retransmitted segment on a
	// congested link must be allowed to arrive before the probe is counted as
	// lost.
	terminalHeartbeatTimeout = 30 * time.Second
	// terminalConsecutivePingFailures is how many probes in a row must fail
	// before the peer is declared dead. One lost probe is not evidence of a
	// dead peer: TCP retransmission, a collapsed congestion window, or a
	// Cloudflare tunnel hiccup all produce a single missed pong. Two
	// consecutive misses do mean the path is gone and the session slot must be
	// released for the next reconnect, instead of leaking it forever.
	terminalConsecutivePingFailures = 2
	// terminalReadLimit caps a single client -> server WebSocket message. Input
	// frames carry JSON-wrapped terminal keystrokes and pastes; the client
	// chunks them (see web/src/lib/terminal-input.ts) so a normal paste never
	// approaches this limit. It is set to 256 KiB, well above the chunk size,
	// because a read limit that is too small does not merely reject one frame:
	// coder/websocket closes the whole connection with StatusMessageTooBig,
	// which the page can only report as an unstable terminal. The limit still
	// exists to bound memory and to reject a client that never chunks, and
	// 256 KiB leaves room for future frame types without being an unbounded
	// allocation.
	terminalReadLimit = int64(256 << 10)
)

// heartbeatDecision is the pure core of the server heartbeat: a peer is dead
// once the current streak of consecutive failed probes reaches the threshold.
// Extracted so the tolerance rule can be unit tested without a socket or a
// clock. The caller increments the streak for every failed probe and resets it
// to zero for every success, which is what makes one loss survivable while two
// in a row still reap a genuinely dead peer.
func heartbeatDecision(consecutiveFailures, threshold int) bool {
	if threshold < 1 {
		threshold = 1
	}
	return consecutiveFailures >= threshold
}

// runTerminalHeartbeat probes the peer until the socket context is done or the
// peer is declared dead, then calls onDead exactly once. probe reports whether
// one probe succeeded; a failed probe only counts toward the streak.
//
// The probe context derives from socketCtx, so a cancelled session aborts an
// in-flight probe immediately instead of waiting out the timeout. Callers own
// cancellation: onDead is expected to close the session and cancel the socket
// context, which stops this loop.
func runTerminalHeartbeat(
	socketCtx context.Context,
	interval, timeout time.Duration,
	threshold int,
	probe func(ctx context.Context) error,
	onDead func(),
) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0
	// healthyAt is when the last probe succeeded. The grace window starts
	// there, not at the first failure: a probe that times out only reports
	// after `timeout` has already elapsed, so waiting another full timeout
	// from that point would let a dead peer hold its session slot for up to
	// interval+2*timeout.
	healthyAt := time.Now()
	for {
		select {
		case <-socketCtx.Done():
			return
		case <-ticker.C:
			probeCtx, probeCancel := context.WithTimeout(socketCtx, timeout)
			probeErr := probe(probeCtx)
			probeCancel()
			if probeErr == nil {
				failures = 0
				healthyAt = time.Now()
				continue
			}
			failures++
			if !heartbeatDecision(failures, threshold) {
				continue
			}
			if time.Since(healthyAt) < timeout {
				continue
			}
			onDead()
			return
		}
	}
}

// terminalHeartbeatProbe adapts a WebSocket ping to the probe signature used
// by runTerminalHeartbeat.
func terminalHeartbeatProbe(connection *websocket.Conn) func(context.Context) error {
	return func(ctx context.Context) error {
		return connection.Ping(ctx)
	}
}

// Immutable per-server overrides keep wire tests isolated from other sessions.
type terminalHeartbeatBudget struct{ interval, timeout time.Duration }

func (s *Server) heartbeatBudget() terminalHeartbeatBudget {
	if s.terminalHeartbeat.interval > 0 && s.terminalHeartbeat.timeout > 0 {
		return s.terminalHeartbeat
	}
	return terminalHeartbeatBudget{terminalHeartbeatInterval, terminalHeartbeatTimeout}
}
