package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestTwoFactorStoredFormatsRemainAuthenticated(t *testing.T) {
	for _, aad := range []string{"hx-proxygroup/admin-totp/v1", "hx-pxy/admin-totp/v1"} {
		t.Run(aad, func(t *testing.T) {
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
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			tokenPath := filepath.Join(root, "admin-setup-token")
			service, err := NewService(database, tokenPath, logger, box)
			if err != nil {
				t.Fatal(err)
			}
			setupAdmin(t, service, tokenPath)
			now := time.Unix(1700000000, 0).UTC()
			service.now = func() time.Time { return now }
			const value = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
			encrypted, err := box.Seal([]byte(value), []byte(aad))
			if err != nil {
				t.Fatal(err)
			}
			record := store.AdminTwoFactorRecord{SecretEncrypted: encrypted, CreatedAt: now, UpdatedAt: now}
			if err := database.UpsertAdminTwoFactor(ctx, record); err != nil {
				t.Fatal(err)
			}
			code, err := TOTPCode(value, now)
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
			if err := service.VerifyTwoFactor(ctx, session.Token, "127.0.0.1", "invalid"); !errors.Is(err, ErrInvalidTwoFactorCode) {
				t.Fatalf("invalid code = %v", err)
			}
			if err := service.VerifyTwoFactor(ctx, session.Token, "127.0.0.1", code); err != nil {
				t.Fatal(err)
			}
			stored, err := database.GetAdminTwoFactor(ctx)
			if err != nil || !bytes.Equal(stored.SecretEncrypted, encrypted) {
				t.Fatal("verification must not rewrite encrypted data")
			}
			if err := service.DisableTwoFactor(ctx, code); err != nil {
				t.Fatal(err)
			}
			// A different context, a corrupted envelope and a different key must
			// all fail closed; compatibility never bypasses GCM authentication.
			for _, failure := range []string{"context", "tamper", "key"} {
				record.Enabled = true
				record.SecretEncrypted = append([]byte(nil), encrypted...)
				switch failure {
				case "context":
					record.SecretEncrypted, err = box.Seal([]byte(value), []byte("another-purpose"))
				case "tamper":
					record.SecretEncrypted[len(record.SecretEncrypted)-1] ^= 1
				case "key":
					other, newErr := secret.New(bytes.Repeat([]byte{1}, 32))
					if newErr != nil {
						t.Fatal(newErr)
					}
					record.SecretEncrypted, err = other.Seal([]byte(value), []byte(aad))
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := database.UpsertAdminTwoFactor(ctx, record); err != nil {
					t.Fatal(err)
				}
				if err := service.VerifyTwoFactor(ctx, session.Token, "127.0.0.1", code); err == nil {
					t.Fatalf("%s must not verify", failure)
				}
				status, err := service.TwoFactorStatus(ctx, session.Token)
				if err != nil || status.Verified {
					t.Fatalf("failed verification must not authorize terminal: %+v %v", status, err)
				}
			}
		})
	}
}
