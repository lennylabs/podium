package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// freshRegistry returns a registry with `n` artifacts directly under
// the empty path so the notable list has plenty of entries to cap.
func freshRegistry(t *testing.T, n int) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for i := 0; i < n; i++ {
		// Create varied IDs alphabetically: a, b, c, ..., z, aa, ab, ...
		id := fmtID(i)
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "context",
			Layer: "L",
		}); err != nil {
			t.Fatalf("PutManifest %d: %v", i, err)
		}
	}
	return core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
}

func fmtID(i int) string {
	if i < 26 {
		return string(rune('a' + i))
	}
	return string(rune('a'+(i/26-1))) + string(rune('a'+(i%26)))
}

// Spec: §4.5.5 — notable list is capped at notable_count; default 10.
// The cap itself is not surfaced in the rendering note: notes cover
// only budget tightening and depth-cap cases per §4.5.5.
func TestLoadDomain_NotableCountDefault(t *testing.T) {
	t.Parallel()
	reg := freshRegistry(t, 25)
	res, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Notable) != core.DefaultNotableCount {
		t.Errorf("len(Notable) = %d, want %d", len(res.Notable), core.DefaultNotableCount)
	}
	if res.Note != "" {
		t.Errorf("Note should be empty for cap-to-default; got %q", res.Note)
	}
}

// Spec: §4.5.5 — caller-supplied notable_count overrides the default.
func TestLoadDomain_NotableCountCallerOverride(t *testing.T) {
	t.Parallel()
	reg := freshRegistry(t, 25)
	res, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{
		NotableCount: 3,
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Notable) != 3 {
		t.Errorf("len(Notable) = %d, want 3", len(res.Notable))
	}
}

// Spec: §4.5.5 — featured artifacts surface first in the notable list,
// in author-supplied order; the rest fill in alphabetically.
func TestLoadDomain_FeaturedSurfacesFirst(t *testing.T) {
	t.Parallel()
	reg := freshRegistry(t, 5)
	res, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{
		Featured: []string{"d", "b"},
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	wantOrder := []string{"d", "b", "a", "c", "e"}
	if len(res.Notable) != len(wantOrder) {
		t.Fatalf("got %d notable, want %d", len(res.Notable), len(wantOrder))
	}
	for i, want := range wantOrder {
		if res.Notable[i].ID != want {
			t.Errorf("Notable[%d] = %q, want %q", i, res.Notable[i].ID, want)
		}
	}
}

// Spec: §4.5.5 — depth above the resolved ceiling is capped silently
// and surfaced in the rendering note.
func TestLoadDomain_DepthAboveCeilingNoted(t *testing.T) {
	t.Parallel()
	reg := freshRegistry(t, 1)
	res, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{
		Depth: 99,
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if !strings.Contains(res.Note, "capped") {
		t.Errorf("Note should mention capping, got %q", res.Note)
	}
}

// Spec: §4.5.5 — a featured ID that does not match any visible
// artifact is dropped silently; the remaining notable list is
// alphabetical.
func TestLoadDomain_FeaturedUnknownDropped(t *testing.T) {
	t.Parallel()
	reg := freshRegistry(t, 3)
	res, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{
		Featured: []string{"does-not-exist", "b"},
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	wantOrder := []string{"b", "a", "c"}
	for i, want := range wantOrder {
		if res.Notable[i].ID != want {
			t.Errorf("Notable[%d] = %q, want %q", i, res.Notable[i].ID, want)
		}
	}
}

// nestedRegistry creates a registry under finance with a sparse
// risk subdomain (1 artifact), a sparse close-reporting subdomain
// (1 artifact), and a populated cap-markets subdomain (3 artifacts).
// Used by the fold_below_artifacts tests.
func nestedRegistry(t *testing.T) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	put := func(id string) {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Layer: "L",
		}); err != nil {
			t.Fatalf("PutManifest %s: %v", id, err)
		}
	}
	put("finance/close-reporting/report-runner")
	put("finance/risk/cohort-detector")
	put("finance/cap-markets/alpha")
	put("finance/cap-markets/beta")
	put("finance/cap-markets/gamma")
	return core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}},
	})
}

