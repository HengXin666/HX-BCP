package mihomo

import (
	"context"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTUNDefaultAndGroupValidation(t *testing.T) {
	settings := systemsettings.Default()
	config, rule, err := compileTUN(settings, nil)
	if err != nil || config["enable"] != false || rule != "" {
		t.Fatal("TUN not disabled by default")
	}
	settings.TUN = &systemsettings.TUNSettings{Enabled: true, ProxyGroupID: "g"}
	if _, _, err = compileTUN(settings, nil); err == nil {
		t.Fatal("missing group accepted")
	}
	if _, _, err = compileTUN(settings, []store.ProxyGroupRecord{{ID: "g", Name: "exit"}}); err == nil {
		t.Fatal("disabled group accepted")
	}
	config, rule, err = compileTUN(settings, []store.ProxyGroupRecord{{ID: "g", Name: "exit", Enabled: true}})
	if err != nil || rule != "IN-TYPE,TUN,exit" || config["auto-redirect"] != false {
		t.Fatalf("invalid TUN %v %s %v", config, rule, err)
	}
	// Real parser validation, not starting a privileged TUN on the test host.
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("Mihomo unavailable")
	}
	document := map[string]any{"tun": config, "dns": map[string]any{"enable": true, "nameserver": []string{"1.1.1.1"}}, "proxy-groups": []map[string]any{{"name": "exit", "type": "select", "proxies": []string{"DIRECT"}}}, "rules": []string{rule, "MATCH,DIRECT"}}
	data, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "candidate.yaml")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, binary, "-t", "-d", dir, "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("real TUN config rejected: %s %v", output, err)
	}
	if strings.Contains(string(data), "auto-redirect: true") {
		t.Fatal("unexpected host firewall takeover")
	}
}
