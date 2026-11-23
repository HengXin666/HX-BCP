package mihomo

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestDockerEntryStateTracksWithheldEntries(t *testing.T) {
	var state dockerEntryState
	state.set([]WithheldEntry{
		{ListenerID: "a", Reason: WithheldSourceDisabled},
		{ListenerID: "b", BindAddress: "172.30.0.1", Reason: WithheldBridgeAbsent},
	})
	if entry, ok := state.get("b"); !ok || entry.Reason != WithheldBridgeAbsent {
		t.Fatalf("b = %+v %v", entry, ok)
	}
	if waiting := state.waitingAddresses(); len(waiting) != 1 || waiting[0] != "172.30.0.1" {
		t.Fatalf("waiting = %v (only bridge-absent entries wait for an address)", waiting)
	}
	state.set(nil)
	if _, ok := state.get("b"); ok {
		t.Fatal("a later apply that serves the entry must clear it")
	}
	if waiting := state.waitingAddresses(); len(waiting) != 0 {
		t.Fatalf("waiting = %v", waiting)
	}
}

// One line per apply names every withheld listener and its reason.
func TestLogWithheldEntriesIsOneBoundedLine(t *testing.T) {
	var buffer bytes.Buffer
	manager := &Manager{logger: slog.New(slog.NewTextHandler(&buffer, nil))}
	manager.logWithheldEntries(nil)
	if buffer.Len() != 0 {
		t.Fatalf("no withheld entries must log nothing, got %q", buffer.String())
	}
	manager.logWithheldEntries([]WithheldEntry{
		{ListenerID: "probe-resi-01", Reason: WithheldPinnedNodeUnavailable},
		{ListenerID: "probe-resi-02", Reason: WithheldPinnedNodeUnavailable},
	})
	if lines := strings.Count(strings.TrimSpace(buffer.String()), "\n") + 1; lines != 1 {
		t.Fatalf("log lines = %d, want 1:\n%s", lines, buffer.String())
	}
	for _, wanted := range []string{"probe-resi-01", "probe-resi-02", WithheldPinnedNodeUnavailable} {
		if !strings.Contains(buffer.String(), wanted) {
			t.Fatalf("log %q does not name %q", buffer.String(), wanted)
		}
	}
}
