package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/proxylog"
)

type ProxyLogService interface {
	Subscribe(context.Context, proxylog.Filter) (<-chan proxylog.Event, error)
}

type ProxyLogListenerService interface {
	Get(context.Context, string) (listener.Listener, error)
}

type ProxyLogGroupService interface {
	Get(context.Context, string) (proxygroup.Group, error)
}

type LogHandler struct {
	logs      ProxyLogService
	listeners ProxyLogListenerService
	groups    ProxyLogGroupService
}

func NewLogHandler(
	logs ProxyLogService,
	listeners ProxyLogListenerService,
	groups ProxyLogGroupService,
) (*LogHandler, error) {
	if logs == nil || listeners == nil || groups == nil {
		return nil, errors.New("proxy logs, listeners, and groups are required")
	}
	return &LogHandler{logs: logs, listeners: listeners, groups: groups}, nil
}

func (h *LogHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeLogError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeLogError(writer, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported")
		return
	}
	filter, err := h.resolveFilter(request.Context(), request)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, listener.ErrNotFound) || errors.Is(err, proxygroup.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeLogError(writer, status, "invalid_log_filter", err.Error())
		return
	}
	events, err := h.logs.Subscribe(request.Context(), filter)
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "log_stream_unavailable"
		if errors.Is(err, proxylog.ErrBusy) {
			status = http.StatusTooManyRequests
			code = "log_stream_busy"
		}
		writeLogError(writer, status, code, err.Error())
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache, no-store")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	_, _ = writer.Write([]byte("event: ready\ndata: {}\n\n"))
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return
			}
			if _, err := fmt.Fprintf(writer, "event: log\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := writer.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *LogHandler) resolveFilter(ctx context.Context, request *http.Request) (proxylog.Filter, error) {
	filter := proxylog.Filter{
		Level: strings.TrimSpace(request.URL.Query().Get("level")),
		Node:  strings.TrimSpace(request.URL.Query().Get("node_id")),
	}
	if filter.Node == "" {
		filter.Node = strings.TrimSpace(request.URL.Query().Get("node"))
	}
	groupID := strings.TrimSpace(request.URL.Query().Get("proxy_group_id"))
	listenerID := strings.TrimSpace(request.URL.Query().Get("listener_id"))
	if listenerID != "" {
		item, err := h.listeners.Get(ctx, listenerID)
		if err != nil {
			return proxylog.Filter{}, err
		}
		if groupID != "" && item.ProxyGroupID != groupID {
			return proxylog.Filter{}, errors.New("listener_id does not belong to proxy_group_id")
		}
		groupID = item.ProxyGroupID
	}
	if groupID != "" {
		group, err := h.groups.Get(ctx, groupID)
		if err != nil {
			return proxylog.Filter{}, err
		}
		filter.ProxyGroup = group.Name
	}
	return filter, nil
}

func writeLogError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
