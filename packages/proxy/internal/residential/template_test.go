package residential

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestRenderSubstitutesEveryPlaceholder(t *testing.T) {
	t.Parallel()

	rendered, err := Render("{user}-region-{region}-session-{session}-sessTime-{ttl}", Variables{
		User:    "acct123",
		Region:  "us",
		Session: "a1b2c3d4",
		TTL:     "600",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	const want = "acct123-region-us-session-a1b2c3d4-sessTime-600"
	if rendered != want {
		t.Fatalf("Render() = %q, want %q", rendered, want)
	}
}

func TestRenderRejectsInvalidTemplates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		template string
	}{
		{"empty", "   "},
		{"unknown placeholder", "{user}-{unknown}"},
		{"unterminated", "{user}-{session"},
		{"empty placeholder", "{user}-{}"},
		{"missing user", "session-{session}"},
		{"too long", "{user}" + strings.Repeat("x", maximumTemplateLength)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Render(testCase.template, Variables{User: "acct", Session: "s1"}); !errors.Is(err, ErrInvalidTemplate) {
				t.Fatalf("Render(%q) error = %v, want ErrInvalidTemplate", testCase.template, err)
			}
		})
	}
}

// A rendered username becomes the user half of a user:password pair. Any value
// that could break that framing must be rejected rather than escaped, otherwise
// a crafted region label could inject a password or a second field.
func TestRenderRejectsCredentialFramingBreakers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		variables Variables
	}{
		{"colon in user", Variables{User: "acct:pass", Session: "s1"}},
		{"colon in region", Variables{User: "acct", Region: "us:west", Session: "s1"}},
		{"space in session", Variables{User: "acct", Session: "s 1"}},
		{"newline in session", Variables{User: "acct", Session: "s1\nx"}},
		{"tab in region", Variables{User: "acct", Region: "u\ts", Session: "s1"}},
		{"brace in session", Variables{User: "acct", Session: "{user}"}},
		{"non ascii", Variables{User: "acct", Region: "美国", Session: "s1"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := Render("{user}-region-{region}-session-{session}", testCase.variables)
			if !errors.Is(err, ErrInvalidTemplate) {
				t.Fatalf("Render(%+v) error = %v, want ErrInvalidTemplate", testCase.variables, err)
			}
		})
	}
}

func TestRenderRequiresGatewayUser(t *testing.T) {
	t.Parallel()

	if _, err := Render("{user}-session-{session}", Variables{Session: "s1"}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("Render() with empty user error = %v, want ErrInvalidTemplate", err)
	}
}

