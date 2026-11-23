package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/proxyservice"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type noopReconciler struct{}

func (noopReconciler) Apply(context.Context) error { return nil }

// TestProxyGroupCreateRejectsOneNodeWithRemedy is the contract the caller
// actually sees. The rule lives in the domain service, but a rejection that
// arrives as an opaque 422 would just send the caller looking for another
// field; the response has to carry the remedy, which is listener.node_id.
func TestProxyGroupCreateRejectsOneNodeWithRemedy(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "api-single-node.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	nodeID, err := database.UpsertResidentialSessionNode(ctx, "channel-1", store.ResidentialSessionNode{
		ID: "node-a", Fingerprint: "fp-node-a", DisplayName: "node a",
		Protocol: "http", CanonicalConfigEncrypted: []byte{0x01},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	groups, err := proxygroup.NewService(database, noopReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithProxyGroups(groups))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	// allow_empty used to be advertised as an exemption for a one-node group;
	// it only makes a zero-member group dormant. Both spellings are refused.
	bodies := []string{
		`{"name":"solo-hk","strategy":"manual","source_spec":{"node_ids":["` + nodeID + `"]}}`,
		`{"name":"solo-hk-empty","strategy":"manual","source_spec":{"node_ids":["` + nodeID + `"],"allow_empty":true}}`,
	}
	for _, body := range bodies {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/proxy-groups", strings.NewReader(body)))

		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body = %s", recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
		}
		if payload.Error.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", payload.Error.Code)
		}
		for _, want := range []string{"代理组节点必须 > 1", "listener.node_id"} {
			if !strings.Contains(payload.Error.Message, want) {
				t.Fatalf("message = %q, want it to mention %q", payload.Error.Message, want)
			}
		}
	}
	// Nothing may be written by a rejected create.
	records, err := database.ListProxyGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("rejected create left %d group(s) behind", len(records))
	}
}

// TestProxyServiceCreatePinnedNodeBuildsNoGroup walks the remedy the one-node
// rejection points at, end to end through HTTP: listener.node_id must publish a
// service and leave the group count unchanged.
func TestProxyServiceCreatePinnedNodeBuildsNoGroup(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "pinned-service.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	nodeID, err := database.UpsertResidentialSessionNode(ctx, "channel-1", store.ResidentialSessionNode{
		ID: "node-a", Fingerprint: "fp-node-a", DisplayName: "node a",
		Protocol: "http", CanonicalConfigEncrypted: []byte{0x01},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	groups, err := proxygroup.NewService(database, noopReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	listeners, err := listener.NewService(database, box, noopReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	// A pinned listener row still needs a live anchor group (the column is NOT
	// NULL and references proxy_groups). This is not "a group for the node": it
	// carries no traffic for the pinned service. A DIRECT-only group is the
	// cheapest legal anchor.
	if _, err := groups.Create(ctx, proxygroup.CreateRequest{
		Name: "anchor", Strategy: "manual",
		SourceSpec: proxygroup.SourceSpec{IncludeDirect: true},
	}); err != nil {
		t.Fatal(err)
	}
	services, err := proxyservice.NewService(groups, listeners, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithProxyGroups(groups), WithListeners(listeners), WithProxyServices(services))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"listener":{"name":"solo","kind":"mixed","bind_address":"127.0.0.1","port":7890,` +
		`"auth":{"username":"hx-solo","password":"hx-solo-pass"},"node_id":"` + nodeID + `"}}`
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/proxy-services", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Listener struct {
			NodeID string `json:"node_id"`
		} `json:"listener"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Listener.NodeID != nodeID {
		t.Fatalf("listener node_id = %q, want %q", payload.Listener.NodeID, nodeID)
	}
	records, err := database.ListProxyGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Name != "anchor" {
		t.Fatalf("groups after a pinned create = %+v, want only the pre-existing anchor", records)
	}
}

// TestCapabilityCatalogStatesTheGroupNodeDistinction keeps the machine-readable
// contract from assuming the caller already knows it. Measured 2026-10-18: an
// agent read GET /proxy-groups, put one of those ids into source_spec.node_ids,
// and was answered "a group needs more than one node" — because nothing on the
// wire said the two id spaces are different.
func TestCapabilityCatalogStatesTheGroupNodeDistinction(t *testing.T) {
	catalog := newCapabilityServer(t).CapabilityCatalog()
	joined := strings.Join(catalog.ObjectModel, "\n")
	for _, want := range []string{"node-…", "group-…", "listener.node_id", "source_spec.node_ids", "separate id spaces"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("object_model does not mention %q:\n%s", want, joined)
		}
	}
	// The distinction must be stated once, not re-worded per endpoint: four
	// copies would drift into four different stories.
	if len(catalog.ObjectModel) < 3 {
		t.Fatalf("object_model has %d entries, want the four distinctions spelled out", len(catalog.ObjectModel))
	}
}
