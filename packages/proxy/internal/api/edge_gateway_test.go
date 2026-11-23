package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

func TestEdgeRelayForwardsWebSocketBytesToMihomoListener(t *testing.T) {
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backend := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isWebSocketUpgrade(request) {
			http.Error(writer, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Error("backend does not support hijacking")
			return
		}
		connection, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("backend hijack: %v", err)
			return
		}
		defer connection.Close()
		if _, err := buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"); err != nil {
			t.Errorf("backend handshake: %v", err)
			return
		}
		if err := buffered.Flush(); err != nil {
			t.Errorf("backend flush: %v", err)
			return
		}
		payload := make([]byte, len("client-frame"))
		if _, err := io.ReadFull(connection, payload); err != nil {
			t.Errorf("backend read client payload: %v", err)
			return
		}
		if string(payload) != "client-frame" {
			t.Errorf("backend payload = %q, want client-frame", payload)
			return
		}
		if _, err := connection.Write([]byte("server-frame")); err != nil {
			t.Errorf("backend write server payload: %v", err)
		}
	})}
	go func() { _ = backend.Serve(backendListener) }()
	t.Cleanup(func() { _ = backend.Close() })

	service := &edgeListenerService{items: []listener.Listener{{
		ID:          "listener-edge-1",
		Kind:        "vless",
		BindAddress: "127.0.0.1",
		Port:        backendListener.Addr().(*net.TCPAddr).Port,
		Transport: listener.Transport{
			Type:   "ws",
			WSPath: "/edge",
		},
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
		Enabled:        true,
	}}}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithListeners(service))
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(server.Handler())
	t.Cleanup(gateway.Close)

	gatewayURL, err := url.Parse(gateway.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", gatewayURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	path := listener.WebSocketPathPrefix + "edge"
	if _, err := fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: proxy.example.com\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", path); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("gateway handshake = %q", statusLine)
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := connection.Write([]byte("client-frame")); err != nil {
		t.Fatal(err)
	}
	responsePayload := make([]byte, len("server-frame"))
	if _, err := io.ReadFull(reader, responsePayload); err != nil {
		t.Fatal(err)
	}
	if string(responsePayload) != "server-frame" {
		t.Fatalf("gateway payload = %q, want server-frame", responsePayload)
	}
}

func TestEdgeRelayRequiresReservedPathHostAndUpgrade(t *testing.T) {
	service := &edgeListenerService{items: []listener.Listener{{
		ID: "listener-edge-2", Kind: "vless", BindAddress: "127.0.0.1", Port: 18088,
		Transport:      listener.Transport{Type: "ws", WSPath: "/edge"},
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
		Enabled:        true,
	}}}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithListeners(service))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	plain := httptest.NewRecorder()
	plainRequest := httptest.NewRequest(http.MethodGet, listener.WebSocketPathPrefix+"edge", nil)
	plainRequest.Host = "proxy.example.com"
	handler.ServeHTTP(plain, plainRequest)
	if plain.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain edge request status = %d, want %d", plain.Code, http.StatusUpgradeRequired)
	}

	unknown := httptest.NewRecorder()
	unknownRequest := newEdgeUpgradeRequest(listener.WebSocketPathPrefix+"unknown", "proxy.example.com")
	handler.ServeHTTP(unknown, unknownRequest)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown edge route status = %d, want %d", unknown.Code, http.StatusNotFound)
	}

	wrongHost := httptest.NewRecorder()
	wrongHostRequest := newEdgeUpgradeRequest(listener.WebSocketPathPrefix+"edge", "other.example.com")
	handler.ServeHTTP(wrongHost, wrongHostRequest)
	if wrongHost.Code != http.StatusNotFound {
		t.Fatalf("wrong host edge route status = %d, want %d", wrongHost.Code, http.StatusNotFound)
	}

	legacyPath := httptest.NewRecorder()
	legacyRequest := newEdgeUpgradeRequest("/edge", "proxy.example.com")
	handler.ServeHTTP(legacyPath, legacyRequest)
	if legacyPath.Code != http.StatusNotFound {
		t.Fatalf("legacy edge route status = %d, want %d", legacyPath.Code, http.StatusNotFound)
	}
}

func newEdgeUpgradeRequest(path, host string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Host = host
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Sec-WebSocket-Version", "13")
	return request
}

type edgeListenerService struct {
	items []listener.Listener
}

func (s *edgeListenerService) Create(context.Context, listener.CreateRequest) (listener.Listener, error) {
	return listener.Listener{}, errors.New("edge test service does not create listeners")
}

func (s *edgeListenerService) Get(context.Context, string) (listener.Listener, error) {
	return listener.Listener{}, listener.ErrNotFound
}

func (s *edgeListenerService) List(context.Context) ([]listener.Listener, error) {
	return append([]listener.Listener(nil), s.items...), nil
}

func (s *edgeListenerService) Update(context.Context, string, listener.UpdateRequest) (listener.Listener, error) {
	return listener.Listener{}, listener.ErrNotFound
}

func (s *edgeListenerService) Delete(context.Context, string, int) error {
	return listener.ErrNotFound
}

func (s *edgeListenerService) ExportByShareToken(context.Context, string, string) (listener.ShareExport, error) {
	return listener.ShareExport{}, listener.ErrNotFound
}

func (s *edgeListenerService) RotateShareToken(context.Context, string) (listener.Listener, error) {
	return listener.Listener{}, listener.ErrNotFound
}
