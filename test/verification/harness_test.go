// Package verification holds CI-runnable, scaled-down implementations of the
// §11 verification categories the build plan enumerates: performance, soak, and
// chaos. The full-scale targets (1K QPS sustained, 24h soak, real Postgres
// failover) run against a deployed cluster with load-generation and
// fault-injection infrastructure; these tests exercise the same mechanisms in
// process at a scale that completes in CI, so the categories are falsifiable
// rather than aspirational. They guard against gross regressions (errors under
// concurrency, goroutine leaks, panics on backend outage), not SLO numbers.
package verification

import (
	"context"
	"fmt"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

const vTenant = "verify"

// seedRegistry returns an in-process registry pre-populated with n manifests
// spread across domains, tags, and descriptions so search hits a non-trivial
// candidate set.
func seedRegistry(tb testing.TB, st store.Store, n int) *core.Registry {
	tb.Helper()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: vTenant}); err != nil {
		tb.Fatalf("CreateTenant: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := st.PutManifest(ctx, store.ManifestRecord{
			TenantID:    vTenant,
			ArtifactID:  fmt.Sprintf("dom%d/artifact-%d", i%16, i),
			Version:     "1.0.0",
			ContentHash: fmt.Sprintf("sha256:%064d", i),
			Type:        "skill",
			Description: fmt.Sprintf("Artifact %d about variance, ledgers, and reconciliation", i),
			Tags:        []string{"finance", fmt.Sprintf("dom-%d", i%16)},
			Layer:       "shared",
		}); err != nil {
			tb.Fatalf("PutManifest: %v", err)
		}
	}
	return core.New(st, vTenant, []layer.Layer{
		{ID: "shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
}
