// Package cfworker wraps the Cloudflare Workers API for the fleet maintainer:
// deploy/delete/list workers, workers.dev subdomains, and KV namespaces.
package cfworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	cfAPIBase = "https://api.cloudflare.com/client/v4"
	userAgent = "hx-pxy-fleet/1"
)

// Client talks to the Cloudflare API for one account token.
type Client struct {
	apiToken string
	proxy    string
	http     *http.Client
}

// NewClient builds a CF API client (proxy is an optional HTTP CONNECT address).
func NewClient(apiToken, proxy string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{apiToken: apiToken, proxy: proxy, http: &http.Client{Timeout: timeout}}
}

// WorkerInfo is a lightweight worker script listing entry.
type WorkerInfo struct {
	ID        string
	CreatedOn string
}

func (c *Client) do(ctx context.Context, method, url string, body []byte, contentType string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.proxy != "" {
		transport.Proxy = http.ProxyURL(mustParseProxy(c.proxy))
	}
	response, err := (&http.Client{Transport: transport, Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("cf api request: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, response.StatusCode, err
	}
	return payload, response.StatusCode, nil
}

// call performs a CF API call and unwraps the standard envelope.
func (c *Client) call(ctx context.Context, method, path string, body []byte, contentType string) (map[string]any, error) {
	payload, status, err := c.do(ctx, method, cfAPIBase+path, body, contentType)
	if err != nil {
		return nil, err
	}
	if status >= 200 && status < 300 {
		var envelope map[string]any
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("cf api bad json (HTTP %d)", status)
		}
		return envelope, nil
	}
	return nil, fmt.Errorf("cf api %s %s -> HTTP %d: %s", method, path, status, truncate(string(payload), 300))
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

// AccountOK checks the token/account is usable; 401/403 means revoked or banned.
func (c *Client) AccountOK(ctx context.Context, accountID string) (bool, string) {
	_, status, err := c.do(ctx, "GET", cfAPIBase+"/accounts/"+accountID+"/workers/scripts", nil, "")
	if err != nil {
		return true, "" // network error: not treated as a ban
	}
	if status == 401 || status == 403 {
		return false, fmt.Sprintf("CF API HTTP %d (账号被封或 token 吊销)", status)
	}
	return true, ""
}

// EnsureSubdomain returns the account workers.dev subdomain, creating one if absent.
func (c *Client) EnsureSubdomain(ctx context.Context, accountID, preferred string) (string, error) {
	envelope, err := c.call(ctx, "GET", "/accounts/"+accountID+"/workers/subdomain", nil, "")
	if err == nil {
		if result, ok := envelope["result"].(map[string]any); ok {
			if sub := strings.TrimSpace(fmt.Sprint(result["subdomain"])); sub != "" {
				return sub, nil
			}
		}
	}
	candidate := preferred
	if candidate == "" {
		candidate = sanitizeSubdomain(accountID)
	}
	body, _ := json.Marshal(map[string]any{"subdomain": candidate})
	envelope, err = c.call(ctx, "PUT", "/accounts/"+accountID+"/workers/subdomain", body, "application/json")
	if err != nil {
		return "", err
	}
	if result, ok := envelope["result"].(map[string]any); ok {
		if sub := strings.TrimSpace(fmt.Sprint(result["subdomain"])); sub != "" {
			return sub, nil
		}
	}
	return candidate, nil
}

func sanitizeSubdomain(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			out.WriteRune(r)
		}
		if out.Len() >= 16 {
			break
		}
	}
	if out.Len() == 0 {
		return "cfr"
	}
	return out.String()
}

// ListWorkers returns the worker script names on the account.
func (c *Client) ListWorkers(ctx context.Context, accountID string) ([]WorkerInfo, error) {
	envelope, err := c.call(ctx, "GET", "/accounts/"+accountID+"/workers/scripts", nil, "")
	if err != nil {
		return nil, err
	}
	items, _ := envelope["result"].([]any)
	workers := make([]WorkerInfo, 0, len(items))
	for _, item := range items {
		entry, _ := item.(map[string]any)
		workers = append(workers, WorkerInfo{
			ID:        fmt.Sprint(entry["id"]),
			CreatedOn: fmt.Sprint(entry["created_on"]),
		})
	}
	return workers, nil
}

