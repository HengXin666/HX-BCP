// Package auth implements the single-administrator authentication model:
// one-time setup token bootstrap, Argon2id password storage, database-backed
// sessions with CSRF tokens, login rate limiting and full session revocation.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

var (
	ErrNotConfigured                = errors.New("administrator account is not configured")
	ErrAlreadyConfigured            = errors.New("administrator account already exists")
	ErrInvalidCredentials           = errors.New("invalid username or password")
	ErrInvalidSetupToken            = errors.New("invalid setup token")
	ErrSessionExpired               = errors.New("session is missing or expired")
	ErrLockedOut                    = errors.New("too many failed logins, retry later")
	ErrInvalidUsername              = errors.New("username must contain 3 to 64 characters")
	ErrWeakPassword                 = errors.New("password must contain 10 to 128 characters")
	ErrInvalidAPIKeyName            = errors.New("api key name must contain 1 to 64 characters")
	ErrTwoFactorUnavailable         = errors.New("two-factor authentication is unavailable")
	ErrTwoFactorNotConfigured       = errors.New("two-factor authentication is not configured")
	ErrTwoFactorAlreadyEnabled      = errors.New("two-factor authentication is already enabled")
	ErrTwoFactorNotEnabled          = errors.New("two-factor authentication is not enabled")
	ErrInvalidTwoFactorCode         = errors.New("invalid two-factor authentication code")
	ErrTwoFactorLockedOut           = errors.New("too many two-factor authentication attempts, retry later")
	ErrTwoFactorVerificationExpired = errors.New("two-factor verification has expired")
)

const (
	sessionAbsoluteTTL       = 7 * 24 * time.Hour
	sessionIdleTimeout       = 24 * time.Hour
	tokenBytes               = 32
	twoFactorVerificationTTL = 15 * time.Minute
)

type Repository interface {
	GetAdminAccount(context.Context) (store.AdminAccountRecord, error)
	CreateAdminAccount(context.Context, store.AdminAccountRecord) error
	UpdateAdminPassword(context.Context, string, time.Time) (int, error)
	UpdateAdminUsername(context.Context, string, time.Time) (int, error)
	CreateAdminSession(context.Context, store.AdminSessionRecord) error
	GetAdminSession(context.Context, string) (store.AdminSessionRecord, error)
	TouchAdminSession(context.Context, string, time.Time) error
	DeleteAdminSession(context.Context, string) error
	DeleteAllAdminSessions(context.Context) error
	DeleteExpiredAdminSessions(context.Context, time.Time, time.Duration) error
	SetAdminSessionTwoFactorVerifiedAt(context.Context, string, *time.Time) error
	ClearAdminSessionTwoFactorVerification(context.Context) error
	GetAdminTwoFactor(context.Context) (store.AdminTwoFactorRecord, error)
	UpsertAdminTwoFactor(context.Context, store.AdminTwoFactorRecord) error
	UpdateAdminTwoFactorEnabled(context.Context, bool, time.Time) error
	DeleteAdminTwoFactor(context.Context) error
	CreateAPIKey(context.Context, store.APIKeyRecord) error
	GetAPIKeyByHash(context.Context, string) (store.APIKeyRecord, error)
	TouchAPIKey(context.Context, string, time.Time) error
	ListAPIKeys(context.Context) ([]store.APIKeyRecord, error)
	DeleteAPIKey(context.Context, string) error
}

// Session is the authenticated view handed to the API layer.
type Session struct {
	Token               string
	CSRFToken           string
	Username            string
	ExpiresAt           time.Time
	TwoFactorVerifiedAt *time.Time
	// MachineCredential reports that this session came from an API key rather
	// than a browser login. The two are otherwise indistinguishable once
	// resolved (both yield a Session with the administrator's username), but a
	// policy that constrains what an unattended caller may write needs the
	// distinction, and deriving it from an empty CSRFToken would be reading a
	// coincidence: the CSRF token is empty because a header credential needs no
	// CSRF defence, not because the caller is a machine.
	MachineCredential bool
}