// Spec: §4.5.5 — fold_below_artifacts collapses a sparse subdomain
// into the parent's leaf set; folded artifacts surface in Notable
// with a folded_from annotation.
func TestLoadDomain_FoldBelowArtifacts(t *testing.T) {
	t.Parallel()
	reg := nestedRegistry(t)
	res, err := reg.LoadDomain(context.Background(), publicID, "finance", core.LoadDomainOptions{
		FoldBelowArtifacts: 2,
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	subPaths := []string{}
	for _, s := range res.Subdomains {
		subPaths = append(subPaths, s.Path)
	}
	if len(subPaths) != 1 || subPaths[0] != "finance/cap-markets" {
		t.Errorf("Subdomains = %v, want only finance/cap-markets", subPaths)
	}
	notableIDs := map[string]string{}
	for _, n := range res.Notable {
		notableIDs[n.ID] = n.FoldedFrom
	}
	if folded, ok := notableIDs["finance/close-reporting/report-runner"]; !ok {
		t.Errorf("expected report-runner folded into Notable, got %v", notableIDs)
	} else if folded != "close-reporting" {
		t.Errorf("FoldedFrom = %q, want %q", folded, "close-reporting")
	}
	if folded, ok := notableIDs["finance/risk/cohort-detector"]; !ok {
		t.Errorf("expected cohort-detector folded into Notable, got %v", notableIDs)
	} else if folded != "risk" {
		t.Errorf("FoldedFrom = %q, want %q", folded, "risk")
	}
}

// Spec: §4.5.5 — fold_passthrough_chains collapses single-child
// intermediates so the immediate-children list shows the deepest
// non-passthrough descendant directly. Canonical IDs are preserved.
func TestLoadDomain_FoldPassthroughChains(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	for _, leaf := range []string{"x", "y"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: "a/b/c/d/" + leaf,
			Version: "1.0.0", ContentHash: "sha256:" + leaf, Type: "skill", Layer: "L",
		})
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}},
	})
	tru := true
	res, err := reg.LoadDomain(context.Background(), publicID, "a", core.LoadDomainOptions{
		FoldPassthroughChains: &tru,
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Subdomains) != 1 || res.Subdomains[0].Path != "a/b/c/d" {
		t.Errorf("Subdomains = %+v, want one entry a/b/c/d", res.Subdomains)
	}
}

// Spec: §4.5.5 — disabling fold_passthrough_chains preserves the
// directory hierarchy in the rendered tree.
func TestLoadDomain_FoldPassthroughDisabled(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	for _, leaf := range []string{"x", "y"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: "a/b/c/d/" + leaf,
			Version: "1.0.0", ContentHash: "sha256:" + leaf, Type: "skill", Layer: "L",
		})
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}},
	})
	off := false
	res, err := reg.LoadDomain(context.Background(), publicID, "a", core.LoadDomainOptions{
		FoldPassthroughChains: &off,
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Subdomains) != 1 || res.Subdomains[0].Path != "a/b" {
		t.Errorf("Subdomains = %+v, want a/b without passthrough collapse", res.Subdomains)
	}
}

// Spec: §4.5.5 — target_response_tokens tightens the notable list
// to fit a soft budget; the rendering note describes the reduction.
func TestLoadDomain_TargetResponseTokensTightens(t *testing.T) {
	t.Parallel()
	reg := freshRegistry(t, 25)
	res, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{
		TargetResponseTokens: 30,
	})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Notable) >= core.DefaultNotableCount {
		t.Errorf("len(Notable) = %d, expected tightening below default", len(res.Notable))
	}
	if !strings.Contains(res.Note, "reduced") {
		t.Errorf("Note should mention reduction, got %q", res.Note)
	}
}
