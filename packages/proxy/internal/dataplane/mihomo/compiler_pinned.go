package mihomo

import (
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// withholdPinnedListeners leaves out listeners whose pinned node is not in the
// compiled proxy set.
//
// The listener API rejects a pin that names a missing, disabled or retired
// node, so a row only reaches compile with a dead pin when the node went away
// after the listener was created — a subscription refresh retiring it, for
// example. That is one stale row, not a broken document: failing the compile
// rejects every other listener and every caller of Apply (the node-quality pass
// among them) for as long as nobody edits the row, which is what turns one
// retired node into a permanently failing data plane. The listener is withheld
// instead and stays visible as an unusable row.
//
// The withheld listener is never repointed at its proxy group. A listener that
// names one exit and cannot reach it must not silently publish another exit,
// which is the whole point of pinning the node.
//
// See .agents/notes/implemented/feature/2026-09-26-listener-pins-a-node.md.
func withholdPinnedListeners(listeners []store.ListenerRecord, nodes []store.NodeConfigRecord) ([]store.ListenerRecord, []WithheldEntry) {
	if len(listeners) == 0 {
		return listeners, nil
	}
	compiled := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		compiled[node.ID] = struct{}{}
	}
	kept := make([]store.ListenerRecord, 0, len(listeners))
	var withheld []WithheldEntry
	for _, record := range listeners {
		if nodeID := strings.TrimSpace(record.NodeID); nodeID != "" {
			if _, exists := compiled[nodeID]; !exists {
				withheld = append(withheld, WithheldEntry{
					ListenerID: record.ID,
					Reason:     WithheldPinnedNodeUnavailable,
				})
				continue
			}
		}
		kept = append(kept, record)
	}
	return kept, withheld
}

// routesForCompile drops the residential client routes of withheld listeners.
// Their rule text names the listener's carrier, which the document no longer
// carries; a route for a withheld listener cannot be reached either way.
func routesForCompile(routes []store.ResidentialClientRouteRecord, withheld map[string]bool) []store.ResidentialClientRouteRecord {
	if len(withheld) == 0 {
		return routes
	}
	kept := make([]store.ResidentialClientRouteRecord, 0, len(routes))
	for _, route := range routes {
		if withheld[route.ListenerID] {
			continue
		}
		kept = append(kept, route)
	}
	return kept
}
