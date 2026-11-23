package proxygroup

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// channelStore is the real store plus one residential channel owning a group.
type channelStore struct {
	*store.Store
	residentialGroupID string
}

func (s channelStore) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return []store.ResidentialChannelRecord{{ID: "channel", ProxyGroupID: s.residentialGroupID}}, nil
}

func TestGroupTypeSeparatesResidentialSubscriptionAndCustom(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := channelStore{Store: database}
	service, err := NewService(repository, &testReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	residential, err := service.Create(ctx, CreateRequest{Name: "resi", Strategy: "manual", SourceSpec: SourceSpec{AllowEmpty: true}, EmptyBehavior: "fail-closed"})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := service.Create(ctx, CreateRequest{Name: "local-out", Strategy: "manual", SourceSpec: SourceSpec{IncludeDirect: true}})
	if err != nil {
		t.Fatal(err)
	}
	repository.residentialGroupID = residential.ID
	service.repository = repository
	groups, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, group := range groups {
		types[group.ID] = group.Type
	}
	if types[residential.ID] != TypeResidential || types[direct.ID] != TypeCustom {
		t.Fatalf("types = %+v", types)
	}
	got, err := service.Get(ctx, residential.ID)
	if err != nil || got.Type != TypeResidential {
		t.Fatalf("Get type = %q err = %v", got.Type, err)
	}
	if typeFromSpec(SourceSpec{SubscriptionIDs: []string{"s"}}) != TypeSubscription {
		t.Fatal("a subscription-sourced group must be typed subscription")
	}
}

func TestParseTypeFilter(t *testing.T) {
	if filter, err := ParseTypeFilter(""); err != nil || filter != nil {
		t.Fatalf("empty filter = %v %v", filter, err)
	}
	filter, err := ParseTypeFilter("residential, Subscription")
	if err != nil || !filter[TypeResidential] || !filter[TypeSubscription] || filter[TypeCustom] {
		t.Fatalf("filter = %v err = %v", filter, err)
	}
	if _, err := ParseTypeFilter("住宅"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown type err = %v", err)
	}
}
