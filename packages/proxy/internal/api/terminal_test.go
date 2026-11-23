package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	terminalservice "github.com/HengXin666/hx-pxy/internal/terminal"
)

func TestTerminalWebSocketRejectsCrossOriginAndTracksStepUpRevocation(t *testing.T) {
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "terminal-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenPath := filepath.Join(root, "admin-setup-token")
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.NewService(database, tokenPath, logger, box)
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnsureSetupToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	setupToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.Setup(context.Background(), strings.TrimSpace(string(setupToken)), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	twoFactorSetup, err := authService.BeginTwoFactorSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	twoFactorCode, err := auth.TOTPCode(twoFactorSetup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnableTwoFactor(context.Background(), twoFactorCode); err != nil {
		t.Fatal(err)
	}
	// Simulate an enabled authenticator carried across the product rename.
	record, err := database.GetAdminTwoFactor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	record.SecretEncrypted, err = box.Seal([]byte(twoFactorSetup.Secret), []byte("hx-proxygroup/admin-totp/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertAdminTwoFactor(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	authSession, err := authService.Login(context.Background(), "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	terminalService, err := terminalservice.NewService(terminalservice.Config{Enabled: true, Shell: "/bin/sh"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer terminalService.Shutdown()
	server, err := NewServer(&stubBundleService{}, logger, WithAuth(authService), WithTerminal(terminalService))
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	websocketURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/api/v1/terminal/ws"

	headers := http.Header{}
	headers.Set("Cookie", sessionCookieName+"="+authSession.Token)
	headers.Set("Origin", testServer.URL)
	if connection, response, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: headers}); err == nil {
		connection.CloseNow()
		t.Fatal("unverified two-factor terminal WebSocket must be rejected")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("unverified two-factor response = %#v, err = %v", response, err)
	}
	verifyResponse := postJSON(t, testServer.Client(), testServer.URL+"/api/v1/auth/2fa/verify", map[string]string{"code": twoFactorCode}, map[string]string{
		"Cookie":       sessionCookieName + "=" + authSession.Token,
		"X-CSRF-Token": authSession.CSRFToken,
		"Origin":       testServer.URL,
	})
	verifyResponse.Body.Close()
	if verifyResponse.StatusCode != http.StatusOK {
		t.Fatalf("legacy TOTP HTTP verification = %d", verifyResponse.StatusCode)
	}

	headers.Set("Origin", "https://attacker.invalid")
	if connection, response, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: headers}); err == nil {
		connection.CloseNow()
		t.Fatal("cross-origin terminal WebSocket must be rejected")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin response = %#v, err = %v", response, err)
	}

	headers.Set("Origin", testServer.URL)
	previousInterval := terminalAuthRevalidateInterval
	terminalAuthRevalidateInterval = 20 * time.Millisecond
	defer func() { terminalAuthRevalidateInterval = previousInterval }()
	connection, _, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("same-origin terminal dial: %v", err)
	}
	defer connection.CloseNow()

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	modeReceived := false
	inputSent := false
	for !modeReceived {
		kind, payload, err := connection.Read(readCtx)
		if err != nil {
			t.Fatalf("read terminal mode: %v", err)
		}
		if kind != websocket.MessageText {
			continue
		}
		var mode terminalModeMessage
		if json.Unmarshal(payload, &mode) == nil && mode.Type == "mode" {
			if mode.Echo && mode.Canonical {
				modeReceived = true
			} else if !inputSent {
				input, _ := json.Marshal(terminalMessage{Type: "input", Data: "x"})
				if err := connection.Write(readCtx, websocket.MessageText, input); err != nil {
					t.Fatalf("write terminal input: %v", err)
				}
				inputSent = true
			}
		}
	}

	deadline := time.Now().Add(time.Second)
	for terminalService.Status().ActiveSessions != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if terminalService.Status().ActiveSessions != 1 {
		t.Fatal("terminal session did not become active")
	}

	// The sliding 2FA window must keep an open terminal alive: the socket
	// revalidation renews the verification timestamp instead of letting the
	// 15-minute TTL lapse mid-session.
	verifiedBefore, err := authService.Authenticate(context.Background(), authSession.Token)
	if err != nil {
		t.Fatal(err)
	}
	if verifiedBefore.TwoFactorVerifiedAt == nil {
		t.Fatal("open terminal session must keep two-factor verification")
	}
	time.Sleep(80 * time.Millisecond)
	verifiedAfter, err := authService.Authenticate(context.Background(), authSession.Token)
	if err != nil {
		t.Fatal(err)
	}
	if verifiedAfter.TwoFactorVerifiedAt == nil || !verifiedAfter.TwoFactorVerifiedAt.After(*verifiedBefore.TwoFactorVerifiedAt) {
		t.Fatal("terminal revalidation must slide the two-factor verification window forward")
	}

	if err := authService.DisableTwoFactor(context.Background(), twoFactorCode); err != nil {
		t.Fatal(err)
	}
	closedCtx, cancelClosed := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelClosed()
	for {
		if _, _, err := connection.Read(closedCtx); err != nil {
			break
		}
	}
	deadline = time.Now().Add(time.Second)
	for terminalService.Status().ActiveSessions != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if terminalService.Status().ActiveSessions != 0 {
		t.Fatal("disabled two-factor authentication left terminal running")
	}

	// Re-enable 2FA on the same administrator session to verify that a later
	// session revocation still terminates an already-open terminal.
	twoFactorSetup, err = authService.BeginTwoFactorSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	twoFactorCode, err = auth.TOTPCode(twoFactorSetup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnableTwoFactor(context.Background(), twoFactorCode); err != nil {
		t.Fatal(err)
	}
	if err := authService.VerifyTwoFactor(context.Background(), authSession.Token, "127.0.0.1", twoFactorCode); err != nil {
		t.Fatal(err)
	}
	connection, _, err = websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("same-origin terminal redial: %v", err)
	}
	defer connection.CloseNow()
	if _, _, err := connection.Read(readCtx); err != nil {
		t.Fatalf("read terminal mode after re-enable: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for terminalService.Status().ActiveSessions != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if terminalService.Status().ActiveSessions != 1 {
		t.Fatal("terminal session did not become active after re-enabling two-factor authentication")
	}

	if err := authService.LogoutAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, err := connection.Read(closedCtx); err != nil {
			break
		}
	}
	deadline = time.Now().Add(time.Second)
	for terminalService.Status().ActiveSessions != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if terminalService.Status().ActiveSessions != 0 {
		t.Fatal("revoked administrator session left terminal running")
	}
}

// memoryCwdStore is the API-test CwdStore: it records writes so the test can
// assert that a cwd control frame reaches the terminal service.
type memoryCwdStore struct {
	mutex  sync.Mutex
	values map[string]string
}

func (m *memoryCwdStore) GetMetadata(_ context.Context, key string) (string, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	value, ok := m.values[key]
	if !ok {
		return "", store.ErrNotFound
	}
	return value, nil
}

func (m *memoryCwdStore) SetMetadata(_ context.Context, key, value string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.values[key] = value
	return nil
}

func (m *memoryCwdStore) read(key string) string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.values[key]
}

