package quickstart

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/proxyservice"
	"github.com/HengXin666/hx-pxy/internal/subscription"
)

type fakeSubscriptions struct {
	created        []subscription.CreateRequest
	createdRecord  subscription.Subscription
	refreshID      string
	refreshResult  subscription.RefreshResult
	refreshErr     error
	deleted        []string
	nextVersionSeq int
}

func (s *fakeSubscriptions) Create(_ context.Context, request subscription.CreateRequest) (subscription.Subscription, error) {
	s.created = append(s.created, request)
	s.nextVersionSeq++
	s.createdRecord = subscription.Subscription{
		ID: "sub-1", Name: request.Name, Version: s.nextVersionSeq,
	}
	return s.createdRecord, nil
}

func (s *fakeSubscriptions) Refresh(_ context.Context, id string) (subscription.RefreshResult, error) {
	s.refreshID = id
	if s.refreshErr != nil {
		return subscription.RefreshResult{}, s.refreshErr
	}
	return s.refreshResult, nil
}

func (s *fakeSubscriptions) Delete(_ context.Context, id string, _ int) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type fakeServices struct {
	received proxyservice.CreateRequest
	record   proxyservice.ServiceRecord
	err      error
}

func (s *fakeServices) Create(_ context.Context, request proxyservice.CreateRequest) (proxyservice.ServiceRecord, error) {
	s.received = request
	if s.err != nil {
		return proxyservice.ServiceRecord{}, s.err
	}
	return s.record, nil
}

func newTestService(t *testing.T) (*Service, *fakeSubscriptions, *fakeServices) {
	t.Helper()
	subscriptions := &fakeSubscriptions{
		refreshResult: subscription.RefreshResult{SubscriptionID: "sub-1", EstimatedNodes: 12},
	}
	services := &fakeServices{
		record: proxyservice.ServiceRecord{
			Group:    proxyGroupStub("group-1"),
			Listener: listener.Listener{ID: "listener-1", SharePath: "/sub/" + testToken},
		},
	}
	service, err := NewService(subscriptions, services)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, subscriptions, services
}

const testToken = "0123456789abcdef0123456789abcdef"

