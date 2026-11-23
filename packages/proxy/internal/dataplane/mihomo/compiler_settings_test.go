package mihomo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
	"gopkg.in/yaml.v3"
)

func TestCompilerAppliesGlobalDNSAndPerformanceSettings(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings := systemsettings.Default()
	settings.DNS.IPv6 = true
	settings.DNS.EnhancedMode = "fake-ip"
	settings.DNS.Nameserver = []string{"https://1.1.1.1/dns-query"}
	settings.Performance.LogLevel = "info"
	settings.Performance.KeepAliveIdle = 30
	if err := systemsettings.Save(ctx, database, settings); err != nil {
		t.Fatal(err)
	}
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompiler(database, box)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(compiled.YAML, &document); err != nil {
		t.Fatal(err)
	}
	if document["ipv6"] != true || document["log-level"] != "info" || document["keep-alive-idle"] != 30 {
		t.Fatalf("compiled globals = %+v", document)
	}
	dns, ok := document["dns"].(map[string]any)
	if !ok || dns["enhanced-mode"] != "fake-ip" {
		t.Fatalf("compiled DNS = %+v", document["dns"])
	}
}
