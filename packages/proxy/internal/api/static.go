package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type spaHandler struct {
	root  string
	index string
}

func newSPAHandler(root string) http.Handler {
	return spaHandler{root: root, index: filepath.Join(root, "index.html")}
}

func (handler spaHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := filepath.Clean(filepath.FromSlash("/" + strings.TrimPrefix(request.URL.Path, "/")))
	candidate := filepath.Join(handler.root, clean)
	if relative, err := filepath.Rel(handler.root, candidate); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			setStaticCacheHeaders(writer, clean)
			http.ServeFile(writer, request, candidate)
			return
		}
	}
	// SPA fallback: index.html must revalidate so updated hashed bundles are
	// picked up, while the hashed assets themselves can be cached immutably.
	writer.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(writer, request, handler.index)
}

// setStaticCacheHeaders marks Vite's content-hashed build artifacts as
// immutable: the filename changes whenever the content changes, so a long
// max-age never serves stale code. Non-hashed files under web root stay
// revalidated via no-cache.
func setStaticCacheHeaders(writer http.ResponseWriter, clean string) {
	if strings.HasPrefix(clean, "/assets/") {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	writer.Header().Set("Cache-Control", "no-cache")
}
