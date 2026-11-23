package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPv4FromAddressRejectsUnusableAddresses(t *testing.T) {
	for name, address := range map[string]net.Addr{
		"loopback":    &net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		"ipv6":        &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		"link-local":  &net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
		"unspecified": &net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
	} {
		if got := ipv4FromAddress(address); got != "" {
			t.Errorf("ipv4FromAddress(%s) = %q, want empty", name, got)
		}
	}
	if got := ipv4FromAddress(&net.IPNet{IP: net.ParseIP("172.17.0.1"), Mask: net.CIDRMask(16, 32)}); got != "172.17.0.1" {
		t.Fatalf("ipv4FromAddress(bridge) = %q, want 172.17.0.1", got)
	}
}

func TestResolveNetworkAddressesNeverReportsUnusableAddresses(t *testing.T) {
	addresses := resolveNetworkAddresses()
	for _, value := range addresses.LANAddresses {
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			t.Fatalf("LANAddresses contains a non-IPv4 entry: %q", value)
		}
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			t.Fatalf("LANAddresses contains an unusable address: %q", value)
		}
	}
	if addresses.DockerGateway != "" {
		gateway := net.ParseIP(addresses.DockerGateway)
		if gateway == nil || gateway.To4() == nil {
			t.Fatalf("docker gateway is not IPv4: %q", addresses.DockerGateway)
		}
		// A container reachable address must also be listed in the LAN set so a
		// client can fall back to it when the docker bridge field is empty.
		found := false
		for _, value := range addresses.LANAddresses {
			if value == addresses.DockerGateway {
				found = true
			}
		}
		if !found {
			t.Fatalf("docker gateway %q is missing from LANAddresses", addresses.DockerGateway)
		}
	}
}

func TestSystemNetworkEndpointRejectsNonGet(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	server.handleSystemNetwork(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/system/network", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /system/network status = %d, want 405", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	server.handleSystemNetwork(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/system/network", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /system/network status = %d, want 200", recorder.Code)
	}
	var payload NetworkAddresses
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.LANAddresses == nil {
		t.Fatal("lan_addresses must serialise as [] rather than null")
	}
}