// APIKey is the public view of a machine credential. Key carries the
// plaintext secret and is populated only by CreateAPIKey; list views never
// expose it, and the server persists only its hash.
type APIKey struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Key        string    `json:"key,omitempty"`
}

type Service struct {
	repository       Repository
	setupTokenPath   string
	logger           *slog.Logger
	limiter          *loginLimiter
	twoFactorBox     *secret.Box
	twoFactorLimiter *loginLimiter
	now              func() time.Time
}

func NewService(repository Repository, setupTokenPath string, logger *slog.Logger, secretBoxes ...*secret.Box) (*Service, error) {
	if repository == nil {
		return nil, errors.New("auth repository is required")
	}
	if strings.TrimSpace(setupTokenPath) == "" {
		return nil, errors.New("setup token path is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	var twoFactorBox *secret.Box
	if len(secretBoxes) > 0 {
		twoFactorBox = secretBoxes[0]
	}
	return &Service{
		repository:       repository,
		setupTokenPath:   setupTokenPath,
		logger:           logger,
		limiter:          newLoginLimiter(5, 5*time.Minute),
		twoFactorBox:     twoFactorBox,
		twoFactorLimiter: newLoginLimiter(5, 5*time.Minute),
		now:              time.Now,
	}, nil
}

// Configured reports whether the administrator account exists.
func (s *Service) Configured(ctx context.Context) (bool, error) {
	_, err := s.repository.GetAdminAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// EnsureSetupToken writes a one-time setup token file (0600) when no
// administrator exists yet. The token value is never logged; only its path.
func (s *Service) EnsureSetupToken(ctx context.Context) error {
	configured, err := s.Configured(ctx)
	if err != nil {
		return err
	}
	if configured {
		_ = os.Remove(s.setupTokenPath)
		return nil
	}
	if _, err := os.Stat(s.setupTokenPath); err == nil {
		return nil
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.setupTokenPath, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write setup token: %w", err)
	}
	s.logger.Info("administrator setup token created; complete setup via POST /api/v1/auth/setup", "path", s.setupTokenPath)
	return nil
}

// Setup creates the administrator account after validating the one-time
// setup token, then removes the token file.
func (s *Service) Setup(ctx context.Context, setupToken, username, password string) error {
	configured, err := s.Configured(ctx)
	if err != nil {
		return err
	}
	if configured {
		return ErrAlreadyConfigured
	}
	stored, err := os.ReadFile(s.setupTokenPath)
	if err != nil {
		return fmt.Errorf("read setup token: %w", err)
	}
	expected := strings.TrimSpace(string(stored))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(strings.TrimSpace(setupToken))) != 1 {
		return ErrInvalidSetupToken
	}
	username, err = validateUsername(username)
	if err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if err := s.repository.CreateAdminAccount(ctx, store.AdminAccountRecord{
		Username:        username,
		PasswordHash:    hash,
		PasswordVersion: 1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); errors.Is(err, store.ErrConflict) {
		return ErrAlreadyConfigured
	} else if err != nil {
		return err
	}
	_ = os.Remove(s.setupTokenPath)
	s.logger.Info("administrator account created", "username", username)
	return nil
}

// Login verifies credentials and creates a session. clientKey identifies the
// caller for rate limiting (remote IP).
func (s *Service) Login(ctx context.Context, clientKey, username, password string) (Session, error) {
	account, err := s.repository.GetAdminAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, ErrNotConfigured
	}
	if err != nil {
		return Session{}, err
	}
	limiterKey := clientKey + "|" + strings.ToLower(strings.TrimSpace(username))
	if !s.limiter.Allow(limiterKey, s.now()) {
		s.logger.Warn("login locked out", "client", clientKey)
		return Session{}, ErrLockedOut
	}
	matched := subtle.ConstantTimeCompare([]byte(account.Username), []byte(strings.TrimSpace(username))) == 1
	passwordOK, err := VerifyPassword(account.PasswordHash, password)
	if err != nil {
		return Session{}, err
	}
	if !matched || !passwordOK {
		s.limiter.RecordFailure(limiterKey, s.now())
		s.logger.Warn("login failed", "client", clientKey)
		return Session{}, ErrInvalidCredentials
	}
	s.limiter.Reset(limiterKey)
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	record := store.AdminSessionRecord{
		TokenHash:       hashToken(token),
		CSRFToken:       csrfToken,
		PasswordVersion: account.PasswordVersion,
		CreatedAt:       now,
		LastUsedAt:      now,
		ExpiresAt:       now.Add(sessionAbsoluteTTL),
	}
	if err := s.repository.CreateAdminSession(ctx, record); err != nil {
		return Session{}, err
	}
	_ = s.repository.DeleteExpiredAdminSessions(ctx, now, sessionIdleTimeout)
	s.logger.Info("administrator logged in", "client", clientKey)
	return Session{Token: token, CSRFToken: csrfToken, Username: account.Username, ExpiresAt: record.ExpiresAt}, nil
}

// Authenticate resolves a session token to an active session, enforcing
// absolute expiry, idle timeout and password version.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if strings.TrimSpace(token) == "" {
		return Session{}, ErrSessionExpired
	}
	record, err := s.repository.GetAdminSession(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, ErrSessionExpired
	}
	if err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	if now.After(record.ExpiresAt) || now.Sub(record.LastUsedAt) > sessionIdleTimeout {
		_ = s.repository.DeleteAdminSession(ctx, record.TokenHash)
		return Session{}, ErrSessionExpired
	}
	account, err := s.repository.GetAdminAccount(ctx)
	if err != nil {
		return Session{}, ErrSessionExpired
	}
	if record.PasswordVersion != account.PasswordVersion {
		_ = s.repository.DeleteAdminSession(ctx, record.TokenHash)
		return Session{}, ErrSessionExpired
	}
	// Throttle write amplification: refresh last_used_at at most once per minute.
	if now.Sub(record.LastUsedAt) > time.Minute {
		_ = s.repository.TouchAdminSession(ctx, record.TokenHash, now)
	}
	verifiedAt := record.TwoFactorVerifiedAt
	if verifiedAt != nil && now.Sub(*verifiedAt) >= twoFactorVerificationTTL {
		verifiedAt = nil
		_ = s.repository.SetAdminSessionTwoFactorVerifiedAt(ctx, record.TokenHash, nil)
	}
	return Session{Token: token, CSRFToken: record.CSRFToken, Username: account.Username, ExpiresAt: record.ExpiresAt, TwoFactorVerifiedAt: verifiedAt}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return s.repository.DeleteAdminSession(ctx, hashToken(token))
}

