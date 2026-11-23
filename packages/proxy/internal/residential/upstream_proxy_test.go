package residential

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDialThroughHTTPConnectProxy(t *testing.T) {
	t.Parallel()
	targetAddress := startEchoTarget(t)
	proxyAddress := startHTTPConnectProxy(t)

	connection, err := dialThroughProxy(context.Background(), "http://"+proxyAddress, targetAddress)
	if err != nil {
		t.Fatalf("dialThroughProxy(http) error = %v", err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatalf("echo response = %q, want pong", response)
	}
}

func TestDialThroughSOCKS5Proxy(t *testing.T) {
	t.Parallel()
	targetAddress := startEchoTarget(t)
	proxyAddress := startSOCKS5Proxy(t)

	connection, err := dialThroughProxy(context.Background(), "socks5://"+proxyAddress, targetAddress)
	if err != nil {
		t.Fatalf("dialThroughProxy(socks5) error = %v", err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatalf("echo response = %q, want pong", response)
	}
}

func TestAPITransportUsesConfiguredProxyProtocol(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"http://127.0.0.1:7890",
		"https://proxy.example.com:8443",
		"socks5://127.0.0.1:1080",
	} {
		transport, err := newAPITransport(raw)
		if err != nil {
			t.Fatalf("newAPITransport(%q) error = %v", raw, err)
		}
		transport.CloseIdleConnections()
		if transport.DialContext == nil {
			t.Fatalf("newAPITransport(%q) did not configure a dialer", raw)
		}
	}
	if sanitized := sanitizeProxyError(io.ErrUnexpectedEOF, "http://user:secret@127.0.0.1:7890").Error(); strings.Contains(sanitized, "secret") {
		t.Fatalf("proxy error leaked credentials: %s", sanitized)
	}
}

func TestSanitizeProxyErrorEmptyProxyPreservesMessage(t *testing.T) {
	t.Parallel()
	raw := errors.New("fetch cf-worker nodes: lookup bpb.x70hrsl-a5a.workers.dev: no such host")
	sanitized := sanitizeProxyError(raw, "")
	if sanitized.Error() != raw.Error() {
		t.Fatalf("empty proxyRaw must preserve message verbatim, got %q", sanitized.Error())
	}
	if strings.Contains(sanitized.Error(), "configured proxy") {
		t.Fatalf("empty proxyRaw must not inject placeholder text: %s", sanitized.Error())
	}
}

func startEchoTarget(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				buffer := make([]byte, 4)
				if _, err := io.ReadFull(connection, buffer); err == nil && string(buffer) == "ping" {
					_, _ = connection.Write([]byte("pong"))
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func startHTTPConnectProxy(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				request, err := http.ReadRequest(bufio.NewReader(connection))
				if err != nil || request.Method != http.MethodConnect {
					return
				}
				target, err := net.DialTimeout("tcp", request.Host, time.Second)
				if err != nil {
					return
				}
				defer target.Close()
				_, _ = connection.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				proxyConnections(target, connection)
			}()
		}
	}()
	return listener.Addr().String()
}

func startSOCKS5Proxy(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5Connection(connection)
		}
	}()
	return listener.Addr().String()
}

func handleSOCKS5Connection(connection net.Conn) {
	defer connection.Close()
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return
	}
	if _, err := connection.Write([]byte{5, 0}); err != nil {
		return
	}
	requestHeader := make([]byte, 4)
	if _, err := io.ReadFull(connection, requestHeader); err != nil || requestHeader[0] != 5 || requestHeader[1] != 1 {
		return
	}
	var host string
	switch requestHeader[3] {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(connection, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(connection, length); err != nil {
			return
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(connection, address); err != nil {
			return
		}
		host = string(address)
	case 4:
		address := make([]byte, 16)
		if _, err := io.ReadFull(connection, address); err != nil {
			return
		}
		host = net.IP(address).String()
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(connection, portBytes); err != nil {
		return
	}
	target, err := net.DialTimeout("tcp", net.JoinHostPort(host, stringPort(binary.BigEndian.Uint16(portBytes))), time.Second)
	if err != nil {
		return
	}
	defer target.Close()
	if _, err := connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	proxyConnections(target, connection)
}

func stringPort(port uint16) string {
	return strconv.Itoa(int(port))
}

func proxyConnections(left, right net.Conn) {
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(left, right)
		_ = left.Close()
		done <- struct{}{}
	}()
	_, _ = io.Copy(right, left)
	_ = right.Close()
	<-done
}
