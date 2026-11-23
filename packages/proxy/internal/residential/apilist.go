package residential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiFetchTimeout  = 20 * time.Second
	apiFetchMaxBytes = 512 << 10
)

// apiURLWithRegion applies the control-plane-selected region to the common
// country/region query parameter used by extraction APIs. BestProxy uses cc;
// other vendors can keep an existing country, country_code, region, or area
// parameter and receive the same behavior without another provider-specific
// parser.
func apiURLWithRegion(raw, region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return raw, nil
	}
	if err := validateRegion(region); err != nil {
		return "", err
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("%w: api_url must be an https URL", ErrInvalid)
	}
	query := parsed.Query()
	parameter := "cc"
	for _, candidate := range []string{"cc", "country", "country_code", "region", "area"} {
		if _, exists := query[candidate]; exists {
			parameter = candidate
			break
		}
	}
	query.Set(parameter, region)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func apiURLHasRegionParameter(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	query := parsed.Query()
	for _, candidate := range []string{"cc", "country", "country_code", "region", "area"} {
		if _, exists := query[candidate]; exists {
			return true
		}
	}
	return false
}

// FetchedNode is one proxy endpoint returned by an api-list extraction API.
type FetchedNode struct {
	Server   string
	Port     int
	Username string
	Password string
}

// NodeFetcher fetches the current proxy endpoint list from a vendor extraction
// API. It is injectable so tests never touch the network.
type NodeFetcher func(context.Context, string) ([]FetchedNode, error)

// fetchNodesFromAPI is the default NodeFetcher. It performs one bounded HTTPS
// GET and parses the common vendor payload shapes:
//
//	{"code":200,"data":{"list":["ip:port", ...]}}   (BestProxy json)
//	plain text lines of "ip:port"                     (generic fallback)
func fetchNodesFromAPI(ctx context.Context, apiURL string) ([]FetchedNode, error) {
	return fetchNodesFromAPIWithProxy(ctx, apiURL, "")
}

func fetchNodesFromAPIWithProxy(ctx context.Context, apiURL, proxyURL string) ([]FetchedNode, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build api-list request: %w", sanitizeAPIListError(sanitizeProxyError(err, proxyURL), apiURL))
	}
	if err := validatePublicAPIHost(ctx, request.URL.Hostname()); err != nil {
		return nil, fmt.Errorf("validate api-list endpoint: %w", err)
	}
	request.Header.Set("Accept", "application/json, text/plain")
	transport, err := newAPITransport(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure api-list proxy: %w", sanitizeProxyError(err, proxyURL))
	}
	client := &http.Client{
		Timeout:   apiFetchTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("api-list endpoint must not redirect")
		},
	}
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch api-list nodes: %w", sanitizeAPIListError(sanitizeProxyError(err, proxyURL), apiURL))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("api-list endpoint returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, apiFetchMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read api-list response: %w", err)
	}
	if len(body) > apiFetchMaxBytes {
		return nil, fmt.Errorf("api-list response exceeds %d bytes", apiFetchMaxBytes)
	}
	return parseAPINodes(body)
}

