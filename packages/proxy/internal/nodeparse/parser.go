package nodeparse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Node struct {
	DisplayName string         `json:"display_name"`
	Protocol    string         `json:"protocol"`
	Fingerprint string         `json:"fingerprint"`
	Canonical   map[string]any `json:"canonical"`
}

type Failure struct {
	Index    int    `json:"index"`
	Protocol string `json:"protocol,omitempty"`
	Name     string `json:"name,omitempty"`
	Reason   string `json:"reason"`
}

type Result struct {
	DetectedFormat string              `json:"detected_format"`
	Nodes          []Node              `json:"nodes"`
	Failures       []Failure           `json:"failures,omitempty"`
	Providers      []ProviderReference `json:"-"`
}

// ProviderReference describes an external Mihomo proxy-provider. Fetching is
// intentionally owned by the subscription service so SSRF and file policies
// stay identical to ordinary subscription sources.
type ProviderReference struct {
	Name      string
	Type      string
	URL       string
	Path      string
	Headers   map[string]string
	UserAgent string
}

var nativeProtocols = map[string]struct{}{
	"anytls": {}, "hysteria": {}, "hysteria2": {}, "http": {}, "https": {},
	"mieru": {}, "openvpn": {}, "shadow-tls": {}, "snell": {}, "socks": {},
	"socks5": {}, "ss": {}, "ssh": {}, "ssr": {}, "trojan": {}, "tuic": {},
	"vless": {}, "vmess": {}, "wireguard": {},
}

// SupportedProtocols returns the outbound protocol names accepted from native
// Mihomo YAML. Actual availability is still determined by the installed
// Mihomo build during candidate validation.
//
// openvpn is listed because the installed Mihomo build carries a native
// OpenVPN outbound (`adapter/outbound/openvpn.go`), which is how VPNGate-style
// home-broadband exits are consumed. It is deliberately *not* in the same
// category as the share-URI protocols: it only ever arrives as native YAML with
// inline ca/cert/key, and the compiler injects a remote DNS resolver for it
// (see convertNodeConfig) because the tunnel's own resolver must be used once it
// is up. masque is intentionally absent — it needs an IPv6 endpoint and a
// different field set (private-key/public-key/ip), which no source we consume
// provides.
// See .agents/notes/implemented/feature/2026-10-05-openvpn-outbound-as-first-class-node.md.
func SupportedProtocols() []string {
	return []string{"anytls", "hysteria", "hysteria2", "http", "mieru", "openvpn", "shadow-tls", "snell", "socks5", "ss", "ssh", "ssr", "trojan", "tuic", "vless", "vmess", "wireguard"}
}

func Parse(content []byte) (Result, error) {
	return parse(content, 0)
}

func parse(content []byte, depth int) (Result, error) {
	if depth > 2 {
		return Result{}, errors.New("subscription encoding nesting is too deep")
	}
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return Result{}, errors.New("subscription content is empty")
	}

	if result, ok := parseClashYAML(trimmed); ok {
		return result, nil
	}
	if result, ok := parseSingBoxJSON(trimmed); ok {
		return result, nil
	}
	// A document that also carries schemeless lines is not a pure share URI list:
	// the URI line parser skips those lines rather than failing on them, so
	// claiming the document here would drop endpoints silently. It falls through
	// to the proxy-list reader, which handles both shapes in one pass.
	if result := parseURILines(string(trimmed)); (len(result.Nodes) > 0 || len(result.Failures) > 0) && !hasBareEndpointLines(string(trimmed)) {
		result.DetectedFormat = "uri-list"
		return result, nil
	}
	if decoded, ok := decodeBase64(trimmed); ok {
		result, err := parse(decoded, depth+1)
		if err != nil {
			return Result{}, err
		}
		result.DetectedFormat = "base64-" + result.DetectedFormat
		return result, nil
	}
	// A flat proxy list has no container header and no scheme, so it can only be
	// recognized last, and only when enough of its lines really are endpoints.
	// See proxylist.go for why a decorated share URI list does NOT come through
	// here: it carries schemes and is repaired inside parseURILines instead.
	if result := parseProxyList(string(trimmed)); looksLikeProxyList(string(trimmed)) {
		return result, nil
	}
	return Result{}, errors.New("subscription format is not supported")
}

