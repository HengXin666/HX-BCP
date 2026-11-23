package systemsettings

import "fmt"

// TUN is opt-in and omitted documents preserve the stored value.
// See .agents/notes/implemented/feature/2026-10-14-managed-tun.md.
type TUNSettings struct {
	Enabled      bool   `json:"enabled"`
	ProxyGroupID string `json:"proxy_group_id"`
}

func validateTUN(tun *TUNSettings, dns DNSSettings) error {
	if tun == nil || !tun.Enabled {
		return nil
	}
	if tun.ProxyGroupID == "" || len(tun.ProxyGroupID) > 128 {
		return fmt.Errorf("%w: tun.proxy_group_id is required", ErrInvalid)
	}
	if !dns.Enabled {
		return fmt.Errorf("%w: TUN requires DNS enabled", ErrInvalid)
	}
	return nil
}
