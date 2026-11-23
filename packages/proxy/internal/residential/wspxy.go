package residential

import (
	"bytes"
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

	"github.com/HengXin666/hx-pxy/internal/store"
)

const (
	wsPxyControlTimeout  = 15 * time.Second
	wsPxyControlMaxBytes = 8 << 10
)

// wsPxyGatewayPlaceholder fills the gateway columns of hx-cf-wspxy providers.
// The real CONNECT listener is a loopback port returned by POST /session, so
// the placeholder never reaches the data plane.
const wsPxyGatewayPlaceholder = "hx-cf-wspxy.invalid"

// WsPxySession is one HX-CF-WsPxy control-plane session. Proxy is a local
// HTTP CONNECT URL; every CONNECT on that port reuses one pin-sticky WS.
type WsPxySession struct {
	ID     string
	Server string
	Port   int
	Pin    string
	Egress string
}

// WsPxyControl talks to a local HX-CF-WsPxy SessionPlane. The control plane
// never implements WSP1; Mihomo dials the returned HTTP CONNECT port.
type WsPxyControl interface {
	Create(ctx context.Context, controlURL string) (WsPxySession, error)
	Rotate(ctx context.Context, controlURL, id string) (WsPxySession, error)
	Destroy(ctx context.Context, controlURL, id string) error
	Health(ctx context.Context, controlURL string) error
}

type httpWsPxyControl struct{}

type wsPxyView struct {
	ID     string `json:"id"`
	Proxy  string `json:"proxy"`
	Pin    string `json:"pin"`
	Egress string `json:"egress"`
	Error  string `json:"error"`
}

func (httpWsPxyControl) Create(ctx context.Context, controlURL string) (WsPxySession, error) {
	view, err := wsPxyJSON(ctx, http.MethodPost, controlURL, "/session", http.StatusCreated, http.StatusOK)
	if err != nil {
		return WsPxySession{}, err
	}
	return sessionFromWsPxyView(view)
}

func (httpWsPxyControl) Rotate(ctx context.Context, controlURL, id string) (WsPxySession, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/?#") {
		return WsPxySession{}, fmt.Errorf("%w: hx-cf-wspxy session id is invalid", ErrInvalid)
	}
	view, err := wsPxyJSON(ctx, http.MethodPost, controlURL, "/session/"+id+"/rotate", http.StatusOK)
	if err != nil {
		return WsPxySession{}, err
	}
	return sessionFromWsPxyView(view)
}

func (httpWsPxyControl) Destroy(ctx context.Context, controlURL, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if strings.ContainsAny(id, "/?#") {
		return fmt.Errorf("%w: hx-cf-wspxy session id is invalid", ErrInvalid)
	}
	_, err := wsPxyJSON(ctx, http.MethodDelete, controlURL, "/session/"+id, http.StatusOK)
	return err
}

func (httpWsPxyControl) Health(ctx context.Context, controlURL string) error {
	_, err := wsPxyJSON(ctx, http.MethodGet, controlURL, "/health", http.StatusOK)
	return err
}

func sessionFromWsPxyView(view wsPxyView) (WsPxySession, error) {
	id := strings.TrimSpace(view.ID)
	if id == "" {
		return WsPxySession{}, fmt.Errorf("%w: hx-cf-wspxy session response has no id", ErrInvalid)
	}
	parsed, err := url.Parse(strings.TrimSpace(view.Proxy))
	if err != nil || parsed.Host == "" {
		return WsPxySession{}, fmt.Errorf("%w: hx-cf-wspxy session proxy URL is invalid", ErrInvalid)
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return WsPxySession{}, fmt.Errorf("%w: hx-cf-wspxy session proxy must be http", ErrInvalid)
	}
	host := parsed.Hostname()
	if err := requireLoopbackHost(host); err != nil {
		return WsPxySession{}, fmt.Errorf("%w: hx-cf-wspxy proxy must bind loopback: %v", ErrInvalid, err)
	}
	port := 80
	if parsed.Scheme == "https" {
		port = 443
	}
	if raw := parsed.Port(); raw != "" {
		parsedPort, err := strconv.Atoi(raw)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return WsPxySession{}, fmt.Errorf("%w: hx-cf-wspxy session proxy port is invalid", ErrInvalid)
		}
		port = parsedPort
	}
	return WsPxySession{
		ID:     id,
		Server: host,
		Port:   port,
		Pin:    strings.TrimSpace(view.Pin),
		Egress: strings.TrimSpace(view.Egress),
	}, nil
}

