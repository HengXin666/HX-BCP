package auth

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "auth.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	tokenPath := filepath.Join(root, "admin-setup-token")
	service, err := NewService(database, tokenPath, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	return service, tokenPath
}

func setupAdmin(t *testing.T, service *Service, tokenPath string) {
	t.Helper()
	ctx := context.Background()
	if err := service.EnsureSetupToken(ctx); err != nil {
		t.Fatalf("ensure setup token: %v", err)
	}
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read setup token: %v", err)
	}
	if err := service.Setup(ctx, strings.TrimSpace(string(token)), "admin", "correct horse battery"); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestSetupTokenLifecycle(t *testing.T) {
	service, tokenPath := newTestService(t)
	ctx := context.Background()

	if err := service.EnsureSetupToken(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("setup token file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("setup token must be 0600, got %v", info.Mode().Perm())
	}

	if err := service.Setup(ctx, "wrong-token", "admin", "correct horse battery"); !errors.Is(err, ErrInvalidSetupToken) {
		t.Fatalf("expected ErrInvalidSetupToken, got %v", err)
	}
	setupAdmin(t, service, tokenPath)
	if _, err := os.Stat(tokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("setup token file must be removed after setup")
	}
	if err := service.Setup(ctx, "anything", "admin", "correct horse battery"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("expected ErrAlreadyConfigured, got %v", err)
	}
	// EnsureSetupToken must not recreate the token once configured.
	if err := service.EnsureSetupToken(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("setup token must not be recreated after setup")
	}
}

func TestSetupRejectsWeakPasswords(t *testing.T) {
	service, tokenPath := newTestService(t)
	ctx := context.Background()
	if err := service.EnsureSetupToken(ctx); err != nil {
		t.Fatal(err)
	}
	token, _ := os.ReadFile(tokenPath)
	if err := service.Setup(ctx, strings.TrimSpace(string(token)), "admin", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("expected ErrWeakPassword, got %v", err)
	}
}

func TestLoginSessionAndCSRF(t *testing.T) {
	service, tokenPath := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, service, tokenPath)

	if _, err := service.Login(ctx, "127.0.0.1", "admin", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	session, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.Token == "" || session.CSRFToken == "" || session.Token == session.CSRFToken {
		t.Fatal("session and CSRF tokens must be distinct non-empty values")
	}
	resolved, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if resolved.Username != "admin" || resolved.CSRFToken != session.CSRFToken {
		t.Fatalf("unexpected session view: %+v", resolved)
	}
	if _, err := service.Authenticate(ctx, "forged-token"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired for forged token, got %v", err)
	}
	if err := service.Logout(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired after logout, got %v", err)
	}
}

func TestLoginLockout(t *testing.T) {
	service, tokenPath := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, service, tokenPath)

	for range 5 {
		if _, err := service.Login(ctx, "10.0.0.9", "admin", "bad"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	}
	if _, err := service.Login(ctx, "10.0.0.9", "admin", "correct horse battery"); !errors.Is(err, ErrLockedOut) {
		t.Fatalf("expected ErrLockedOut, got %v", err)
	}
	// A different client key is not affected.
	if _, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery"); err != nil {
		t.Fatalf("other client must not be locked out: %v", err)
	}
	// After the lockout window the client may try again.
	service.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if _, err := service.Login(ctx, "10.0.0.9", "admin", "correct horse battery"); err != nil {
		t.Fatalf("lockout must expire: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	service, tokenPath := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, service, tokenPath)
	session, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	// Idle timeout.
	service.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected idle expiry, got %v", err)
	}
}

func TestChangePasswordRevokesAllSessions(t *testing.T) {
	service, tokenPath := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, service, tokenPath)
	first, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, "127.0.0.2", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ChangePassword(ctx, "wrong", "another strong passphrase"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if err := service.ChangePassword(ctx, "correct horse battery", "another strong passphrase"); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{first, second} {
		if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("expected all sessions revoked, got %v", err)
		}
	}
	if _, err := service.Login(ctx, "127.0.0.1", "admin", "another strong passphrase"); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if _, err := service.Login(ctx, "127.0.0.3", "admin", "correct horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password must stop working, got %v", err)
	}
}

func TestChangeUsernameRevokesAllSessions(t *testing.T) {
	service, tokenPath := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, service, tokenPath)
	first, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, "127.0.0.2", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ChangeUsername(ctx, "wrong", "new-admin"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if err := service.ChangeUsername(ctx, "correct horse battery", "x"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("expected ErrInvalidUsername, got %v", err)
	}
	if err := service.ChangeUsername(ctx, "correct horse battery", "new-admin"); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{first, second} {
		if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("expected all sessions revoked, got %v", err)
		}
	}
	if _, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old username must stop working, got %v", err)
	}
	if _, err := service.Login(ctx, "127.0.0.1", "new-admin", "correct horse battery"); err != nil {
		t.Fatalf("login with new username: %v", err)
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("s3cret passphrase!")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	ok, err := VerifyPassword(hash, "s3cret passphrase!")
	if err != nil || !ok {
		t.Fatalf("verify failed: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong")
	if err != nil || ok {
		t.Fatalf("wrong password must not verify: ok=%v err=%v", ok, err)
	}
	if _, err := VerifyPassword("$plain$nope", "x"); err == nil {
		t.Fatal("malformed hash must error")
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	service, tokenPath := newTestService(t)
	setupAdmin(t, service, tokenPath)
	ctx := context.Background()

	if _, err := service.CreateAPIKey(ctx, "  "); !errors.Is(err, ErrInvalidAPIKeyName) {
		t.Fatalf("blank name: got %v, want ErrInvalidAPIKeyName", err)
	}
	if _, err := service.CreateAPIKey(ctx, strings.Repeat("k", 65)); !errors.Is(err, ErrInvalidAPIKeyName) {
		t.Fatalf("overlong name: got %v, want ErrInvalidAPIKeyName", err)
	}

	created, err := service.CreateAPIKey(ctx, "ci runner")
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if !strings.HasPrefix(created.Key, apiKeyPrefix) || created.Key == "" {
		t.Fatalf("key %q must carry the %q prefix", created.Key, apiKeyPrefix)
	}
	if created.ID == "" {
		t.Fatal("CreateAPIKey() returned empty id")
	}

	session, err := service.AuthenticateAPIKey(ctx, created.Key)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey() error = %v", err)
	}
	if session.CSRFToken != "" {
		t.Fatalf("API-key session CSRF token = %q, want empty", session.CSRFToken)
	}
	if session.Username != "admin" {
		t.Fatalf("session username = %q, want admin", session.Username)
	}

	if _, err := service.AuthenticateAPIKey(ctx, apiKeyPrefix+"wrong"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("wrong key: got %v, want ErrSessionExpired", err)
	}
	if _, err := service.AuthenticateAPIKey(ctx, ""); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("empty key: got %v, want ErrSessionExpired", err)
	}

	keys, err := service.ListAPIKeys(ctx)
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0].Key != "" {
		t.Fatalf("ListAPIKeys() = %+v, want one key without secret", keys)
	}

	if err := service.RevokeAPIKey(ctx, created.ID); err != nil {
		t.Fatalf("RevokeAPIKey() error = %v", err)
	}
	if _, err := service.AuthenticateAPIKey(ctx, created.Key); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("revoked key: got %v, want ErrSessionExpired", err)
	}
	if err := service.RevokeAPIKey(ctx, created.ID); err != nil {
		t.Fatalf("RevokeAPIKey() idempotent error = %v", err)
	}
}
