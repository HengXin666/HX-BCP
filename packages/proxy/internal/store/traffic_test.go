package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTrafficWritesQueryCompactionAndEntityDeletion(t *testing.T) {
	ctx := context.Background()
	storage, err := Open(ctx, filepath.Join(t.TempDir(), "traffic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	if _, err := storage.db.ExecContext(ctx, `
INSERT INTO nodes(id, fingerprint, display_name, protocol, canonical_config_encrypted,
 lifecycle_state, first_seen_at, last_seen_at, version)
VALUES ('node-1', 'fingerprint-1', 'Node 1', 'socks5', X'01', 'candidate', ?, ?, 1)
`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	writes := []TrafficWrite{{
		ResourceType: TrafficResourceNode, ResourceID: "node-1", BucketStart: now.Add(-31 * 24 * time.Hour),
		Granularity: time.Minute, UploadBytes: 1, DownloadBytes: 2, ConnectionCount: 1, ActiveConnections: 0,
	}, {
		ResourceType: TrafficResourceNode, ResourceID: "node-1", BucketStart: now.Add(-8 * 24 * time.Hour),
		Granularity: time.Minute, UploadBytes: 3, DownloadBytes: 4, ConnectionCount: 1, ActiveConnections: 0,
	}, {
		ResourceType: TrafficResourceNode, ResourceID: "node-1", BucketStart: now.Add(-25 * time.Hour),
		Granularity: time.Minute, UploadBytes: 10, DownloadBytes: 20, ConnectionCount: 1, ActiveConnections: 2,
	}, {
		ResourceType: TrafficResourceNode, ResourceID: "node-1", BucketStart: now.Add(-2 * time.Minute),
		Granularity: time.Minute, UploadBytes: 30, DownloadBytes: 40, ConnectionCount: 2, ActiveConnections: 1,
	}}
	if err := storage.WriteTraffic(ctx, writes, now); err != nil {
		t.Fatal(err)
	}
	total, err := storage.GetTrafficTotal(ctx, TrafficResourceNode, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if total.UploadBytes != 44 || total.DownloadBytes != 66 || total.ConnectionCount != 5 || total.ActiveConnections != 1 {
		t.Fatalf("total = %+v", total)
	}
	today, err := storage.ListTrafficSummaries(ctx, TrafficResourceNode, now.Add(-time.Hour), now.Add(time.Minute), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(today) != 1 || today[0].UploadBytes != 30 || today[0].DownloadBytes != 40 || today[0].ConnectionCount != 2 {
		t.Fatalf("time range summary = %+v", today)
	}
	if err := storage.CompactTraffic(ctx, now); err != nil {
		t.Fatal(err)
	}
	points, err := storage.ListTrafficBuckets(ctx, TrafficResourceNode, "node-1", now.Add(-2*24*time.Hour), now.Add(time.Minute), 5*time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("points = %+v", points)
	}
	hourPoints, err := storage.ListTrafficBuckets(ctx, TrafficResourceNode, "node-1", now.Add(-10*24*time.Hour), now, time.Hour, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(hourPoints) != 3 {
		t.Fatalf("hour points = %+v", hourPoints)
	}
	expiredPoints, err := storage.ListTrafficBuckets(ctx, TrafficResourceNode, "node-1", now.Add(-40*24*time.Hour), now.Add(-30*24*time.Hour), time.Hour, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(expiredPoints) != 0 {
		t.Fatalf("expired points were retained: %+v", expiredPoints)
	}
	if _, err := storage.db.ExecContext(ctx, "DELETE FROM nodes WHERE id = 'node-1'"); err != nil {
		t.Fatal(err)
	}
	total, err = storage.GetTrafficTotal(ctx, TrafficResourceNode, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if total.UploadBytes != 0 || total.DownloadBytes != 0 {
		t.Fatalf("traffic survived local entity deletion: %+v", total)
	}
}

func TestResidentialChannelTrafficUsesStableResourceType(t *testing.T) {
	ctx := context.Background()
	storage, err := Open(ctx, filepath.Join(t.TempDir(), "residential-traffic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := storage.WriteTraffic(ctx, []TrafficWrite{{
		ResourceType:      TrafficResourceResidentialChannel,
		ResourceID:        "residential-channel-1",
		BucketStart:       now,
		Granularity:       time.Minute,
		UploadBytes:       128,
		DownloadBytes:     512,
		ConnectionCount:   1,
		ActiveConnections: 1,
	}}, now); err != nil {
		t.Fatal(err)
	}
	total, err := storage.GetTrafficTotal(ctx, TrafficResourceResidentialChannel, "residential-channel-1")
	if err != nil {
		t.Fatal(err)
	}
	if total.UploadBytes != 128 || total.DownloadBytes != 512 || total.ConnectionCount != 1 {
		t.Fatalf("residential channel total = %+v", total)
	}
}
