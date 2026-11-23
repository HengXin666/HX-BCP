package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/proxylog"
)

func TestLogHandlerResolvesListenerAndStreamsSSE(t *testing.T) {
	t.Parallel()
	logs := &fakeProxyLogs{events: []proxylog.Event{{
		Timestamp: time.Unix(10, 0).UTC(), Level: "info", Message: "routed", ProxyGroupID: "group-1", ProxyGroup: "Fast", NodeID: "node-1", Node: "Tokyo",
	}}}
	handler, err := NewLogHandler(
		logs,
		&fakeLogListeners{item: listener.Listener{ID: "listener-1", ProxyGroupID: "group-1"}},
		&fakeLogGroups{item: proxygroup.Group{ID: "group-1", Name: "Fast"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?listener_id=listener-1&level=info", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if logs.filter.ProxyGroup != "Fast" || logs.filter.Level != "info" {
		t.Fatalf("filter = %#v", logs.filter)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "event: ready") || !strings.Contains(body, `"node_id":"node-1"`) {
		t.Fatalf("SSE body = %q", body)
	}
}

func TestLogHandlerRejectsMismatchedListenerGroup(t *testing.T) {
	t.Parallel()
	handler, err := NewLogHandler(
		&fakeProxyLogs{},
		&fakeLogListeners{item: listener.Listener{ID: "listener-1", ProxyGroupID: "group-1"}},
		&fakeLogGroups{},
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?listener_id=listener-1&proxy_group_id=group-2", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
}

type fakeProxyLogs struct {
	events []proxylog.Event
	filter proxylog.Filter
}

func (s *fakeProxyLogs) Subscribe(_ context.Context, filter proxylog.Filter) (<-chan proxylog.Event, error) {
	s.filter = filter
	result := make(chan proxylog.Event, len(s.events))
	for _, event := range s.events {
		result <- event
	}
	close(result)
	return result, nil
}

type fakeLogListeners struct {
	item listener.Listener
}

func (s *fakeLogListeners) Get(context.Context, string) (listener.Listener, error) {
	return s.item, nil
}

type fakeLogGroups struct {
	item proxygroup.Group
}

func (s *fakeLogGroups) Get(context.Context, string) (proxygroup.Group, error) {
	return s.item, nil
}
