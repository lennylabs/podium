package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
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
// Phase: 8
func TestLoadDomain_NotableCountDefault(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	reg := freshRegistry(t, 25)
	res, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(res.Notable) != core.DefaultNotableCount {
		t.Errorf("len(Notable) = %d, want %d", len(res.Notable), core.DefaultNotableCount)
	}
	if !strings.Contains(res.Note, "truncated") {
		t.Errorf("Note should mention truncation, got %q", res.Note)
	}
}

// Spec: §4.5.5 — caller-supplied notable_count overrides the default.
// Phase: 8
func TestLoadDomain_NotableCountCallerOverride(t *testing.T) {
	testharness.RequirePhase(t, 8)
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
// Phase: 8
func TestLoadDomain_FeaturedSurfacesFirst(t *testing.T) {
	testharness.RequirePhase(t, 8)
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
// Phase: 8
func TestLoadDomain_DepthAboveCeilingNoted(t *testing.T) {
	testharness.RequirePhase(t, 8)
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
// Phase: 8
func TestLoadDomain_FeaturedUnknownDropped(t *testing.T) {
	testharness.RequirePhase(t, 8)
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
