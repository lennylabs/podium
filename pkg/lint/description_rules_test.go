package lint

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// rec builds a minimal artifact record with the given canonical ID and
// universal description for the description-quality rules under test.
func rec(id, desc string) filesystem.ArtifactRecord {
	return filesystem.ArtifactRecord{
		ID:       id,
		Artifact: &manifest.Artifact{Type: manifest.TypeContext, Version: "1.0.0", Description: desc},
	}
}

// runAdvisory runs only the §3.3 / §12 description-quality rules.
func runAdvisory(records []filesystem.ArtifactRecord) []Diagnostic {
	return (&Linter{Rules: DescriptionAdvisoryRules()}).Lint(nil, records)
}

// Spec: §3.3 / §12 — "The registry lints for thin descriptions." A
// single-word or very short summary cannot answer "when should I use
// this?" and draws a lint.thin_description warning.
func TestThinDescription_FlagsSingleWordAndShort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id, desc string
		thin     bool
	}{
		{"a", "demo", true},                                 // 1 word
		{"b", "AP", true},                                   // 2 chars
		{"c", "helper tool", true},                          // 2 words, 11 chars
		{"d", "Greet the user.", false},                     // 3 words, 15 chars — at the bound
		{"e", "Reconcile vendor invoices at close.", false}, // rich
		{"f", "  ", false},                                  // whitespace-only is absent, not thin
		{"g", "", false},                                    // absent: left to required-field rules
	}
	for _, c := range cases {
		diags := runAdvisory([]filesystem.ArtifactRecord{rec(c.id, c.desc)})
		got := hasCode(diags, "lint.thin_description")
		if got != c.thin {
			t.Errorf("desc %q: thin=%v, want %v (%v)", c.desc, got, c.thin, diags)
		}
		if got && diags[0].Severity != SeverityWarning {
			t.Errorf("desc %q: severity=%v, want warning", c.desc, diags[0].Severity)
		}
	}
}

// Spec: §3.3 — the description-quality checks evaluate the text Layers 1
// and 2 retrieve over, which for a skill lives in SKILL.md. A skill with a
// thin SKILL.md description is flagged even though ARTIFACT.md carries no
// description.
func TestThinDescription_UsesSkillDescription(t *testing.T) {
	t.Parallel()
	r := filesystem.ArtifactRecord{
		ID:       "greetings/hi",
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Version: "1.0.0"},
		Skill:    &manifest.Skill{Name: "hi", Description: "greet"},
	}
	if !hasCode(runAdvisory([]filesystem.ArtifactRecord{r}), "lint.thin_description") {
		t.Errorf("thin SKILL.md description not flagged")
	}
	r.Skill.Description = "Greet a returning user by name at session start."
	if hasCode(runAdvisory([]filesystem.ArtifactRecord{r}), "lint.thin_description") {
		t.Errorf("rich SKILL.md description should not be flagged")
	}
}

// Spec: §4.3.4 lint_suppress — the advisory rules are non-error, so a
// per-artifact lint_suppress entry silences them.
func TestThinDescription_HonorsSuppress(t *testing.T) {
	t.Parallel()
	r := rec("x", "demo")
	r.Artifact.LintSuppress = []string{"lint.thin_description"}
	if hasCode(runAdvisory([]filesystem.ArtifactRecord{r}), "lint.thin_description") {
		t.Errorf("lint_suppress did not silence lint.thin_description")
	}
}

// Spec: §3.3 / §12 — "flags clusters of artifacts whose summaries
// collide." Descriptions that match after case/punctuation/whitespace
// normalization cluster, and every member draws a warning naming the
// peers.
func TestCollidingDescriptions_ClustersNormalizedEqual(t *testing.T) {
	t.Parallel()
	records := []filesystem.ArtifactRecord{
		rec("finance/close-a", "Close the monthly books."),
		rec("finance/close-b", "close the monthly books"),
		rec("finance/close-c", "CLOSE  THE  MONTHLY  BOOKS!"),
		rec("finance/pay", "Submit an approved vendor payment."),
	}
	diags := runAdvisory(records)
	collisions := map[string]string{}
	for _, d := range diags {
		if d.Code == "lint.colliding_descriptions" {
			collisions[d.ArtifactID] = d.Message
		}
	}
	for _, id := range []string{"finance/close-a", "finance/close-b", "finance/close-c"} {
		if _, ok := collisions[id]; !ok {
			t.Errorf("%s not flagged as colliding: %v", id, diags)
		}
	}
	if _, ok := collisions["finance/pay"]; ok {
		t.Errorf("distinct description flagged as colliding")
	}
	// The warning on close-a names its two peers.
	if msg := collisions["finance/close-a"]; !strings.Contains(msg, "finance/close-b") || !strings.Contains(msg, "finance/close-c") {
		t.Errorf("collision message missing peers: %q", msg)
	}
}

// Spec: §3.3 — an empty description is not a collision (otherwise every
// artifact without a summary would cluster). Two empty descriptions draw
// no colliding-summary warning.
func TestCollidingDescriptions_EmptyNeverCollides(t *testing.T) {
	t.Parallel()
	records := []filesystem.ArtifactRecord{rec("a", ""), rec("b", "")}
	if hasCode(runAdvisory(records), "lint.colliding_descriptions") {
		t.Errorf("empty descriptions should not collide")
	}
}

// A description that appears exactly once never collides (boundary: a
// cluster needs two or more members).
func TestCollidingDescriptions_UniqueNotFlagged(t *testing.T) {
	t.Parallel()
	records := []filesystem.ArtifactRecord{
		rec("a", "Reconcile vendor invoices at month-end close."),
		rec("b", "Submit an approved vendor payment for release."),
	}
	if hasCode(runAdvisory(records), "lint.colliding_descriptions") {
		t.Errorf("unique descriptions should not collide: %v", runAdvisory(records))
	}
}

// Spec: §4.3.4 lint_suppress — a colliding artifact that suppresses the
// rule is silenced while its peer is still flagged.
func TestCollidingDescriptions_HonorsSuppress(t *testing.T) {
	t.Parallel()
	a := rec("a", "Close the monthly books.")
	a.Artifact.LintSuppress = []string{"lint.colliding_descriptions"}
	b := rec("b", "Close the monthly books.")
	diags := runAdvisory([]filesystem.ArtifactRecord{a, b})
	for _, d := range diags {
		if d.Code == "lint.colliding_descriptions" && d.ArtifactID == "a" {
			t.Errorf("suppressed artifact still flagged: %v", d)
		}
	}
	if !hasCode(diags, "lint.colliding_descriptions") {
		t.Errorf("non-suppressed peer should still be flagged")
	}
}

// Spec: §3.3 — the advisory rules are deliberately absent from AllRules()
// so the author-facing `podium lint` keeps the per-artifact schema rules;
// the registry runs DescriptionAdvisoryRules() at ingest time.
func TestDescriptionAdvisories_NotInAllRules(t *testing.T) {
	t.Parallel()
	for _, r := range AllRules() {
		if r.Code() == "lint.thin_description" || r.Code() == "lint.colliding_descriptions" {
			t.Errorf("advisory rule %s must not be in AllRules()", r.Code())
		}
	}
}

func TestNormalizeDescription(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Close the books.":    "close the books",
		"close   the   books": "close the books",
		"CLOSE-THE-BOOKS!":    "close the books",
		"  Trim me.  ":        "trim me",
		"":                    "",
		"...":                 "",
	}
	for in, want := range cases {
		if got := normalizeDescription(in); got != want {
			t.Errorf("normalizeDescription(%q) = %q, want %q", in, got, want)
		}
	}
}
