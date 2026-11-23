package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/subscription"
)

const subscriptionBodyLimit = 5 << 20

func (s *Server) handleSubscriptions(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		limit, err := parseIntegerQuery(request, "limit", 100, 1, 500)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		offset, err := parseIntegerQuery(request, "offset", 0, 0, 1_000_000)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		items, err := s.subscriptions.List(request.Context(), limit, offset)
		if err != nil {
			s.handleSubscriptionError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"items":  items,
			"limit":  limit,
			"offset": offset,
		})
	case http.MethodPost:
		if request.URL.Query().Get("action") == "refresh" {
			var refreshRequest struct {
				SubscriptionIDs []string `json:"subscription_ids"`
			}
			if err := decodeJSONBody(writer, request, &refreshRequest); err != nil {
				s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			results, err := s.subscriptions.RefreshMany(request.Context(), refreshRequest.SubscriptionIDs)
			if err != nil {
				s.handleSubscriptionError(writer, request, err)
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"items": results})
			return
		}
		var createRequest subscription.CreateRequest
		if err := decodeJSONBodyWithLimit(writer, request, &createRequest, subscriptionBodyLimit); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.subscriptions.Create(request.Context(), createRequest)
		if err != nil {
			s.handleSubscriptionError(writer, request, err)
			return
		}
		writer.Header().Set("Location", "/api/v1/subscriptions/"+created.ID)
		writeJSON(writer, http.StatusCreated, created)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleSubscription(writer http.ResponseWriter, request *http.Request) {
	remainder := strings.TrimPrefix(request.URL.Path, "/api/v1/subscriptions/")
	if remainder == "" || remainder == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	parts := strings.Split(remainder, "/")
	if len(parts) > 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		if parts[1] != "refresh" {
			http.NotFound(writer, request)
			return
		}
		s.handleSubscriptionRefresh(writer, request, id)
		return
	}

	switch request.Method {
	case http.MethodGet:
		item, err := s.subscriptions.Get(request.Context(), id)
		if err != nil {
			s.handleSubscriptionError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, item)
	case http.MethodPut:
		var updateRequest subscription.UpdateRequest
		if err := decodeJSONBodyWithLimit(writer, request, &updateRequest, subscriptionBodyLimit); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := s.subscriptions.Update(request.Context(), id, updateRequest)
		if err != nil {
			s.handleSubscriptionError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case http.MethodDelete:
		version, err := parseIntegerQuery(request, "version", 0, 1, 1<<30)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if err := s.subscriptions.Delete(request.Context(), id, version); err != nil {
			s.handleSubscriptionError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleSubscriptionRefresh(writer http.ResponseWriter, request *http.Request, id string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	result, err := s.subscriptions.Refresh(request.Context(), id)
	if err != nil {
		s.handleSubscriptionError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) handleSubscriptionError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, subscription.ErrInvalid):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "invalid_subscription", err.Error())
	case errors.Is(err, subscription.ErrNotFound):
		s.writeAPIError(writer, request, http.StatusNotFound, "subscription_not_found", "subscription not found")
	case errors.Is(err, subscription.ErrConflict):
		s.writeAPIError(writer, request, http.StatusConflict, "subscription_conflict", "subscription was modified or conflicts with an existing resource")
	default:
		s.handleError(writer, request, err)
	}
}

func parseIntegerQuery(request *http.Request, name string, fallback, minimum, maximum int) (int, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		if fallback < minimum {
			return 0, errors.New(name + " is required")
		}
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New(name + " is outside the allowed range")
	}
	return parsed, nil
}

func decodeJSONBodyWithLimit(writer http.ResponseWriter, request *http.Request, destination any, limit int64) error {
	if request.Body == nil {
		return errors.New("request body is required")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}
