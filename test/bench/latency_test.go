// Package bench holds the §7.1 latency benchmarks. The numbers
// are not enforced in CI today; the goal of the suite is to give
// operators a reproducible measurement of registry meta-tool
// throughput on representative input sizes.
//
// Run via: go test -bench=. -benchmem ./test/bench/
package bench

import (
	"context"
	"fmt"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

const benchTenant = "bench"

// seedRegistry returns a registry pre-populated with N manifests.
// Tags / descriptions are spread so search hits a non-trivial set
// of candidate matches.
func seedRegistry(b *testing.B, n int) *core.Registry {
	b.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: benchTenant}); err != nil {
		b.Fatalf("CreateTenant: %v", err)
	}
	for i := 0; i < n; i++ {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID:    benchTenant,
			ArtifactID:  fmt.Sprintf("dom%d/artifact-%d", i%32, i),
			Version:     "1.0.0",
			ContentHash: fmt.Sprintf("sha256:%064d", i),
			Type:        "skill",
			Description: fmt.Sprintf("Artifact %d about variance, p&l, ledgers", i),
			Tags:        []string{"q4", "finance", fmt.Sprintf("dom-%d", i%32)},
			Layer:       "team-shared",
		})
	}
	reg := core.New(st, benchTenant, []layer.Layer{
		{ID: "team-shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	return reg
}

// Spec: §7.1 search_artifacts latency target. This benchmark gives
// a reproducible reading; the live SLO depends on the deployment
// (BM25-only vs hybrid retrieval).
func BenchmarkSearchArtifacts(b *testing.B) {
	for _, size := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			reg := seedRegistry(b, size)
			id := layer.Identity{IsPublic: true}
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := reg.SearchArtifacts(ctx, id, core.SearchArtifactsOptions{
					Query: "variance",
					TopK:  10,
				})
				if err != nil {
					b.Fatalf("search: %v", err)
				}
			}
		})
	}
}

// Spec: §7.1 load_artifact latency target.
func BenchmarkLoadArtifact(b *testing.B) {
	reg := seedRegistry(b, 1000)
	id := layer.Identity{IsPublic: true}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := reg.LoadArtifact(ctx, id, "dom0/artifact-0", core.LoadArtifactOptions{})
		if err != nil {
			b.Fatalf("load: %v", err)
		}
	}
}

// Spec: §7.1 load_domain latency target.
func BenchmarkLoadDomain(b *testing.B) {
	reg := seedRegistry(b, 1000)
	id := layer.Identity{IsPublic: true}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := reg.LoadDomain(ctx, id, "dom0", core.LoadDomainOptions{})
		if err != nil {
			b.Fatalf("load: %v", err)
		}
	}
}