func TestCreateRefreshesBeforePublishing(t *testing.T) {
	t.Parallel()

	service, subscriptions, services := newTestService(t)
	result, err := service.Create(context.Background(), Request{
		Name:            "hk-pool",
		SubscriptionURL: "https://example.com/sub?token=x",
		SourceClass:     "subscription",
		Strategy:        "url-test",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The refresh must happen before the group is created: a group that selects
	// from an unrefreshed subscription matches nothing and publishes a service
	// that accepts connections and routes nothing.
	if subscriptions.refreshID != "sub-1" {
		t.Fatalf("refresh id = %q, want sub-1", subscriptions.refreshID)
	}
	if len(subscriptions.created) != 1 {
		t.Fatalf("created %d subscriptions, want 1", len(subscriptions.created))
	}
	if got := services.received.SourceSpec.SubscriptionIDs; len(got) != 1 || got[0] != "sub-1" {
		t.Fatalf("group source subscription ids = %v, want [sub-1]", got)
	}
	if services.received.SourceSpec.SortBy != "latency" {
		t.Fatalf("partial sort_by = %q, want latency", services.received.SourceSpec.SortBy)
	}
	if result.SharePath != "/sub/"+testToken {
		t.Fatalf("share path = %q", result.SharePath)
	}
	if result.ConsumerNodesPath != listener.ConsumerNodesPath+testToken {
		t.Fatalf("consumer nodes path = %q", result.ConsumerNodesPath)
	}
	if result.EstimatedNodes != 12 {
		t.Fatalf("estimated nodes = %d, want 12", result.EstimatedNodes)
	}
	// A successful call must not roll anything back.
	if len(subscriptions.deleted) != 0 {
		t.Fatalf("deleted %v after a successful create", subscriptions.deleted)
	}
	if len(result.Workflow) == 0 {
		t.Fatal("workflow is empty; a caller cannot audit the steps taken")
	}
}

// TestCreateRejectsOneNodeSourceBeforeBuildingTheGroup keeps the quickstart
// error next to the cause. The group service would reject the same request two
// steps later (after registering and refreshing a subscription), but the caller
// would have to read through a wrapped failure to find listener.node_id.
func TestCreateRejectsOneNodeSourceBeforeBuildingTheGroup(t *testing.T) {
	t.Parallel()

	service, subscriptions, services := newTestService(t)
	subscriptions.refreshResult = subscription.RefreshResult{SubscriptionID: "sub-1", EstimatedNodes: 1}

	_, err := service.Create(context.Background(), Request{
		Name:               "solo",
		SubscriptionInline: "1.2.3.4:8080",
		SourceClass:        "pool",
	})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "listener.node_id") {
		t.Fatalf("Create() error = %v, want ErrInvalid naming listener.node_id", err)
	}
	if services.received.Name != "" {
		t.Fatalf("group was created despite the single-node source: %+v", services.received)
	}
	if len(subscriptions.deleted) != 1 || subscriptions.deleted[0] != "sub-1" {
		t.Fatalf("deleted = %v, want the created subscription rolled back", subscriptions.deleted)
	}
}

func TestCreateRollsBackSubscriptionWhenPublishFails(t *testing.T) {
	t.Parallel()

	service, subscriptions, services := newTestService(t)
	services.err = proxyservice.ErrCreateFailed

	_, err := service.Create(context.Background(), Request{
		Name:            "hk-pool",
		SubscriptionURL: "https://example.com/sub?token=x",
		SourceClass:     "subscription",
	})
	if !errors.Is(err, ErrCreateFailed) {
		t.Fatalf("Create() error = %v, want ErrCreateFailed", err)
	}
	// The subscription this call created must not survive a later failure, or a
	// retry would accumulate duplicate sources.
	if len(subscriptions.deleted) != 1 || subscriptions.deleted[0] != "sub-1" {
		t.Fatalf("deleted = %v, want [sub-1]", subscriptions.deleted)
	}
}

func TestCreateRollsBackSubscriptionWhenRefreshFails(t *testing.T) {
	t.Parallel()

	service, subscriptions, services := newTestService(t)
	subscriptions.refreshErr = errors.New("fetch refused")

	_, err := service.Create(context.Background(), Request{
		Name:            "hk-pool",
		SubscriptionURL: "https://example.com/sub?token=x",
		SourceClass:     "subscription",
	})
	if !errors.Is(err, ErrCreateFailed) {
		t.Fatalf("Create() error = %v, want ErrCreateFailed", err)
	}
	if len(subscriptions.deleted) != 1 {
		t.Fatalf("deleted = %v, want the created subscription removed", subscriptions.deleted)
	}
	// Nothing should have been published after a failed refresh.
	if services.received.Name != "" {
		t.Fatalf("a service was created despite the refresh failing: %+v", services.received)
	}
}

func TestCreateValidatesSourceExclusivity(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	cases := []struct {
		name    string
		request Request
		want    string
	}{
		{
			name:    "no source",
			request: Request{Name: "pool"},
			want:    "provide subscription_url, subscription_inline, subscription_ids, or node_ids",
		},
		{
			name: "url and ids together",
			request: Request{
				Name: "pool", SubscriptionURL: "https://example.com/sub",
				SourceClass:     "subscription",
				SubscriptionIDs: []string{"sub-9"},
			},
			want: "mutually exclusive",
		},
		{
			name: "url and inline together",
			request: Request{
				Name: "pool", SubscriptionURL: "https://example.com/sub",
				SourceClass:        "subscription",
				SubscriptionInline: "1.2.3.4:8080",
			},
			want: "mutually exclusive",
		},
		{
			name: "inline and ids together",
			request: Request{
				Name: "pool", SubscriptionInline: "1.2.3.4:8080",
				SourceClass:     "subscription",
				SubscriptionIDs: []string{"sub-9"},
			},
			want: "mutually exclusive",
		},
		{
			name:    "whitespace-only inline is not a source",
			request: Request{Name: "pool", SubscriptionInline: "   \n  \n"},
			want:    "provide subscription_url",
		},
		{
			name:    "missing name",
			request: Request{NodeIDs: []string{"node-1", "node-2"}},
			want:    "name is required",
		},
		{
			// One node is a pin, not a group. Quickstart would have to build a
			// group around it, which is the shape the group rule refuses; the
			// error has to point at listener.node_id instead.
			name:    "single node is not a group",
			request: Request{Name: "pool", NodeIDs: []string{"node-1"}},
			want:    "listener.node_id",
		},
	}
	for _, testCase := range cases {
		_, err := service.Create(context.Background(), testCase.request)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: error = %v, want ErrInvalid", testCase.name, err)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s: error = %v, want it to mention %q", testCase.name, err, testCase.want)
		}
	}
}

