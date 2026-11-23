package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/fleet"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// FleetService is the API surface of the built-in CF worker fleet maintainer.
type FleetService interface {
	Sweep(context.Context) (fleet.Summary, error)
	ListAccounts(context.Context) ([]store.CFAccountRecord, error)
	ListAccountWorkers(context.Context, string) ([]store.FleetWorkerRecord, error)
	AddAccount(context.Context, fleet.AddAccountRequest) (store.CFAccountRecord, error)
	RemoveAccount(context.Context, string) error
	LastSummary() fleet.Summary
}

func WithFleet(service FleetService) Option {
	return func(server *Server) error {
		if service == nil {
			return errString("fleet service is required")
		}
		server.fleet = service
		return nil
	}
}

func errString(message string) error {
	return &optionError{message: message}
}

type optionError struct{ message string }

func (e *optionError) Error() string { return e.message }

func (s *Server) handleFleetStatus(writer http.ResponseWriter, request *http.Request) {
	accounts, err := s.fleet.ListAccounts(request.Context())
	if err != nil {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "fleet_list_failed", err.Error())
		return
	}
	type workerView struct {
		WorkerName string `json:"worker_name"`
		Canonical  string `json:"canonical_url"`
		ProviderID string `json:"provider_id"`
		Failures   int    `json:"failures"`
		LastCheck  string `json:"last_check"`
		LastDetail string `json:"last_detail"`
	}
	type accountView struct {
		ID       string       `json:"id"`
		Email    string       `json:"email"`
		Status   string       `json:"status"`
		BannedAt string       `json:"banned_at"`
		Workers  []workerView `json:"workers"`
	}
	views := make([]accountView, 0, len(accounts))
	total := 0
	for _, account := range accounts {
		workers, listErr := s.fleet.ListAccountWorkers(request.Context(), account.ID)
		if listErr != nil {
			continue
		}
		view := accountView{
			ID: account.ID, Email: account.Email,
			Status: account.Status, BannedAt: account.BannedAt,
			// Non-nil so the JSON contract always exposes workers as an array
			// ([]), never null. A nil slice serializes to null, which crashes
			// the fleet UI's workers.filter().
			Workers: []workerView{},
		}
		for _, record := range workers {
			view.Workers = append(view.Workers, workerView{
				WorkerName: record.WorkerName, Canonical: record.CanonicalURL,
				ProviderID: record.ProviderID, Failures: record.Failures,
				LastCheck: record.LastCheck, LastDetail: record.LastDetail,
			})
			if account.Status == "active" {
				total++
			}
		}
		views = append(views, view)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"accounts":      views,
		"total_workers": total,
		"last_summary":  s.fleet.LastSummary(),
	})
}

func (s *Server) handleFleetSweep(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		s.writeAPIError(writer, request, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	go func() {
		_, _ = s.fleet.Sweep(context.Background())
	}()
	writeJSON(writer, http.StatusAccepted, map[string]any{"started": true})
}

func (s *Server) handleFleetAccounts(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		accounts, err := s.fleet.ListAccounts(request.Context())
		if err != nil {
			s.writeAPIError(writer, request, http.StatusInternalServerError, "fleet_list_failed", err.Error())
			return
		}
		type view struct {
			ID          string `json:"id"`
			Email       string `json:"email"`
			CFAccountID string `json:"cf_account_id"`
			Status      string `json:"status"`
			BannedAt    string `json:"banned_at"`
		}
		items := make([]view, 0, len(accounts))
		for _, account := range accounts {
			items = append(items, view{
				ID: account.ID, Email: account.Email,
				CFAccountID: account.CFAccountID, Status: account.Status,
				BannedAt: account.BannedAt,
			})
		}
		writeJSON(writer, http.StatusOK, map[string]any{"accounts": items})
	case http.MethodPost:
		var payload fleet.AddAccountRequest
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&payload); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_json", "请求体不是有效 JSON")
			return
		}
		record, err := s.fleet.AddAccount(request.Context(), payload)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "fleet_account_create_failed", err.Error())
			return
		}
		writeJSON(writer, http.StatusCreated, map[string]any{
			"id": record.ID, "email": record.Email, "status": record.Status,
		})
	default:
		s.writeAPIError(writer, request, http.StatusMethodNotAllowed, "method_not_allowed", "GET/POST required")
	}
}

func (s *Server) handleFleetAccount(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/api/v1/fleet/accounts/")
	if id == "" {
		s.writeAPIError(writer, request, http.StatusNotFound, "not_found", "account id missing")
		return
	}
	if request.Method != http.MethodDelete {
		s.writeAPIError(writer, request, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required")
		return
	}
	if err := s.fleet.RemoveAccount(request.Context(), id); err != nil {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "fleet_account_delete_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"deleted": id})
}
