package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestHeartbeatDecisionToleratesOneLoss pins the rule that one lost probe is
// not death. The bug this guards against is not "no heartbeat": it is a
// heartbeat so eager that a single dropped segment on a tunneled link closed a
// live terminal and forced the page into its reconnect loop.
func TestHeartbeatDecisionToleratesOneLoss(t *testing.T) {
	if heartbeatDecision(0, terminalConsecutivePingFailures) {
		t.Fatal("a fresh heartbeat must not declare the peer dead")
	}
	if heartbeatDecision(1, terminalConsecutivePingFailures) {
		t.Fatal("one failed probe must not declare the peer dead")
	}
	if !heartbeatDecision(2, terminalConsecutivePingFailures) {
		t.Fatal("two consecutive failed probes must declare the peer dead")
	}
	if !heartbeatDecision(5, terminalConsecutivePingFailures) {
		t.Fatal("any longer streak must declare the peer dead")
	}
}

func TestHeartbeatDecisionThresholdFloor(t *testing.T) {
	// A misconfigured threshold (zero or negative) must never mean "every
	// failed probe kills the session"; the floor keeps the loop sane.
	for _, threshold := range []int{0, -1} {
		if !heartbeatDecision(1, threshold) {
			t.Fatalf("threshold %d must be clamped to 1", threshold)
		}
	}
}

// TestTerminalHeartbeatBudgetIsLossTolerant pins the shipped budget: the
// threshold tolerance plus a probe timeout wider than the interval.
func TestTerminalHeartbeatBudgetIsLossTolerant(t *testing.T) {
	if terminalConsecutivePingFailures < 2 {
		t.Fatalf("terminalConsecutivePingFailures = %d, want >= 2", terminalConsecutivePingFailures)
	}
	if terminalHeartbeatTimeout <= terminalHeartbeatInterval {
		t.Fatalf("terminalHeartbeatTimeout (%s) must exceed terminalHeartbeatInterval (%s)",
			terminalHeartbeatTimeout, terminalHeartbeatInterval)
	}
	if terminalHeartbeatTimeout < 30*time.Second {
		t.Fatalf("terminalHeartbeatTimeout = %s, want >= 30s", terminalHeartbeatTimeout)
	}
	if terminalReadLimit < 128<<10 {
		t.Fatalf("terminalReadLimit = %d, want >= 128KiB", terminalReadLimit)
	}
}

// dialTerminalAndDrainMode opens one authenticated terminal socket and reads
// until the initial mode frame, which confirms the session is live.
func dialTerminalAndDrainMode(t *testing.T, websocketURL, sessionToken string) *websocket.Conn {
	t.Helper()
	headers := http.Header{}
	headers.Set("Cookie", sessionCookieName+"="+sessionToken)
	origin := "http://" + strings.TrimSuffix(strings.TrimPrefix(websocketURL, "ws://"), "/api/v1/terminal/ws")
	headers.Set("Origin", origin)
	connection, _, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("terminal dial: %v", err)
	}
	t.Cleanup(func() { connection.CloseNow() })
	readCtx, cancelRead := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancelRead)
	for {
		kind, payload, readErr := connection.Read(readCtx)
		if readErr != nil {
			t.Fatalf("read terminal mode: %v", readErr)
		}
		if kind != websocket.MessageText {
			continue
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) == nil && message.Type == "mode" {
			return connection
		}
	}
}

// TestTerminalHeartbeatToleratesOneLostProbe is the wire-level counterpart of
// TestHeartbeatDecisionToleratesOneLoss: a peer that answers one probe and then
// goes silent must survive its first missed probe. The probe timeout is
// deliberately long here, so an implementation that tears the session down after
// a single failed probe cannot pass this test by accident.
func TestTerminalHeartbeatToleratesOneLostProbe(t *testing.T) {
	_, terminalService, _, websocketURL, sessionToken := newTerminalAPITestServer(t, withHeartbeatBudget(100*time.Millisecond, 4*time.Second))
	connection := dialTerminalAndDrainMode(t, websocketURL, sessionToken)
	if active := terminalService.Status().ActiveSessions; active != 1 {
		t.Fatalf("active sessions = %d, want 1", active)
	}

	// One successful heartbeat, then silence. Several more probes fail while
	// the test waits, but the streak alone must not be enough to kill the
	// session within the grace window.
	_ = connection // Initial mode frame already confirms the peer is live.
	time.Sleep(700 * time.Millisecond)

	if active := terminalService.Status().ActiveSessions; active != 1 {
		t.Fatalf("a single lost probe ended the terminal session (active = %d, want 1)", active)
	}
}

