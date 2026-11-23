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

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/proxyservice"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// recordingProxyServices captures the request the handler finally hands to the
// application service, so a test can assert on the contract rather than on the
// response body alone.
type recordingProxyServices struct {
	requests []proxyservice.UpdateRequest
}

func (service *recordingProxyServices) Create(context.Context, proxyservice.CreateRequest) (proxyservice.ServiceRecord, error) {
	return proxyservice.ServiceRecord{}, nil
}

func (service *recordingProxyServices) Update(_ context.Context, request proxyservice.UpdateRequest) (proxyservice.ServiceRecord, error) {
	service.requests = append(service.requests, request)
	return proxyservice.ServiceRecord{}, nil
}

func newProxyServiceServer(t *testing.T, service ProxyServiceService) string {
	t.Helper()
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithProxyServices(service))
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	return testServer.URL
}

func putProxyService(t *testing.T, endpoint, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("PUT %s error = %v", endpoint, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func postProxyService(t *testing.T, endpoint, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s error = %v", endpoint, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// newRealProxyServiceServer wires the real application service into the HTTP
// handler. These tests cover the whole path a caller sees (decode -> domain
// validation -> error mapping); a stub returning a hand-made sentinel would only
// prove that handleError can map it, not that the service produces it.
func newRealProxyServiceServer(t *testing.T, groups proxyservice.GroupService, listeners proxyservice.ListenerService) string {
	t.Helper()
	service, err := proxyservice.NewService(groups, listeners, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return newProxyServiceServer(t, service)
}

// apiFakeGroups answers lookups from one stored group and counts the creates, so
// a test can assert that a reuse request created nothing.
type apiFakeGroups struct {
	stored  proxygroup.Group
	getErr  error
	creates int
}

func (fake *apiFakeGroups) Create(context.Context, proxygroup.CreateRequest) (proxygroup.Group, error) {
	fake.creates++
	return proxygroup.Group{ID: "group-created", Version: 1, Enabled: true}, nil
}

func (fake *apiFakeGroups) Get(context.Context, string) (proxygroup.Group, error) {
	return fake.stored, fake.getErr
}

func (fake *apiFakeGroups) Update(context.Context, string, proxygroup.UpdateRequest) (proxygroup.Group, error) {
	return fake.stored, nil
}

func (fake *apiFakeGroups) Delete(context.Context, string, int) error { return nil }

type apiFakeListeners struct{ creates int }

func (fake *apiFakeListeners) Delete(context.Context, string, int) error { return nil }

func (fake *apiFakeListeners) Create(_ context.Context, request listener.CreateRequest) (listener.Listener, error) {
	fake.creates++
	return listener.Listener{ID: "listener-new", ProxyGroupID: request.ProxyGroupID, Kind: request.Kind}, nil
}

func (fake *apiFakeListeners) Get(context.Context, string) (listener.Listener, error) {
	return listener.Listener{}, listener.ErrNotFound
}

func (fake *apiFakeListeners) Update(context.Context, string, listener.UpdateRequest) (listener.Listener, error) {
	return listener.Listener{}, nil
}

func (fake *apiFakeListeners) EnsureSharedInbounds(
	context.Context,
	listener.SharedInboundSpec,
	[]store.ProxyGroupRecord,
	[]listener.SharedInboundMember,
) ([]listener.Listener, error) {
	return nil, nil
}

// reuseProxyServiceBody is the payload the create form sends for a residential
// source: a listener that names an existing group, and no group-creation field.
const reuseProxyServiceBody = `{"listener":{"name":"住宅入口","kind":"mixed","port":7890,"auth":{"username":"hx-user","password":"hx-pass"},"proxy_group_id":"group-residential"}}`

func TestCreateProxyServiceReusingAnEnabledGroupReturns201(t *testing.T) {
	groups := &apiFakeGroups{stored: proxygroup.Group{
		ID: "group-residential", Version: 3, Name: "residential-linkup", Enabled: true,
	}}
	listeners := &apiFakeListeners{}
	endpoint := newRealProxyServiceServer(t, groups, listeners) + "/api/v1/proxy-services"

	response := postProxyService(t, endpoint, reuseProxyServiceBody)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	var record proxyservice.ServiceRecord
	if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
		t.Fatalf("decode created service: %v", err)
	}
	if record.Group.ID != "group-residential" {
		t.Fatalf("group = %q, want the reused %q", record.Group.ID, "group-residential")
	}
	if record.Listener.ProxyGroupID != "group-residential" {
		t.Fatalf("listener group = %q, want %q", record.Listener.ProxyGroupID, "group-residential")
	}
	if groups.creates != 0 {
		t.Fatalf("group creates = %d, want 0: the request named an existing group", groups.creates)
	}
	if listeners.creates != 1 {
		t.Fatalf("listener creates = %d, want 1", listeners.creates)
	}
}

// TestCreateProxyServiceReusingADisabledGroupIsAValidationError pins the status
// the operator's mistake deserves: the request is well-formed but refers to a
// group whose exits are not in the data plane, so it is a 422 - never the 500 a
// caller would get from an unmapped domain error, and never a silently created
// entry point that routes nowhere.
func TestCreateProxyServiceReusingADisabledGroupIsAValidationError(t *testing.T) {
	groups := &apiFakeGroups{stored: proxygroup.Group{
		ID: "group-residential", Version: 2, Name: "residential-linkup", Enabled: false,
	}}
	listeners := &apiFakeListeners{}
	endpoint := newRealProxyServiceServer(t, groups, listeners) + "/api/v1/proxy-services"

	response := postProxyService(t, endpoint, reuseProxyServiceBody)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnprocessableEntity)
	}
	if code := errorCode(t, response); code != "validation_failed" {
		t.Fatalf("error code = %q, want %q", code, "validation_failed")
	}
	if listeners.creates != 0 {
		t.Fatalf("listener creates = %d, want 0", listeners.creates)
	}
}

// TestCreateProxyServiceReusingAnUnknownGroupIsAValidationError is the same
// boundary for a stale id.
func TestCreateProxyServiceReusingAnUnknownGroupIsAValidationError(t *testing.T) {
	groups := &apiFakeGroups{
		stored: proxygroup.Group{ID: "group-other", Enabled: true},
		getErr: proxygroup.ErrNotFound,
	}
	listeners := &apiFakeListeners{}
	endpoint := newRealProxyServiceServer(t, groups, listeners) + "/api/v1/proxy-services"

	response := postProxyService(t, endpoint, reuseProxyServiceBody)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnprocessableEntity)
	}
	if code := errorCode(t, response); code != "validation_failed" {
		t.Fatalf("error code = %q, want %q", code, "validation_failed")
	}
	if groups.creates != 0 {
		t.Fatalf("group creates = %d, want 0", groups.creates)
	}
}