func parseSingBoxJSON(content []byte) (Result, bool) {
	var document struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(content, &document); err != nil || document.Outbounds == nil {
		return Result{}, false
	}
	result := Result{DetectedFormat: "sing-box-json", Nodes: make([]Node, 0, len(document.Outbounds))}
	for index, raw := range document.Outbounds {
		normalized, err := singBoxToCanonical(raw)
		if err != nil {
			result.Failures = append(result.Failures, Failure{
				Index: index, Protocol: strings.ToLower(strings.TrimSpace(stringValue(raw["type"]))),
				Name: stringValue(raw["tag"]), Reason: err.Error(),
			})
			continue
		}
		node, err := nodeFromClash(normalized)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Index: index, Name: stringValue(raw["tag"]), Reason: err.Error()})
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}
	return result, true
}

func singBoxToCanonical(raw map[string]any) (map[string]any, error) {
	input := normalizeMap(raw)
	protocol := strings.ToLower(stringValue(input["type"]))
	switch protocol {
	case "shadowsocks":
		protocol = "ss"
	case "socks":
		protocol = "socks5"
	case "hysteria", "hysteria2", "tuic", "ssh", "wireguard", "mieru", "anytls", "vless", "vmess", "trojan", "http":
	default:
		return nil, fmt.Errorf("unsupported sing-box outbound type %q", protocol)
	}
	canonical := map[string]any{
		"name": stringValue(input["tag"]), "type": protocol,
		"server": input["server"], "port": input["server_port"],
	}
	for _, key := range []string{"uuid", "username", "password", "flow", "security", "auth", "auth_str", "obfs", "obfs_password", "congestion_control", "udp_relay_mode", "private_key", "peer_public_key", "reserved", "local_address"} {
		if value, exists := input[key]; exists {
			canonical[strings.ReplaceAll(key, "_", "-")] = value
		}
	}
	if protocol == "hysteria" || protocol == "hysteria2" {
		if value, exists := canonical["auth-str"]; exists {
			canonical["auth-str"] = value
		} else if value, exists := canonical["auth"]; exists {
			canonical["password"] = value
		}
	}
	if protocol == "wireguard" {
		if value, exists := canonical["peer-public-key"]; exists {
			canonical["public-key"] = value
			delete(canonical, "peer-public-key")
		}
	}
	if method, exists := input["method"]; exists {
		canonical["cipher"] = method
	}
	if alterID, exists := input["alter_id"]; exists {
		canonical["alter-id"] = alterID
	}
	if tlsConfig, ok := input["tls"].(map[string]any); ok {
		if enabled, _ := tlsConfig["enabled"].(bool); enabled {
			canonical["tls"] = true
		}
		if serverName := strings.TrimSpace(stringValue(tlsConfig["server_name"])); serverName != "" {
			canonical["servername"] = serverName
		}
	}
	if transport, ok := input["transport"].(map[string]any); ok {
		transportType := strings.ToLower(stringValue(transport["type"]))
		if transportType == "ws" || transportType == "websocket" {
			canonical["network"] = "ws"
			options := map[string]any{}
			if path := strings.TrimSpace(stringValue(transport["path"])); path != "" {
				options["path"] = path
			}
			if headers, exists := transport["headers"]; exists {
				options["headers"] = headers
			}
			canonical["ws-opts"] = options
		}
	}
	return canonical, nil
}

