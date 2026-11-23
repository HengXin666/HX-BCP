package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

type fakeSettingsService struct {
	settings systemsettings.Settings
}

func (service *fakeSettingsService) Get(context.Context) (systemsettings.Settings, error) {
	return service.settings, nil
}

func (service *fakeSettingsService) Update(_ context.Context, settings systemsettings.Settings) (systemsettings.Settings, error) {
	service.settings = settings
	return settings, nil
}

func TestSettingsAPIReadsAndUpdatesGlobalSettings(t *testing.T) {
	service := &fakeSettingsService{settings: systemsettings.Default()}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithSettings(service))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d", recorder.Code)
	}

	next := systemsettings.Default()
	next.Performance.LogLevel = "info"
	payload, _ := json.Marshal(next)
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.settings.Performance.LogLevel != "info" {
		t.Fatalf("PUT status = %d, settings = %+v", recorder.Code, service.settings)
	}
}
