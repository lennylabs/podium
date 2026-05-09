package core

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// DomainAnalysis is the §4.5.5 analyze report for one subtree. The
// fields are intentionally simple so the operator can read the
// JSON and grep for problems; structured tools consume the same
// shape.
type DomainAnalysis struct {
	Path                   string                       `json:"path"`
	ArtifactCount          int                          `json:"artifact_count"`
	RecursiveCount         int                          `json:"recursive_count"`
	ChildCount             int                          `json:"child_count"`
	PassthroughChainLength int                          `json:"passthrough_chain_length"`
	TagClusterEntropy      float64                      `json:"tag_cluster_entropy"`
	FoldCandidates         []DomainAnalysisCandidate    `json:"fold_candidates,omitempty"`
	SplitCandidates        []DomainAnalysisCandidate    `json:"split_candidates,omitempty"`
	Children               []DomainAnalysisChild        `json:"children,omitempty"`
}

// DomainAnalysisCandidate names a subdomain that the analyzer
// flags as a candidate for one of the §4.5.5 reshape operations.
type DomainAnalysisCandidate struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// DomainAnalysisChild is the per-immediate-child summary the
// analyzer surfaces. Operators eyeball these to decide where to
// rebalance the tree.
type DomainAnalysisChild struct {
	Path           string `json:"path"`
	RecursiveCount int    `json:"recursive_count"`
}

// AnalyzeDomain walks the subtree at path and returns the §4.5.5
// metrics for it. Visibility filtering applies (the analyzer
// respects the caller's effective view); a tenant-scoped admin
// typically calls this with their own admin identity.
//
// Thresholds:
//   - fold candidate: subdomain with recursive_count <= 1.
//   - split candidate: subdomain with recursive_count >= 20 and
//     tag entropy >= 0.7.
// These are §4.5.5's documented heuristics; the registry-wide
// `discovery:` config can override them later.
func (r *Registry) AnalyzeDomain(ctx context.Context, id layer.Identity, path string) (*DomainAnalysis, error) {
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	under := manifestsUnder(visible, path)
	direct := directArtifactsOf(under, path)
	groups := groupByImmediateChild(under, path)

	a := &DomainAnalysis{
		Path:           path,
		ArtifactCount:  len(direct),
		RecursiveCount: len(under),
		ChildCount:     len(groups),
	}

	if path != "" {
		a.PassthroughChainLength = passthroughChainDepth(under, path)
	}

	a.TagClusterEntropy = tagEntropy(under)

	childNames := make([]string, 0, len(groups))
	for k := range groups {
		childNames = append(childNames, k)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		childPath := joinPath(path, name)
		recursive := len(groups[name])
		a.Children = append(a.Children, DomainAnalysisChild{
			Path: childPath, RecursiveCount: recursive,
		})
		if recursive <= 1 {
			a.FoldCandidates = append(a.FoldCandidates, DomainAnalysisCandidate{
				Path:   childPath,
				Reason: "recursive_count <= 1",
			})
		}
		if recursive >= 20 {
			childEntropy := tagEntropy(groups[name])
			if childEntropy >= 0.7 {
				a.SplitCandidates = append(a.SplitCandidates, DomainAnalysisCandidate{
					Path:   childPath,
					Reason: "recursive_count >= 20 and tag entropy >= 0.7",
				})
			}
		}
	}
	return a, nil
}

// passthroughChainDepth measures how far the analyzer can descend
// from path through single-child intermediates with no direct
// artifacts. Returns 0 when path has multiple children or has
// direct artifacts at the current level.
func passthroughChainDepth(under []store.ManifestRecord, path string) int {
	depth := 0
	current := path
	for {
		if len(directArtifactsOf(under, current)) > 0 {
			return depth
		}
		groups := groupByImmediateChild(under, current)
		if len(groups) != 1 {
			return depth
		}
		var only string
		for k := range groups {
			only = k
		}
		current = joinPath(current, only)
		depth++
	}
}

// tagEntropy computes Shannon entropy (base 2) over the tag
// distribution across the supplied manifests. Returns 0 for an
// empty input or one with a single dominant tag.
func tagEntropy(records []store.ManifestRecord) float64 {
	counts := map[string]int{}
	total := 0
	for _, m := range records {
		for _, t := range m.Tags {
			counts[strings.ToLower(t)]++
			total++
		}
	}
	if total == 0 {
		return 0
	}
	h := 0.0
	for _, c := range counts {
		p := float64(c) / float64(total)
		if p > 0 {
			h -= p * math.Log2(p)
		}
	}
	return h
}
