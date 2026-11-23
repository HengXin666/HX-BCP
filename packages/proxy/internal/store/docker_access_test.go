package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDockerAccessEntriesCascadeWithEitherListener(t *testing.T) {
	ctx := context.Background()
	storage := openTestStore(t)
	now := time.Now().UTC()
	if _, err := storage.CreateProxyGroup(ctx, ProxyGroupRecord{
		ID: "group-1", Name: "g", Strategy: "manual", SourceSpecJSON: `{"node_ids":[]}`, RulePipelineJSON: "{}",
		Enabled: true, EmptyBehavior: "fail-closed", Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"source", "entry", "other"} {
		if _, err := storage.CreateListener(ctx, ListenerRecord{
			ID: id, Name: id, Kind: "mixed", BindAddress: "127.0.0.1", Port: 29080 + index, ProxyGroupID: "group-1",
			AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}", ShareToken: "t-" + id,
			Enabled: true, Version: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	link := DockerAccessEntryRecord{ListenerID: "source", EntryListenerID: "entry", BindTarget: "172.17.0.1", CreatedAt: now}
	if err := storage.CreateDockerAccessEntry(ctx, link); err != nil {
		t.Fatal(err)
	}
	if err := storage.CreateDockerAccessEntry(ctx, DockerAccessEntryRecord{ListenerID: "other", EntryListenerID: "entry", BindTarget: "shared", CreatedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("one companion must serve one source, err = %v", err)
	}
	listed, err := storage.ListDockerAccessEntries(ctx)
	if err != nil || len(listed) != 1 || listed[0].EntryListenerID != "entry" || listed[0].BindTarget != "172.17.0.1" {
		t.Fatalf("listed = %+v err = %v", listed, err)
	}
	if err := storage.DeleteListener(ctx, "entry", 1); err != nil {
		t.Fatal(err)
	}
	listed, err = storage.ListDockerAccessEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("deleting the companion must drop the link, got %+v", listed)
	}
	// The source side cascades too.
	if err := storage.CreateDockerAccessEntry(ctx, DockerAccessEntryRecord{ListenerID: "source", EntryListenerID: "other", BindTarget: "shared", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := storage.DeleteListener(ctx, "source", 1); err != nil {
		t.Fatal(err)
	}
	listed, err = storage.ListDockerAccessEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("deleting the source must drop the link, got %+v", listed)
	}
	// A link to a missing listener is rejected by the foreign key.
	if err := storage.CreateDockerAccessEntry(ctx, DockerAccessEntryRecord{ListenerID: "other", EntryListenerID: "missing", BindTarget: "shared", CreatedAt: now}); err == nil {
		t.Fatal("a link to a missing listener must be rejected by the foreign key")
	}
}

func TestDeleteDockerAccessEntryForRemovesByCompanionID(t *testing.T) {
	ctx := context.Background()
	storage := openTestStore(t)
	now := time.Now().UTC()
	if _, err := storage.CreateProxyGroup(ctx, ProxyGroupRecord{
		ID: "group-1", Name: "g", Strategy: "manual", SourceSpecJSON: `{"node_ids":[]}`, RulePipelineJSON: "{}",
		Enabled: true, EmptyBehavior: "fail-closed", Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"source-a", "entry-a", "source-b", "entry-b"} {
		if _, err := storage.CreateListener(ctx, ListenerRecord{
			ID: id, Name: id, Kind: "mixed", BindAddress: "127.0.0.1", Port: 29180 + index, ProxyGroupID: "group-1",
			AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}", ShareToken: "t-" + id,
			Enabled: true, Version: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range []DockerAccessEntryRecord{
		{ListenerID: "source-a", EntryListenerID: "entry-a", BindTarget: "172.17.0.1", CreatedAt: now},
		{ListenerID: "source-b", EntryListenerID: "entry-b", BindTarget: "172.17.0.1", CreatedAt: now},
	} {
		if err := storage.CreateDockerAccessEntry(ctx, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.DeleteDockerAccessEntryFor(ctx, "entry-a"); err != nil {
		t.Fatal(err)
	}
	listed, err := storage.ListDockerAccessEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].EntryListenerID != "entry-b" {
		t.Fatalf("listed = %+v, want only the entry-b link", listed)
	}
	// Listeners stay: only the link is removed.
	for _, id := range []string{"source-a", "entry-a"} {
		if _, err := storage.GetListener(ctx, id); err != nil {
			t.Fatalf("listener %s must survive, err = %v", id, err)
		}
	}
}
