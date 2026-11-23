package listener

import "testing"

func TestPublicPathURLOmitsDefaultHTTPSPort(t *testing.T) {
	got := PublicPathURL(
		PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
		"/sub/redacted?format=clash",
	)
	if got != "https://proxy.example.com/sub/redacted?format=clash" {
		t.Fatalf("PublicPathURL() = %q", got)
	}
}

func TestPublicPathURLKeepsNonDefaultPort(t *testing.T) {
	got := PublicPathURL(
		PublicEndpoint{Host: "proxy.example.com", Port: 8443, TLS: true},
		"/rot/redacted",
	)
	if got != "https://proxy.example.com:8443/rot/redacted" {
		t.Fatalf("PublicPathURL() = %q", got)
	}
}

func TestPublicPathURLRejectsNonPathRoute(t *testing.T) {
	if got := PublicPathURL(PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true}, "rot/redacted"); got != "" {
		t.Fatalf("PublicPathURL() = %q, want empty", got)
	}
}
