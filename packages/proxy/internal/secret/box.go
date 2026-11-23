package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	masterKeySize = 32
	envelopeMagic = "HXP1"
)

type Box struct {
	aead cipher.AEAD
}

func LoadOrCreate(masterKeyPath string) (*Box, error) {
	if masterKeyPath == "" {
		return nil, errors.New("master key path is required")
	}
	absolutePath, err := filepath.Abs(masterKeyPath)
	if err != nil {
		return nil, fmt.Errorf("resolve master key path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return nil, fmt.Errorf("create master key directory: %w", err)
	}

	key, err := readKey(absolutePath)
	if errors.Is(err, os.ErrNotExist) {
		key, err = createKey(absolutePath)
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

func New(key []byte) (*Box, error) {
	if len(key) != masterKeySize {
		return nil, fmt.Errorf("master key must contain %d bytes", masterKeySize)
	}
	block, err := aes.NewCipher(append([]byte(nil), key...))
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plaintext, associatedData []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("secret box is not initialized")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	envelope := make([]byte, 0, len(envelopeMagic)+len(nonce)+len(plaintext)+b.aead.Overhead())
	envelope = append(envelope, envelopeMagic...)
	envelope = append(envelope, nonce...)
	envelope = b.aead.Seal(envelope, nonce, plaintext, associatedData)
	return envelope, nil
}

func (b *Box) Open(envelope, associatedData []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("secret box is not initialized")
	}
	minimumSize := len(envelopeMagic) + b.aead.NonceSize() + b.aead.Overhead()
	if len(envelope) < minimumSize || !bytes.Equal(envelope[:len(envelopeMagic)], []byte(envelopeMagic)) {
		return nil, errors.New("invalid encrypted envelope")
	}
	nonceStart := len(envelopeMagic)
	nonceEnd := nonceStart + b.aead.NonceSize()
	nonce := envelope[nonceStart:nonceEnd]
	plaintext, err := b.aead.Open(nil, nonce, envelope[nonceEnd:], associatedData)
	if err != nil {
		return nil, errors.New("decrypt encrypted envelope")
	}
	return plaintext, nil
}

func readKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("master key path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("master key permissions are %o, want 600", info.Mode().Perm())
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if len(key) != masterKeySize {
		return nil, fmt.Errorf("master key contains %d bytes, want %d", len(key), masterKeySize)
	}
	return key, nil
}

func createKey(path string) ([]byte, error) {
	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return readKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create master key: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(key); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync master key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close master key: %w", err)
	}
	committed = true
	return key, nil
}
