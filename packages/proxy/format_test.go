package hxpxy

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoSourcesAreFormatted(t *testing.T) {
	t.Parallel()

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "ref" || name == "data" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(content)
		if err != nil {
			t.Errorf("format.Source(%s) error = %v", path, err)
			return nil
		}
		if !bytes.Equal(content, formatted) {
			t.Errorf("%s is not gofmt-formatted:\n%s", path, compactFormatDiff(content, formatted))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
}

func compactFormatDiff(actual, expected []byte) string {
	actualLines := strings.Split(string(actual), "\n")
	expectedLines := strings.Split(string(expected), "\n")
	maximum := len(actualLines)
	if len(expectedLines) > maximum {
		maximum = len(expectedLines)
	}
	var output strings.Builder
	differences := 0
	for index := 0; index < maximum && differences < 40; index++ {
		actualLine := "<missing>"
		if index < len(actualLines) {
			actualLine = actualLines[index]
		}
		expectedLine := "<missing>"
		if index < len(expectedLines) {
			expectedLine = expectedLines[index]
		}
		if actualLine == expectedLine {
			continue
		}
		differences++
		fmt.Fprintf(&output, "line %d\n- %s\n+ %s\n", index+1, actualLine, expectedLine)
	}
	if differences == 40 {
		output.WriteString("... additional differences omitted\n")
	}
	return output.String()
}
