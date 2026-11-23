package mihomo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogWriterRotatesQuietOldFileOnNextWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihomo.log")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	writer, err := newRotatingLogWriter(path, 1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = writer.Write([]byte("new\n")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{path: "new\n", path + ".1": "old\n"} {
		data, err := os.ReadFile(name)
		if err != nil || string(data) != want {
			t.Fatalf("%s: %q %v", name, data, err)
		}
	}
}
