package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// seedNodeQualityHistory inserts nodes and quality-check history rows directly
// so the benchmark measures the latest-check query, not the seed path.
func seedNodeQualityHistory(b *testing.B, nodeCount, checksPerNode int) *Store {
	b.Helper()
	storage, err := Open(context.Background(), filepath.Join(b.TempDir(), "node-quality-bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { storage.Close() })
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for node := range nodeCount {
		nodeID := fmt.Sprintf("bench-node-%04d", node)
		if _, err := storage.db.ExecContext(context.Background(), `
INSERT INTO nodes(id, fingerprint, display_name, protocol, canonical_config_encrypted,
                  lifecycle_state, first_seen_at, last_seen_at, version)
VALUES (?, ?, ?, 'http', X'00', 'healthy', ?, ?, 1)
`, nodeID, "fp-"+nodeID, "Bench Node #"+fmt.Sprint(node), now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano)); err != nil {
			b.Fatal(err)
		}
		for check := range checksPerNode {
			checkedAt := now.Add(time.Duration(check) * time.Minute).UTC().Format(time.RFC3339Nano)
			latency := check % 500
			if _, err := storage.db.ExecContext(context.Background(), `
INSERT INTO node_quality_checks(node_id, checked_at, success, latency_ms, test_url)
VALUES (?, ?, 1, ?, 'http://cp.cloudflare.com/generate_204')
`, nodeID, checkedAt, latency); err != nil {
				b.Fatal(err)
			}
		}
	}
	return storage
}

func BenchmarkListLatestNodeHealthResults1000x50(b *testing.B) {
	storage := seedNodeQualityHistory(b, 1000, 50)
	nodeIDs := make([]string, 1000)
	for index := range nodeIDs {
		nodeIDs[index] = fmt.Sprintf("bench-node-%04d", index)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		records, err := storage.ListLatestNodeHealthResults(context.Background(), nodeIDs)
		if err != nil {
			b.Fatal(err)
		}
		if len(records) != 1000 {
			b.Fatalf("records = %d, want 1000", len(records))
		}
	}
}