// apiKeyPrefix marks generated machine credentials so operators can recognize
// them at a glance; the prefix itself carries no security meaning.
const apiKeyPrefix = "hxk_"

// CreateAPIKey issues a new machine credential for the management API. The
// plaintext key is returned exactly once; only its digest is persisted, so
// listing and database leaks never expose usable credentials.
func (s *Service) CreateAPIKey(ctx context.Context, name string) (APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return APIKey{}, ErrInvalidAPIKeyName
	}
	secret, err := randomToken()
	if err != nil {
		return APIKey{}, err
	}
	id, err := randomToken()
	if err != nil {
		return APIKey{}, err
	}
	now := s.now().UTC()
	record := store.APIKeyRecord{
		ID:         "api-key-" + id,
		Name:       name,
		KeyHash:    hashToken(apiKeyPrefix + secret),
		CreatedAt:  now,
		LastUsedAt: now,
	}
	if err := s.repository.CreateAPIKey(ctx, record); err != nil {
		return APIKey{}, err
	}
	return APIKey{
		ID:         record.ID,
		Name:       record.Name,
		CreatedAt:  record.CreatedAt,
		LastUsedAt: record.LastUsedAt,
		Key:        apiKeyPrefix + secret,
	}, nil
}

// AuthenticateAPIKey resolves a machine credential to a session. The returned
// session carries an empty CSRF token: bearer credentials are not attached by
// browsers automatically, so the API layer treats an empty CSRF token as an
// API-key session and skips the cookie CSRF gate.
func (s *Service) AuthenticateAPIKey(ctx context.Context, rawKey string) (Session, error) {
	if strings.TrimSpace(rawKey) == "" {
		return Session{}, ErrSessionExpired
	}
	record, err := s.repository.GetAPIKeyByHash(ctx, hashToken(rawKey))
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, ErrSessionExpired
	}
	if err != nil {
		return Session{}, err
	}
	account, err := s.repository.GetAdminAccount(ctx)
	if err != nil {
		return Session{}, ErrSessionExpired
	}
	// Throttle write amplification: refresh last_used_at at most once per minute.
	now := s.now().UTC()
	if now.Sub(record.LastUsedAt) > time.Minute {
		_ = s.repository.TouchAPIKey(ctx, record.ID, now)
	}
	return Session{
		Token:             rawKey,
		Username:          account.Username,
		ExpiresAt:         record.LastUsedAt,
		MachineCredential: true,
	}, nil
}

