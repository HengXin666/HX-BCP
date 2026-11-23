package pipeline

import "testing"

func TestRegionFromName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Australia 01":                   "",
		"Russia-Moscow":                  "",
		"US-Los Angeles 02":              "US",
		"美国 01":                          "US",
		"\U0001F1EF\U0001F1F5 Tokyo":     "JP",
		"\U0001F1E9\U0001F1EA Frankfurt": "DE",
		"hk01":                           "HK",
	}
	for name, want := range cases {
		if got := RegionFromName(name); got != want {
			t.Errorf("RegionFromName(%q) = %q, want %q", name, got, want)
		}
	}
}
