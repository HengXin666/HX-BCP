package auth

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestTOTPCodeMatchesRFC6238SixDigitVectors(t *testing.T) {
	secretValue := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	tests := []struct {
		unix int64
		want string
	}{
		{unix: 59, want: "287082"},
		{unix: 1111111109, want: "081804"},
		{unix: 1111111111, want: "050471"},
		{unix: 1234567890, want: "005924"},
		{unix: 2000000000, want: "279037"},
		{unix: 20000000000, want: "353130"},
	}
	for _, test := range tests {
		got, err := TOTPCode(secretValue, time.Unix(test.unix, 0))
		if err != nil {
			t.Fatalf("TOTPCode(%d) error = %v", test.unix, err)
		}
		if got != test.want {
			t.Errorf("TOTPCode(%d) = %q, want %q", test.unix, got, test.want)
		}
	}
}

func TestTwoFactorSetupEnableVerifyAndExpiry(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(root, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(database, filepath.Join(root, "admin-setup-token"), slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})), box)
	if err != nil {
		t.Fatal(err)
	}
	setupAdmin(t, service, filepath.Join(root, "admin-setup-token"))
	baseTime := time.Unix(1700000000, 0).UTC()
	service.now = func() time.Time { return baseTime }

	setup, err := service.BeginTwoFactorSetup(ctx)
	if err != nil {
		t.Fatalf("BeginTwoFactorSetup() error = %v", err)
	}
	if setup.Secret == "" || setup.OTPAuthURL == "" {
		t.Fatalf("incomplete setup response: %+v", setup)
	}
	record, err := database.GetAdminTwoFactor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(record.SecretEncrypted) == setup.Secret {
		t.Fatal("two-factor secret must be encrypted at rest")
	}
	code, err := TOTPCode(setup.Secret, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnableTwoFactor(ctx, code); err != nil {
		t.Fatalf("EnableTwoFactor() error = %v", err)
	}

	session, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.TwoFactorStatus(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Verified {
		t.Fatalf("unexpected initial two-factor status: %+v", status)
	}
	if err := service.VerifyTwoFactor(ctx, session.Token, "127.0.0.1", code); err != nil {
		t.Fatalf("VerifyTwoFactor() error = %v", err)
	}
	verified, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if verified.TwoFactorVerifiedAt == nil {
		t.Fatal("successful two-factor verification must be attached to the session")
	}

	service.now = func() time.Time { return baseTime.Add(twoFactorVerificationTTL + time.Second) }
	expired, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if expired.TwoFactorVerifiedAt != nil {
		t.Fatal("two-factor step-up must expire")
	}
}

func TestRenewTwoFactorVerification(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(root, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(database, filepath.Join(root, "admin-setup-token"), slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})), box)
	if err != nil {
		t.Fatal(err)
	}
	setupAdmin(t, service, filepath.Join(root, "admin-setup-token"))
	baseTime := time.Unix(1700000000, 0).UTC()
	service.now = func() time.Time { return baseTime }

	setup, err := service.BeginTwoFactorSetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, err := TOTPCode(setup.Secret, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnableTwoFactor(ctx, code); err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(ctx, "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyTwoFactor(ctx, session.Token, "127.0.0.1", code); err != nil {
		t.Fatal(err)
	}

	// Renewal slides the verified-at timestamp forward while the window is open.
	if err := service.RenewTwoFactorVerification(ctx, session.Token); err != nil {
		t.Fatalf("RenewTwoFactorVerification() error = %v", err)
	}
	renewed, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.TwoFactorVerifiedAt == nil || !renewed.TwoFactorVerifiedAt.Equal(baseTime) {
		t.Fatalf("renewal must re-stamp verification at now, got %v", renewed.TwoFactorVerifiedAt)
	}

	// Once the window lapses, an open channel can no longer renew.
	service.now = func() time.Time { return baseTime.Add(twoFactorVerificationTTL + time.Second) }
	if err := service.RenewTwoFactorVerification(ctx, session.Token); !errors.Is(err, ErrTwoFactorVerificationExpired) {
		t.Fatalf("renewal after TTL = %v, want ErrTwoFactorVerificationExpired", err)
	}

	// An unknown or revoked session cannot renew either.
	if err := service.RenewTwoFactorVerification(ctx, "missing-token"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("renewal with unknown token = %v, want ErrSessionExpired", err)
	}
}
