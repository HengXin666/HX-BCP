package residential

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/nodeparse"
)

// FetchedWorkerNode is one proxy endpoint returned by a Cloudflare Worker
// panel subscription link (BPB-Worker-Panel). Canonical is the full node
// configuration parsed from the share URI, ready for the Mihomo compiler.
type FetchedWorkerNode struct {
	Protocol  string
	Server    string
	Port      int
	Canonical map[string]any
}

// WorkerConfigFetcher fetches the current node list from a Cloudflare Worker
// panel subscription link. It is injectable so tests never touch the network.
type WorkerConfigFetcher func(context.Context, string, string) ([]FetchedWorkerNode, error)

// fetchWorkerConfig is the default WorkerConfigFetcher. It performs one
// bounded HTTPS GET through the optional control-plane exit proxy, then parses
// the BPB raw subscription payload (base64-encoded VLESS/Trojan share URIs).
func fetchWorkerConfig(ctx context.Context, workerURL, proxyURL string) ([]FetchedWorkerNode, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, workerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build cf-worker request: %w", sanitizeAPIListError(sanitizeProxyError(err, proxyURL), workerURL))
	}
	if err := validatePublicAPIHost(ctx, request.URL.Hostname()); err != nil {
		return nil, fmt.Errorf("validate cf-worker endpoint: %w", err)
	}
	request.Header.Set("Accept", "application/json, text/plain")
	transport, err := newAPITransport(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure cf-worker proxy: %w", sanitizeProxyError(err, proxyURL))
	}
	client := &http.Client{
		Timeout:   apiFetchTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("cf-worker endpoint must not redirect")
		},
	}
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch cf-worker nodes: %w", sanitizeAPIListError(sanitizeProxyError(err, proxyURL), workerURL))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("cf-worker endpoint returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, apiFetchMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read cf-worker response: %w", err)
	}
	if len(body) > apiFetchMaxBytes {
		return nil, fmt.Errorf("cf-worker response exceeds %d bytes", apiFetchMaxBytes)
	}
	return parseWorkerConfig(body)
}

// parseWorkerConfig decodes a Cloudflare Worker panel payload into proxy nodes.
// BPB answers with base64-encoded VLESS/Trojan share URIs; nodeparse already
// understands that envelope plus plain URI lists, so the same parser that
// handles subscriptions also covers the panel payload.
func parseWorkerConfig(body []byte) ([]FetchedWorkerNode, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, errors.New("cf-worker endpoint returned an empty response")
	}
	result, err := nodeparse.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse cf-worker subscription: %w", err)
	}
	nodes := make([]FetchedWorkerNode, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		protocol := strings.ToLower(strings.TrimSpace(node.Protocol))
		if protocol != "vless" && protocol != "trojan" {
			continue
		}
		server := strings.TrimSpace(canonicalString(node.Canonical["server"]))
		normalizedServer := normalizeWorkerServer(server)
		if normalizedServer != server {
			node.Canonical["server"] = normalizedServer
			for _, key := range []string{"sni", "servername", "host"} {
				if value, exists := node.Canonical[key]; exists {
					if strings.EqualFold(strings.TrimSpace(canonicalString(value)), server) {
						node.Canonical[key] = normalizedServer
					}
				}
			}
			if query, ok := node.Canonical["query"].(map[string]any); ok {
				for _, key := range []string{"host", "sni"} {
					if value, exists := query[key]; exists {
						if strings.EqualFold(strings.TrimSpace(canonicalString(value)), server) {
							query[key] = normalizedServer
						}
					}
				}
			}
		}
		server = normalizedServer
		port, ok := canonicalPort(node.Canonical["port"])
		if server == "" || !ok || len(node.Canonical) == 0 {
			continue
		}
		nodes = append(nodes, FetchedWorkerNode{
			Protocol:  protocol,
			Server:    server,
			Port:      port,
			Canonical: node.Canonical,
		})
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("cf-worker endpoint returned no vless/trojan nodes: %.120s", text)
	}
	return nodes, nil
}

// workerSessions wraps fetched panel nodes into pooled Session values, keeping
// only the requested protocol and capping the pool at the requested size.
// The canonical config travels with the session so the compiler can render the
// full VLESS/Trojan WebSocket endpoint without any protocol work in the
// control plane.
func workerSessions(nodes []FetchedWorkerNode, protocol string, size int) []Session {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	seen := make(map[string]struct{}, len(nodes))
	sessions := make([]Session, 0, size)
	index := 0
	for _, node := range nodes {
		if protocol != "" && node.Protocol != protocol {
			continue
		}
		key := node.Protocol + "|" + net.JoinHostPort(node.Server, strconv.Itoa(node.Port))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		sessions = append(sessions, Session{
			Index:     index,
			ID:        net.JoinHostPort(node.Server, strconv.Itoa(node.Port)),
			Server:    node.Server,
			Port:      node.Port,
			Canonical: node.Canonical,
		})
		index++
		if size > 0 && len(sessions) >= size {
			break
		}
	}
	return sessions
}

// canonicalString extracts a string from a canonical node field. Canonical
// values come from nodeparse and may be strings, numbers, booleans or nil,
// so a tolerant extractor keeps the cf-worker path independent of parser
// internals.
func canonicalString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// commonTLDs is the set of public top-level domains the cf-worker node
// normalizer treats as fully qualified. Bare BPB panel nodes end with a
// Cloudflare account subdomain (e.g. "bpb.x70hlrsl-a5a") that is not a TLD,
// so any host whose last label is not a known TLD gets the workers.dev
// suffix appended.
var commonTLDs = map[string]struct{}{
	"com": {}, "net": {}, "org": {}, "io": {}, "dev": {}, "me": {}, "co": {},
	"cc": {}, "top": {}, "xyz": {}, "info": {}, "biz": {}, "app": {}, "page": {},
	"site": {}, "online": {}, "tech": {}, "store": {}, "cloud": {}, "cf": {},
	"workers": {}, "pages": {}, "de": {}, "jp": {}, "cn": {}, "ru": {}, "uk": {},
}

// normalizeWorkerServer completes bare Cloudflare Worker panel hostnames with
// the ".workers.dev" suffix. BPB panels advertise their own workers.dev
// endpoint as a bare hostname (e.g. "bpb.x70hlrsl-a5a"), which otherwise
// fails DNS resolution at dial time. Public domain nodes such as
// "www.speedtest.net", IP literals, and already-qualified names are left
// untouched.
func normalizeWorkerServer(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return server
	}
	host := server
	portSuffix := ""
	if parsedHost, port, err := net.SplitHostPort(server); err == nil {
		host = parsedHost
		portSuffix = ":" + port
	}
	if net.ParseIP(host) != nil {
		return server
	}
	last := host[strings.LastIndex(host, ".")+1:]
	if _, ok := commonTLDs[strings.ToLower(last)]; ok {
		return server
	}
	return host + ".workers.dev" + portSuffix
}

func canonicalPort(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, typed > 0 && typed < 65536
	case int64:
		return int(typed), typed > 0 && typed < 65536
	case float64:
		return int(typed), typed > 0 && typed < 65536 && typed == float64(int(typed))
	case string:
		number, err := strconv.Atoi(strings.TrimSpace(typed))
		return number, err == nil && number > 0 && number < 65536
	default:
		return 0, false
	}
}

// cloneCanonical deep-copies a canonical node config so later name and
// dialer-proxy mutations never leak into the parsed source map.
func cloneCanonical(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case map[string]any:
			result[key] = cloneCanonical(typed)
		case []any:
			result[key] = append([]any(nil), typed...)
		default:
			result[key] = value
		}
	}
	return result
}
