package integration

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §12 — "ingest-time lint flags newly-set unlisted: true for
// review." End-to-end through the SQLite store and the HTTP reingest endpoint:
// flipping a layer's DOMAIN.md from listed to unlisted surfaces the
// lint.domain_newly_unlisted advisory in the §7.3.1 reingest result, while the
// initial listed ingest does not.
func TestNewlyUnlistedAdvisory_ReingestSurfacesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	layerDir := t.TempDir()
	domainPath := filepath.Join(layerDir, "finance", "DOMAIN.md")
	lcaWriteArtifact(t, filepath.Join(layerDir, "finance/ap"),
		"---\ntype: context\nversion: 1.0.0\ndescription: ap\nsensitivity: low\n---\n\nbody\n")
	writeDomain := func(content string) {
		t.Helper()
		if err := os.WriteFile(domainPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write DOMAIN.md: %v", err)
		}
	}
	writeDomain("---\ndescription: Finance\n---\n\n# Finance\n")

	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "finance-layer", SourceType: "local", LocalPath: layerDir, Order: 1,
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}

	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithReingestRunner(localReingestRunner(st, nil))
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	// First reingest: listed — no advisory.
	first := lcaReingest(t, ts.URL, "finance-layer")
	if hasReingestAdvisory(first, "lint.domain_newly_unlisted") {
		t.Fatalf("listed DOMAIN.md flagged as newly-unlisted: %v", first["advisories"])
	}

	// Flip to unlisted and reingest: advisory surfaces.
	writeDomain("---\nunlisted: true\ndescription: Finance\n---\n\n# Finance\n")
	second := lcaReingest(t, ts.URL, "finance-layer")
	if !hasReingestAdvisory(second, "lint.domain_newly_unlisted") {
		t.Fatalf("newly-set unlisted: true not surfaced in reingest advisories: %v", second["advisories"])
	}
}

// hasReingestAdvisory reports whether the reingest response carries an
// advisory with the given code.
func hasReingestAdvisory(resp map[string]any, code string) bool {
	advisories, _ := resp["advisories"].([]any)
	for _, raw := range advisories {
		a, _ := raw.(map[string]any)
		if a["code"] == code {
			return true
		}
	}
	return false
}

// Spec: §12 — "soft cap is configurable." End-to-end through the
// ingest orchestrator and the built-in local source: a per-package soft cap
// lowered via PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES turns a bundled package
// that is well under the 10 MB default into a gating lint failure. t.Setenv
// precludes t.Parallel.
func TestConfigurablePackageCap_GatesIngest(t *testing.T) {
	t.Setenv("PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES", "4096")

	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// One artifact with an 8 KB bundled resource (under the 10 MB default,
	// over the 4 KB configured per-package cap) alongside a clean one, so the
	// gated package surfaces in LintFailures without aborting the whole layer.
	artDir := filepath.Join(t.TempDir(), "layer")
	lcaWriteArtifact(t, filepath.Join(artDir, "finance/big"),
		"---\ntype: context\nversion: 1.0.0\ndescription: big\nsensitivity: low\n---\n\nbody\n")
	if err := os.WriteFile(filepath.Join(artDir, "finance/big", "data.bin"), make([]byte, 8*1024), 0o644); err != nil {
		t.Fatalf("write resource: %v", err)
	}
	lcaWriteArtifact(t, filepath.Join(artDir, "finance/small"),
		"---\ntype: context\nversion: 1.0.0\ndescription: small\nsensitivity: low\n---\n\nbody\n")

	cfg := store.LayerConfig{TenantID: "t", ID: "big-layer", SourceType: "local", LocalPath: artDir, Order: 1}

	res, err := ingest.SourceIngestWithOptions(ctx, st, source.Local{}, cfg, ingest.SourceIngestOptions{
		Linter: lint.NewIngestLinter(true), // reads the lowered cap from the env
	})
	if err != nil {
		t.Fatalf("SourceIngestWithOptions: %v", err)
	}

	gated := false
	for _, f := range res.LintFailures {
		if f.Code == "lint.bundled_resource_size" && f.Severity == lint.SeverityError {
			gated = true
		}
	}
	if !gated {
		t.Fatalf("lowered per-package cap should gate the 8 KB package; failures=%+v accepted=%d", res.LintFailures, res.Accepted)
	}

	// Sanity: with the default cap the same package ingests cleanly.
	st2, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry2.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	if err := st2.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	os.Unsetenv("PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES")
	clean, err := ingest.SourceIngestWithOptions(ctx, st2, source.Local{}, cfg, ingest.SourceIngestOptions{
		Linter: lint.NewIngestLinter(true),
	})
	if err != nil {
		t.Fatalf("SourceIngestWithOptions (default cap): %v", err)
	}
	if len(clean.LintFailures) != 0 {
		t.Errorf("default cap should not gate an 8 KB package: %+v", clean.LintFailures)
	}
}
