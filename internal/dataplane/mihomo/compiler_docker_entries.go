package mihomo

import (
	"context"
	"fmt"
	"net"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// DockerEntryRepository names the companion listeners created for container
// access (internal/dockeraccess). It is optional; without it every listener
// row compiles as before.
type DockerEntryRepository interface {
	ListDockerAccessEntries(context.Context) ([]store.DockerAccessEntryRecord, error)
}

// gateDockerEntries leaves out companion listeners that must not be served:
//
//   - the source service is disabled or gone — a companion only exists to
//     reach its source, so it must not keep forwarding on its own;
//   - a bridge-bound companion whose gateway address is no longer on this host
//     (compose network removed, dockerd not up yet). Emitting it would make
//     the endpoint pre-check fail and block every other change to the data
//     plane, while only the Docker entry is actually unusable.
//
// See .agents/notes/implemented/feature/2026-10-15-docker-access-entry.md.
//
// Every companion left out is reported as a WithheldEntry, so the manager can
// re-apply once a missing bridge address appears and the control plane never
// reports a withheld entry as dialable.
func (c *Compiler) gateDockerEntries(ctx context.Context, listeners []store.ListenerRecord) ([]store.ListenerRecord, []WithheldEntry, error) {
	repository, ok := c.repository.(DockerEntryRepository)
	if !ok {
		return listeners, nil, nil
	}
	links, err := repository.ListDockerAccessEntries(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("load docker access entries: %w", err)
	}
	if len(links) == 0 {
		return listeners, nil, nil
	}
	sourceOf := make(map[string]store.DockerAccessEntryRecord, len(links))
	for _, link := range links {
		sourceOf[link.EntryListenerID] = link
	}
	enabled := make(map[string]bool, len(listeners))
	for _, record := range listeners {
		enabled[record.ID] = record.Enabled
	}
	present := c.hostAddressPresent
	if present == nil {
		present = hostHasAddress
	}
	kept := make([]store.ListenerRecord, 0, len(listeners))
	var withheld []WithheldEntry
	for _, record := range listeners {
		link, companion := sourceOf[record.ID]
		if companion && record.Enabled {
			if !enabled[link.ListenerID] {
				withheld = append(withheld, WithheldEntry{ListenerID: record.ID, Reason: WithheldSourceDisabled})
				continue
			}
			if link.BindTarget != "shared" && !present(record.BindAddress) {
				withheld = append(withheld, WithheldEntry{ListenerID: record.ID, BindAddress: record.BindAddress, Reason: WithheldBridgeAbsent})
				continue
			}
		}
		kept = append(kept, record)
	}
	return kept, withheld, nil
}

// Reasons a listener is left out of the compiled document.
const (
	WithheldSourceDisabled = "source_disabled"
	WithheldBridgeAbsent   = "bridge_absent"
	// WithheldPinnedNodeUnavailable is an enabled listener whose pinned node is
	// missing from the compiled proxy set. See compiler_pinned.go.
	WithheldPinnedNodeUnavailable = "pinned_node_unavailable"
)

// WithheldEntry is an enabled listener the data plane does not serve.
type WithheldEntry struct {
	ListenerID string
	// BindAddress is set for WithheldBridgeAbsent: the gateway address whose
	// appearance should trigger a re-apply.
	BindAddress string
	Reason      string
}

func hostHasAddress(address string) bool {
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, candidate := range addresses {
		if network, ok := candidate.(*net.IPNet); ok && network.IP.Equal(ip) {
			return true
		}
	}
	return false
}