// newTerminalAPITestServer builds the full auth + 2FA + terminal stack and
// returns the pieces a WebSocket test needs.
func newTerminalAPITestServer(t *testing.T, options ...Option) (*auth.Service, *terminalservice.Service, *memoryCwdStore, string, string) {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "terminal-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenPath := filepath.Join(root, "admin-setup-token")
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.NewService(database, tokenPath, logger, box)
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnsureSetupToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	setupToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.Setup(context.Background(), strings.TrimSpace(string(setupToken)), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	twoFactorSetup, err := authService.BeginTwoFactorSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	twoFactorCode, err := auth.TOTPCode(twoFactorSetup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnableTwoFactor(context.Background(), twoFactorCode); err != nil {
		t.Fatal(err)
	}
	authSession, err := authService.Login(context.Background(), "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.VerifyTwoFactor(context.Background(), authSession.Token, "127.0.0.1", twoFactorCode); err != nil {
		t.Fatal(err)
	}
	cwdStore := &memoryCwdStore{values: map[string]string{}}
	terminalService, err := terminalservice.NewService(terminalservice.Config{
		Enabled:  true,
		Shell:    "/bin/sh",
		CwdStore: cwdStore,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminalService.Shutdown)
	server, err := NewServer(&stubBundleService{}, logger, append([]Option{WithAuth(authService), WithTerminal(terminalService)}, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	websocketURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/api/v1/terminal/ws"
	return authService, terminalService, cwdStore, websocketURL, authSession.Token
}

// TestTerminalWebSocketPingPongAndCwdReport verifies the lossy-network control
// protocol: the server answers application-level pings with pongs (so the
// client can detect a half-dead connection) and persists cwd reports.
func TestTerminalWebSocketPingPongAndCwdReport(t *testing.T) {
	_, _, cwdStore, websocketURL, sessionToken := newTerminalAPITestServer(t)
	headers := http.Header{}
	headers.Set("Cookie", sessionCookieName+"="+sessionToken)
	origin := "http://" + strings.TrimSuffix(strings.TrimPrefix(websocketURL, "ws://"), "/api/v1/terminal/ws")
	headers.Set("Origin", origin)
	connection, _, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("terminal dial: %v", err)
	}
	defer connection.CloseNow()

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	// Drain the initial mode frame.
	for {
		kind, payload, readErr := connection.Read(readCtx)
		if readErr != nil {
			t.Fatalf("read terminal mode: %v", readErr)
		}
		if kind != websocket.MessageText {
			continue
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) == nil && message.Type == "mode" {
			break
		}
	}

	// ping -> pong.
	ping, _ := json.Marshal(terminalMessage{Type: "ping"})
	if err := connection.Write(readCtx, websocket.MessageText, ping); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	pongSeen := false
	for time.Now().Before(deadline) && !pongSeen {
		kind, payload, readErr := connection.Read(readCtx)
		if readErr != nil {
			t.Fatalf("read pong: %v", readErr)
		}
		if kind != websocket.MessageText {
			continue
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) == nil && message.Type == "pong" {
			pongSeen = true
		}
	}
	if !pongSeen {
		t.Fatal("server did not answer the application-level ping with a pong")
	}

	// cwd control frame -> persisted in the terminal service's CwdStore.
	reportDir := t.TempDir()
	cwd, _ := json.Marshal(terminalMessage{Type: "cwd", Cwd: reportDir})
	if err := connection.Write(readCtx, websocket.MessageText, cwd); err != nil {
		t.Fatalf("write cwd report: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value := cwdStore.read("terminal_cwd:admin")
		if value == reportDir {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cwd report never reached the store; want %q", reportDir)
}

// TestTerminalHeartbeatReapsSilentPeer verifies the server heartbeat: a peer
// that stops reading cannot answer pings, so the server must close the socket
// and free the session slot instead of leaking it forever.
func TestTerminalHeartbeatReapsSilentPeer(t *testing.T) {
	_, terminalService, _, websocketURL, sessionToken := newTerminalAPITestServer(t, withHeartbeatBudget(30*time.Millisecond, 60*time.Millisecond))
	headers := http.Header{}
	headers.Set("Cookie", sessionCookieName+"="+sessionToken)
	origin := "http://" + strings.TrimSuffix(strings.TrimPrefix(websocketURL, "ws://"), "/api/v1/terminal/ws")
	headers.Set("Origin", origin)
	connection, _, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("terminal dial: %v", err)
	}
	defer connection.CloseNow()

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	// Read the initial mode frame so the session is confirmed active, then
	// STOP reading: a real browser auto-pongs, but a peer that never reads
	// cannot answer server pings.
	deadline := time.Now().Add(3 * time.Second)
	for {
		kind, payload, readErr := connection.Read(readCtx)
		if readErr != nil {
			t.Fatalf("read terminal mode: %v", readErr)
		}
		if kind != websocket.MessageText {
			continue
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) == nil && message.Type == "mode" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mode frame never arrived")
		}
	}
	if active := terminalService.Status().ActiveSessions; active != 1 {
		t.Fatalf("active sessions = %d, want 1", active)
	}

	// Now the peer goes silent. The heartbeat must reap it and release the slot.
	deadline = time.Now().Add(3 * time.Second)
	for terminalService.Status().ActiveSessions != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active := terminalService.Status().ActiveSessions; active != 0 {
		t.Fatalf("silent peer was not reaped by the heartbeat; active = %d", active)
	}
}

func withHeartbeatBudget(interval, timeout time.Duration) Option {
	return func(s *Server) error { s.terminalHeartbeat = terminalHeartbeatBudget{interval, timeout}; return nil }
}