// ListAPIKeys returns the machine credentials without any secret material.
func (s *Service) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	records, err := s.repository.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]APIKey, 0, len(records))
	for _, record := range records {
		keys = append(keys, APIKey{
			ID:         record.ID,
			Name:       record.Name,
			CreatedAt:  record.CreatedAt,
			LastUsedAt: record.LastUsedAt,
		})
	}
	return keys, nil
}

// RevokeAPIKey permanently invalidates a machine credential. Revoking a key
// that does not exist is a no-op.
func (s *Service) RevokeAPIKey(ctx context.Context, id string) error {
	return s.repository.DeleteAPIKey(ctx, id)
}

func (s *Service) LogoutAll(ctx context.Context) error {
	return s.repository.DeleteAllAdminSessions(ctx)
}

// ChangePassword verifies the current password, stores the new hash and
// revokes every session (including the caller's).
func (s *Service) ChangePassword(ctx context.Context, currentPassword, newPassword string) error {
	account, err := s.repository.GetAdminAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotConfigured
	}
	if err != nil {
		return err
	}
	ok, err := VerifyPassword(account.PasswordHash, currentPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if _, err := s.repository.UpdateAdminPassword(ctx, hash, s.now()); err != nil {
		return err
	}
	if err := s.repository.DeleteAllAdminSessions(ctx); err != nil {
		return err
	}
	s.logger.Info("administrator password changed; all sessions revoked")
	return nil
}

// ChangeUsername verifies the current password, updates the login name and
// revokes every session so the new identity is required immediately.
func (s *Service) ChangeUsername(ctx context.Context, currentPassword, newUsername string) error {
	account, err := s.repository.GetAdminAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotConfigured
	}
	if err != nil {
		return err
	}
	ok, err := VerifyPassword(account.PasswordHash, currentPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	newUsername, err = validateUsername(newUsername)
	if err != nil {
		return err
	}
	if _, err := s.repository.UpdateAdminUsername(ctx, newUsername, s.now()); err != nil {
		return err
	}
	if err := s.repository.DeleteAllAdminSessions(ctx); err != nil {
		return err
	}
	s.logger.Info("administrator username changed; all sessions revoked", "username", newUsername)
	return nil
}

func validateUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 {
		return "", ErrInvalidUsername
	}
	return username, nil
}

func validatePassword(password string) error {
	if len(password) < 10 || len(password) > 128 {
		return ErrWeakPassword
	}
	return nil
}

func randomToken() (string, error) {
	var buffer [tokenBytes]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(buffer[:]), nil
}

// hashToken stores only a digest of the session token so a database leak
// does not expose usable session credentials.
func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
