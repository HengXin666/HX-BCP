package mihomo

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRotatingLogWriterBoundsFilesAndKeepsBackups(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mihomo.log")
	writer, err := newRotatingLogWriter(path, 64, 2)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		if _, err := writer.Write(bytes.Repeat([]byte{byte('a' + index)}, 24)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", candidate, err)
		}
		if info.Size() > 64 {
			t.Errorf("%s size = %d, want <= 64", filepath.Base(candidate), info.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup error = %v", err)
	}
}

func TestRotatingLogWriterSerializesConcurrentWrites(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mihomo.log")
	writer, err := newRotatingLogWriter(path, 256, 1)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for write := 0; write < 20; write++ {
				if _, writeErr := writer.Write([]byte("bounded concurrent log line\n")); writeErr != nil {
					t.Errorf("Write() error = %v", writeErr)
					return
				}
			}
		}()
	}
	group.Wait()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".1"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 256 {
			t.Errorf("%s size = %d, want <= 256", filepath.Base(candidate), info.Size())
		}
	}
}

func TestRotatingLogWriterBoundsExistingOversizedLog(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mihomo.log")
	content := bytes.Repeat([]byte("0123456789"), 20)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	writer, err := newRotatingLogWriter(path, 64, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if len(backup) != 64 {
		t.Fatalf("backup size = %d, want 64", len(backup))
	}
	if !bytes.Equal(backup, content[len(content)-64:]) {
		t.Fatal("backup does not contain the most recent log tail")
	}
}