func sessionsFromWsPxy(views []WsPxySession, size int) []Session {
	if size < 1 {
		size = 1
	}
	if len(views) > size {
		views = views[:size]
	}
	sessions := make([]Session, 0, len(views))
	for index, view := range views {
		sessions = append(sessions, Session{
			Index:  index,
			ID:     view.ID,
			Server: view.Server,
			Port:   view.Port,
		})
	}
	return sessions
}

func wsPxyJSON(ctx context.Context, method, controlURL, path string, wantStatus ...int) (wsPxyView, error) {
	endpoint, err := wsPxyEndpoint(controlURL, path)
	if err != nil {
		return wsPxyView{}, err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(nil))
	if err != nil {
		return wsPxyView{}, fmt.Errorf("build hx-cf-wspxy request: %w", err)
	}
	if err := validateWsPxyControlHost(request.URL.Hostname()); err != nil {
		return wsPxyView{}, err
	}
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout: wsPxyControlTimeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			Proxy:             nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("hx-cf-wspxy control URL must not redirect")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return wsPxyView{}, fmt.Errorf("%w: hx-cf-wspxy control: %v", ErrProviderUnreachable, err)
	}
	defer response.Body.Close()
	allowed := false
	for _, status := range wantStatus {
		if response.StatusCode == status {
			allowed = true
			break
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, wsPxyControlMaxBytes+1))
	if err != nil {
		return wsPxyView{}, fmt.Errorf("read hx-cf-wspxy response: %w", err)
	}
	if len(body) > wsPxyControlMaxBytes {
		return wsPxyView{}, fmt.Errorf("%w: hx-cf-wspxy response is too large", ErrInvalid)
	}
	if !allowed {
		return wsPxyView{}, fmt.Errorf("%w: hx-cf-wspxy control returned status %d", ErrProviderUnreachable, response.StatusCode)
	}
	var view wsPxyView
	if len(bytes.TrimSpace(body)) == 0 {
		return view, nil
	}
	if err := json.Unmarshal(body, &view); err != nil {
		return wsPxyView{}, fmt.Errorf("%w: hx-cf-wspxy control returned invalid JSON", ErrInvalid)
	}
	if view.Error != "" {
		return wsPxyView{}, fmt.Errorf("%w: hx-cf-wspxy: %s", ErrProviderUnreachable, view.Error)
	}
	return view, nil
}

func wsPxyEndpoint(controlURL, path string) (string, error) {
	normalized, err := validateWsPxyControlURL(controlURL)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("%w: hx-cf-wspxy control URL is invalid", ErrInvalid)
	}
	resolved := base.ResolveReference(&url.URL{Path: path})
	return resolved.String(), nil
}

