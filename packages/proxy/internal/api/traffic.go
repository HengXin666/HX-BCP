package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/metrics"
)

func (s *Server) handleTraffic(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	resourceType := strings.TrimSpace(request.URL.Query().Get("resource_type"))
	resourceID := strings.TrimSpace(request.URL.Query().Get("resource_id"))
	if !metrics.ValidResourceType(resourceType) {
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", "resource_type must be listener, proxy_group, node, or residential_channel")
		return
	}
	if resourceID == "" {
		limit, ok := parseBoundedInteger(request.URL.Query().Get("limit"), 100, 1, 1000)
		if !ok {
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", "limit must be between 1 and 1000")
			return
		}
		offset, ok := parseBoundedInteger(request.URL.Query().Get("offset"), 0, 0, 1_000_000)
		if !ok {
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", "offset must be non-negative")
			return
		}
		items, err := s.listTrafficSummaries(request, resourceType, limit, offset)
		if err != nil {
			if errors.Is(err, metrics.ErrInvalidQuery) {
				s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
			} else {
				s.handleError(writer, request, err)
			}
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset})
		return
	}

	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)
	var err error
	if raw := strings.TrimSpace(request.URL.Query().Get("from")); raw != "" {
		from, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", "from must be an RFC3339 timestamp")
			return
		}
	}
	if raw := strings.TrimSpace(request.URL.Query().Get("to")); raw != "" {
		to, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", "to must be an RFC3339 timestamp")
			return
		}
	}
	maxPoints, ok := parseBoundedInteger(request.URL.Query().Get("max_points"), 240, 1, 500)
	if !ok {
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", "max_points must be between 1 and 500")
		return
	}
	series, err := s.traffic.Query(request.Context(), resourceType, resourceID, from, to, maxPoints)
	if err != nil {
		if errors.Is(err, metrics.ErrInvalidQuery) {
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		} else {
			s.handleError(writer, request, err)
		}
		return
	}
	writeJSON(writer, http.StatusOK, series)
}

func (s *Server) listTrafficSummaries(request *http.Request, resourceType string, limit, offset int) ([]metrics.Summary, error) {
	fromValue := strings.TrimSpace(request.URL.Query().Get("from"))
	toValue := strings.TrimSpace(request.URL.Query().Get("to"))
	if fromValue == "" && toValue == "" {
		return s.traffic.ListSummaries(request.Context(), resourceType, limit, offset)
	}
	if fromValue == "" || toValue == "" {
		return nil, fmt.Errorf("%w: from and to must be provided together", metrics.ErrInvalidQuery)
	}
	from, err := time.Parse(time.RFC3339, fromValue)
	if err != nil {
		return nil, fmt.Errorf("%w: from must be an RFC3339 timestamp", metrics.ErrInvalidQuery)
	}
	to, err := time.Parse(time.RFC3339, toValue)
	if err != nil {
		return nil, fmt.Errorf("%w: to must be an RFC3339 timestamp", metrics.ErrInvalidQuery)
	}
	if rangeService, ok := s.traffic.(TrafficRangeService); ok {
		return rangeService.ListSummariesBetween(request.Context(), resourceType, from, to, limit, offset)
	}
	return nil, fmt.Errorf("%w: traffic range summaries are unavailable", metrics.ErrInvalidQuery)
}

func parseBoundedInteger(raw string, fallback, minimum, maximum int) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= minimum && value <= maximum
}