func errorCode(t *testing.T, response *http.Response) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return payload.Error.Code
}

// TestUpdateProxyServiceAddressIsAuthoritative pins the fix for an id that was
// parsed and then dropped: PUT /proxy-services/<id> used to edit whatever
// group_id the body named, so any session could write to any service by
// addressing a different one.
func TestUpdateProxyServiceAddressIsAuthoritative(t *testing.T) {
	service := &recordingProxyServices{}
	endpoint := newProxyServiceServer(t, service) + "/api/v1/proxy-services/group-addressed"

	response := putProxyService(t, endpoint, `{"group_version":1,"listener_id":"listener-1","listener_version":1}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if len(service.requests) != 1 {
		t.Fatalf("update calls = %d, want 1", len(service.requests))
	}
	if got := service.requests[0].GroupID; got != "group-addressed" {
		t.Fatalf("group id = %q, want the addressed id %q", got, "group-addressed")
	}
}

func TestUpdateProxyServiceRejectsContradictingBody(t *testing.T) {
	service := &recordingProxyServices{}
	endpoint := newProxyServiceServer(t, service) + "/api/v1/proxy-services/group-addressed"

	response := putProxyService(t, endpoint, `{"group_id":"group-other","group_version":1,"listener_id":"listener-1","listener_version":1}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if code := errorCode(t, response); code != "invalid_request" {
		t.Fatalf("error code = %q, want %q", code, "invalid_request")
	}
	if len(service.requests) != 0 {
		t.Fatalf("update calls = %d, want 0", len(service.requests))
	}
}

// TestUpdateProxyServiceRejectsEmptyBody is the regression guard for the
// original symptom: a request whose body never arrived decoded into a
// zero-valued request and was answered with 404, which blamed a missing
// resource for a malformed request.
func TestUpdateProxyServiceRejectsEmptyBody(t *testing.T) {
	service := &recordingProxyServices{}
	endpoint := newProxyServiceServer(t, service) + "/api/v1/proxy-services/group-addressed"

	for _, body := range []string{"", "{}", `{"group_version":1}`, `{"listener_id":"listener-1"}`} {
		response := putProxyService(t, endpoint, body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want %d", body, response.StatusCode, http.StatusBadRequest)
		}
	}
	if len(service.requests) != 0 {
		t.Fatalf("update calls = %d, want 0", len(service.requests))
	}
}
