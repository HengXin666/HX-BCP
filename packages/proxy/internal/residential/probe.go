package residential

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultExitIPEndpoint is the echo service used to observe the egress address.
const DefaultExitIPEndpoint = "https://api.ipify.org?format=json"

const (
	probeTimeout      = 15 * time.Second
	maximumProbeBytes = 8 << 10
	maximumProbeHops  = 3
)

// TestResult reports what a provider test connection observed. It is the primary
// way an operator confirms that a username template matches what the vendor
// actually expects, which matters for presets whose syntax is unverified.
type TestResult struct {
	Success bool `json:"success"`
	// ExitIP is the public address the vendor exited from.
	ExitIP string `json:"exit_ip,omitempty"`
	// RenderedUsernamePreview shows the shape of the generated username with the
	// account login masked, so a template can be debugged without leaking the
	// credential.
	RenderedUsernamePreview string `json:"rendered_username_preview,omitempty"`
	LatencyMS               int    `json:"latency_ms,omitempty"`
	// Detail carries mode-specific context that does not fit the fixed fields,
	// such as how many nodes a cf-worker panel yielded or why the exit IP is
	// verified by the channel data plane instead of the control plane.
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// TestProvider dials the vendor gateway directly from the control plane and
// reports the observed exit IP.
//
// This is a one-shot diagnostic request, not a traffic path: user traffic still
// flows only through the data plane. The echo endpoint is constrained to HTTPS
// and validated so this cannot be turned into an SSRF primitive.
func (s *Service) TestProvider(ctx context.Context, providerID, echoURL string) (TestResult, error) {
	record, err := s.repository.GetResidentialProvider(ctx, providerID)
	if err != nil {
		return TestResult{}, mapStoreError(err)
	}
	provider := s.providerFromRecord(record)
	credentials, err := s.providerCredentials(record)
	if err != nil {
		return TestResult{}, err
	}
	regionSelection, err := normalizeRegionSelection(
		string(provider.DefaultRegionMode),
		provider.DefaultRegion,
		provider.DefaultRandomRegions,
	)
	if err != nil {
		return TestResult{Success: false, Error: err.Error()}, nil
	}
	sessions, err := s.providerSessions(ctx, provider, credentials, regionSelection, 1)
	if err != nil {
		return TestResult{Success: false, Error: err.Error()}, nil
	}
	session := sessions[0]
	if provider.RotationMode == RotationCloudflareWorker {
		return s.testCloudflareWorkerProvider(ctx, provider, session)
	}
	if provider.RotationMode == RotationHXCFWsPxy {
		return s.testHXCFWsPxyProvider(ctx, provider, session)
	}

	endpoint, err := normalizeEchoURL(echoURL)
	if err != nil {
		return TestResult{}, err
	}
	preview := maskUsername(session.Username, credentials.Username)

	started := time.Now()
	scheme := provider.Protocol
	server := provider.GatewayHost
	port := provider.GatewayPort
	username := session.Username
	password := credentials.Password
	if session.Server != "" {
		// api-list sessions normally have no login, but some extraction APIs
		// return an authenticated endpoint alongside host and port.
		server = session.Server
		port = session.Port
		username = session.Username
		password = session.Password
	}
	exitIP, err := probeEndpoint(ctx, scheme, server, port, username, password, endpoint, provider.APIProxyURL)
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		return TestResult{
			Success:                 false,
			RenderedUsernamePreview: preview,
			LatencyMS:               latency,
			Error:                   err.Error(),
		}, nil
	}
	return TestResult{
		Success:                 true,
		ExitIP:                  exitIP,
		RenderedUsernamePreview: preview,
		LatencyMS:               latency,
	}, nil
}

// testCloudflareWorkerProvider verifies a cf-worker provider from the control
// plane. The control plane must not implement VLESS/Trojan, so instead of
// tunneling an HTTP probe through the node it confirms the panel link parsed
// into at least one endpoint and performs a TCP reachability check against the
// first endpoint, optionally through the configured exit proxy. The actual
// residential exit IP is only observable from the channel data plane, which
// the Detail field explains.
func (s *Service) testCloudflareWorkerProvider(
	ctx context.Context,
	provider Provider,
	session Session,
) (TestResult, error) {
	if session.Server == "" || session.Port < 1 || session.Port > 65535 {
		return TestResult{
			Success: false,
			Error:   "cf-worker endpoint has no usable server:port",
		}, nil
	}
	started := time.Now()
	dialContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	connection, err := dialThroughProxy(
		dialContext,
		provider.APIProxyURL,
		net.JoinHostPort(session.Server, strconv.Itoa(session.Port)),
	)
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		return TestResult{
			Success:   false,
			LatencyMS: latency,
			Error:     sanitizeProxyError(err, provider.APIProxyURL).Error(),
		}, nil
	}
	_ = connection.Close()
	return TestResult{
		Success:   true,
		LatencyMS: latency,
		Detail:    "已从 CF Worker 面板解析到节点并通过 TCP 可达性检查；真实出口 IP 由渠道数据面验证",
	}, nil
}

