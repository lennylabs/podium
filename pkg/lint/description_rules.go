package lint

import (
	"context"
	"fmt"
	"sort"
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
	// MinDescriptionChars is the lower bound, in runes, below which a
	// non-empty description is treated as thin.
	MinDescriptionChars = 15
	// MinDescriptionWords is the lower bound, in whitespace-separated
	// words, below which a description is treated as thin.
	MinDescriptionWords = 3
)

// DescriptionAdvisoryRules returns the §3.3 / §12 registry ingest-time
// description-quality checks: a thin-description flag and a
// colliding-summaries flag. Both are advisory (warning severity).
//
// They are intentionally NOT part of AllRules(): the spec attributes the
// behavior to "the registry" (§3.3 "The registry lints for thin
// descriptions and flags clusters of artifacts whose summaries collide")
// and to "ingest-time lint" (§12), so the registry ingest pipeline runs
// them over the ingested record set, while the author-facing `podium lint`
// keeps the per-artifact schema rules in AllRules(). The colliding-summary
// check is inherently cross-artifact and only carries meaning over a set
// of artifacts, which the ingest batch supplies.
func DescriptionAdvisoryRules() []Rule {
	return []Rule{
		ruleThinDescription{},
		ruleCollidingDescriptions{},
	}
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
// case. Honors the per-artifact lint_suppress flag.
type ruleThinDescription struct{}

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
		if chars < MinDescriptionChars || words < MinDescriptionWords {
			out = append(out, warn(rec.ID, r.Code(),
				fmt.Sprintf("description is thin (%d chars, %d words); answer \"when should I use this?\" in one or two sentences (want at least %d chars and %d words)",
					chars, words, MinDescriptionChars, MinDescriptionWords)))
		}
	}
	return out
}

// ruleCollidingDescriptions flags clusters of artifacts whose summaries
// collide (spec §3.3 "flags clusters of artifacts whose summaries
// collide", §12). Two descriptions collide when they are identical after
// normalization (lowercased, runs of punctuation and whitespace collapsed
// to single spaces, trimmed), so "Close the books.", "close the books",
// and "CLOSE  THE  BOOKS!" cluster together. Every member of a cluster of
// two or more draws one warning naming the colliding peers. An empty
// description never collides. Honors the per-artifact lint_suppress flag.
type ruleCollidingDescriptions struct{}

func (ruleCollidingDescriptions) Code() string { return "lint.colliding_descriptions" }

func (r ruleCollidingDescriptions) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	// Group canonical IDs by normalized description across the whole
	// ingested set so a cluster is visible from any of its members.
	byNorm := map[string][]string{}
	for _, rec := range records {
		norm := normalizeDescription(effectiveDescription(rec))
		if norm == "" {
			continue
		}
		byNorm[norm] = append(byNorm[norm], rec.ID)
	}
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact != nil && rec.Artifact.Suppresses(r.Code()) {
			continue
		}
		norm := normalizeDescription(effectiveDescription(rec))
		if norm == "" {
			continue
		}
		others := make([]string, 0, len(byNorm[norm]))
		for _, id := range byNorm[norm] {
			if id != rec.ID {
				others = append(others, id)
			}
		}
		if len(others) == 0 {
			continue
		}
		sort.Strings(others)
		out = append(out, warn(rec.ID, r.Code(),
			fmt.Sprintf("description collides with %s; give each artifact a distinct summary so search and load_domain can tell them apart",
				strings.Join(others, ", "))))
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
