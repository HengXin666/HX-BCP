package mihomo

import (
	"context"
	"sync"
	"time"
)

// DockerEntryWatchInterval is how often a bridge-bound Docker entry that was
// left out of the document is re-checked. dockerd usually starts after
// hx-pxyd on boot, so the first apply often withholds every bridge entry; a
// short poll brings them up without waiting for an unrelated change.
const DockerEntryWatchInterval = 30 * time.Second

// dockerEntryState holds the companions the running document leaves out. It
// has its own lock so readers (the Docker access assessment, the listener
// list) never wait for an apply holding Manager.mu.
type dockerEntryState struct {
	mu      sync.RWMutex
	entries map[string]WithheldEntry
}

func (s *dockerEntryState) set(entries []WithheldEntry) {
	next := make(map[string]WithheldEntry, len(entries))
	for _, entry := range entries {
		next[entry.ListenerID] = entry
	}
	s.mu.Lock()
	s.entries = next
	s.mu.Unlock()
}

func (s *dockerEntryState) get(id string) (WithheldEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[id]
	return entry, ok
}

// waitingAddresses lists the bind addresses of bridge entries withheld only
// because their address is not on the host yet.
func (s *dockerEntryState) waitingAddresses() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for _, entry := range s.entries {
		if entry.Reason == WithheldBridgeAbsent && entry.BindAddress != "" {
			out = append(out, entry.BindAddress)
		}
	}
	return out
}

// logWithheldEntries records the listeners the applied document leaves out. It
// is one line per apply on purpose: withheld rows are few, and a per-listener
// line would turn a single retired node into a log flood of its own.
func (m *Manager) logWithheldEntries(entries []WithheldEntry) {
	if len(entries) == 0 {
		return
	}
	ids := make([]string, 0, len(entries))
	reasons := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ListenerID)
		reasons = append(reasons, entry.Reason)
	}
	m.logger.Warn(
		"listeners withheld from the mihomo document",
		"component", "dataplane",
		"listener_ids", ids,
		"reasons", reasons,
	)
}

// DockerEntryWithheld reports whether the running document leaves out the
// Docker companion entry with this listener id, and why.
func (m *Manager) DockerEntryWithheld(listenerID string) (string, bool) {
	entry, ok := m.withheld.get(listenerID)
	return entry.Reason, ok
}

// RunDockerEntryWatch re-applies the configuration when the address of a
// withheld bridge entry appears on the host. It returns when ctx ends.
func (m *Manager) RunDockerEntryWatch(ctx context.Context) error {
	ticker := time.NewTicker(DockerEntryWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.reapplyWhenBridgeAppears(ctx)
		}
	}
}

func (m *Manager) reapplyWhenBridgeAppears(ctx context.Context) {
	present := m.compiler.hostAddressPresent
	if present == nil {
		present = hostHasAddress
	}
	for _, address := range m.withheld.waitingAddresses() {
		if !present(address) {
			continue
		}
		if err := m.Apply(ctx); err != nil {
			m.logger.Warn("re-apply after Docker bridge appeared failed", "component", "dataplane", "address", address, "error", err)
		} else {
			m.logger.Info("Docker bridge appeared; entries re-applied", "component", "dataplane", "address", address)
		}
		return
	}
}
