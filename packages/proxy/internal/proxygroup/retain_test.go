package proxygroup

import (
	"errors"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/pipeline"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestValidateRetainRequiresALimitAndSaneValues(t *testing.T) {
	if err := validateRetain(SourceSpec{}); err != nil {
		t.Fatalf("no retain: %v", err)
	}
	if err := validateRetain(SourceSpec{Limit: 2, NodeIDs: []string{"residential-exit"}, Retain: &pipeline.RetainSpec{Margin: 10}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retain on a non-subscription group err = %v", err)
	}
	if err := validateRetain(SourceSpec{SubscriptionIDs: []string{"s"}, Retain: &pipeline.RetainSpec{Margin: 10}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retain without limit err = %v", err)
	}
	if err := validateRetain(SourceSpec{Limit: 3, SubscriptionIDs: []string{"s"}, Retain: &pipeline.RetainSpec{Margin: -1}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative margin err = %v", err)
	}
	if err := validateRetain(SourceSpec{Limit: 3, SubscriptionIDs: []string{"s"}, Retain: &pipeline.RetainSpec{Margin: 20, MinDwellSeconds: 600}}); err != nil {
		t.Fatalf("valid retain err = %v", err)
	}
}

func TestResolveNodeIDsRetainingWithoutHistoryMatchesPlainResolution(t *testing.T) {
	fast, slow := 10, 200
	candidates := []store.GroupNodeCandidate{
		{NodeConfigRecord: store.NodeConfigRecord{ID: "a", Fingerprint: "fa", LifecycleState: "healthy"}, SubscriptionIDs: []string{"s"}, LastLatencyMS: &slow},
		{NodeConfigRecord: store.NodeConfigRecord{ID: "b", Fingerprint: "fb", LifecycleState: "healthy"}, SubscriptionIDs: []string{"s"}, LastLatencyMS: &fast},
	}
	spec := SourceSpec{SubscriptionIDs: []string{"s"}, Limit: 1, Retain: &pipeline.RetainSpec{Margin: 1000}}
	if got := ResolveNodeIDsRetaining(spec, candidates, pipeline.Incumbents{}); len(got) != 1 || got[0] != "b" {
		t.Fatalf("got %v", got)
	}
	incumbents := pipeline.Incumbents{JoinedAt: map[string]time.Time{"fa": time.Unix(0, 0)}, Now: time.Unix(3600, 0)}
	if got := ResolveNodeIDsRetaining(spec, candidates, incumbents); len(got) != 1 || got[0] != "a" {
		t.Fatalf("incumbent not retained: %v", got)
	}
}
