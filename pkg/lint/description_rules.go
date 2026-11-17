package lint

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Description-quality thresholds (spec §3.3 "Description quality", §12
// "Manifest description quality"). A description must answer "when should
// I use this?" in one or two sentences (§3.3); these bounds flag the
// degenerate cases (a single word, a placeholder, a near-empty string)
// that cannot. The values are a deliberately conservative heuristic: a
// description that clears either bound is left alone, so only genuinely
// thin summaries draw the advisory.
const (
	// MinDescriptionChars is the default lower bound, in code points,
	// below which a non-empty description is treated as thin.
	MinDescriptionChars = 15
	// MinDescriptionWords is the default lower bound, in
	// white-space-separated words, below which a description is treated
	// as thin.
	MinDescriptionWords = 3
)

// descriptionRules builds the description-quality rules from th, which
// the caller has already resolved.
func descriptionRules(th Thresholds) []Rule {
	return []Rule{
		ruleThinDescription{minChars: th.MinDescriptionChars, minWords: th.MinDescriptionWords},
		ruleCollidingDescriptions{t: th.DescriptionSimilarity},
	}
}

// AdvisoryRules returns the §7.3.1 catalog advisories: lint.thin_description,
// lint.colliding_descriptions, and lint.near_duplicate_content, each built
// from l.Thresholds with every unset field at its default. A nil l uses the
// defaults. The advisories stay out of AllRules(); Lint appends them when
// IncludeAdvisories is set.
//
// Spec: §7.3.1, §13.12
func (l *Linter) AdvisoryRules() []Rule {
	th := DefaultThresholds()
	if l != nil {
		th = l.Thresholds.resolved()
	}
	return append(descriptionRules(th), ruleNearDuplicateContent{t: th.ContentSimilarity})
}

// effectiveDescription returns the description that drives discovery for a
// record: the universal ARTIFACT.md `description`, falling back to the
// SKILL.md `description` for skills whose prose-side manifest carries it
// (§4.3.4). Layers 1 and 2 retrieve over this text (§3.3), so the
// description-quality checks evaluate it for every type, skills included.
func effectiveDescription(rec filesystem.ArtifactRecord) string {
	if rec.Artifact != nil && strings.TrimSpace(rec.Artifact.Description) != "" {
		return rec.Artifact.Description
	}
	if rec.Skill != nil && strings.TrimSpace(rec.Skill.Description) != "" {
		return rec.Skill.Description
	}
	if rec.Artifact != nil {
		return rec.Artifact.Description
	}
	return ""
}

// ruleThinDescription flags an artifact whose effective description is
// present but too thin to answer "when should I use this?" (spec §3.3,
// §12). An absent description is left to the per-type required-field rules
// (a skill with an empty description is already a hard error via
// lint.skill_md_compliance); this rule targets the present-but-inadequate
// case. minChars counts code points and minWords counts white-space-separated
// words (§7.3.1, §13.12). Honors the per-artifact lint_suppress flag.
type ruleThinDescription struct {
	minChars int
	minWords int
}

func (ruleThinDescription) Code() string { return "lint.thin_description" }

func (r ruleThinDescription) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact != nil && rec.Artifact.Suppresses(r.Code()) {
			continue
		}
		desc := strings.TrimSpace(effectiveDescription(rec))
		if desc == "" {
			continue
		}
		chars := utf8.RuneCountInString(desc)
		words := len(strings.Fields(desc))
		if chars < r.minChars || words < r.minWords {
			out = append(out, warn(rec.ID, r.Code(),
				fmt.Sprintf("description is thin (%d chars, %d words); answer \"when should I use this?\" in one or two sentences (want at least %d chars and %d words)",
					chars, words, r.minChars, r.minWords)))
		}
	}
	return out
}

// ruleCollidingDescriptions flags artifacts whose descriptions overlap:
// two artifacts whose normalized descriptions have word sets with a
// similarity of t or higher (§7.3.1). Each flagged artifact draws one
// warning naming its peers by descending similarity and then by ID, with
// more than 10 peers abbreviated to a count. An empty description never
// overlaps. Suppression is one-sided: an artifact whose lint_suppress lists
// the code draws no warning, and its peers still name it.
//
// Spec: §7.3.1
type ruleCollidingDescriptions struct {
	t float64
}

func (ruleCollidingDescriptions) Code() string { return "lint.colliding_descriptions" }

func (r ruleCollidingDescriptions) Check(ctx context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	in := newInterner()
	var sets []tokenSet
	for i, rec := range records {
		toks := in.words(wordTokens(effectiveDescription(rec)))
		if len(toks) == 0 {
			continue
		}
		sets = append(sets, tokenSet{owner: i, tokens: toks})
	}
	groups := groupIdentical(sets)
	pairs, err := similarPairs(ctx, groups, r.t)
	if err != nil {
		return nil
	}
	name := func(s int) string { return records[sets[s].owner].ID }
	peers := newPeerIndex(sets, groups, pairs, name)
	var out []Diagnostic
	for s, set := range sets {
		rec := records[set.owner]
		if rec.Artifact.Suppresses(r.Code()) {
			continue
		}
		first, total := peers.summary(s)
		if total == 0 {
			continue
		}
		out = append(out, warn(rec.ID, r.Code(),
			fmt.Sprintf("description overlaps %s; give each artifact a distinct summary so search and load_domain can tell them apart",
				formatPeers(first, total, name))))
	}
	return out
}

// normalizeDescription lowercases the string and replaces every run of
// non-alphanumeric runes with a single space, then trims, so collisions
// are detected modulo case, punctuation, and whitespace.
func normalizeDescription(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, ru := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(ru) || unicode.IsDigit(ru):
			b.WriteRune(ru)
			prevSpace = false
		case !prevSpace:
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}
