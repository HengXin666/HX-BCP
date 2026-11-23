package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteLoaderRejectsPrivateAddressByDefaultAndSupportsConditionalFetch(t *testing.T) {
	t.Parallel()

	const entityTag = `"example-v1"`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") == entityTag {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		writer.Header().Set("ETag", entityTag)
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("vless://example-node\n"))
	}))
	defer server.Close()

	loader := NewDefaultSourceLoader()
	_, err := loader.Load(context.Background(), SourceRemote, SourceConfig{URL: server.URL}, FetchCondition{})
	if err == nil {
		t.Fatal("Load(private default) error = nil, want rejection")
	}
	if strings.Contains(err.Error(), server.URL) {
		t.Fatalf("Load(private default) leaks URL: %v", err)
	}

	first, err := loader.Load(context.Background(), SourceRemote, SourceConfig{
		URL:          server.URL,
		AllowPrivate: true,
	}, FetchCondition{})
	if err != nil {
		t.Fatalf("Load(private allowed) error = %v", err)
	}
	if first.NotModified || string(first.Content) != "vless://example-node\n" {
		t.Fatalf("first fetch = %#v", first)
	}
	if first.Metadata.ETag != entityTag || first.Metadata.Size != int64(len(first.Content)) {
		t.Fatalf("first metadata = %#v", first.Metadata)
	}

	second, err := loader.Load(context.Background(), SourceRemote, SourceConfig{
		URL:          server.URL,
		AllowPrivate: true,
	}, FetchCondition{ETag: entityTag})
	if err != nil {
		t.Fatalf("Load(conditional) error = %v", err)
	}
	if !second.NotModified || len(second.Content) != 0 {
		t.Fatalf("conditional fetch = %#v", second)
	}
}

func TestRemoteLoaderDoesNotLeakURLQueryInErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL + "/subscription?marker=private-query-value"
	server.Close()

	loader := NewDefaultSourceLoader()
	_, err := loader.Load(context.Background(), SourceRemote, SourceConfig{
		URL:          endpoint,
		AllowPrivate: true,
	}, FetchCondition{})
	if err == nil {
		t.Fatal("Load(closed server) error = nil")
	}
	if strings.Contains(err.Error(), "private-query-value") || strings.Contains(err.Error(), endpoint) {
		t.Fatalf("Load() error leaks URL details: %v", err)
	}
}

func TestFileLoaderRejectsSymbolicLinkAndReadsRegularFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourcePath := filepath.Join(root, "subscription.txt")
	if err := os.WriteFile(sourcePath, []byte("trojan://example-node\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	loader := NewDefaultSourceLoader()
	loaded, err := loader.Load(context.Background(), SourceFile, SourceConfig{FilePath: sourcePath}, FetchCondition{})
	if err != nil {
		t.Fatalf("Load(file) error = %v", err)
	}
	if string(loaded.Content) != "trojan://example-node\n" {
		t.Fatalf("loaded content = %q", loaded.Content)
	}

	linkPath := filepath.Join(root, "subscription-link.txt")
	if err := os.Symlink(sourcePath, linkPath); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	if _, err := loader.Load(context.Background(), SourceFile, SourceConfig{FilePath: linkPath}, FetchCondition{}); err == nil {
		t.Fatal("Load(symlink) error = nil")
	}
}

func TestSourceInspection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		content        string
		contentType    string
		format         string
		estimatedNodes int
	}{
		{name: "uri list", content: "vless://one\nvmess://two\n", format: "uri-list", estimatedNodes: 2},
		{name: "clash yaml", content: "proxies:\n  - name: one\n    type: ss\n  - name: two\n    type: vmess\nrules: []\n", format: "clash-yaml", estimatedNodes: 2},
		{name: "json", content: `{"proxies":[]}`, format: "json"},
		{name: "yaml content type", content: "mixed: value", contentType: "application/yaml", format: "yaml"},
		{name: "unknown", content: "plain text", format: "unknown"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			summary := inspectSource([]byte(test.content), test.contentType)
			if summary.DetectedFormat != test.format || summary.EstimatedNodes != test.estimatedNodes {
				t.Fatalf("inspectSource() = %#v", summary)
			}
		})
	}
}

func TestReadLimitedRejectsOversizeInput(t *testing.T) {
	t.Parallel()

	_, err := readLimited(strings.NewReader("12345"), 4)
	if err == nil {
		t.Fatal("readLimited() error = nil")
	}
}
