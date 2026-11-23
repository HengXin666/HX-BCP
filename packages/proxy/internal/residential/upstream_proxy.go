package residential

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const proxyDialTimeout = 15 * time.Second

// contextDialer adapts a context-aware dial function to x/net/proxy's Dialer
// interface. It is also used by HTTP transports when a SOCKS5 hop is present.
type contextDialer struct {
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func (d contextDialer) Dial(network, address string) (net.Conn, error) {
	return d.dialContext(context.Background(), network, address)
}

func (d contextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.dialContext(ctx, network, address)
}

func parseConfiguredProxy(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return nil, errors.New("proxy URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" {
		return nil, errors.New("proxy URL uses an unsupported protocol")
	}
	if parsed.Port() == "" {
		return nil, errors.New("proxy URL has no port")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("proxy URL port is invalid")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("proxy URL contains an unsupported path or query")
	}
	return parsed, nil
}

func dialDirectContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{Timeout: proxyDialTimeout, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
}

// dialThroughProxy opens a TCP connection to target through one configured
// control-plane proxy. HTTP and HTTPS use CONNECT; SOCKS5 delegates name
// resolution to the SOCKS server, which is useful for a local Mihomo listener.
func dialThroughProxy(ctx context.Context, proxyRaw, target string) (net.Conn, error) {
	proxyRaw = strings.TrimSpace(proxyRaw)
	if proxyRaw == "" {
		return dialDirectContext(ctx, "tcp", target)
	}
	parsed, err := parseConfiguredProxy(proxyRaw)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "socks5":
		auth := proxyAuth(parsed)
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, contextDialer{dialContext: dialDirectContext})
		if err != nil {
			return nil, err
		}
		return dialWithContext(ctx, dialer, "tcp", target)
	case "http", "https":
		return dialHTTPConnectProxy(ctx, parsed, target)
	default:
		return nil, errors.New("proxy URL uses an unsupported protocol")
	}
}

func dialWithContext(ctx context.Context, dialer proxy.Dialer, network, address string) (net.Conn, error) {
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, network, address)
	}
	result := make(chan struct {
		connection net.Conn
		err        error
	}, 1)
	go func() {
		connection, err := dialer.Dial(network, address)
		result <- struct {
			connection net.Conn
			err        error
		}{connection: connection, err: err}
	}()
	select {
	case outcome := <-result:
		return outcome.connection, outcome.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func proxyAuth(proxyURL *url.URL) *proxy.Auth {
	if proxyURL.User == nil {
		return nil
	}
	password, _ := proxyURL.User.Password()
	return &proxy.Auth{User: proxyURL.User.Username(), Password: password}
}

func dialHTTPConnectProxy(ctx context.Context, proxyURL *url.URL, target string) (net.Conn, error) {
	if strings.ContainsAny(target, "\r\n") {
		return nil, errors.New("proxy target is invalid")
	}
	connection, err := dialDirectContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = connection.Close()
		}
	}()
	if proxyURL.Scheme == "https" {
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		connection = tlsConnection
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	request := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\nProxy-Connection: Keep-Alive\r\n"
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		encoded := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username() + ":" + password))
		request += "Proxy-Authorization: Basic " + encoded + "\r\n"
	}
	request += "\r\n"
	if _, err := connection.Write([]byte(request)); err != nil {
		return nil, err
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, err
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy CONNECT returned status %d", response.StatusCode)
	}
	_ = connection.SetDeadline(time.Time{})
	closeOnError = false
	return connection, nil
}

// newAPITransport builds a bounded transport for vendor extraction requests.
// Direct requests use the public-address dialer; a configured HTTP/HTTPS proxy
// is contacted directly, while SOCKS5 carries the destination connection.
func newAPITransport(proxyRaw string) (*http.Transport, error) {
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: apiFetchTimeout,
		IdleConnTimeout:       10 * time.Second,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   2,
		DisableKeepAlives:     true,
	}
	if strings.TrimSpace(proxyRaw) == "" {
		transport.DialContext = dialPublicAPIContext
		return transport, nil
	}
	parsed, err := parseConfiguredProxy(proxyRaw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "socks5" {
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, proxyAuth(parsed), contextDialer{dialContext: dialDirectContext})
		if err != nil {
			return nil, err
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialWithContext(ctx, dialer, network, address)
		}
		return transport, nil
	}
	transport.Proxy = http.ProxyURL(parsed)
	transport.DialContext = dialDirectContext
	return transport, nil
}

// newChainedProxyTransport makes the residential proxy the final HTTP/SOCKS5
// hop while dialing that proxy server through the optional foreign upstream.
// This is used only by the diagnostic request; Mihomo performs the real chain.
func newChainedProxyTransport(
	protocol, server string,
	port int,
	username, password string,
	upstreamProxyURL string,
) (*http.Transport, error) {
	proxyURL := &url.URL{Scheme: protocol, Host: net.JoinHostPort(server, strconv.Itoa(port))}
	if username != "" {
		proxyURL.User = url.UserPassword(username, password)
	}
	transport := &http.Transport{
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: probeTimeout,
		IdleConnTimeout:       10 * time.Second,
		MaxIdleConns:          1,
		MaxIdleConnsPerHost:   1,
		DisableKeepAlives:     true,
	}
	dialGateway := func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.TrimSpace(upstreamProxyURL) == "" {
			return dialPublicAPIContext(ctx, network, address)
		}
		return dialThroughProxy(ctx, upstreamProxyURL, address)
	}
	if protocol == "socks5" {
		forward := contextDialer{dialContext: dialGateway}
		dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, proxyAuth(proxyURL), forward)
		if err != nil {
			return nil, err
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialWithContext(ctx, dialer, network, address)
		}
		return transport, nil
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.DialContext = dialGateway
	return transport, nil
}

func sanitizeProxyError(err error, proxyRaw string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if proxyRaw != "" {
		if parsed, parseErr := url.Parse(proxyRaw); parseErr == nil && parsed.String() != "" {
			if parsed.User != nil {
				if password, ok := parsed.User.Password(); ok {
					message = strings.ReplaceAll(message, password, "***")
				}
				message = strings.ReplaceAll(message, parsed.User.Username(), "***")
			}
			message = strings.ReplaceAll(message, parsed.String(), "configured proxy")
		}
	}
	return errors.New(message)
}
