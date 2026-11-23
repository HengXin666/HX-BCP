package cfworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// workerReleaseURL is the BPB-Worker-Panel release asset used for deploys.
// The fleet needs BPB (Cloudflare-edge egress) — the lightweight fixed-exit
// worker exits via a fixed upstream VPS IP, which defeats IP rotation.
const workerReleaseURL = "https://github.com/bia-pain-bache/BPB-Worker-Panel/releases/download/v5.1.1/worker.js"

// FetchWorkerScript returns the BPB worker.js, caching a copy on disk.
// A network failure falls back to the cache; with neither, it errors.
func FetchWorkerScript(ctx context.Context, dataDir string) ([]byte, error) {
	cachePath := filepath.Join(dataDir, "cfworker", "worker.js")
	if cached, err := os.ReadFile(cachePath); err == nil && len(cached) > 1000 {
		return cached, nil
	}
	client := &http.Client{Timeout: 60 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, workerReleaseURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response, err := client.Do(request)
	if err != nil {
		if cached, cachedErr := os.ReadFile(cachePath); cachedErr == nil {
			return cached, nil
		}
		return nil, fmt.Errorf("下载 BPB worker.js 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 BPB worker.js -> HTTP %d", response.StatusCode)
	}
	script, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(script) < 1000 {
		return nil, fmt.Errorf("BPB worker.js 内容异常短 (%d bytes)", len(script))
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		_ = os.WriteFile(cachePath, script, 0o644)
	}
	return script, nil
}

// InjectSettings prepends `const EMBEDED_SETTINGS = {...};` to the worker.
func InjectSettings(script []byte, settings map[string]string) []byte {
	injected := "const EMBEDED_SETTINGS = " + jsonString(settings) + ";\n"
	return append([]byte(injected), script...)
}

func jsonString(settings map[string]string) string {
	encoded, _ := json.Marshal(settings)
	return string(encoded)
}

// canonicalSubscriptionURL builds the provision URL for a deployed worker.
func canonicalSubscriptionURL(workerURL, securePath string) string {
	base := strings.TrimRight(workerURL, "/")
	path := strings.Trim(securePath, "/")
	return base + "/" + path + "/sub/raw?app=xray"
}