// testHXCFWsPxyProvider verifies the local SessionPlane and the minted CONNECT
// port. The session created for this probe is destroyed afterwards so a test
// click does not occupy a colo pin.
func (s *Service) testHXCFWsPxyProvider(
	ctx context.Context,
	provider Provider,
	session Session,
) (TestResult, error) {
	if session.ID != "" {
		defer s.destroyWsPxyIDs(ctx, provider.APIURL, []string{session.ID})
	}
	started := time.Now()
	if err := s.wsPxyControl().Health(ctx, provider.APIURL); err != nil {
		return TestResult{
			Success:   false,
			LatencyMS: int(time.Since(started).Milliseconds()),
			Error:     err.Error(),
		}, nil
	}
	if session.Server == "" || session.Port < 1 || session.Port > 65535 {
		return TestResult{
			Success: false,
			Error:   "hx-cf-wspxy session has no usable CONNECT port",
		}, nil
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(session.Server, strconv.Itoa(session.Port)), probeTimeout)
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		return TestResult{
			Success:   false,
			LatencyMS: latency,
			Error:     err.Error(),
		}, nil
	}
	_ = connection.Close()
	return TestResult{
		Success:   true,
		LatencyMS: latency,
		Detail:    "HX-CF-WsPxy 控制面可达，本机 CONNECT 端口已监听；真实 colo 出口 IP 由渠道数据面验证",
	}, nil
}

// probeEndpoint performs one HTTP GET through a proxy endpoint.
func probeEndpoint(
	ctx context.Context,
	scheme, server string,
	port int,
	username, password, endpoint string,
	upstreamProxyURL string,
) (string, error) {
	transport, err := newChainedProxyTransport(scheme, server, port, username, password, upstreamProxyURL)
	if err != nil {
		return "", fmt.Errorf("configure residential probe proxy: %w", sanitizeProxyError(err, upstreamProxyURL))
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   probeTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maximumProbeHops {
				return fmt.Errorf("too many redirects")
			}
			// A redirect must not escape HTTPS or reach a private target.
			if err := validateEchoTarget(request.URL); err != nil {
				return err
			}
			return nil
		},
	}
	requestContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create exit IP request: %w", err)
	}
	request.Header.Set("Accept", "application/json, text/plain")
	response, err := client.Do(request)
	if err != nil {
		return "", sanitizeProbeError(sanitizeProxyError(err, upstreamProxyURL), username, password)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", fmt.Errorf("exit IP endpoint returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumProbeBytes))
	if err != nil {
		return "", fmt.Errorf("read exit IP response: %w", err)
	}
	return parseExitIP(body)
}

// parseExitIP accepts either a bare address or a JSON object carrying one.
func parseExitIP(body []byte) (string, error) {
	trimmed := strings.TrimSpace(string(body))
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.String(), nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
		for _, key := range []string{"ip", "origin", "query", "ipAddress", "client_ip"} {
			value, exists := payload[key]
			if !exists {
				continue
			}
			candidate := strings.TrimSpace(fmt.Sprint(value))
			// Some echo services return a comma-separated forwarding chain.
			if comma := strings.IndexByte(candidate, ','); comma > 0 {
				candidate = strings.TrimSpace(candidate[:comma])
			}
			if ip := net.ParseIP(candidate); ip != nil {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("exit IP endpoint did not return a recognizable address")
}

// normalizeEchoURL validates the echo endpoint, defaulting when unset.
func normalizeEchoURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultExitIPEndpoint, nil
	}
	if len(value) > 512 {
		return "", fmt.Errorf("%w: exit IP endpoint is too long", ErrInvalid)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%w: exit IP endpoint is not a valid URL", ErrInvalid)
	}
	if err := validateEchoTarget(parsed); err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// validateEchoTarget constrains the probe destination. Only HTTPS is accepted and
// literal private, loopback or link-local addresses are refused, so an operator
// cannot aim the probe at internal infrastructure.
func validateEchoTarget(target *url.URL) error {
	if target.Scheme != "https" {
		return fmt.Errorf("%w: exit IP endpoint must use HTTPS", ErrInvalid)
	}
	host := target.Hostname()
	if host == "" {
		return fmt.Errorf("%w: exit IP endpoint host is missing", ErrInvalid)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("%w: exit IP endpoint must not target the local machine", ErrInvalid)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return fmt.Errorf("%w: exit IP endpoint must be a public address", ErrInvalid)
		}
	}
	return nil
}

// maskUsername keeps the template structure visible while hiding the account
// login, so the preview is safe to return over the API and to log.
func maskUsername(rendered, account string) string {
	if account == "" {
		return rendered
	}
	masked := account
	if len(account) > 2 {
		masked = account[:2] + strings.Repeat("*", len(account)-2)
	} else {
		masked = strings.Repeat("*", len(account))
	}
	return strings.ReplaceAll(rendered, account, masked)
}

// sanitizeProbeError strips credentials that Go's URL errors would otherwise
// echo back, since the proxy URL embeds user:password.
func sanitizeProbeError(err error, username, password string) error {
	message := err.Error()
	for _, secret := range []string{password, url.QueryEscape(password), username, url.QueryEscape(username)} {
		if secret == "" {
			continue
		}
		message = strings.ReplaceAll(message, secret, "***")
	}
	return fmt.Errorf("gateway request failed: %s", message)
}
