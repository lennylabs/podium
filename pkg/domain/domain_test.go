package domain

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
)

// Spec: §4.5.4 DOMAIN.md across layers — description and body are
// last-layer-wins; include and exclude append-unique; unlisted
// most-restrictive (true wins).
// Phase: 8
func TestMergeAcrossLayers_LastWinsAndAppend(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	low := &manifest.Domain{
		Description: "low",
		Body:        "low body",
		Include:     []string{"a/*", "b/*"},
	}
	high := &manifest.Domain{
		Description: "high",
		Body:        "high body",
		Include:     []string{"b/*", "c/*"},
		Exclude:     []string{"d/**"},
		Unlisted:    true,
	}
	merged := MergeAcrossLayers([]*manifest.Domain{low, high})
	if merged.Description != "high" {
		t.Errorf("Description = %q, want high", merged.Description)
	}
	if merged.Body != "high body" {
		t.Errorf("Body = %q", merged.Body)
	}
	if len(merged.Include) != 3 {
		t.Errorf("Include = %v, want 3 unique entries", merged.Include)
	}
	if !merged.Unlisted {
		t.Errorf("Unlisted = false, want true")
	}
}

// Spec: §4.5.4 — discovery.max_depth and notable_count merge as
// most-restrictive (lowest non-zero value wins).
// Phase: 8
func TestMergeAcrossLayers_DiscoveryMostRestrictive(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	low := &manifest.Domain{Discovery: &manifest.DomainDiscovery{MaxDepth: 5, NotableCount: 8}}
	high := &manifest.Domain{Discovery: &manifest.DomainDiscovery{MaxDepth: 3, NotableCount: 12}}
	merged := MergeAcrossLayers([]*manifest.Domain{low, high})
	if merged.Discovery.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want 3 (lower wins)", merged.Discovery.MaxDepth)
	}
	if merged.Discovery.NotableCount != 8 {
		t.Errorf("NotableCount = %d, want 8 (lower wins)", merged.Discovery.NotableCount)
	}
}

// Spec: §4.5.4 — fold_below_artifacts merges as highest-wins
// (most-restrictive-wins, opposite direction).
// Phase: 8
func TestMergeAcrossLayers_FoldBelowHighestWins(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	low := &manifest.Domain{Discovery: &manifest.DomainDiscovery{FoldBelowArtifacts: 3}}
	high := &manifest.Domain{Discovery: &manifest.DomainDiscovery{FoldBelowArtifacts: 7}}
	merged := MergeAcrossLayers([]*manifest.Domain{low, high})
	if merged.Discovery.FoldBelowArtifacts != 7 {
		t.Errorf("FoldBelowArtifacts = %d, want 7", merged.Discovery.FoldBelowArtifacts)
	}
}

// Spec: §4.5.5 — DefaultRender returns the registry-wide defaults.
// Phase: 8
func TestDefaultRender(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	r := DefaultRender()
	if r.MaxDepth != 3 || r.NotableCount != 10 || r.TargetResponseTokens != 4000 {
		t.Errorf("got %+v", r)
	}
}
