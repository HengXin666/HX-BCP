package systemsettings

import "testing"

func TestTUNRequiresDNSAndGroup(t *testing.T) {
	settings := Default()
	if settings.TUN == nil || settings.TUN.Enabled {
		t.Fatal("TUN must default off")
	}
	settings.TUN.Enabled = true
	if Validate(settings) == nil {
		t.Fatal("missing group accepted")
	}
	settings.TUN.ProxyGroupID = "group-id"
	if err := Validate(settings); err != nil {
		t.Fatal(err)
	}
	settings.DNS.Enabled = false
	if Validate(settings) == nil {
		t.Fatal("disabled DNS accepted")
	}
}
