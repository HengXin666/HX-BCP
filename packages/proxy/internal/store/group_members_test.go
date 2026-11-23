package store

import (
	"context"
	"testing"
	"time"
)

func TestReplaceGroupMembersKeepsJoinTimeAndDropsLeavers(t *testing.T) {
	ctx := context.Background()
	storage := openTestStore(t)
	start := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"g1", "g2"} {
		if _, err := storage.CreateProxyGroup(ctx, ProxyGroupRecord{
			ID: id, Name: id, Strategy: "select", SourceSpecJSON: `{"node_ids":[]}`, RulePipelineJSON: "{}",
			Enabled: true, EmptyBehavior: "fail-closed", Version: 1, CreatedAt: start, UpdatedAt: start,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.ReplaceGroupMembers(ctx, map[string][]string{"g1": {"a", "b"}, "g2": {"c"}, "ghost": {"x"}}, start); err != nil {
		t.Fatal(err)
	}
	later := start.Add(time.Hour)
	if err := storage.ReplaceGroupMembers(ctx, map[string][]string{"g1": {"b", "d"}}, later); err != nil {
		t.Fatal(err)
	}
	members, err := storage.ListGroupMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || len(members["g1"]) != 2 {
		t.Fatalf("members = %+v", members)
	}
	if !members["g1"]["b"].Equal(start) {
		t.Fatalf("kept member joined_at = %v, want %v", members["g1"]["b"], start)
	}
	if !members["g1"]["d"].Equal(later) {
		t.Fatalf("new member joined_at = %v, want %v", members["g1"]["d"], later)
	}
	if err := storage.DeleteProxyGroup(ctx, "g1", 1); err != nil {
		t.Fatal(err)
	}
	members, err = storage.ListGroupMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatalf("deleting a group must cascade its members, got %+v", members)
	}
}
