package cfworker

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeSubscription checks a worker subscription URL: HTTP 200 and the body
// (plain or base64) contains a vless:// or trojan:// share link.
func ProbeSubscription(ctx context.Context, url string, timeout time.Duration) (bool, string) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "构造请求失败"
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")
	response, err := client.Do(request)
	if err != nil {
		return false, "请求失败: " + truncate(err.Error(), 120)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, "HTTP " + http.StatusText(response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 20000))
	if err != nil {
		return false, "读取失败: " + truncate(err.Error(), 120)
	}
	text := string(body)
	for _, candidate := range []string{text, base64Decode(text)} {
		if strings.Contains(candidate, "vless://") || strings.Contains(candidate, "trojan://") {
			return true, "OK(" + fmtBytes(len(candidate)) + ")"
		}
	}
	return false, "内容不含 vless/trojan 节点"
}

func base64Decode(text string) string {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return text
	}
	return string(decoded)
}

func fmtBytes(n int) string {
	if n > 1024 {
		return fmt.Sprintf("%dKB", n/1024)
	}
	return fmt.Sprintf("%dB", n)
}
