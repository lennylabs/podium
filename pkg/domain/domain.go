// Package domain implements DOMAIN.md merging, glob resolution, and the
// discovery-rendering rules from spec §4.5.
package domain

import (
	"sort"

	"github.com/lennylabs/podium/pkg/manifest"
)

// MergeAcrossLayers merges DOMAIN.md candidates per §4.5.4:
//   - description and prose body: last-layer-wins.
//   - include / exclude / featured / deprioritize / keywords: append-unique.
//   - unlisted: most-restrictive-wins.
//   - discovery.max_depth / notable_count / target_response_tokens:
//     most-restrictive-wins (lowest value).
//   - discovery.fold_below_artifacts: most-restrictive-wins (highest).
//   - discovery.fold_passthrough_chains: most-restrictive (true > false).
//
// Inputs are ordered lowest-precedence first. Returns a single merged
// Domain ready for rendering.
func MergeAcrossLayers(candidates []*manifest.Domain) *manifest.Domain {
	if len(candidates) == 0 {
		return nil
	}
	out := &manifest.Domain{}
	include := []string{}
	exclude := []string{}
	for _, c := range candidates {
		if c == nil {
			continue
		}
		if c.Description != "" {
			out.Description = c.Description
		}
		if c.Body != "" {
			out.Body = c.Body
		}
		if c.Unlisted {
			out.Unlisted = true
		}
		include = append(include, c.Include...)
		exclude = append(exclude, c.Exclude...)
		mergeDiscovery(out, c.Discovery)
	}
	out.Include = uniqueStrings(include)
	out.Exclude = uniqueStrings(exclude)
	return out
}

func mergeDiscovery(dst *manifest.Domain, src *manifest.DomainDiscovery) {
	if src == nil {
		return
	}
	if dst.Discovery == nil {
		dst.Discovery = &manifest.DomainDiscovery{}
	}
	d := dst.Discovery
	d.MaxDepth = minNonZero(d.MaxDepth, src.MaxDepth)
	d.NotableCount = minNonZero(d.NotableCount, src.NotableCount)
	d.TargetResponseTokens = minNonZero(d.TargetResponseTokens, src.TargetResponseTokens)
	if src.FoldBelowArtifacts > d.FoldBelowArtifacts {
		d.FoldBelowArtifacts = src.FoldBelowArtifacts
	}
	if src.FoldPassthroughChains != nil {
		t := *src.FoldPassthroughChains
		if d.FoldPassthroughChains == nil || t {
			d.FoldPassthroughChains = src.FoldPassthroughChains
		}
	}
	d.Featured = uniqueStrings(append(d.Featured, src.Featured...))
	d.Deprioritize = uniqueStrings(append(d.Deprioritize, src.Deprioritize...))
	d.Keywords = uniqueStrings(append(d.Keywords, src.Keywords...))
}

func minNonZero(a, b int) int {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Render is the simplified Phase 8 placeholder for §4.5.5 discovery
// rendering. Final implementation lands in Phase 8 itself; the surface
// is here so cross-package code can target it.
type Render struct {
	MaxDepth             int
	FoldBelowArtifacts   int
	NotableCount         int
	TargetResponseTokens int
}

// DefaultRender is the registry-wide default per §4.5.5 (max_depth: 3,
// notable_count: 10, target_response_tokens: 4000, fold_below_artifacts: 0).
func DefaultRender() Render {
	return Render{
		MaxDepth:             3,
		FoldBelowArtifacts:   0,
		NotableCount:         10,
		TargetResponseTokens: 4000,
	}
}

// SortDescriptors sorts the descriptors lexicographically by Path so the
// renderer's output is stable across map iteration orders.
func SortDescriptors[T interface{ GetPath() string }](in []T) {
	sort.SliceStable(in, func(i, j int) bool { return in[i].GetPath() < in[j].GetPath() })
}
