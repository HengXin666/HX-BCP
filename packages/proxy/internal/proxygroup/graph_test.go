package proxygroup

import (
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestFindCycleDetectsLoops(t *testing.T) {
	edges := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	}
	cycle := findCycle(edges, "a")
	if cycle == nil {
		t.Fatal("expected a cycle")
	}
	if cycle[0] != cycle[len(cycle)-1] {
		t.Fatalf("cycle should start and end at the same member: %v", cycle)
	}
}

func TestFindCycleAcceptsDiamonds(t *testing.T) {
	// a references b and c, both reference d: a valid DAG, not a cycle.
	edges := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
		"d": nil,
	}
	if cycle := findCycle(edges, "a"); cycle != nil {
		t.Fatalf("diamond incorrectly reported as cycle: %v", cycle)
	}
}

func TestFindCycleDetectsSelfLoop(t *testing.T) {
	if cycle := findCycle(map[string][]string{"a": {"a"}}, "a"); cycle == nil {
		t.Fatal("expected self loop to be detected")
	}
}

func TestGroupEdgesSubstitutesCandidate(t *testing.T) {
	records := []store.ProxyGroupRecord{
		{ID: "a", SourceSpecJSON: `{"node_ids":[],"group_ids":["b"]}`},
		{ID: "b", SourceSpecJSON: `{"node_ids":[]}`},
	}
	edges := groupEdges(records, "b", SourceSpec{GroupIDs: []string{"a"}}, "")
	cycle := findCycle(edges, "b")
	if cycle == nil {
		t.Fatal("expected cycle after substituting the candidate spec")
	}
	joined := strings.Join(cycle, "->")
	if !strings.Contains(joined, "a") || !strings.Contains(joined, "b") {
		t.Fatalf("unexpected cycle members: %v", cycle)
	}
}

// An egress chain is a dependency edge, so closing a chain into a loop must be
// rejected by the same walker that catches membership cycles. Mihomo accepts a
// cyclic dialer at load time, so nothing downstream would catch it.
func TestGroupEdgesIncludeDialerEdge(t *testing.T) {
	records := []store.ProxyGroupRecord{
		{ID: "a", SourceSpecJSON: `{"node_ids":["n1"]}`, DialerProxyGroupID: "b"},
		{ID: "b", SourceSpecJSON: `{"node_ids":["n2"]}`},
	}
	edges := groupEdges(records, "b", SourceSpec{NodeIDs: []string{"n3"}}, "a")
	cycle := findCycle(edges, "b")
	if cycle == nil {
		t.Fatal("expected the chain b -> a -> b to be detected as a cycle")
	}
}

// Deleting a group another one dials through would silently turn that chain into
// a direct connection, so the dialer relation must block the delete too.
func TestReferencedByIncludesDialerRelation(t *testing.T) {
	records := []store.ProxyGroupRecord{
		{ID: "a", SourceSpecJSON: `{"node_ids":["n1"]}`, DialerProxyGroupID: "b"},
		{ID: "b", SourceSpecJSON: `{"node_ids":["n2"]}`},
	}
	owners := referencedBy(records, "b")
	if len(owners) != 1 || owners[0] != "a" {
		t.Fatalf("owners = %v, want [a]", owners)
	}
}

// A group that is both a member and a dialer of the target must be reported
// once, not twice: the delete error renders these names to the operator.
func TestReferencedByReportsDialerAndMemberOnce(t *testing.T) {
	records := []store.ProxyGroupRecord{
		{ID: "a", SourceSpecJSON: `{"group_ids":["b"]}`, DialerProxyGroupID: "b"},
		{ID: "b", SourceSpecJSON: `{"node_ids":["n2"]}`},
	}
	owners := referencedBy(records, "b")
	if len(owners) != 1 || owners[0] != "a" {
		t.Fatalf("owners = %v, want exactly [a]", owners)
	}
}

func TestReferencedBy(t *testing.T) {
	records := []store.ProxyGroupRecord{
		{ID: "a", SourceSpecJSON: `{"node_ids":[],"group_ids":["c"]}`},
		{ID: "b", SourceSpecJSON: `{"node_ids":[],"group_ids":["c","a"]}`},
		{ID: "c", SourceSpecJSON: `{"node_ids":[]}`},
	}
	owners := referencedBy(records, "c")
	if len(owners) != 2 {
		t.Fatalf("owners = %v, want a and b", owners)
	}
	if owners := referencedBy(records, "b"); len(owners) != 0 {
		t.Fatalf("owners of b = %v, want none", owners)
	}
}
