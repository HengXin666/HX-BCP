package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

type fakeSource struct{ snapshot RuntimeSnapshot }

func (source *fakeSource) TrafficSnapshot(context.Context) (RuntimeSnapshot, error) {
	return source.snapshot, nil
}

type fakeRepository struct {
	writes   []store.TrafficWrite
	writeErr error
}

func (repository *fakeRepository) WriteTraffic(_ context.Context, writes []store.TrafficWrite, _ time.Time) error {
	if repository.writeErr != nil {
		return repository.writeErr
	}
	repository.writes = append(repository.writes, writes...)
	return nil
}

func TestFailedFlushPreservesPendingCounters(t *testing.T) {
	resource := Resource{Type: store.TrafficResourceListener, ID: "listener-1"}
	source := &fakeSource{snapshot: RuntimeSnapshot{Connections: []Connection{{
		ID: "connection-1", Upload: 50, Download: 75, Resources: []Resource{resource},
	}}}}
	repository := &fakeRepository{writeErr: errors.New("database busy")}
	service, err := NewService(repository, source, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(context.Background()); err == nil {
		t.Fatal("Flush() unexpectedly succeeded")
	}
	repository.writeErr = nil
	if err := service.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.writes) != 1 || repository.writes[0].UploadBytes != 50 || repository.writes[0].DownloadBytes != 75 {
		t.Fatalf("retry lost pending counters: %+v", repository.writes)
	}
}
func (*fakeRepository) GetTrafficTotal(_ context.Context, resourceType, resourceID string) (store.TrafficTotalRecord, error) {
	return store.TrafficTotalRecord{ResourceType: resourceType, ResourceID: resourceID}, nil
}
func (*fakeRepository) ListTrafficTotals(context.Context, string, int, int) ([]store.TrafficTotalRecord, error) {
	return nil, nil
}
func (*fakeRepository) ListTrafficSummaries(context.Context, string, time.Time, time.Time, int, int) ([]store.TrafficTotalRecord, error) {
	return nil, nil
}
func (*fakeRepository) ListTrafficBuckets(context.Context, string, string, time.Time, time.Time, time.Duration, int) ([]store.TrafficBucketRecord, error) {
	return nil, nil
}
func (*fakeRepository) CompactTraffic(context.Context, time.Time) error { return nil }

func TestServiceAggregatesConnectionDeltasAndResetsActiveGauge(t *testing.T) {
	resource := Resource{Type: store.TrafficResourceNode, ID: "node-1"}
	source := &fakeSource{snapshot: RuntimeSnapshot{Connections: []Connection{{
		ID: "connection-1", Upload: 100, Download: 200, Resources: []Resource{resource, resource},
	}}}}
	repository := &fakeRepository{}
	service, err := NewService(repository, source, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 7, 27, 12, 34, 0, 0, time.UTC) }
	if err := service.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.snapshot.Connections[0].Upload = 130
	source.snapshot.Connections[0].Download = 260
	if err := service.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.writes) != 1 {
		t.Fatalf("writes = %+v", repository.writes)
	}
	write := repository.writes[0]
	if write.UploadBytes != 130 || write.DownloadBytes != 260 || write.ConnectionCount != 1 || write.ActiveConnections != 1 {
		t.Fatalf("first write = %+v", write)
	}

	source.snapshot.Connections = nil
	if err := service.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.writes) != 2 || repository.writes[1].ActiveConnections != 0 || repository.writes[1].ConnectionCount != 0 {
		t.Fatalf("gauge reset write = %+v", repository.writes)
	}
}

func TestServiceAggregatesResidentialChannelResource(t *testing.T) {
	resource := Resource{Type: store.TrafficResourceResidentialChannel, ID: "channel-1"}
	source := &fakeSource{snapshot: RuntimeSnapshot{Connections: []Connection{{
		ID: "connection-1", Upload: 12, Download: 34, Resources: []Resource{resource},
	}}}}
	repository := &fakeRepository{}
	service, err := NewService(repository, source, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.writes) != 1 || repository.writes[0].ResourceType != store.TrafficResourceResidentialChannel {
		t.Fatalf("residential channel writes = %+v", repository.writes)
	}
}

func TestCollectPublishesLiveSamplesAndKeepsActiveResources(t *testing.T) {
	resource := Resource{Type: store.TrafficResourceListener, ID: "listener-1"}
	source := &fakeSource{snapshot: RuntimeSnapshot{Connections: []Connection{{
		ID: "connection-1", Upload: 100, Download: 200, Resources: []Resource{resource},
	}}}}
	service, err := NewService(&fakeRepository{}, source, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return start }
	subscription := service.SubscribeLive()
	defer subscription.Close()
	if err := service.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := <-subscription.C
	if len(first.Resources) != 1 || first.Resources[0].ResourceID != resource.ID {
		t.Fatalf("first live sample resources = %+v", first.Resources)
	}

	service.now = func() time.Time { return start.Add(2 * time.Second) }
	if err := service.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := <-subscription.C
	if second.UploadBytesPerSec != 0 || second.DownloadBytesPerSec != 0 {
		t.Fatalf("unchanged connection should have zero rate: %+v", second)
	}
	if len(second.Resources) != 1 || second.Resources[0].ActiveConnections != 1 {
		t.Fatalf("active resource disappeared from live sample: %+v", second.Resources)
	}

	live := service.LiveSnapshot()
	if len(live.History) != 2 || live.Latest.Timestamp != second.Timestamp {
		t.Fatalf("live snapshot = %+v", live)
	}
	if len(live.Latest.Resources) != 1 {
		t.Fatalf("latest resource history was not cloned: %+v", live.Latest.Resources)
	}
}

func TestLiveSubscriptionCloseIsIdempotent(t *testing.T) {
	service, err := NewService(&fakeRepository{}, &fakeSource{}, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	subscription := service.SubscribeLive()
	subscription.Close()
	subscription.Close()
	if _, ok := <-subscription.C; ok {
		t.Fatal("closed live subscription channel is still open")
	}
}

func TestQueryRejectsUnboundedRanges(t *testing.T) {
	service, err := NewService(&fakeRepository{}, &fakeSource{}, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	to := time.Now().UTC()
	if _, err := service.Query(context.Background(), store.TrafficResourceNode, "node-1", to.Add(-31*24*time.Hour), to, 100); err == nil {
		t.Fatal("Query() accepted more than 30 days")
	}
	if _, err := service.Query(context.Background(), store.TrafficResourceNode, "node-1", to.Add(-time.Hour), to, 501); err == nil {
		t.Fatal("Query() accepted more than 500 points")
	}
}

func TestChooseResolutionBoundsPointCount(t *testing.T) {
	tests := []struct {
		span   time.Duration
		points int
		want   time.Duration
	}{
		{6 * time.Hour, 360, time.Minute},
		{24 * time.Hour, 288, 5 * time.Minute},
		{30 * 24 * time.Hour, 500, time.Hour},
	}
	for _, test := range tests {
		if got := chooseResolution(test.span, test.points); got != test.want {
			t.Errorf("chooseResolution(%s, %d) = %s, want %s", test.span, test.points, got, test.want)
		}
	}
}
