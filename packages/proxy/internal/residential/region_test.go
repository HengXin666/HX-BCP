package residential

import (
	"context"
	"testing"
)

func TestNormalizeRegionSelectionRequiresManualCandidates(t *testing.T) {
	t.Parallel()

	selection, err := normalizeRegionSelection(
		string(RegionModeApplicationRandom),
		"",
		[]string{" US ", "JP", "US"},
	)
	if err != nil {
		t.Fatalf("normalizeRegionSelection() error = %v", err)
	}
	if got, want := selection.RandomRegions, []string{"US", "JP"}; !slicesEqual(got, want) {
		t.Fatalf("random regions = %v, want %v", got, want)
	}
	for range 32 {
		region, err := chooseRegion(selection)
		if err != nil {
			t.Fatalf("chooseRegion() error = %v", err)
		}
		if region != "US" && region != "JP" {
			t.Fatalf("chooseRegion() = %q, want one of the configured candidates", region)
		}
	}

	if _, err := normalizeRegionSelection(string(RegionModeApplicationRandom), "", nil); err == nil {
		t.Fatal("application-random selection without candidates succeeded")
	}
	if _, err := normalizeRegionSelection(string(RegionModeFixed), "US", []string{"JP"}); err == nil {
		t.Fatal("fixed selection with random candidates succeeded")
	}
}

func TestAPIURLWithRegionUsesExistingParameterOrBestProxyCC(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "BestProxy cc",
			url:  "https://api.example.com/nodes?num=1&cc=US",
			want: "https://api.example.com/nodes?cc=JP&num=1",
		},
		{
			name: "generic country",
			url:  "https://api.example.com/nodes?country=US",
			want: "https://api.example.com/nodes?country=JP",
		},
		{
			name: "missing parameter",
			url:  "https://api.example.com/nodes?num=1",
			want: "https://api.example.com/nodes?cc=JP&num=1",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := apiURLWithRegion(testCase.url, "JP")
			if err != nil {
				t.Fatalf("apiURLWithRegion() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("apiURLWithRegion() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestApplicationRandomRegionFlowsIntoAPIListAllocation(t *testing.T) {
	t.Parallel()

	var fetchedURL string
	harness := newHarness(t, WithNodeFetcher(func(_ context.Context, apiURL string) ([]FetchedNode, error) {
		fetchedURL = apiURL
		return []FetchedNode{{Server: "11.22.33.44", Port: 8000}}, nil
	}))
	provider, err := harness.service.CreateProvider(context.Background(), CreateProviderRequest{
		Name:                  "api-random-region",
		Vendor:                "bestproxy-api",
		Protocol:              "http",
		APIURL:                "https://api.example.com/nodes?num=1&cc=US",
		RotationMode:          RotationAPIList,
		SessionTTLSeconds:     60,
		MaxConcurrentSessions: 4,
		DefaultRegionMode:     RegionModeApplicationRandom,
		DefaultRandomRegions:  []string{"JP"},
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if provider.DefaultRegionMode != RegionModeApplicationRandom || len(provider.DefaultRandomRegions) != 1 || provider.DefaultRandomRegions[0] != "JP" {
		t.Fatalf("provider region policy = %+v, want application-random JP", provider)
	}
	channel, err := harness.service.CreateChannel(context.Background(), CreateChannelRequest{
		Name:           "api-random-channel",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.RegionMode != RegionModeApplicationRandom || len(channel.RandomRegions) != 1 || channel.RandomRegions[0] != "JP" {
		t.Fatalf("channel region policy = %+v, want inherited application-random JP", channel)
	}
	record, err := harness.store.GetResidentialChannel(context.Background(), channel.ID)
	if err != nil {
		t.Fatalf("GetResidentialChannel() error = %v", err)
	}
	if _, err := harness.service.EnsureClientSessionByToken(context.Background(), record.RotateToken, "window-01"); err != nil {
		t.Fatalf("EnsureClientSessionByToken() error = %v", err)
	}
	if fetchedURL != "https://api.example.com/nodes?cc=JP&num=1" {
		t.Fatalf("fetch URL = %q, want application-selected region", fetchedURL)
	}
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
