package attest

import (
	"strings"

	"github.com/lennylabs/podium/pkg/sign"
)

// CurrentSubjects maps the name of each current subject of a layer to the
// hexadecimal digits of its digest: the layer subject of the layer root with
// the layer digest, and each package's subject with its package digest.
type CurrentSubjects map[string]string

// NewCurrentSubjects returns the current subjects of a layer from its layer
// subject and its artifact subjects, each with a sha256:<hex> digest.
//
// Spec: §7.3.5
func NewCurrentSubjects(layer Subject, artifacts []Subject) CurrentSubjects {
	cur := make(CurrentSubjects, len(artifacts)+1)
	cur[layer.Name] = strings.TrimPrefix(layer.Digest, digestPrefix)
	for _, s := range artifacts {
		cur[s.Name] = strings.TrimPrefix(s.Digest, digestPrefix)
	}
	return cur
}

// Matching returns the names of the subjects of st that match a current
// subject: the same name, and a sha256 member equal to the current digest
// under the §4.7.11 subject rules. Each name appears once, in statement
// order.
//
// Spec: §7.3.5
func (c CurrentSubjects) Matching(st sign.AttestationStatement) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range st.Subjects {
		hex, ok := c[s.Name]
		if !ok || seen[s.Name] || !st.Lists(s.Name, hex) {
			continue
		}
		seen[s.Name] = true
		out = append(out, s.Name)
	}
	return out
}

// PruneReason is why `podium attest import` removes an existing bundle.
type PruneReason string

// The prune reasons of §7.3.5.
const (
	// PruneKeep keeps the bundle.
	PruneKeep PruneReason = ""
	// PruneNoCurrentSubject removes a bundle none of whose subjects
	// matches a current subject.
	PruneNoCurrentSubject PruneReason = "no subject matches the layer"
	// PruneSuperseded removes a bundle whose matching subjects the
	// imported bundles of its signer and predicate type list together.
	PruneSuperseded PruneReason = "the imported bundles of its signer and predicate type list its matching subjects"
)

// Prune applies the §7.3.5 pruning rule to one existing bundle of an
// attestation directory, given the bundles the run imported and the layer's
// current subjects. A bundle is removed when none of its subjects matches a
// current subject, or when the imported bundles with its predicate type and
// its certificate issuer and identity together list every subject of it that
// matches. The rule compares against the imported bundles together, because
// adding one artifact changes how a large layer's subjects divide across
// statements. The signer is read without verification and carries no
// Sigstore instance, so bundles of two instances with one issuer and
// identity group as one signer (§7.3.5). A bundle whose certificate names no
// issuer or no identity groups with no imported bundle, so it is removed only
// when it matches nothing.
//
// Spec: §7.3.5
func Prune(existing sign.BundleSummary, imported []sign.BundleSummary, cur CurrentSubjects) PruneReason {
	matching := cur.Matching(existing.Statement)
	if len(matching) == 0 {
		return PruneNoCurrentSubject
	}
	if existing.Issuer == "" || existing.Identity == "" {
		return PruneKeep
	}
	var group []sign.AttestationStatement
	for _, b := range imported {
		if b.Statement.PredicateType == existing.Statement.PredicateType &&
			b.Issuer == existing.Issuer && b.Identity == existing.Identity {
			group = append(group, b.Statement)
		}
	}
	for _, name := range matching {
		if !anyLists(group, name, cur[name]) {
			return PruneKeep
		}
	}
	return PruneSuperseded
}

// anyLists reports whether a statement of group lists name with hex.
func anyLists(group []sign.AttestationStatement, name, hex string) bool {
	for _, st := range group {
		if st.Lists(name, hex) {
			return true
		}
	}
	return false
}
