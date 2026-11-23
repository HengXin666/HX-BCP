package nodeparse

import (
	"bytes"
	"fmt"
	"testing"
)

func BenchmarkParse10000MihomoNodes(b *testing.B) {
	var source bytes.Buffer
	source.WriteString("payload:\n")
	for index := range 10_000 {
		fmt.Fprintf(&source, "  - {name: node-%05d, type: vless, server: node-%05d.example.com, port: 443, uuid: 11111111-1111-1111-1111-%012d, tls: true}\n", index, index, index)
	}
	content := source.Bytes()
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()
	for range b.N {
		result, err := Parse(content)
		if err != nil || len(result.Nodes) != 10_000 {
			b.Fatalf("Parse() nodes = %d, error = %v", len(result.Nodes), err)
		}
	}
}
