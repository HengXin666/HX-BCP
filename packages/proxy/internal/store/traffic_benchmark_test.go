package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkTrafficBatchWrite1000(b *testing.B) {
	storage, err := Open(context.Background(), filepath.Join(b.TempDir(), "traffic.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer storage.Close()
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	writes := make([]TrafficWrite, 1_000)
	for index := range writes {
		writes[index] = TrafficWrite{ResourceType: TrafficResourceNode, ResourceID: fmt.Sprintf("node-%04d", index), BucketStart: now, Granularity: time.Minute, UploadBytes: 1024, DownloadBytes: 4096, ConnectionCount: 1, ActiveConnections: 1}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		if err := storage.WriteTraffic(context.Background(), writes, now.Add(time.Duration(index)*time.Minute)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTrafficQuery500Points(b *testing.B) {
	storage, err := Open(context.Background(), filepath.Join(b.TempDir(), "traffic-query.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer storage.Close()
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	for batch := range 50 {
		writes := make([]TrafficWrite, 10)
		for index := range writes {
			writes[index] = TrafficWrite{ResourceType: TrafficResourceNode, ResourceID: "node-query", BucketStart: now.Add(time.Duration(batch*10+index) * time.Minute), Granularity: time.Minute, UploadBytes: 1024, DownloadBytes: 4096, ConnectionCount: 1, ActiveConnections: 1}
		}
		if err := storage.WriteTraffic(context.Background(), writes, now); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		points, err := storage.ListTrafficBuckets(context.Background(), TrafficResourceNode, "node-query", now, now.Add(501*time.Minute), time.Minute, 500)
		if err != nil || len(points) != 500 {
			b.Fatalf("points = %d, error = %v", len(points), err)
		}
	}
}