// UploadWorker uploads an ES-module worker (multipart: metadata + script).
func (c *Client) UploadWorker(
	ctx context.Context, accountID, scriptName string, script []byte,
	kvNamespaceID string, variables map[string]string,
) error {
	metadata := map[string]any{
		"main_module":         "worker.js",
		"compatibility_date":  "2024-10-01",
		"compatibility_flags": []string{"nodejs_compat"},
	}
	bindings := make([]any, 0)
	if kvNamespaceID != "" {
		bindings = append(bindings, map[string]any{
			"name":         "kv",
			"type":         "kv_namespace",
			"namespace_id": kvNamespaceID,
		})
	}
	for name, value := range variables {
		bindings = append(bindings, map[string]any{
			"name": name, "type": "plain_text", "text": value,
		})
	}
	if len(bindings) > 0 {
		metadata["bindings"] = bindings
	}
	metaJSON, _ := json.Marshal(metadata)

	// Cloudflare requires an exact multipart shape: the metadata part first
	// (application/json), then the script part (application/javascript+module).
	// Go's multipart.Writer sets text/plain / application/octet-stream, which
	// the API rejects with HTTP 400, so the body is built by hand.
	boundary := fmt.Sprintf("----hxfleet%d", time.Now().UnixNano())
	var buffer bytes.Buffer
	buffer.WriteString("--" + boundary + "\r\n")
	buffer.WriteString("Content-Disposition: form-data; name=\"metadata\"\r\n")
	buffer.WriteString("Content-Type: application/json\r\n\r\n")
	buffer.Write(metaJSON)
	buffer.WriteString("\r\n")
	buffer.WriteString("--" + boundary + "\r\n")
	buffer.WriteString("Content-Disposition: form-data; name=\"worker.js\"; filename=\"worker.js\"\r\n")
	buffer.WriteString("Content-Type: application/javascript+module\r\n\r\n")
	buffer.Write(script)
	buffer.WriteString("\r\n")
	buffer.WriteString("--" + boundary + "--\r\n")

	_, status, err := c.do(
		ctx, "PUT",
		cfAPIBase+"/accounts/"+accountID+"/workers/scripts/"+scriptName,
		buffer.Bytes(), "multipart/form-data; boundary="+boundary,
	)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("upload worker -> HTTP %d", status)
	}
	return nil
}

// EnableScriptSubdomain turns on the script's workers.dev route.
func (c *Client) EnableScriptSubdomain(ctx context.Context, accountID, scriptName string) error {
	body, _ := json.Marshal(map[string]any{"enabled": true})
	_, err := c.call(ctx, "POST",
		"/accounts/"+accountID+"/workers/scripts/"+scriptName+"/subdomain",
		body, "application/json")
	return err
}

// DeleteWorker removes a worker script (404 is treated as already gone).
func (c *Client) DeleteWorker(ctx context.Context, accountID, scriptName string) error {
	_, status, err := c.do(ctx, "DELETE",
		cfAPIBase+"/accounts/"+accountID+"/workers/scripts/"+scriptName, nil, "")
	if err != nil {
		return err
	}
	if status == 404 {
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("delete worker -> HTTP %d", status)
	}
	return nil
}

// FindKVNamespace returns the id of a KV namespace by title, or "".
func (c *Client) FindKVNamespace(ctx context.Context, accountID, title string) (string, error) {
	envelope, err := c.call(ctx, "GET",
		"/accounts/"+accountID+"/storage/kv/namespaces?per_page=100", nil, "")
	if err != nil {
		return "", err
	}
	items, _ := envelope["result"].([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if fmt.Sprint(entry["title"]) == title {
			return fmt.Sprint(entry["id"]), nil
		}
	}
	return "", nil
}

// CreateKVNamespace creates a KV namespace and returns its id.
func (c *Client) CreateKVNamespace(ctx context.Context, accountID, title string) (string, error) {
	body, _ := json.Marshal(map[string]any{"title": title})
	envelope, err := c.call(ctx, "POST",
		"/accounts/"+accountID+"/storage/kv/namespaces", body, "application/json")
	if err != nil {
		return "", err
	}
	if result, ok := envelope["result"].(map[string]any); ok {
		return fmt.Sprint(result["id"]), nil
	}
	return "", fmt.Errorf("create kv namespace: no id in response")
}

// WriteKVValue sets one KV key.
func (c *Client) WriteKVValue(ctx context.Context, accountID, namespaceID, key, value string) error {
	_, status, err := c.do(ctx, "PUT",
		cfAPIBase+"/accounts/"+accountID+"/storage/kv/namespaces/"+namespaceID+"/values/"+key,
		[]byte(value), "text/plain")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("write kv -> HTTP %d", status)
	}
	return nil
}

func mustParseProxy(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		parsed = &url.URL{Scheme: "http", Host: raw}
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "http"
	}
	return parsed
}
