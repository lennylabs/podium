// Package layer implements the LayerComposer (spec §4.6) plus the
// visibility evaluator that runs before composition.
//
// Phase 7 wires the composer into the registry HTTP handlers; Phase 8
// adds DOMAIN.md-driven domain composition on top.
package layer

import (
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// Visibility declares who can see a layer (§4.6).
type Visibility struct {
	Public       bool
	Organization bool
	Groups       []string
	Users        []string
}

// Identity is the OAuth-attested caller (§6.3).
type Identity struct {
	Sub          string
	Email        string
	OrgID        string
	Groups       []string
	IsPublic     bool // true in public-mode (§13.10)
	IsAuthenticated bool
}

// Layer is a composed-view layer with its source visibility and
// precedence position.
type Layer struct {
	ID         string
	Visibility Visibility
	Precedence int
}

// GroupResolver expands a layer's `groups:` filter into the set of
// userNames that belong to the named group. The §6.3.1 SCIM
// receiver wires its `Store.MembersOf` here so SCIM-pushed group
// memberships become first-class in the visibility evaluator. A
// nil resolver short-circuits to the JWT-only path so callers
// without SCIM keep the prior behavior.
type GroupResolver func(group string) []string

// Visible reports whether identity can see layer per §4.6 union semantics.
//
// In public-mode (Identity.IsPublic), every layer is visible regardless
// of declared visibility.
func Visible(layer Layer, id Identity) bool {
	return VisibleWith(layer, id, nil)
}

// VisibleWith is the §4.6 visibility evaluator with the §6.3.1 SCIM
// integration seam: a non-nil resolver expands `groups:` filters
// against an external group-membership store before falling back to
// the caller's JWT groups. Used by the registry's HTTP handlers to
// honor SCIM-pushed memberships per Phase 7's gap callout.
func VisibleWith(layer Layer, id Identity, resolveGroup GroupResolver) bool {
	if id.IsPublic {
		return true
	}
	v := layer.Visibility
	if v.Public {
		return true
	}
	if v.Organization && id.IsAuthenticated {
		return true
	}
	for _, g := range v.Groups {
		for _, ug := range id.Groups {
			if g == ug {
				return true
			}
		}
		if resolveGroup != nil {
			for _, member := range resolveGroup(g) {
				if member == id.Sub || member == id.Email {
					return true
				}
			}
		}
	}
	for _, u := range v.Users {
		if u == id.Sub {
			return true
		}
	}
	return false
}

// EffectiveLayers returns the subset of layers visible to identity, in
// precedence order (lowest first per §4.6).
func EffectiveLayers(layers []Layer, id Identity) []Layer {
	return EffectiveLayersWith(layers, id, nil)
}

// EffectiveLayersWith is the SCIM-aware companion to EffectiveLayers.
func EffectiveLayersWith(layers []Layer, id Identity, resolveGroup GroupResolver) []Layer {
	out := make([]Layer, 0, len(layers))
	for _, l := range layers {
		if VisibleWith(l, id, resolveGroup) {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Precedence < out[j].Precedence
	})
	return out
}

// Compose merges artifacts across layers under the §4.6 rules:
//   - Higher-precedence layers override lower on collision.
//   - Without `extends:`, raw ingest treats collisions as errors;
//     the caller's effective view (this function) treats them as
//     highest-wins.
//   - With `extends:`, fields merge per the field-semantics table.
//
// Phase 7 ships highest-wins; Phase 8 layers on extends: resolution.
func Compose(layers []Layer, candidates map[string][]Candidate) []Composed {
	if len(layers) == 0 || len(candidates) == 0 {
		return nil
	}
	precedence := map[string]int{}
	for _, l := range layers {
		precedence[l.ID] = l.Precedence
	}
	out := []Composed{}
	for id, list := range candidates {
		var winner Candidate
		bestPrec := -1
		for _, c := range list {
			p, ok := precedence[c.LayerID]
			if !ok {
				continue
			}
			if p > bestPrec {
				bestPrec = p
				winner = c
			}
		}
		if bestPrec < 0 {
			continue
		}
		out = append(out, Composed{
			ID:       id,
			LayerID:  winner.LayerID,
			Artifact: winner.Artifact,
			Skill:    winner.Skill,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Candidate is one (layer, artifact) pair under consideration during
// composition.
type Candidate struct {
	LayerID  string
	Artifact *manifest.Artifact
	Skill    *manifest.Skill
}

// Composed is the result of merging candidates for a canonical artifact ID.
type Composed struct {
	ID       string
	LayerID  string
	Artifact *manifest.Artifact
	Skill    *manifest.Skill
}

// MostRestrictiveSensitivity returns the highest sensitivity among the
// candidates' artifacts (§4.6 merge semantics: most-restrictive-wins).
func MostRestrictiveSensitivity(candidates []Candidate) manifest.Sensitivity {
	rank := func(s manifest.Sensitivity) int {
		switch s {
		case manifest.SensitivityHigh:
			return 3
		case manifest.SensitivityMedium:
			return 2
		case manifest.SensitivityLow:
			return 1
		}
		return 0
	}
	best := manifest.Sensitivity("")
	bestRank := 0
	for _, c := range candidates {
		if c.Artifact == nil {
			continue
		}
		r := rank(c.Artifact.Sensitivity)
		if r > bestRank {
			bestRank = r
			best = c.Artifact.Sensitivity
		}
	}
	return best
}

// AppendUniqueTags returns the union of tags across candidates, in
// first-seen order (§4.6 merge semantics for list fields).
func AppendUniqueTags(candidates []Candidate) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		if c.Artifact == nil {
			continue
		}
		for _, tag := range c.Artifact.Tags {
			if seen[tag] {
				continue
			}
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

// SplitArtifactRef splits "id@version" into its components. Empty
// version is the implicit "latest" per §4.7.6.
func SplitArtifactRef(ref string) (id, version string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}
