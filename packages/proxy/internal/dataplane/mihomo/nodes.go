package mihomo

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func (c *Compiler) decryptNode(record store.NodeConfigRecord, proxyName string, groupNameByID map[string]string) (map[string]any, error) {
	plaintext, err := c.cipher.Open(
		record.CanonicalConfigEncrypted,
		[]byte("node:"+record.Fingerprint),
	)
	if err != nil {
		return nil, fmt.Errorf("decrypt node %q: %w", record.DisplayName, err)
	}
	var canonical map[string]any
	if err := json.Unmarshal(plaintext, &canonical); err != nil {
		return nil, fmt.Errorf("decode node %q canonical config: %w", record.DisplayName, err)
	}
	config, err := convertNodeConfig(canonical)
	if err != nil {
		return nil, fmt.Errorf("convert node %q: %w", record.DisplayName, err)
	}
	if groupID := stringValue(canonical[store.ResidentialDialerProxyGroupIDKey]); groupID != "" {
		groupName, exists := groupNameByID[groupID]
		if !exists {
			return nil, fmt.Errorf("residential dialer proxy group %q is missing or disabled", groupID)
		}
		config["dialer-proxy"] = groupName
	}
	config["name"] = proxyName
	return config, nil
}

func convertNodeConfig(canonical map[string]any) (map[string]any, error) {
	config := cloneMap(canonical)
	delete(config, "name")
	protocol := strings.ToLower(stringValue(config["type"]))
	if protocol == "" {
		return nil, fmt.Errorf("node type is empty")
	}
	switch protocol {
	case "https":
		config["type"] = "http"
		config["tls"] = true
	case "socks":
		config["type"] = "socks5"
	case "openvpn":
		config["type"] = "openvpn"
		// An OpenVPN outbound is a level-3 device: it must resolve destination
		// hostnames *inside* the tunnel. Without a remote resolver Mihomo hands
		// the fake-ip address (198.18.0.0/16) to the tun device, which cannot
		// route it — measured as `dial tcp <tunnel-ip>:*->198.18.0.83:443:
		// context deadline exceeded` while the tunnel itself is already up.
		// Injecting the resolver pair is what makes the node usable at all; the
		// operator does not have to know this. Verified end to end against
		// VPNGate (JP) both directly and through a dialer-proxy chain.
		// See .agents/notes/implemented/feature/2026-10-05-openvpn-outbound-as-first-class-node.md.
		if _, exists := config["remote-dns-resolve"]; !exists {
			config["remote-dns-resolve"] = true
		}
		if _, exists := config["dns"]; !exists {
			config["dns"] = []any{"1.1.1.1", "8.8.8.8"}
		}
	case "anytls", "hysteria", "hysteria2", "http", "mieru", "shadow-tls", "snell", "socks5", "ss", "ssh", "ssr", "trojan", "tuic", "vless", "wireguard":
		config["type"] = protocol
	case "vmess":
		config["type"] = "vmess"
		moveKey(config, "id", "uuid")
		moveKey(config, "scy", "cipher")
		moveKey(config, "net", "network")
		if value, exists := config["aid"]; exists {
			if number, ok := integer(value); ok {
				config["alterId"] = number
			}
			delete(config, "aid")
		}
		delete(config, "v")
	default:
		return nil, fmt.Errorf("unsupported node type %q", protocol)
	}
	if value, exists := config["port"]; exists {
		if number, ok := integer(value); ok {
			config["port"] = number
		} else {
			return nil, fmt.Errorf("invalid port")
		}
	}
	applyTLS(config)
	applyTransport(config)
	applyQuery(config)
	delete(config, "ps")
	delete(config, "query")
	delete(config, store.ResidentialDialerProxyGroupIDKey)
	delete(config, store.ResidentialWsPxySessionIDKey)
	return config, nil
}

func applyTLS(config map[string]any) {
	value, exists := config["tls"]
	if !exists {
		return
	}
	switch typed := value.(type) {
	case string:
		value := strings.ToLower(strings.TrimSpace(typed))
		config["tls"] = value == "true" || value == "tls" || value == "reality"
	case bool:
	default:
		config["tls"] = false
	}
	if serverName := firstString(config, "sni", "servername"); serverName != "" {
		config["servername"] = serverName
	}
	delete(config, "sni")
}

