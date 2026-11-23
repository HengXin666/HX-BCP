package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startFileHelper launches a privileged helper serving file operations as the
// current user (root in production) and returns a service routed through it.
func startFileHelper(t *testing.T) *Service {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "files.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	helperErrors := make(chan error, 1)
	go func() {
		helperErrors <- RunHelper(ctx, HelperConfig{
			SocketPath:  socketPath,
			Shell:       "/bin/sh",
			MaxSessions: 2,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-helperErrors; err != nil {
			t.Errorf("stop helper: %v", err)
		}
	})
	waitForSocket(t, socketPath)
	service, err := NewService(Config{
		Enabled:          true,
		PrivilegedSocket: socketPath,
		MaxSessions:      1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestFileOperationsRoundTrip(t *testing.T) {
	for _, privileged := range []bool{false, true} {
		name := "local"
		if privileged {
			name = "privileged"
		}
		t.Run(name, func(t *testing.T) {
			var service *Service
			var err error
			if privileged {
				service = startFileHelper(t)
			} else {
				service, err = NewService(Config{Enabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				if err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			ctx := context.Background()

			// Mkdir then list.
			target := filepath.Join(root, "docs", "notes")
			if err := service.Mkdir(ctx, target); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("mkdir did not create directory: %v", err)
			}

			// Upload and stat.
			payload := []byte("hello privileged file manager\n")
			if err := service.UploadFile(ctx, target, "note.txt", bytes.NewReader(payload), int64(len(payload))); err != nil {
				t.Fatalf("upload: %v", err)
			}
			info, err := service.StatFile(ctx, filepath.Join(target, "note.txt"))
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Size != int64(len(payload)) || info.IsDir {
				t.Fatalf("stat mismatch: %+v", info)
			}

			// Download reads back identical bytes.
			var downloaded bytes.Buffer
			count, err := service.DownloadFile(ctx, filepath.Join(target, "note.txt"), &downloaded)
			if err != nil {
				t.Fatalf("download: %v", err)
			}
			if count != int64(len(payload)) || !bytes.Equal(downloaded.Bytes(), payload) {
				t.Fatalf("download mismatch: %d bytes %q", count, downloaded.String())
			}

			// Listing contains the expected entry.
			entries, err := service.ListFiles(ctx, target)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(entries) != 1 || entries[0].Name != "note.txt" || entries[0].IsDir {
				t.Fatalf("list mismatch: %+v", entries)
			}

			// Remove cleans up.
			if err := service.RemoveFile(ctx, filepath.Join(target, "note.txt")); err != nil {
				t.Fatalf("remove: %v", err)
			}
			if _, err := os.Stat(filepath.Join(target, "note.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("remove did not delete file: %v", err)
			}

			// Missing paths classify as not_found.
			_, err = service.ListFiles(ctx, filepath.Join(root, "missing"))
			var fileErr *FileError
			if !errors.As(err, &fileErr) || fileErr.Code != "not_found" {
				t.Fatalf("missing path error = %v, want not_found FileError", err)
			}
		})
	}
}

func TestLocalFileUploadRejectsOversize(t *testing.T) {
	service, err := NewService(Config{Enabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	payload := strings.Repeat("x", MaxUploadBytes+1)
	err = service.UploadFile(context.Background(), root, "big.bin", strings.NewReader(payload), int64(len(payload)))
	var fileErr *FileError
	if !errors.As(err, &fileErr) || fileErr.Code != "failed" {
		t.Fatalf("oversize upload error = %v, want failed FileError", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "big.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("oversize upload left a file behind: %v", statErr)
	}
}
