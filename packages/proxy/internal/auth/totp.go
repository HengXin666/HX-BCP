package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const (
	twoFactorAssociatedData = "hx-pxy/admin-totp/v1"
	twoFactorIssuer         = "HX-Pxy"
	twoFactorPeriod         = 30 * time.Second
	twoFactorDigits         = 6
	twoFactorSecretBytes    = 20
)

type TwoFactorStatus struct {
	Configured             bool `json:"configured"`
	Enabled                bool `json:"enabled"`
	Verified               bool `json:"verified"`
	VerificationTTLSeconds int  `json:"verification_ttl_seconds"`
}

type TwoFactorSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

func (s *Service) TwoFactorStatus(ctx context.Context, token string) (TwoFactorStatus, error) {
	session, err := s.Authenticate(ctx, token)
	if err != nil {
		return TwoFactorStatus{}, err
	}
	record, err := s.repository.GetAdminTwoFactor(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return TwoFactorStatus{VerificationTTLSeconds: int(twoFactorVerificationTTL / time.Second)}, nil
	}
	if err != nil {
		return TwoFactorStatus{}, err
	}
	return TwoFactorStatus{
		Configured:             true,
		Enabled:                record.Enabled,
		Verified:               session.TwoFactorVerifiedAt != nil,
		VerificationTTLSeconds: int(twoFactorVerificationTTL / time.Second),
	}, nil
}

func (s *Service) BeginTwoFactorSetup(ctx context.Context) (TwoFactorSetup, error) {
	if s.twoFactorBox == nil {
		return TwoFactorSetup{}, ErrTwoFactorUnavailable
	}
	account, err := s.repository.GetAdminAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return TwoFactorSetup{}, ErrNotConfigured
	}
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if record, recordErr := s.repository.GetAdminTwoFactor(ctx); recordErr == nil && record.Enabled {
		return TwoFactorSetup{}, ErrTwoFactorAlreadyEnabled
	} else if recordErr != nil && !errors.Is(recordErr, store.ErrNotFound) {
		return TwoFactorSetup{}, recordErr
	}
	secretValue, err := generateTOTPSecret()
	if err != nil {
		return TwoFactorSetup{}, err
	}
	encrypted, err := s.twoFactorBox.Seal([]byte(secretValue), []byte(twoFactorAssociatedData))
	if err != nil {
		return TwoFactorSetup{}, fmt.Errorf("encrypt two-factor secret: %w", err)
	}
	now := s.now().UTC()
	if err := s.repository.UpsertAdminTwoFactor(ctx, store.AdminTwoFactorRecord{
		SecretEncrypted: encrypted,
		Enabled:         false,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		return TwoFactorSetup{}, err
	}
	return TwoFactorSetup{Secret: secretValue, OTPAuthURL: buildOTPAuthURL(account.Username, secretValue)}, nil
}

func (s *Service) EnableTwoFactor(ctx context.Context, code string) error {
	record, secretValue, err := s.loadTwoFactorSecret(ctx, true)
	if err != nil {
		return err
	}
	if record.Enabled {
		return ErrTwoFactorAlreadyEnabled
	}
	if !verifyTOTP(secretValue, code, s.now()) {
		return ErrInvalidTwoFactorCode
	}
	return s.repository.UpdateAdminTwoFactorEnabled(ctx, true, s.now())
}

func (s *Service) DisableTwoFactor(ctx context.Context, code string) error {
	record, secretValue, err := s.loadTwoFactorSecret(ctx, true)
	if err != nil {
		return err
	}
	if !record.Enabled {
		return ErrTwoFactorNotEnabled
	}
	if !verifyTOTP(secretValue, code, s.now()) {
		return ErrInvalidTwoFactorCode
	}
	if err := s.repository.DeleteAdminTwoFactor(ctx); err != nil {
		return err
	}
	return s.repository.ClearAdminSessionTwoFactorVerification(ctx)
}

func (s *Service) VerifyTwoFactor(ctx context.Context, token, clientKey, code string) error {
	session, err := s.Authenticate(ctx, token)
	if err != nil {
		return err
	}
	record, secretValue, err := s.loadTwoFactorSecret(ctx, true)
	if err != nil {
		return err
	}
	if !record.Enabled {
		return ErrTwoFactorNotEnabled
	}
	limiterKey := "two-factor|" + hashToken(session.Token) + "|" + clientKey
	if !s.twoFactorLimiter.Allow(limiterKey, s.now()) {
		return ErrTwoFactorLockedOut
	}
	if !verifyTOTP(secretValue, code, s.now()) {
		s.twoFactorLimiter.RecordFailure(limiterKey, s.now())
		return ErrInvalidTwoFactorCode
	}
	s.twoFactorLimiter.Reset(limiterKey)
	now := s.now().UTC()
	return s.repository.SetAdminSessionTwoFactorVerifiedAt(ctx, hashToken(token), &now)
}