func parseClashYAML(content []byte) (Result, bool) {
	var document struct {
		Proxies   []map[string]any          `yaml:"proxies"`
		Payload   []map[string]any          `yaml:"payload"`
		Providers map[string]map[string]any `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil || (document.Proxies == nil && document.Payload == nil && document.Providers == nil) {
		return Result{}, false
	}
	format := "clash-yaml"
	items := document.Proxies
	if document.Proxies == nil && document.Payload != nil {
		format = "mihomo-provider-yaml"
		items = document.Payload
	}
	result := Result{DetectedFormat: format, Nodes: make([]Node, 0, len(items))}
	appendClashNodes(&result, items, "")
	providerNames := make([]string, 0, len(document.Providers))
	for name := range document.Providers {
		providerNames = append(providerNames, name)
	}
	slices.Sort(providerNames)
	for _, name := range providerNames {
		raw := normalizeMap(document.Providers[name])
		if inline := mapSlice(raw["payload"]); inline != nil {
			appendClashNodes(&result, inline, name)
			continue
		}
		if inline := mapSlice(raw["proxies"]); inline != nil {
			appendClashNodes(&result, inline, name)
			continue
		}
		providerType := strings.ToLower(strings.TrimSpace(stringValue(raw["type"])))
		reference := ProviderReference{Name: name, Type: providerType, URL: stringValue(raw["url"]), Path: stringValue(raw["path"])}
		reference.Headers, reference.UserAgent = providerHeaders(raw["header"])
		if providerType == "http" && reference.URL != "" || providerType == "file" && reference.Path != "" {
			result.Providers = append(result.Providers, reference)
			continue
		}
		result.Failures = append(result.Failures, Failure{Name: name, Reason: "provider must contain payload/proxies or a valid http/file source"})
	}
	return result, true
}

func appendClashNodes(result *Result, items []map[string]any, providerName string) {
	for index, raw := range items {
		node, err := nodeFromClash(raw)
		if err != nil {
			name := stringValue(raw["name"])
			if providerName != "" {
				name = providerName + ": " + name
			}
			result.Failures = append(result.Failures, Failure{
				Index:  index,
				Name:   name,
				Reason: err.Error(),
			})
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}
}

func nodeFromClash(raw map[string]any) (Node, error) {
	canonical := normalizeMap(raw)
	protocol := strings.ToLower(strings.TrimSpace(stringValue(canonical["type"])))
	if protocol == "" {
		return Node{}, errors.New("missing node type")
	}
	if _, supported := nativeProtocols[protocol]; !supported {
		return Node{}, fmt.Errorf("unsupported Mihomo node type %q", protocol)
	}
	switch protocol {
	case "https":
		protocol = "http"
		canonical["tls"] = true
	case "socks":
		protocol = "socks5"
	case "openvpn":
		// An OpenVPN outbound is useless without its inline CA/cert/key: the
		// data plane rejects it (`openvpn config requires inline <ca> block`).
		// Rejecting here turns a whole-subscription silent failure into a
		// per-node failure row the operator can see.
		if strings.TrimSpace(stringValue(canonical["ca"])) == "" {
			return Node{}, errors.New("openvpn node requires an inline ca block")
		}
	}
	server := strings.TrimSpace(stringValue(canonical["server"]))
	if server == "" {
		return Node{}, errors.New("missing server")
	}
	port, err := integerValue(canonical["port"])
	if err != nil || port < 1 || port > 65535 {
		return Node{}, errors.New("invalid port")
	}
	canonical["type"] = protocol
	canonical["server"] = server
	canonical["port"] = port
	name := strings.TrimSpace(stringValue(canonical["name"]))
	if name == "" {
		name = net.JoinHostPort(server, strconv.Itoa(port))
	}
	return finalize(name, protocol, canonical)
}

func parseURILines(text string) Result {
	result := Result{}
	for index, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "://") {
			continue
		}
		// parseProxyListShareURI is parseURI plus one repair: a share URI that
		// arrived with an export annotation appended to its "#" fragment. A URI
		// that parses on its own is returned untouched, so node names survive.
		node, err := parseProxyListShareURI(line)
		if err != nil {
			protocol := ""
			if separator := strings.Index(line, "://"); separator > 0 {
				protocol = strings.ToLower(line[:separator])
			}
			result.Failures = append(result.Failures, Failure{Index: index, Protocol: protocol, Reason: err.Error()})
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}
	return result
}

func mapSlice(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			result = append(result, mapped)
		}
	}
	return result
}

func providerHeaders(value any) (map[string]string, string) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, ""
	}
	headers := make(map[string]string, len(raw))
	userAgent := ""
	for key, value := range raw {
		text := stringValue(value)
		if items, ok := value.([]any); ok && len(items) > 0 {
			text = stringValue(items[0])
		}
		if strings.EqualFold(key, "user-agent") {
			userAgent = text
			continue
		}
		if text != "" {
			headers[key] = text
		}
	}
	return headers, userAgent
}

func finalize(name, protocol string, canonical map[string]any) (Node, error) {
	canonical = normalizeMap(canonical)
	fingerprintInput := make(map[string]any, len(canonical))
	for key, value := range canonical {
		if key != "name" {
			fingerprintInput[key] = value
		}
	}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return Node{}, fmt.Errorf("encode canonical node: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return Node{
		DisplayName: strings.TrimSpace(name),
		Protocol:    protocol,
		Fingerprint: hex.EncodeToString(digest[:]),
		Canonical:   canonical,
	}, nil
}

func normalizeMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[strings.ToLower(strings.TrimSpace(key))] = normalizeValue(value)
	}
	return result
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeMap(typed)
	case map[any]any:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[fmt.Sprint(key)] = normalizeValue(item)
		}
		return normalizeMap(converted)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizeValue(item)
		}
		return result
	case string:
		return strings.TrimSpace(typed)
	default:
		return value
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func integerValue(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	case uint64:
		return int(typed), nil
	case float64:
		return int(typed), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(typed))
	default:
		return 0, errors.New("not an integer")
	}
}
