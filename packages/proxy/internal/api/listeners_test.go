package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

// usernameTakenListeners reproduces the application-layer refusal: a member
// whose username is already claimed by another member of the same family.
type usernameTakenListeners struct{ edgeListenerService }

func (*usernameTakenListeners) Create(context.Context, listener.CreateRequest) (listener.Listener, error) {
	return listener.Listener{}, fmt.Errorf(
		"%w: username %q is already used by listener %q on the standard shared inbound; choose a different username",
		listener.ErrUsernameTaken, "hx-user", "shadowmere 入口",
	)
}

// TestCreateListenerMapsDuplicateUsernameToConflict pins the caller-visible
// contract: a duplicate username is a request conflict, not a data-plane
// failure, so the client can fix the request instead of retrying it.
func TestCreateListenerMapsDuplicateUsernameToConflict(t *testing.T) {
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithListeners(&usernameTakenListeners{}))
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/listeners", strings.NewReader(
		`{"name":"second","kind":"mixed","bind_address":"0.0.0.0","port":7890,"proxy_group_id":"group-a","auth":{"username":"hx-user","password":"hx-pass"}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "shared_inbound_username_taken" {
		t.Fatalf("error code = %q, want shared_inbound_username_taken", body.Error.Code)
	}
}

func TestRequestedShareFormat(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		userAgent string
		want      string
	}{
		{name: "explicit format wins", query: "?format=sing-box", userAgent: "Clash-Verge/v2", want: "sing-box"},
		{name: "clash verge", userAgent: "Clash-Verge/v2.4.2", want: "clash"},
		{name: "mihomo", userAgent: "mihomo/1.19", want: "clash"},
		{name: "sing-box", userAgent: "sing-box 1.12", want: "sing-box"},
		{name: "v2rayn remains default", userAgent: "v2rayN/7.0", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/sub/token"+test.query, nil)
			request.Header.Set("User-Agent", test.userAgent)
			if got := requestedShareFormat(request); got != test.want {
				t.Fatalf("requestedShareFormat() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCanonicalShareFormat(t *testing.T) {
	tests := map[string]string{
		"":         "v2rayn",
		"v2rayn":   "v2rayn",
		"clash":    "clash",
		"Mihomo":   "clash",
		"singbox":  "sing-box",
		"sing-box": "sing-box",
		"uri":      "uri",
	}
	for input, want := range tests {
		if got := canonicalShareFormat(input); got != want {
			t.Errorf("canonicalShareFormat(%q) = %q, want %q", input, got, want)
		}
	}
}
