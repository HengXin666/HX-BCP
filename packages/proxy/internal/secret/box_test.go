package secret

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBoxRoundTripAndAssociatedData(t *testing.T) {
	t.Parallel()

	box, err := New(bytes.Repeat([]byte{0x42}, masterKeySize))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	plaintext := []byte("subscription-token")
	aad := []byte("subscription:sub-1")
	envelope, err := box.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Contains(envelope, plaintext) {
		t.Fatal("encrypted envelope contains plaintext")
	}
	opened, err := box.Open(envelope, aad)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("Open() = %q, want %q", opened, plaintext)
	}
	if _, err := box.Open(envelope, []byte("subscription:sub-2")); err == nil {
		t.Fatal("Open() with wrong associated data error = nil")
	}

	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := box.Open(tampered, aad); err == nil {
		t.Fatal("Open() with tampered ciphertext error = nil")
	}
}

func TestLoadOrCreatePersistsSecureKey(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "secrets", "master.key")
	first, err := LoadOrCreate(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreate(first) error = %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("master key permissions = %o, want 600", info.Mode().Perm())
	}
	second, err := LoadOrCreate(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreate(second) error = %v", err)
	}
	envelope, err := first.Seal([]byte("persistent"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	opened, err := second.Open(envelope, []byte("aad"))
	if err != nil || string(opened) != "persistent" {
		t.Fatalf("second box Open() = %q, err=%v", opened, err)
	}
}

func TestLoadOrCreateRejectsLoosePermissions(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{1}, masterKeySize), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadOrCreate(keyPath); err == nil {
		t.Fatal("LoadOrCreate() error = nil, want permissions error")
	}
}