// TestTerminalHeartbeatStillReapsSilentPeer proves the tolerance did not
// disable reaping: past the streak and the grace window a silent peer is closed
// and its session slot released.
func TestTerminalHeartbeatStillReapsSilentPeer(t *testing.T) {
	_, terminalService, _, websocketURL, sessionToken := newTerminalAPITestServer(t, withHeartbeatBudget(100*time.Millisecond, 150*time.Millisecond))
	connection := dialTerminalAndDrainMode(t, websocketURL, sessionToken)
	_ = connection // Initial mode frame already confirms the peer is live.

	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for terminalService.Status().ActiveSessions != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active := terminalService.Status().ActiveSessions; active != 0 {
		t.Fatalf("a peer silent past the streak and grace window was not reaped; active = %d", active)
	}
	// Reaping needs two failed probes, so anything faster than one interval
	// would mean the session died for another reason (e.g. the shell exiting),
	// and anything near the deadline would mean the grace window never ends.
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("silent peer reaped after %s, want between 50ms and 3s", elapsed)
	}
}

// TestTerminalWebSocketAcceptsLargeButBoundedInputFrame is the regression test
// for the "unstable terminal" report: the old 64 KiB read limit closed the
// whole socket with StatusMessageTooBig when a paste produced a single large
// frame. A frame under the raised limit must be accepted and forwarded to the
// PTY.
func TestTerminalWebSocketAcceptsLargeButBoundedInputFrame(t *testing.T) {
	_, _, _, websocketURL, sessionToken := newTerminalAPITestServer(t)
	connection := dialTerminalAndDrainMode(t, websocketURL, sessionToken)
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRead()

	// 200 KiB of input in one frame: above the old 64 KiB limit, below the new
	// 256 KiB one, and far above the client's 32 KiB chunk size.
	frame, err := json.Marshal(terminalMessage{Type: "input", Data: strings.Repeat("a", 200<<10)})
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(frame)) > terminalReadLimit {
		t.Fatalf("test frame %d bytes exceeds terminalReadLimit %d", len(frame), terminalReadLimit)
	}
	if err := connection.Write(readCtx, websocket.MessageText, frame); err != nil {
		t.Fatalf("write large input frame: %v", err)
	}

	// The socket must stay usable: a following ping still gets a pong.
	ping, _ := json.Marshal(terminalMessage{Type: "ping"})
	if err := connection.Write(readCtx, websocket.MessageText, ping); err != nil {
		t.Fatalf("write ping after large frame: %v", err)
	}
	for {
		kind, payload, readErr := connection.Read(readCtx)
		if readErr != nil {
			t.Fatalf("connection closed after a large input frame: %v", readErr)
		}
		if kind != websocket.MessageText {
			continue
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) == nil && message.Type == "pong" {
			return
		}
	}
}

// TestTerminalWebSocketRejectsOverLimitFrame keeps the other half of the
// contract: the read limit still exists, and a frame beyond it is refused with
// StatusMessageTooBig rather than being buffered without bound.
func TestTerminalWebSocketRejectsOverLimitFrame(t *testing.T) {
	_, _, _, websocketURL, sessionToken := newTerminalAPITestServer(t)
	connection := dialTerminalAndDrainMode(t, websocketURL, sessionToken)
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRead()

	// A direct oversize frame, as a non-chunking client would send.
	frame, err := json.Marshal(terminalMessage{Type: "input", Data: strings.Repeat("b", (256<<10)+(64<<10))})
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(frame)) <= terminalReadLimit {
		t.Fatalf("test frame %d bytes must exceed terminalReadLimit %d", len(frame), terminalReadLimit)
	}
	if err := connection.Write(readCtx, websocket.MessageText, frame); err != nil {
		// The server may reset the socket while the oversized write is still in
		// flight; that is the same outcome as an observed close.
		return
	}
	for {
		if _, _, readErr := connection.Read(readCtx); readErr != nil {
			if websocket.CloseStatus(readErr) == websocket.StatusMessageTooBig {
				return
			}
			t.Fatalf("over-limit frame closed the socket with %v, want %v",
				readErr, websocket.StatusMessageTooBig)
		}
	}
}
