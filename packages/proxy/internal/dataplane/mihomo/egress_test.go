package mihomo

import (
	"os"
	"strings"
	"testing"
)

func TestParseDefaultRouteInterfaceUsesLowestActiveMetric(t *testing.T) {
	t.Parallel()
	routes := `Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT
tun0 00000000 00000000 0000 0 0 1 00000000 0 0 0
eth1 00000000 0100000A 0003 0 0 200 00000000 0 0 0
eth0 00000000 0100000A 0003 0 0 100 00000000 0 0 0
eth2 0000000A 00000000 0001 0 0 1 00FFFFFF 0 0 0
`
	name, err := parseDefaultRouteInterface(strings.NewReader(routes))
	if err != nil {
		t.Fatalf("parseDefaultRouteInterface() error = %v", err)
	}
	if name != "eth0" {
		t.Fatalf("interface = %q, want eth0", name)
	}
}

func TestParseDefaultRouteInterfaceRejectsMissingDefault(t *testing.T) {
	t.Parallel()
	_, err := parseDefaultRouteInterface(strings.NewReader("Iface Destination Gateway Flags RefCnt Use Metric Mask\neth0 0000000A 0 1 0 0 0 00FFFFFF\n"))
	if err == nil {
		t.Fatal("parseDefaultRouteInterface() error = nil, want rejection")
	}
}

func TestResolveEgressInterfaceAllowsExplicitDisable(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"off", "none", "disabled"} {
		name, err := ResolveEgressInterface(value)
		if err != nil || name != "" {
			t.Errorf("ResolveEgressInterface(%q) = %q, %v; want empty, nil", value, name, err)
		}
	}
}

func TestInterfaceIsWireless(t *testing.T) {
	t.Parallel()
	// 本机 wlo1 是 Wi-Fi（/sys/class/net/wlo1/wireless 存在）
	if info, err := os.Stat("/sys/class/net/wlo1/wireless"); err == nil && info.IsDir() {
		if !interfaceIsWireless("wlo1") {
			t.Fatal("interfaceIsWireless(wlo1) = false, want true for the Wi-Fi NIC")
		}
	}
	// 不存在的接口必须返回 false 而不崩溃
	if interfaceIsWireless("definitely-not-an-interface-xyz") {
		t.Fatal("interfaceIsWireless(nonexistent) = true, want false")
	}
}

func TestResolveEgressInterfaceSkipsWireless(t *testing.T) {
	t.Parallel()
	// 20260821 回归：Wi-Fi 接口（wlo1）做 auto egress 会导致 mihomo 出站
	// WebSocket reset（cf-worker 节点实测）。auto 解析到无线接口时应返回空。
	info, err := os.Stat("/sys/class/net/wlo1/wireless")
	if err != nil || !info.IsDir() {
		t.Skip("no wireless interface on this host")
	}
	name, err := ResolveEgressInterface("auto")
	if err != nil {
		t.Fatalf("ResolveEgressInterface(auto) error = %v", err)
	}
	if name != "" {
		t.Fatalf("ResolveEgressInterface(auto) = %q on a wireless default route, want empty", name)
	}
}
