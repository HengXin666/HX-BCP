package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

type benchmarkCompilerRepository struct{ nodes []store.NodeConfigRecord }

func (repository benchmarkCompilerRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return nil, nil
}
func (repository benchmarkCompilerRepository) ListListeners(context.Context) ([]store.ListenerRecord, error) {
	return nil, nil
}
func (repository benchmarkCompilerRepository) ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error) {
	return repository.nodes, nil
}
func (repository benchmarkCompilerRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return nil, nil
}
func (benchmarkCompilerRepository) ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error) {
	return nil, nil
}
func (benchmarkCompilerRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (benchmarkCompilerRepository) GetMetadata(context.Context, string) (string, error) {
	return "", store.ErrNotFound
}

type benchmarkCipher struct{}

func (benchmarkCipher) Open(content, _ []byte) ([]byte, error) { return content, nil }

func BenchmarkCompile10000Nodes(b *testing.B) {
	nodes := make([]store.NodeConfigRecord, 10_000)
	for index := range nodes {
		fingerprint := fmt.Sprintf("%064x", index+1)
		canonical, err := json.Marshal(map[string]any{
			"type": "vless", "server": fmt.Sprintf("node-%05d.example.com", index), "port": 443,
			"uuid": fmt.Sprintf("11111111-1111-1111-1111-%012d", index), "tls": true,
		})
		if err != nil {
			b.Fatal(err)
		}
		nodes[index] = store.NodeConfigRecord{ID: fmt.Sprintf("node-%05d", index), Fingerprint: fingerprint, DisplayName: fmt.Sprintf("Node %05d", index), Protocol: "vless", LifecycleState: "healthy", CanonicalConfigEncrypted: canonical}
	}
	compiler, err := NewCompiler(benchmarkCompilerRepository{nodes: nodes}, benchmarkCipher{})
	if err != nil {
		b.Fatal(err)
	}
	compiler.setControllerSocket("/tmp/hx-benchmark.sock")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		compiled, err := compiler.Compile(context.Background())
		if err != nil || compiled.ProxyCount != 10_000 {
			b.Fatalf("Compile() proxies = %d, error = %v", compiled.ProxyCount, err)
		}
	}
}