// parseAPINodes decodes a vendor extraction response into proxy endpoints. A
// JSON object carrying {"code":200,"data":{"list":[...]}} is treated as a
// BestProxy-style payload; anything else is split into host:port lines.
func parseAPINodes(body []byte) ([]FetchedNode, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, errors.New("api-list endpoint returned an empty response")
	}
	var candidates []string
	nodes := make([]FetchedNode, 0)
	if strings.HasPrefix(text, "{") {
		var payload struct {
			Code int `json:"code"`
			Data struct {
				List json.RawMessage `json:"list"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("api-list endpoint returned invalid JSON: %v", err)
		}
		if payload.Code != 0 && payload.Code != 200 {
			return nil, fmt.Errorf("api-list endpoint rejected the request (code %d)", payload.Code)
		}
		var list []json.RawMessage
		if err := json.Unmarshal(payload.Data.List, &list); err == nil {
			for _, raw := range list {
				if node, ok := parseJSONNode(raw); ok {
					nodes = append(nodes, node)
				}
			}
		} else {
			var lineList string
			if err := json.Unmarshal(payload.Data.List, &lineList); err == nil {
				candidates = strings.Split(lineList, "\n")
			} else {
				return nil, fmt.Errorf("api-list endpoint returned an invalid data.list")
			}
		}
	} else {
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				candidates = append(candidates, line)
			}
		}
	}
	for _, candidate := range candidates {
		if node, ok := parseNode(candidate); ok {
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("api-list endpoint returned no usable nodes: %.120s", text)
	}
	return nodes, nil
}

// parseNode accepts "host:port", "http://host:port" or "socks5://host:port".
func parseNode(candidate string) (FetchedNode, bool) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return FetchedNode{}, false
	}
	host := candidate
	if strings.Contains(candidate, "://") {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host == "" {
			return FetchedNode{}, false
		}
		host = parsed.Host
		server, portText, err := net.SplitHostPort(host)
		if err != nil {
			return FetchedNode{}, false
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 || !validFetchedNodeServer(server) {
			return FetchedNode{}, false
		}
		node := FetchedNode{Server: server, Port: port}
		if parsed.User != nil {
			node.Username = parsed.User.Username()
			node.Password, _ = parsed.User.Password()
		}
		return node, true
	}
	// BestProxy may return authenticated endpoints as host:port:user:password.
	// Keep this deliberately limited to the common IPv4 form; IPv6 endpoints
	// must use a URL with brackets so colons remain unambiguous.
	parts := strings.Split(candidate, ":")
	if len(parts) == 4 && net.ParseIP(parts[0]) != nil {
		port, err := strconv.Atoi(parts[1])
		if err != nil || port < 1 || port > 65535 || parts[2] == "" || !validFetchedNodeServer(parts[0]) {
			return FetchedNode{}, false
		}
		return FetchedNode{
			Server:   parts[0],
			Port:     port,
			Username: parts[2],
			Password: parts[3],
		}, true
	}
	server, portText, err := net.SplitHostPort(host)
	if err != nil {
		return FetchedNode{}, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return FetchedNode{}, false
	}
	if !validFetchedNodeServer(server) {
		return FetchedNode{}, false
	}
	return FetchedNode{Server: server, Port: port}, true
}

func validFetchedNodeServer(server string) bool {
	return validateGatewayHost(strings.ToLower(strings.TrimSpace(server))) == nil
}

// dialPublicAPIContext resolves every address before dialing and refuses
// restricted ranges. Dialing the chosen IP directly prevents a DNS answer from
// changing between validation and connection establishment.
func dialPublicAPIContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split api-list address: %w", err)
	}
	if parsedPort, err := strconv.Atoi(port); err != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil, errors.New("api-list port is invalid")
	}
	addresses := make([]net.IP, 0, 4)
	if literal := net.ParseIP(host); literal != nil {
		addresses = append(addresses, literal)
	} else {
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve api-list host: %w", err)
		}
		for _, item := range resolved {
			addresses = append(addresses, item.IP)
		}
	}
	var lastErr error
	for _, ip := range addresses {
		if isRestrictedAPIAddress(ip) {
			lastErr = fmt.Errorf("api-list host resolved to restricted address %s", ip)
			continue
		}
		connection, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(
			ctx,
			network,
			net.JoinHostPort(ip.String(), port),
		)
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("api-list host resolved to no addresses")
	}
	return nil, lastErr
}

func isRestrictedAPIAddress(ip net.IP) bool {
	return ip == nil ||
		ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast()
}

func validatePublicAPIHost(ctx context.Context, host string) error {
	if literal := net.ParseIP(host); literal != nil {
		if isRestrictedAPIAddress(literal) {
			return errors.New("api-list endpoint resolves to a restricted address")
		}
		return nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve api-list endpoint host: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("api-list endpoint host has no addresses")
	}
	for _, address := range addresses {
		if isRestrictedAPIAddress(address.IP) {
			return errors.New("api-list endpoint resolves to a restricted address")
		}
	}
	return nil
}

func sanitizeAPIListError(err error, apiURL string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	message = strings.ReplaceAll(message, apiURL, "api-list endpoint")
	if parsed, parseErr := url.Parse(apiURL); parseErr == nil {
		if parsed.RawQuery != "" {
			message = strings.ReplaceAll(message, parsed.RawQuery, "<redacted-query>")
		}
	}
	return errors.New(message)
}

// parseJSONNode accepts the object form used by some extraction APIs, while
// keeping string entries compatible with the BestProxy list format.
func parseJSONNode(raw json.RawMessage) (FetchedNode, bool) {
	var candidate string
	if err := json.Unmarshal(raw, &candidate); err == nil {
		return parseNode(strings.TrimSpace(candidate))
	}
	var object struct {
		Host     string          `json:"host"`
		IP       string          `json:"ip"`
		Server   string          `json:"server"`
		Port     json.RawMessage `json:"port"`
		Username string          `json:"username"`
		Password string          `json:"password"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return FetchedNode{}, false
	}
	host := strings.TrimSpace(object.Host)
	if host == "" {
		host = strings.TrimSpace(object.IP)
	}
	if host == "" {
		host = strings.TrimSpace(object.Server)
	}
	var portText string
	if err := json.Unmarshal(object.Port, &portText); err != nil {
		var portNumber int
		if json.Unmarshal(object.Port, &portNumber) != nil {
			return FetchedNode{}, false
		}
		portText = strconv.Itoa(portNumber)
	}
	node, ok := parseNode(net.JoinHostPort(host, portText))
	if !ok {
		return FetchedNode{}, false
	}
	node.Username = object.Username
	node.Password = object.Password
	return node, true
}

// sessionsFromNodes caps the fetched endpoint list to the pool size and wraps
// each endpoint in a Session so the existing channel and rotation machinery
// applies unchanged.
func sessionsFromNodes(nodes []FetchedNode, size int) []Session {
	if size < 1 {
		size = 1
	}
	if len(nodes) > size {
		nodes = nodes[:size]
	}
	sessions := make([]Session, 0, len(nodes))
	for index, node := range nodes {
		sessions = append(sessions, Session{
			Index:    index,
			ID:       net.JoinHostPort(node.Server, strconv.Itoa(node.Port)),
			Server:   node.Server,
			Port:     node.Port,
			Username: node.Username,
			Password: node.Password,
		})
	}
	return sessions
}
