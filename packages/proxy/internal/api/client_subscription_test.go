package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnifiedClientSubscriptionAPIRemoved(t *testing.T) {
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/client-subscription", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/client-subscription", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("POST status=%d body=%s", response.Code, response.Body.String())
	}
}
