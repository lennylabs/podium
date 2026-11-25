// Package layer implements the LayerComposer (spec §4.6) plus the
// visibility evaluator that runs before composition.
package layer

import (
	"slices"
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
	Sub    string
	Email  string
	OrgID  string
	Groups []string
	// Scopes carries the raw "podium:*" OAuth scope claims (§6.3.1). When
	// non-empty they narrow the caller's surface: the visible/loadable set
	// is the intersection of layer visibility and the scope grants, and
	// the smaller surface wins. Empty means no scope narrowing.
	Scopes          []string
	IsPublic        bool // true in public-mode (§13.10)
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
// against an external group-membership store before falling back
// to the caller's JWT groups. Used by the registry's HTTP
// handlers to honor SCIM-pushed memberships.
//
// Only `public: true` and the bypasses carried by Identity.IsPublic admit an
// unauthenticated caller. The `organization`, `groups`, and `users` fields
// admit an authenticated caller alone, and a `groups` or `users` member admits
// it only through a non-empty value. An anonymous caller carries an empty
// subject, so without both guards an empty `users` entry, or an empty member
// name a group resolver returns, would equal that subject and admit every
// request that carries no credential.
//
// Spec: §4.6, §6.3.3
func VisibleWith(layer Layer, id Identity, resolveGroup GroupResolver) bool {
	if id.IsPublic {
		return true
	}
	v := layer.Visibility
	if v.Public {
		return true
	}
	// Spec: §6.3.3 — a request carrying no credential, or a trusted-headers
	// request without identity headers, is anonymous and sees public
	// visibility only.
	if !id.IsAuthenticated {
		return false
	}
	if v.Organization {
		return true
	}
	return inGroups(v.Groups, id, resolveGroup) || namesCaller(v.Users, id)
}

// inGroups reports whether the authenticated caller id belongs to one of the
// non-empty groups, either through the groups its credential carries or
// through the members resolveGroup returns for the group. An empty group name
// names no group, so it is skipped before either check.
func inGroups(groups []string, id Identity, resolveGroup GroupResolver) bool {
	for _, g := range groups {
		if g == "" {
			continue
		}
		if slices.Contains(id.Groups, g) {
			return true
		}
		if resolveGroup != nil && namesCaller(resolveGroup(g), id) {
			return true
		}
	}
	return false
}

// namesCaller reports whether one of the non-empty values equals the caller's
// non-empty subject or non-empty email.
//
// spec: §4.6 — a `users:` entry is matched against the caller's OIDC subject
// or email, and the section's config example uses email-style identifiers
// (users: [alice@acme.com]). IdPs often carry the human identifier in Email
// while Sub is opaque (auth0|abc123), so the group-resolver path matches a
// directory member name on the same terms.
func namesCaller(values []string, id Identity) bool {
	for _, v := range values {
		if v == "" {
			continue
		}
		if v == id.Sub || v == id.Email {
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

// Compose returns, for each canonical ID, the candidate from the
// highest-precedence layer in layers. It applies no §4.6 rule. Ingest and filesystem.Walk enforce the
// collision rule on their own inputs, and the consumer applies the workspace
// overlay's exception (§6.4). No production path calls Compose.
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

// Composed is the candidate Compose selects for a canonical artifact ID.
type Composed struct {
	ID       string
	LayerID  string
	Artifact *manifest.Artifact
	Skill    *manifest.Skill
}

// SplitArtifactRef splits "id@version" into its components. Empty
// version is the implicit "latest" per §4.7.6.
func SplitArtifactRef(ref string) (id, version string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}
