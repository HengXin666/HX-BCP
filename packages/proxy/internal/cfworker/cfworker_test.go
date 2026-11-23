package cfworker

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNeutralNameUnique(t *testing.T) {
	used := map[string]bool{"alder42": true, "willow95": true, "falcon63": true}
	for i := 0; i < 50; i++ {
		name := NeutralName(used)
		if used[name] {
			t.Fatalf("duplicate name %q", name)
		}
		used[name] = true
	}
}

func TestInjectSettings(t *testing.T) {
	injected := InjectSettings([]byte("export default {};"), map[string]string{
		"vlUUID": "abc", "securePath": "xyz",
	})
	text := string(injected)
	if !strings.Contains(text, "const EMBEDED_SETTINGS = ") {
		t.Fatal("missing EMBEDED_SETTINGS injection")
	}
	if !strings.Contains(text, `"vlUUID":"abc"`) {
		t.Fatal("missing field in injected settings")
	}
}

func TestProbeSubscriptionAcceptsNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := base64.StdEncoding.EncodeToString([]byte("vless://abc@host:443?type=ws"))
		_, _ = writer.Write([]byte(body))
	}))
	defer server.Close()
	ok, _ := ProbeSubscription(context.Background(), server.URL, 5*time.Second)
	if !ok {
		t.Fatal("expected probe OK")
	}
}

func TestProbeSubscriptionRejectsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("not a subscription"))
	}))
	defer server.Close()
	ok, detail := ProbeSubscription(context.Background(), server.URL, 5*time.Second)
	if ok {
		t.Fatal("expected probe failure")
	}
	if !strings.Contains(detail, "不含") {
		t.Fatalf("unexpected detail: %s", detail)
	}
}