func TestCreateReusesExistingSubscriptionsWithoutRefreshing(t *testing.T) {
	t.Parallel()

	service, subscriptions, services := newTestService(t)
	if _, err := service.Create(context.Background(), Request{
		Name:            "hk-pool",
		SubscriptionIDs: []string{"sub-existing"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The caller owns the refresh when it supplies ids; refreshing here would
	// duplicate the fetch it already performed.
	if subscriptions.refreshID != "" || len(subscriptions.created) != 0 {
		t.Fatalf("reused-subscription path touched the source: refresh=%q created=%v",
			subscriptions.refreshID, subscriptions.created)
	}
	if got := services.received.SourceSpec.SubscriptionIDs; len(got) != 1 || got[0] != "sub-existing" {
		t.Fatalf("group source subscription ids = %v", got)
	}
}

// The inline path is the one a flat proxy list takes, so it must be a first-class
// source: stored as an Inline subscription (encrypted like any other), refreshed
// before the group is built, and handed to the group by subscription id.
func TestCreateRegistersInlineListAsAnInlineSubscription(t *testing.T) {
	t.Parallel()

	service, subscriptions, services := newTestService(t)
	if _, err := service.Create(context.Background(), Request{
		Name:               "pool",
		SubscriptionInline: "1.2.3.4:8080\n5.6.7.8:3128\n",
		SourceClass:        "pool",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(subscriptions.created) != 1 {
		t.Fatalf("created %d subscriptions, want 1", len(subscriptions.created))
	}
	created := subscriptions.created[0]
	if created.SourceType != subscription.SourceInline {
		t.Fatalf("source type = %q, want inline", created.SourceType)
	}
	if created.SourceConfig.Inline != "1.2.3.4:8080\n5.6.7.8:3128\n" {
		t.Fatalf("inline document = %q", created.SourceConfig.Inline)
	}
	if created.SourceConfig.URL != "" {
		t.Fatalf("inline source also carried a URL: %q", created.SourceConfig.URL)
	}
	// Same ordering guarantee as the remote path: a group built from an
	// unrefreshed subscription would publish a service that routes nothing.
	if subscriptions.refreshID != "sub-1" {
		t.Fatalf("refresh id = %q, want sub-1", subscriptions.refreshID)
	}
	if got := services.received.SourceSpec.SubscriptionIDs; len(got) != 1 || got[0] != "sub-1" {
		t.Fatalf("group source subscription ids = %v, want [sub-1]", got)
	}
}

func TestCreateDefaultsToLoopback(t *testing.T) {
	t.Parallel()

	service, _, services := newTestService(t)
	if _, err := service.Create(context.Background(), Request{
		Name: "pool", NodeIDs: []string{"node-1", "node-2"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Reaching a service from outside must be an explicit decision.
	if services.received.Listener.BindAddress != "127.0.0.1" {
		t.Fatalf("bind address = %q, want loopback", services.received.Listener.BindAddress)
	}
	if services.received.Listener.Kind != "mixed" {
		t.Fatalf("kind = %q, want mixed", services.received.Listener.Kind)
	}
	if services.received.Listener.Port != defaultPort {
		t.Fatalf("port = %d, want %d", services.received.Listener.Port, defaultPort)
	}
}

func proxyGroupStub(id string) proxygroup.Group {
	return proxygroup.Group{ID: id, Name: "hk-pool", Enabled: true}
}
