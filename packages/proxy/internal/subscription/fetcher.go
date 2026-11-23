package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFetchTimeout = 30 * time.Second
	maximumSourceBytes  = 16 << 20
	maximumRedirects    = 5
)

type FetchCondition struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

type FetchMetadata struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	Size         int64  `json:"size"`
}

type FetchResult struct {
	Content     []byte
	Metadata    FetchMetadata
	NotModified bool
}

type SourceLoader interface {
	Load(context.Context, SourceType, SourceConfig, FetchCondition) (FetchResult, error)
}

type DefaultSourceLoader struct {
	resolver *net.Resolver
	dialer   *net.Dialer
}

func NewDefaultSourceLoader() *DefaultSourceLoader {
	return &DefaultSourceLoader{
		resolver: net.DefaultResolver,
		dialer: &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

func (loader *DefaultSourceLoader) Load(
	ctx context.Context,
	sourceType SourceType,
	config SourceConfig,
	condition FetchCondition,
) (FetchResult, error) {
	switch sourceType {
	case SourceRemote:
		return loader.loadRemote(ctx, config, condition)
	case SourceInline:
		return FetchResult{
			Content: []byte(config.Inline),
			Metadata: FetchMetadata{
				ContentType: "application/octet-stream",
				Size:        int64(len(config.Inline)),
			},
		}, nil
	case SourceFile:
		return loader.loadFile(ctx, config.FilePath)
	default:
		return FetchResult{}, fmt.Errorf("unsupported source type %q", sourceType)
	}
}

func (loader *DefaultSourceLoader) loadRemote(
	ctx context.Context,
	config SourceConfig,
	condition FetchCondition,
) (FetchResult, error) {
	parsedURL, err := url.Parse(config.URL)
	if err != nil {
		return FetchResult{}, fmt.Errorf("parse subscription URL: %w", err)
	}
	if err := validateRemoteURL(parsedURL); err != nil {
		return FetchResult{}, err
	}

	timeout := defaultFetchTimeout
	if config.TimeoutSeconds > 0 {
		timeout = time.Duration(config.TimeoutSeconds) * time.Second
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		DialContext: func(dialContext context.Context, network, address string) (net.Conn, error) {
			return loader.dialContext(dialContext, network, address, config.AllowPrivate)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maximumRedirects {
				return errors.New("subscription redirect limit exceeded")
			}
			return validateRemoteURL(request.URL)
		},
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create subscription request: %w", err)
	}
	if config.UserAgent != "" {
		request.Header.Set("User-Agent", config.UserAgent)
	} else {
		request.Header.Set("User-Agent", "HX-Pxy/1")
	}
	for key, value := range config.Headers {
		request.Header.Set(key, value)
	}
	if condition.ETag != "" {
		request.Header.Set("If-None-Match", condition.ETag)
	}
	if condition.LastModified != "" {
		request.Header.Set("If-Modified-Since", condition.LastModified)
	}

	response, err := client.Do(request)
	if err != nil {
		return FetchResult{}, sanitizeRequestError(ctx, err)
	}
	defer response.Body.Close()
	metadata := FetchMetadata{
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
		ContentType:  response.Header.Get("Content-Type"),
		StatusCode:   response.StatusCode,
	}
	if response.StatusCode == http.StatusNotModified {
		return FetchResult{Metadata: metadata, NotModified: true}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FetchResult{}, fmt.Errorf("subscription server returned HTTP %d", response.StatusCode)
	}
	content, err := readLimited(response.Body, maximumSourceBytes)
	if err != nil {
		return FetchResult{}, fmt.Errorf("read subscription response: %w", err)
	}
	if len(content) == 0 {
		return FetchResult{}, errors.New("subscription response is empty")
	}
	metadata.Size = int64(len(content))
	return FetchResult{Content: content, Metadata: metadata}, nil
}

func (loader *DefaultSourceLoader) loadFile(ctx context.Context, filePath string) (FetchResult, error) {
	if err := ctx.Err(); err != nil {
		return FetchResult{}, err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return FetchResult{}, fmt.Errorf("stat subscription file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return FetchResult{}, errors.New("subscription file must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return FetchResult{}, errors.New("subscription file must be a regular file")
	}
	if info.Size() > maximumSourceBytes {
		return FetchResult{}, fmt.Errorf("subscription file exceeds %d bytes", maximumSourceBytes)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return FetchResult{}, fmt.Errorf("open subscription file: %w", err)
	}
	defer file.Close()
	content, err := readLimited(&contextReader{ctx: ctx, reader: file}, maximumSourceBytes)
	if err != nil {
		return FetchResult{}, fmt.Errorf("read subscription file: %w", err)
	}
	if len(content) == 0 {
		return FetchResult{}, errors.New("subscription file is empty")
	}
	return FetchResult{
		Content: content,
		Metadata: FetchMetadata{
			ContentType: "application/octet-stream",
			Size:        int64(len(content)),
		},
	}, nil
}

func (loader *DefaultSourceLoader) dialContext(
	ctx context.Context,
	network string,
	address string,
	allowPrivate bool,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split dial address: %w", err)
	}
	if parsedPort, err := strconv.Atoi(port); err != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil, errors.New("remote port is invalid")
	}

	addresses := make([]net.IP, 0, 4)
	if literal := net.ParseIP(host); literal != nil {
		addresses = append(addresses, literal)
	} else {
		resolved, err := loader.resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve subscription host: %w", err)
		}
		for _, item := range resolved {
			addresses = append(addresses, item.IP)
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("subscription host resolved to no addresses")
	}

	var lastError error
	for _, ip := range addresses {
		if !allowPrivate && isRestrictedAddress(ip) {
			lastError = fmt.Errorf("subscription host resolved to restricted address %s", ip)
			continue
		}
		connection, err := loader.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return connection, nil
		}
		lastError = err
	}
	if lastError == nil {
		lastError = errors.New("no permitted subscription address")
	}
	return nil, lastError
}

func validateRemoteURL(remoteURL *url.URL) error {
	if remoteURL == nil || remoteURL.Host == "" {
		return errors.New("subscription URL requires a host")
	}
	if remoteURL.Scheme != "http" && remoteURL.Scheme != "https" {
		return errors.New("subscription URL must use HTTP or HTTPS")
	}
	if remoteURL.User != nil {
		return errors.New("subscription URL userinfo is not allowed")
	}
	if remoteURL.Fragment != "" {
		return errors.New("subscription URL fragment is not allowed")
	}
	return nil
}

func isRestrictedAddress(ip net.IP) bool {
	return ip == nil ||
		ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast()
}

func readLimited(reader io.Reader, maximum int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("source exceeds %d bytes", maximum)
	}
	return content, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func normalizedContentType(value string) string {
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(strings.ToLower(value))
}

func sanitizeRequestError(ctx context.Context, err error) error {
	if contextError := ctx.Err(); contextError != nil {
		return contextError
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return errors.New("subscription network request timed out")
		}
		return errors.New("subscription network request failed")
	}
	return errors.New("subscription request failed")
}
