package metrics

import (
	"fmt"
	"github.com/HengXin666/hx-pxy/internal/hostnetwork"
	"testing"
	"time"
)

func TestConnectionOriginsUseActualBridgePrefixes(t *testing.T) {
	devices := []hostnetwork.Interface{{Kind: "docker", Addresses: []string{"10.88.0.1/16", "fd00:1::1/64"}}, {Kind: "host", Addresses: []string{"192.168.1.10/24"}}}
	for source, want := range map[string]string{"127.0.0.1": "local", "::1": "local", "::ffff:127.0.0.1": "local", "10.88.0.1": "local", "192.168.1.10": "local", "10.88.0.8": "docker", "fd00:1::8": "docker", "172.17.0.2": "external", "192.168.1.11": "external", "1.1.1.1": "external", "": "unknown"} {
		if got := classifyOrigin(source, devices); got != want {
			t.Errorf("%s: %s, want %s", source, got, want)
		}
	}
}

func TestActiveConnectionDetailsBoundedAndCopySafe(t *testing.T) {
	snapshot := RuntimeSnapshot{}
	for index := 510; index > 0; index-- {
		snapshot.Connections = append(snapshot.Connections, Connection{ID: fmt.Sprintf("%04d", index)})
	}
	service := &Service{connections: connectionDetails(snapshot, time.Now())}
	value := service.ActiveConnections()
	if value.Total != 510 || len(value.Items) != 500 || value.Items[0].ID != "0001" {
		t.Fatalf("bad selection: %d/%d", value.Total, len(value.Items))
	}
	value.Items[0].ID = "mutated"
	if service.ActiveConnections().Items[0].ID != "0001" {
		t.Fatal("caller mutated cached sample")
	}
}
