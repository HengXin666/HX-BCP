package proxygroup

import (
	"encoding/json"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// groupEdges builds the group-reference adjacency list (group id -> referenced
// group ids) from stored records, substituting the candidate's pending spec so
// validation sees the state after the write would land.
func groupEdges(records []store.ProxyGroupRecord, candidateID string, candidate SourceSpec, candidateDialer string) map[string][]string {
	edges := make(map[string][]string, len(records)+1)
	for _, record := range records {
		if record.ID == candidateID {
			continue
		}
		var spec SourceSpec
		if err := json.Unmarshal([]byte(record.SourceSpecJSON), &spec); err != nil {
			continue
		}
		edges[record.ID] = appendDialerEdge(spec.GroupIDs, record.DialerProxyGroupID)
	}
	edges[candidateID] = appendDialerEdge(candidate.GroupIDs, candidateDialer)
	return edges
}

// appendDialerEdge folds the egress chain into the reference edges. A group that
// dials through another group depends on it exactly as much as a member
// reference does, so cycle detection must see both or a chain could be closed
// into an infinite dialer loop. The loop is not catchable at load time: Mihomo
// accepts a runtime dialer cycle, so this is the only place that can refuse it.
func appendDialerEdge(groupIDs []string, dialerID string) []string {
	dialerID = strings.TrimSpace(dialerID)
	if dialerID == "" {
		return groupIDs
	}
	edges := make([]string, 0, len(groupIDs)+1)
	edges = append(edges, groupIDs...)
	return append(edges, dialerID)
}

// findCycle returns a reference path that loops back onto itself, starting the
// search from the given group, or nil when the graph below it is acyclic.
func findCycle(edges map[string][]string, start string) []string {
	const (
		visiting = 1
		done     = 2
	)
	states := make(map[string]int, len(edges))
	var path []string
	var walk func(id string) []string
	walk = func(id string) []string {
		states[id] = visiting
		path = append(path, id)
		for _, next := range edges[id] {
			switch states[next] {
			case visiting:
				// Trim the path down to where the loop begins.
				for index, member := range path {
					if member == next {
						return append(append([]string(nil), path[index:]...), next)
					}
				}
				return append(append([]string(nil), path...), next)
			case done:
				continue
			default:
				if cycle := walk(next); cycle != nil {
					return cycle
				}
			}
		}
		states[id] = done
		path = path[:len(path)-1]
		return nil
	}
	return walk(start)
}

// referencedBy lists the ids of groups that reference the target group, either
// as a member or as an egress dialer. Both block a delete: dropping a group
// another one dials through would silently turn that chain into a direct dial.
func referencedBy(records []store.ProxyGroupRecord, targetID string) []string {
	var owners []string
	for _, record := range records {
		if record.ID == targetID {
			continue
		}
		// The dialer relation is checked first and short-circuits, so a group
		// that is both a member and a dialer is reported exactly once.
		if strings.TrimSpace(record.DialerProxyGroupID) == targetID {
			owners = append(owners, record.ID)
			continue
		}
		var spec SourceSpec
		if err := json.Unmarshal([]byte(record.SourceSpecJSON), &spec); err != nil {
			continue
		}
		for _, id := range spec.GroupIDs {
			if id == targetID {
				owners = append(owners, record.ID)
				break
			}
		}
	}
	return owners
}
