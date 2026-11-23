package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/metrics"
	"github.com/HengXin666/hx-pxy/internal/overview"
)

type fakeOverviewService struct {
	mu    sync.Mutex
	count int
}

func (service *fakeOverviewService) Status() mihomo.Status {
	return mihomo.Status{Running: true}
}

func (service *fakeOverviewService) OverviewSnapshot(context.Context) (overview.Snapshot, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.count++
	return overview.Snapshot{Connections: []overview.Connection{{
		ID: "connection-1", Upload: int64(service.count * 100), Download: int64(service.count * 200),
	}}}, nil
}

func TestOverviewStreamEmitsIdleFrameAndSecondLevelSamples(t *testing.T) {
	service := &fakeOverviewService{}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithOverview(service))
	if err != nil {
		t.Fatal(err)
	}
	server.overviewInterval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview/stream", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
	body := recorder.Body.String()
	if count := strings.Count(body, "event: sample"); count < 2 {
		t.Fatalf("sample count = %d, body = %q", count, body)
	}
	if !strings.Contains(body, `"upload_bytes_per_second":0`) {
		t.Fatalf("initial idle sample missing: %q", body)
	}
}

func TestOverviewStreamRejectsWrites(t *testing.T) {
	server, err := NewServer(&stubBundleService{}, slog.Default(), WithOverview(&fakeOverviewService{}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/overview/stream", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

type fakeLiveTrafficService struct {
	*fakeTrafficService
	live metrics.LiveSnapshot
}

func (service *fakeLiveTrafficService) LiveSnapshot() metrics.LiveSnapshot {
	return service.live
}

func (service *fakeLiveTrafficService) SubscribeLive() *metrics.LiveSubscription {
	return &metrics.LiveSubscription{C: make(chan metrics.LiveSample)}
}

func TestOverviewStreamSendsCollectorHistoryWithoutDuplicateSample(t *testing.T) {
	first := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	traffic := &fakeLiveTrafficService{
		fakeTrafficService: &fakeTrafficService{},
		live: metrics.LiveSnapshot{History: []metrics.LiveSample{
			{Timestamp: first, DownloadBytesPerSec: 100, ActiveConnections: 1},
			{Timestamp: first.Add(time.Second), DownloadBytesPerSec: 200, ActiveConnections: 2},
		}},
	}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithTraffic(traffic), WithOverview(&fakeOverviewService{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview/stream", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if strings.Count(body, "event: history") != 1 || strings.Count(body, "event: sample") != 0 {
		t.Fatalf("unexpected live SSE events: %q", body)
	}
	if !strings.Contains(body, `"download_bytes_per_second":200`) {
		t.Fatalf("collector history missing: %q", body)
	}
}
