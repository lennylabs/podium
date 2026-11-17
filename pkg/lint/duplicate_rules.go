package lint

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// minUnitWords is the §7.3.1 bound below which a unit is not compared.
const minUnitWords = 50

// Unit names for an artifact's prose body (§7.3.1).
const (
	skillBodyUnit    = "SKILL.md body"
	artifactBodyUnit = "ARTIFACT.md body"
)

// contentUnit is one compared unit of lint.near_duplicate_content: the
// index of the record that holds it, its name (a body name or a resource
// path), and whether it is a bundled resource.
type contentUnit struct {
	record   int
	name     string
	resource bool
}

// ruleNearDuplicateContent flags units of different artifacts whose
// five-word shingle sets have a similarity of t or higher. A unit is an
// artifact's prose body (SKILL.md for a skill, ARTIFACT.md for any other
// type) or a bundled resource that is valid UTF-8 with no NUL byte. A unit
// of fewer than 50 words is not compared, and two units of one artifact are
// not compared. Each flagged unit draws one warning on its artifact naming
// the peer units by descending similarity and then by identifier.
// Suppression is one-sided, as for lint.colliding_descriptions.
//
// Spec: §7.3.1
type ruleNearDuplicateContent struct {
	t float64
}

func (ruleNearDuplicateContent) Code() string { return "lint.near_duplicate_content" }

// Check returns no diagnostics when ctx is cancelled before the join
// finishes. Rule.Check returns no error, so the caller checks ctx.Err()
// itself rather than reading an empty result as a clean catalog.
func (r ruleNearDuplicateContent) Check(ctx context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	units, sets, groups, pairs, err := nearDuplicateJoin(ctx, records, r.t)
	if err != nil {
		return nil
	}
	ident := func(s int) string { return records[units[s].record].ID + " " + units[s].name }
	peers := newPeerIndex(sets, groups, pairs, ident)
	var out []Diagnostic
	for s, u := range units {
		rec := records[u.record]
		if rec.Artifact.Suppresses(r.Code()) {
			continue
		}
		first, total := peers.summary(s)
		if total == 0 {
			continue
		}
		out = append(out, warn(rec.ID, r.Code(),
			fmt.Sprintf("%s nearly duplicates %s; %s", u.name, formatPeers(first, total, ident), remedy(u))))
	}
	return out
}

// remedy names the action the author can take for a unit of u's kind. A
// body can move into one artifact that the others reference; a bundled
// resource can only be kept once when the artifacts can share it.
func remedy(u contentUnit) string {
	if u.resource {
		return "keep one copy if the artifacts can share it"
	}
	return "keep one copy, or move the shared text into one artifact and reference it"
}

// nearDuplicateJoin collects the units of records in record order and then
// path order, builds their shingle sets, groups identical sets, and joins
// the groups at threshold t. units and sets are parallel. A unit of fewer
// than 50 words is skipped.
func nearDuplicateJoin(ctx context.Context, records []filesystem.ArtifactRecord, t float64) (units []contentUnit, sets []tokenSet, groups []setGroup, pairs []scoredPair, err error) {
	in := newInterner()
	for i, rec := range records {
		for _, u := range recordUnits(i, rec) {
			words := wordTokens(u.text)
			if len(words) < minUnitWords {
				continue
			}
			units = append(units, u.contentUnit)
			sets = append(sets, tokenSet{owner: i, tokens: in.shingles(words)})
		}
	}
	groups = groupIdentical(sets)
	pairs, err = similarPairs(ctx, groups, t)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return units, sets, groups, pairs, nil
}

// textUnit is a contentUnit with its text.
type textUnit struct {
	contentUnit
	text string
}

// recordUnits returns the prose body of rec and each of its text resources,
// the resources in path order. Resources exclude the root ARTIFACT.md and
// SKILL.md, so a body is never also a resource unit.
func recordUnits(i int, rec filesystem.ArtifactRecord) []textUnit {
	var out []textUnit
	switch {
	case rec.Skill != nil:
		out = append(out, textUnit{contentUnit{record: i, name: skillBodyUnit}, rec.Skill.Body})
	case rec.Artifact != nil:
		out = append(out, textUnit{contentUnit{record: i, name: artifactBodyUnit}, rec.Artifact.Body})
	}
	paths := make([]string, 0, len(rec.Resources))
	for p, b := range rec.Resources {
		if utf8.Valid(b) && bytes.IndexByte(b, 0) < 0 {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		out = append(out, textUnit{contentUnit{record: i, name: p, resource: true}, string(rec.Resources[p])})
	}
	return out
}
