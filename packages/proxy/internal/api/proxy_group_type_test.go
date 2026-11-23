package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/proxygroup"
)

type typedProxyGroups struct{ stubProxyGroupService }

func (typedProxyGroups) List(context.Context) ([]proxygroup.Group, error) {
	return []proxygroup.Group{
		{ID: "r", Type: proxygroup.TypeResidential},
		{ID: "s", Type: proxygroup.TypeSubscription},
		{ID: "c", Type: proxygroup.TypeCustom},
	}, nil
}

func TestProxyGroupListExposesAndFiltersType(t *testing.T) {
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithListeners(&consumerNodeService{}), WithProxyGroups(typedProxyGroups{}))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	get := func(query string) (int, []proxygroup.Group) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/proxy-groups"+query, nil))
		var body struct {
			Items []proxygroup.Group `json:"items"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return recorder.Code, body.Items
	}
	if code, items := get(""); code != http.StatusOK || len(items) != 3 || items[0].Type != "residential" {
		t.Fatalf("unfiltered = %d %+v", code, items)
	}
	if code, items := get("?type=residential"); code != http.StatusOK || len(items) != 1 || items[0].ID != "r" {
		t.Fatalf("residential = %d %+v", code, items)
	}
	if code, items := get("?type=subscription,custom"); code != http.StatusOK || len(items) != 2 {
		t.Fatalf("subscription,custom = %d %+v", code, items)
	}
	if code, _ := get("?type=nope"); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown type = %d, want 422", code)
	}
}