// RenewTwoFactorVerification slides the two-factor verification window of an
// already-verified session forward. Privileged long-lived channels (the
// browser terminal) call this periodically so an actively used session is not
// cut off when the original code reaches its TTL. The window still lapses when
// renewal stops, and a session whose window already lapsed cannot renew.
func (s *Service) RenewTwoFactorVerification(ctx context.Context, token string) error {
	session, err := s.Authenticate(ctx, token)
	if err != nil {
		return err
	}
	if session.TwoFactorVerifiedAt == nil {
		return ErrTwoFactorVerificationExpired
	}
	now := s.now().UTC()
	return s.repository.SetAdminSessionTwoFactorVerifiedAt(ctx, hashToken(token), &now)
}

func (s *Service) loadTwoFactorSecret(ctx context.Context, requireSecretBox bool) (store.AdminTwoFactorRecord, string, error) {
	if requireSecretBox && s.twoFactorBox == nil {
		return store.AdminTwoFactorRecord{}, "", ErrTwoFactorUnavailable
	}
	record, err := s.repository.GetAdminTwoFactor(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return store.AdminTwoFactorRecord{}, "", ErrTwoFactorNotConfigured
	}
	if err != nil {
		return store.AdminTwoFactorRecord{}, "", err
	}
	plaintext, err := s.twoFactorBox.Open(record.SecretEncrypted, []byte(twoFactorAssociatedData))
	if err != nil {
		// AAD is a storage contract, not branding. Keep both deployed formats.
		// See .agents/notes/implemented/bug-fix/2026-10-15-totp-upgrade-compatibility.md.
		plaintext, err = s.twoFactorBox.Open(record.SecretEncrypted, []byte("hx-proxygroup/admin-totp/v1"))
	}
	if err != nil {
		return store.AdminTwoFactorRecord{}, "", fmt.Errorf("decrypt two-factor secret: %w", err)
	}
	return record, string(plaintext), nil
}

func generateTOTPSecret() (string, error) {
	secretBytes := make([]byte, twoFactorSecretBytes)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", fmt.Errorf("generate two-factor secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secretBytes), nil
}

func buildOTPAuthURL(username, secretValue string) string {
	values := url.Values{}
	values.Set("secret", secretValue)
	values.Set("issuer", twoFactorIssuer)
	values.Set("algorithm", "SHA1")
	values.Set("digits", strconv.Itoa(twoFactorDigits))
	values.Set("period", strconv.Itoa(int(twoFactorPeriod/time.Second)))
	label := url.PathEscape(twoFactorIssuer + ":" + username)
	return "otpauth://totp/" + label + "?" + values.Encode()
}

func verifyTOTP(secretValue, inputCode string, now time.Time) bool {
	code, err := normalizeTOTPCode(inputCode)
	if err != nil {
		return false
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secretValue)))
	if err != nil || len(decoded) == 0 {
		return false
	}
	counter := now.Unix() / int64(twoFactorPeriod/time.Second)
	for offset := int64(-1); offset <= 1; offset++ {
		candidateCounter := counter + offset
		if candidateCounter < 0 {
			continue
		}
		candidate := totpCode(decoded, uint64(candidateCounter))
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func TOTPCode(secretValue string, now time.Time) (string, error) {
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secretValue)))
	if err != nil || len(decoded) == 0 {
		return "", errors.New("invalid TOTP secret")
	}
	counter := now.Unix() / int64(twoFactorPeriod/time.Second)
	if counter < 0 {
		return "", errors.New("TOTP time is before Unix epoch")
	}
	return totpCode(decoded, uint64(counter)), nil
}

func totpCode(secretValue []byte, counter uint64) string {
	var counterBytes [8]byte
	binary.BigEndian.PutUint64(counterBytes[:], counter)
	digest := hmac.New(sha1.New, secretValue)
	_, _ = digest.Write(counterBytes[:])
	sum := digest.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%0*d", twoFactorDigits, value%1000000)
}

func normalizeTOTPCode(input string) (string, error) {
	input = strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(input))
	if len(input) != twoFactorDigits {
		return "", errors.New("TOTP code must contain six digits")
	}
	for _, char := range input {
		if char < '0' || char > '9' {
			return "", errors.New("TOTP code must contain six digits")
		}
	}
	return input, nil
}
