package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/alert"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type AlertService interface {
	List(ctx context.Context, status string, limit int) ([]store.AlertRecord, error)
	Acknowledge(ctx context.Context, id string) error
	Settings(ctx context.Context) (alert.SettingsView, error)
	UpdateSettings(ctx context.Context, request alert.UpdateSettingsRequest) (alert.SettingsView, error)
	SendTest(ctx context.Context) error
}

func WithAlerts(service AlertService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("alert service is required")
		}
		server.alerts = service
		return nil
	}
}

type alertView struct {
	ID             string     `json:"id"`
	Rule           string     `json:"rule"`
	TargetID       string     `json:"target_id"`
	TargetName     string     `json:"target_name"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	Message        string     `json:"message"`
	FiredAt        time.Time  `json:"fired_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	LastNotifiedAt *time.Time `json:"last_notified_at,omitempty"`
	NotifyCount    int        `json:"notify_count"`
	Acknowledged   bool       `json:"acknowledged"`
}

func (s *Server) handleAlerts(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	records, err := s.alerts.List(request.Context(), status, limit)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	items := make([]alertView, 0, len(records))
	for _, record := range records {
		items = append(items, alertView{
			ID:             record.ID,
			Rule:           record.Rule,
			TargetID:       record.TargetID,
			TargetName:     record.TargetName,
			Severity:       record.Severity,
			Status:         record.Status,
			Message:        record.Message,
			FiredAt:        record.FiredAt,
			ResolvedAt:     record.ResolvedAt,
			LastNotifiedAt: record.LastNotifiedAt,
			NotifyCount:    record.NotifyCount,
			Acknowledged:   record.Acknowledged,
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAlertItem(writer http.ResponseWriter, request *http.Request) {
	remainder := strings.TrimPrefix(request.URL.Path, "/api/v1/alerts/")
	switch remainder {
	case "settings":
		s.handleAlertSettings(writer, request)
		return
	case "settings/test":
		s.handleAlertSettingsTest(writer, request)
		return
	}
	parts := strings.Split(remainder, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "ack" {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if err := s.alerts.Acknowledge(request.Context(), parts[0]); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAlertSettings(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		view, err := s.alerts.Settings(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, view)
	case http.MethodPut:
		var body alert.UpdateSettingsRequest
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		view, err := s.alerts.UpdateSettings(request.Context(), body)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, view)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut)
	}
}

func (s *Server) handleAlertSettingsTest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if err := s.alerts.SendTest(request.Context()); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "sent"})
}