// Leaving an optional placeholder blank is legitimate: it means "let the vendor
// choose", e.g. any region.
func TestRenderAllowsBlankOptionalValues(t *testing.T) {
	t.Parallel()

	rendered, err := Render("{user}{region}-session-{session}", Variables{User: "acct", Session: "s1"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if rendered != "acct-session-s1" {
		t.Fatalf("Render() = %q, want %q", rendered, "acct-session-s1")
	}
}

func TestTemplateUsesSession(t *testing.T) {
	t.Parallel()

	if !TemplateUsesSession("{user}-session-{session}") {
		t.Fatal("TemplateUsesSession() = false for a template with {session}")
	}
	if TemplateUsesSession("{user}") {
		t.Fatal("TemplateUsesSession() = true for a template without {session}")
	}
	if TemplateUsesSession("{user}-{bogus}") {
		t.Fatal("TemplateUsesSession() = true for an invalid template")
	}
}

func TestValidateTemplateAcceptsRegisteredPresets(t *testing.T) {
	t.Parallel()

	for _, preset := range Presets() {
		// api-list and cf-worker presets have no username template: their
		// endpoints come from an extraction API or a Cloudflare Worker panel
		// subscription link instead of a gateway login.
		if preset.RotationMode != RotationAPIList && preset.RotationMode != RotationCloudflareWorker &&
			preset.RotationMode != RotationHXCFWsPxy {
			if err := ValidateTemplate(preset.UsernameTemplate); err != nil {
				t.Errorf("preset %q template %q is invalid: %v", preset.Vendor, preset.UsernameTemplate, err)
			}
		}
		if preset.RotationMode == RotationSessionTemplate && !TemplateUsesSession(preset.UsernameTemplate) {
			t.Errorf("preset %q claims session rotation but omits {session}", preset.Vendor)
		}
		if !slicesContains(SupportedProtocols(), preset.Protocol) && !slicesContains(SupportedWorkerProtocols(), preset.Protocol) {
			t.Errorf("preset %q uses unsupported protocol %q", preset.Vendor, preset.Protocol)
		}
		if !slicesContains(SupportedRotationModes(), preset.RotationMode) {
			t.Errorf("preset %q uses unsupported rotation mode %q", preset.Vendor, preset.RotationMode)
		}
	}
}

// The BestProxy preset is verified against the syntax the operator's own
// production tool (hx-auto-outlook) uses: `账号_area-国家_life-分钟_session-会话ID`.
func TestBestProxyPresetIsRegisteredAndVerified(t *testing.T) {
	t.Parallel()

	preset, found := PresetByVendor("bestproxy")
	if !found {
		t.Fatal("PresetByVendor(\"bestproxy\") not found")
	}
	if !preset.Verified {
		t.Fatal("BestProxy preset syntax is verified and must stay verified")
	}
	if preset.DocURL == "" || preset.Notes == "" {
		t.Fatal("a verified preset must still carry a doc URL and operator guidance")
	}
	if !TemplateUsesSession(preset.UsernameTemplate) {
		t.Fatal("BestProxy preset must support sticky sessions for rotation")
	}
	if preset.GatewayHost != "proxy.bestproxy.com" || preset.GatewayPort != 2312 {
		t.Fatalf("BestProxy preset gateway = %s:%d, want proxy.bestproxy.com:2312", preset.GatewayHost, preset.GatewayPort)
	}
}

func TestBestProxyPresetRendersVerifiedGatewaySyntax(t *testing.T) {
	t.Parallel()

	preset, found := PresetByVendor("bestproxy")
	if !found {
		t.Fatal("PresetByVendor(\"bestproxy\") not found")
	}
	rendered, err := Render(preset.UsernameTemplate, Variables{
		User:    "acct123",
		Region:  "US",
		Session: "stable1234",
		TTL:     strconv.Itoa(preset.SessionTTLSeconds),
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	const want = "acct123_area-US_life-60_session-stable1234"
	if rendered != want {
		t.Fatalf("Render() = %q, want %q", rendered, want)
	}
}

// The RapidProxy preset renders the dialect `user-residential-region-session-<id>-stime-<minutes>`
// the operator's own working curl creds use: hxloli666-residential-GLOBAL-session-...-stime-10.
func TestRapidProxyPresetIsRegisteredAndVerified(t *testing.T) {
	t.Parallel()

	preset, found := PresetByVendor("rapidproxy")
	if !found {
		t.Fatal("PresetByVendor(\"rapidproxy\") not found")
	}
	if !preset.Verified {
		t.Fatal("RapidProxy preset syntax is verified and must stay verified")
	}
	if !TemplateUsesSession(preset.UsernameTemplate) {
		t.Fatal("RapidProxy preset must support sticky sessions for rotation")
	}
	if preset.GatewayHost != "us.rapidproxy.io" || preset.GatewayPort != 5001 {
		t.Fatalf("RapidProxy preset gateway = %s:%d, want us.rapidproxy.io:5001", preset.GatewayHost, preset.GatewayPort)
	}
	if preset.RotationMode != RotationSessionTemplate {
		t.Fatalf("RapidProxy preset rotation = %q, want session-template", preset.RotationMode)
	}
}

func TestRapidProxyPresetRendersGatewaySyntax(t *testing.T) {
	t.Parallel()

	preset, found := PresetByVendor("rapidproxy")
	if !found {
		t.Fatal("PresetByVendor(\"rapidproxy\") not found")
	}
	rendered, err := Render(preset.UsernameTemplate, Variables{
		User:    "hxloli666",
		Region:  "GLOBAL",
		Session: "42441522",
		TTL:     "10",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	const want = "hxloli666-residential-GLOBAL-session-42441522-stime-10"
	if rendered != want {
		t.Fatalf("Render() = %q, want %q", rendered, want)
	}
}

func TestRapidProxyPresetRendersCountryCode(t *testing.T) {
	t.Parallel()

	preset, _ := PresetByVendor("rapidproxy")
	rendered, err := Render(preset.UsernameTemplate, Variables{
		User:    "acct123",
		Region:  "US",
		Session: "stable12345",
		TTL:     "60",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	const want = "acct123-residential-US-session-stable12345-stime-60"
	if rendered != want {
		t.Fatalf("Render() = %q, want %q", rendered, want)
	}
}

// Region codes like BestProxy's area parameter are case-significant to the
// vendor, so uppercase two-letter country codes must survive validation.
func TestValidateRegionPreservesCase(t *testing.T) {
	t.Parallel()

	for _, region := range []string{"US", "us", "HongKong", "US-CA"} {
		if err := validateRegion(region); err != nil {
			t.Errorf("validateRegion(%q) error = %v", region, err)
		}
	}
	for _, region := range []string{"u s", "us:west", "美国", "us\n"} {
		if err := validateRegion(region); err == nil {
			t.Errorf("validateRegion(%q) = nil, want error", region)
		}
	}
}

func TestPresetsReturnsACopy(t *testing.T) {
	t.Parallel()

	first := Presets()
	if len(first) == 0 {
		t.Fatal("Presets() is empty")
	}
	first[0].Vendor = "mutated"
	if Presets()[0].Vendor == "mutated" {
		t.Fatal("Presets() exposed the package-level registry")
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
