package api

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strings"
)

// gzipMiddleware compresses compressible GET responses on the fly. Streaming
// endpoints (SSE, WebSocket, ndjson progress) and ranged file downloads are
// excluded because gzip would break their framing or byte-range semantics.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			next.ServeHTTP(writer, request)
			return
		}
		if !strings.Contains(request.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(writer, request)
			return
		}
		if request.Header.Get("Range") != "" || request.Header.Get("Upgrade") != "" {
			next.ServeHTTP(writer, request)
			return
		}
		if request.URL.Query().Get("stream") == "1" || isNonCompressiblePath(request.URL.Path) {
			next.ServeHTTP(writer, request)
			return
		}
		gzipWriter := &gzipResponseWriter{ResponseWriter: writer}
		next.ServeHTTP(gzipWriter, request)
		// Close flushes the trailing gzip footer; a nil writer means the
		// response was never compressed and Close is a no-op.
		_ = gzipWriter.Close()
	})
}

// isNonCompressiblePath reports paths whose response framing must stay intact:
// SSE event streams, websocket upgrades, and the node-check ndjson stream.
func isNonCompressiblePath(path string) bool {
	switch {
	case path == "/api/v1/overview/stream":
		return true
	case path == "/api/v1/logs/stream":
		return true
	case strings.HasPrefix(path, "/api/v1/terminal/ws"):
		return true
	case strings.HasPrefix(path, "/__hx-proxy__/"):
		return true
	default:
		return false
	}
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz         *gzip.Writer
	compress   bool
	headerSent bool
}

func (writer *gzipResponseWriter) WriteHeader(status int) {
	if writer.headerSent {
		return
	}
	writer.headerSent = true
	if writer.compressible(status) {
		writer.compress = true
		writer.Header().Del("Content-Length")
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Add("Vary", "Accept-Encoding")
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *gzipResponseWriter) Write(data []byte) (int, error) {
	if !writer.headerSent {
		writer.WriteHeader(http.StatusOK)
	}
	if !writer.compress {
		return writer.ResponseWriter.Write(data)
	}
	if writer.gz == nil {
		writer.gz = gzip.NewWriter(writer.ResponseWriter)
	}
	return writer.gz.Write(data)
}

func (writer *gzipResponseWriter) Flush() {
	if writer.gz != nil {
		_ = writer.gz.Flush()
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// Close flushes and closes the underlying gzip writer when compression was
// actually engaged, so the gzip footer reaches the client.
func (writer *gzipResponseWriter) Close() error {
	if writer.gz != nil {
		return writer.gz.Close()
	}
	return nil
}

// compressible decides whether a response body should be gzip-compressed.
// Only successful responses with text-like content types are candidates.
func (writer *gzipResponseWriter) compressible(status int) bool {
	if status < 200 || status >= 300 || status == 204 || status == 205 || status == 304 {
		return false
	}
	return compressibleContentType(writer.Header().Get("Content-Type"))
}

func compressibleContentType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if mediaType == "" {
		return false
	}
	if strings.HasPrefix(mediaType, "text/") {
		return mediaType != "text/event-stream"
	}
	switch {
	case mediaType == "application/json",
		strings.HasSuffix(mediaType, "+json"),
		mediaType == "application/javascript",
		strings.HasSuffix(mediaType, "+javascript"),
		mediaType == "application/xml",
		strings.HasSuffix(mediaType, "+xml"),
		mediaType == "application/x-yaml",
		mediaType == "image/svg+xml",
		mediaType == "application/wasm":
		return true
	default:
		return false
	}
}

var _ io.Writer = (*gzipResponseWriter)(nil)
