package mihomo

import (
	"context"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

type dockerEntryRepository struct {
	chainRepository
	links []store.DockerAccessEntryRecord
}

func (r dockerEntryRepository) ListDockerAccessEntries(context.Context) ([]store.DockerAccessEntryRecord, error) {
	return r.links, nil
}

func TestGateDockerEntriesSkipsCompanionsThatMustNotServe(t *testing.T) {
	listeners := []store.ListenerRecord{
		{ID: "src-on", Enabled: true}, {ID: "src-off", Enabled: false},
		{ID: "entry-ok", BindAddress: "172.17.0.1", Enabled: true},
		{ID: "entry-src-off", BindAddress: "172.17.0.1", Enabled: true},
		{ID: "entry-gone-bridge", BindAddress: "172.30.0.1", Enabled: true},
		{ID: "entry-shared", BindAddress: "0.0.0.0", Enabled: true},
		{ID: "unrelated", BindAddress: "172.30.0.1", Enabled: true},
	}
	repository := dockerEntryRepository{links: []store.DockerAccessEntryRecord{
		{ListenerID: "src-on", EntryListenerID: "entry-ok", BindTarget: "172.17.0.1"},
		{ListenerID: "src-off", EntryListenerID: "entry-src-off", BindTarget: "172.17.0.1"},
		{ListenerID: "src-on", EntryListenerID: "entry-gone-bridge", BindTarget: "172.30.0.1"},
		{ListenerID: "src-on", EntryListenerID: "entry-shared", BindTarget: "shared"},
	}}
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	compiler.hostAddressPresent = func(address string) bool { return address == "172.17.0.1" }
	kept, withheld, err := compiler.gateDockerEntries(context.Background(), listeners)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, record := range kept {
		got[record.ID] = true
	}
	for _, id := range []string{"src-on", "src-off", "entry-ok", "entry-shared", "unrelated"} {
		if !got[id] {
			t.Errorf("%s must still compile", id)
		}
	}
	for _, id := range []string{"entry-src-off", "entry-gone-bridge"} {
		if got[id] {
			t.Errorf("%s must be gated out", id)
		}
	}
	if len(withheld) != 2 ||
		withheld[0] != (WithheldEntry{ListenerID: "entry-src-off", Reason: WithheldSourceDisabled}) ||
		withheld[1] != (WithheldEntry{ListenerID: "entry-gone-bridge", BindAddress: "172.30.0.1", Reason: WithheldBridgeAbsent}) {
		t.Fatalf("withheld = %+v", withheld)
	}
}