// splitEarlyData splits a WebSocket path like `/foo?ed=2048` into the clean
// path (`/foo`) and the early-data size (2048). The `ed` query parameter is
// the BPB / cfnew (Cloudflare Worker VLESS) convention for WebSocket max
// early data; mihomo consumes it via `ws-opts.max-early-data` plus
// `early-data-header-name: Sec-WebSocket-Protocol`, never as part of the
// request path. A missing or malformed `ed` returns the path untouched with
// earlyData 0. 20260821.
func splitEarlyData(path string) (string, int) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", 0
	}
	queryIndex := strings.IndexByte(path, '?')
	if queryIndex < 0 {
		return path, 0
	}
	clean := path[:queryIndex]
	query, err := url.ParseQuery(path[queryIndex+1:])
	if err != nil {
		return path, 0
	}
	raw := strings.TrimSpace(query.Get("ed"))
	if raw == "" {
		return path, 0
	}
	size, err := strconv.Atoi(raw)
	if err != nil || size <= 0 {
		return path, 0
	}
	return clean, size
}

func applyTransport(config map[string]any) {
	// 20260821 cf-worker（BPB/cfnew）兼容：workerSessions 的 canonical 把传输
	// 类型放在 query.type（如 "ws"），没有提升到顶层 network。这里同时查
	// query.type，否则 mihomo 看到 ws-opts 却没有 network: ws，WebSocket
	// 转发不生效（connection reset）。
	network := strings.ToLower(firstString(config, "network", "net"))
	if network == "" {
		if query, ok := config["query"].(map[string]any); ok {
			if qtype := strings.ToLower(stringValue(query["type"])); qtype != "" {
				network = qtype
			}
		}
	}
	if network != "" {
		config["network"] = network
	}
	host := stringValue(config["host"])
	if host == "" {
		if query, ok := config["query"].(map[string]any); ok {
			host = stringValue(query["host"])
		}
	}
	path := stringValue(config["path"])
	if path == "" {
		if query, ok := config["query"].(map[string]any); ok {
			path = stringValue(query["path"])
		}
	}
	switch network {
	case "ws":
		options := map[string]any{}
		// 20260821 BPB / cfnew（Cloudflare Worker VLESS）兼容：订阅节点 path 形如
		// `/?ed=2048`——`ed` 是 WebSocket 早数据（max early data）参数，mihomo 需要
		// 显式的 max-early-data + early-data-header-name 配置，path 只保留 `/`。
		// 否则 mihomo 1.19.30 把 `?ed=2048` 当作普通 path 发送，worker 端
		// Sec-WebSocket-Protocol 握手不匹配 → connection reset by peer。
		cleanPath, earlyData := splitEarlyData(path)
		if cleanPath != "" {
			options["path"] = cleanPath
		}
		if earlyData > 0 {
			options["max-early-data"] = earlyData
			options["early-data-header-name"] = "Sec-WebSocket-Protocol"
		}
		if host != "" {
			options["headers"] = map[string]string{"Host": host}
		}
		if len(options) > 0 {
			config["ws-opts"] = options
		}
	case "grpc":
		serviceName := firstString(config, "serviceName", "service-name")
		if serviceName != "" {
			config["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
		}
	case "h2":
		options := map[string]any{}
		if path != "" {
			options["path"] = path
		}
		if host != "" {
			options["host"] = []string{host}
		}
		if len(options) > 0 {
			config["h2-opts"] = options
		}
	}
	delete(config, "net")
	delete(config, "host")
	delete(config, "path")
	delete(config, "serviceName")
	delete(config, "service-name")
}

func applyQuery(config map[string]any) {
	raw, ok := config["query"].(map[string]any)
	if !ok {
		return
	}
	security := strings.ToLower(stringValue(raw["security"]))
	if security == "tls" || security == "reality" {
		config["tls"] = true
	}
	if security == "reality" {
		reality := map[string]any{}
		if publicKey := firstString(raw, "pbk", "public-key"); publicKey != "" {
			reality["public-key"] = publicKey
		}
		if shortID := firstString(raw, "sid", "short-id"); shortID != "" {
			reality["short-id"] = shortID
		}
		if len(reality) > 0 {
			config["reality-opts"] = reality
		}
	}
	if serverName := firstString(raw, "sni", "servername"); serverName != "" {
		config["servername"] = serverName
	}
	if fingerprint := firstString(raw, "fp", "client-fingerprint"); fingerprint != "" {
		config["client-fingerprint"] = fingerprint
	}
	if flow := stringValue(raw["flow"]); flow != "" {
		config["flow"] = flow
	}
	for _, mapping := range []struct{ from, to string }{
		{"auth", "auth-str"}, {"auth_str", "auth-str"},
		{"congestion_control", "congestion-controller"}, {"disable_mtu_discovery", "disable-mtu-discovery"},
		{"downmbps", "down"}, {"heartbeat", "heartbeat-interval"}, {"obfs", "obfs"},
		{"obfs-password", "obfs-password"}, {"obfs_password", "obfs-password"},
		{"password", "password"}, {"reduce_rtt", "fast-open"}, {"udp_relay_mode", "udp-relay-mode"},
		{"upmbps", "up"}, {"uuid", "uuid"},
	} {
		if value, exists := raw[mapping.from]; exists && stringValue(value) != "" {
			config[mapping.to] = value
		}
	}
	if insecure := strings.ToLower(firstString(raw, "insecure", "allowinsecure", "skip-cert-verify")); insecure == "1" || insecure == "true" {
		config["skip-cert-verify"] = true
	}
	network := strings.ToLower(firstString(raw, "type", "network"))
	if network != "" && network != "tcp" {
		config["network"] = network
	}
	host := stringValue(raw["host"])
	path := stringValue(raw["path"])
	// alpn must always be a slice in Mihomo YAML. Share URIs (including the
	// Cloudflare Worker panel payloads this project consumes) carry either a
	// single protocol or a comma-separated list, and a scalar value would be
	// rejected by `mihomo -t` with "'alpn' is not a slice".
	if value, exists := raw["alpn"]; exists {
		if alpn, ok := normalizeALPN(value); ok {
			config["alpn"] = alpn
		}
	}
	switch network {
	case "ws":
		options := map[string]any{}
		// 20260821 BPB / cfnew（Cloudflare Worker VLESS）兼容：订阅节点 path 形如
		// `/?ed=2048`——`ed` 是 WebSocket 早数据（max early data）参数，mihomo 需要
		// 显式的 max-early-data + early-data-header-name 配置，path 只保留 `/`。
		// 否则 mihomo 1.19.30 把 `?ed=2048` 当作普通 path 发送，worker 端
		// Sec-WebSocket-Protocol 握手不匹配 → connection reset by peer。
		cleanPath, earlyData := splitEarlyData(path)
		if cleanPath != "" {
			options["path"] = cleanPath
		}
		if earlyData > 0 {
			options["max-early-data"] = earlyData
			options["early-data-header-name"] = "Sec-WebSocket-Protocol"
		}
		if host != "" {
			options["headers"] = map[string]string{"Host": host}
		}
		if len(options) > 0 {
			config["ws-opts"] = options
		}
	case "grpc":
		if serviceName := firstString(raw, "serviceName", "service-name"); serviceName != "" {
			config["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
		}
	}
}

// normalizeALPN converts an alpn share-URI value into the slice shape Mihomo
// requires. Values arrive as a single protocol ("http/1.1"), a comma-separated
// list ("h2,http/1.1"), or an existing slice; every form collapses into a
// non-empty string slice. The second result reports whether any value remains.
func normalizeALPN(value any) ([]string, bool) {
	var items []string
	switch typed := value.(type) {
	case string:
		for _, part := range strings.Split(typed, ",") {
			if part = strings.TrimSpace(part); part != "" {
				items = append(items, part)
			}
		}
	case []string:
		items = append(items, typed...)
	case []any:
		for _, item := range typed {
			if text := strings.TrimSpace(stringValue(item)); text != "" {
				items = append(items, text)
			}
		}
	}
	if len(items) == 0 {
		return nil, false
	}
	return items, true
}

func cloneMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case map[string]any:
			result[key] = cloneMap(typed)
		case []any:
			result[key] = append([]any(nil), typed...)
		default:
			result[key] = value
		}
	}
	return result
}

func moveKey(values map[string]any, from, to string) {
	if value, exists := values[from]; exists {
		values[to] = value
		delete(values, from)
	}
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func integer(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	case string:
		number, err := strconv.Atoi(strings.TrimSpace(typed))
		return number, err == nil
	default:
		return 0, false
	}
}