// validateWsPxyControlURL accepts only a loopback HTTP(S) origin. HX-CF-WsPxy
// binds 127.0.0.1; allowing a public host here would turn provider save into
// an SSRF primitive. This is an explicit exception to validateAPIURL.
func validateWsPxyControlURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: api_url is required for hx-cf-wspxy rotation", ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.User != nil {
		return "", fmt.Errorf("%w: hx-cf-wspxy api_url must be an http(s) loopback origin", ErrInvalid)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: hx-cf-wspxy api_url must use http or https", ErrInvalid)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("%w: hx-cf-wspxy api_url must not include a path", ErrInvalid)
	}
	if parsed.Port() == "" {
		return "", fmt.Errorf("%w: hx-cf-wspxy api_url must include a port", ErrInvalid)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("%w: hx-cf-wspxy api_url port is invalid", ErrInvalid)
	}
	if err := validateWsPxyControlHost(parsed.Hostname()); err != nil {
		return "", err
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func validateWsPxyControlHost(host string) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return fmt.Errorf("%w: hx-cf-wspxy api_url host is missing", ErrInvalid)
	}
	if host == "localhost" || host == "::1" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: hx-cf-wspxy api_url must be 127.0.0.1, ::1, or localhost", ErrInvalid)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("%w: hx-cf-wspxy api_url must point at loopback", ErrInvalid)
	}
	return nil
}

func requireLoopbackHost(host string) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("host %q is not loopback", host)
	}
	return nil
}

func (s *Service) wsPxyControl() WsPxyControl {
	if s != nil && s.wspxy != nil {
		return s.wspxy
	}
	return httpWsPxyControl{}
}

func (s *Service) createWsPxySessions(ctx context.Context, controlURL string, size int) ([]Session, error) {
	if size < 1 {
		size = 1
	}
	created := make([]WsPxySession, 0, size)
	var createErr error
	for i := 0; i < size; i++ {
		view, err := s.wsPxyControl().Create(ctx, controlURL)
		if err != nil {
			createErr = err
			break
		}
		created = append(created, view)
	}
	if createErr != nil {
		s.destroyWsPxyIDs(ctx, controlURL, wsPxyIDs(created))
		return nil, createErr
	}
	sessions := sessionsFromWsPxy(created, size)
	if len(sessions) == 0 {
		s.destroyWsPxyIDs(ctx, controlURL, wsPxyIDs(created))
		return nil, fmt.Errorf("%w: hx-cf-wspxy control returned no sessions", ErrInvalid)
	}
	return sessions, nil
}

func wsPxyIDs(sessions []WsPxySession) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session.ID != "" {
			ids = append(ids, session.ID)
		}
	}
	return ids
}

func (s *Service) destroyWsPxyIDs(ctx context.Context, controlURL string, ids []string) {
	control := s.wsPxyControl()
	for _, id := range ids {
		_ = control.Destroy(ctx, controlURL, id)
	}
}

func (s *Service) destroyWsPxyNodes(ctx context.Context, controlURL string, nodes []store.NodeConfigRecord) {
	s.destroyWsPxyIDs(ctx, controlURL, s.wsPxyIDsFromNodes(nodes))
}

func (s *Service) wsPxyIDsFromNodes(nodes []store.NodeConfigRecord) []string {
	ids := make([]string, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		id := s.wsPxySessionIDFromNode(node)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (s *Service) wsPxySessionIDFromNode(node store.NodeConfigRecord) string {
	if s == nil || s.cipher == nil || len(node.CanonicalConfigEncrypted) == 0 {
		return ""
	}
	plaintext, err := s.cipher.Open(node.CanonicalConfigEncrypted, []byte("node:"+node.Fingerprint))
	if err != nil {
		return ""
	}
	var canonical map[string]any
	if err := json.Unmarshal(plaintext, &canonical); err != nil {
		return ""
	}
	id, _ := canonical[store.ResidentialWsPxySessionIDKey].(string)
	return strings.TrimSpace(id)
}

func (s *Service) wsPxySessionIDFromFingerprint(ctx context.Context, channelID, fingerprint string) (string, error) {
	if fingerprint == "" {
		return "", nil
	}
	nodes, err := s.repository.ListResidentialSessionNodes(ctx, channelID)
	if err != nil {
		return "", err
	}
	for _, node := range nodes {
		if node.Fingerprint == fingerprint {
			return s.wsPxySessionIDFromNode(node), nil
		}
	}
	return "", nil
}
