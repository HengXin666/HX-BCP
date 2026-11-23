package mihomo

import (
	"fmt"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

const managedTUNDevice = "hx-pxy-tun"

// Only the TUN inbound uses this default route; dedicated services keep theirs.
// See .agents/notes/implemented/feature/2026-10-14-managed-tun.md.
func compileTUN(settings systemsettings.Settings, groups []store.ProxyGroupRecord) (map[string]any, string, error) {
	if settings.TUN == nil || !settings.TUN.Enabled {
		return map[string]any{"enable": false}, "", nil
	}
	groupName := ""
	for _, group := range groups {
		if group.ID == settings.TUN.ProxyGroupID && group.Enabled {
			groupName = group.Name
		}
	}
	if groupName == "" {
		return nil, "", fmt.Errorf("TUN proxy group is missing or disabled")
	}
	return map[string]any{
		"enable": true, "device": managedTUNDevice, "stack": "mixed",
		"auto-route": true, "auto-detect-interface": true, "strict-route": false,
		"auto-redirect": false, "dns-hijack": []string{"any:53"},
		"route-exclude-address": []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16", "fc00::/7", "fe80::/10", "::1/128"},
	}, "IN-TYPE,TUN," + groupName, nil
}
