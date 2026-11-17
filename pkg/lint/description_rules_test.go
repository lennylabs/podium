package lint

import (
	"context"
	"fmt"
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
		Artifact: &manifest.Artifact{Type: manifest.TypeAgent, Version: "1.0.0", Description: desc},
	}
}

// runAdvisory runs only the §7.3.1 catalog advisories at their defaults.
func runAdvisory(records []filesystem.ArtifactRecord) []Diagnostic {
	return (&Linter{Rules: (&Linter{}).AdvisoryRules()}).Lint(context.Background(), nil, records)
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

// Spec: §7.3.1, §13.12 — the thin-description bounds are configurable,
// and the character bound counts code points. At the default bound the
// 14-code-point, 26-byte description "éé éé éééééééé" is thin, which a
// rule comparing len(desc) with the bound misses. A configured word bound
// replaces the MinDescriptionWords constant.
func TestThinDescription_ConfiguredBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		th   Thresholds
		desc string
		thin bool
	}{
		{"chars 20: 19 code points", Thresholds{MinDescriptionChars: 20}, "abcd efgh ijkl mnop", true},
		{"chars 20: 20 code points", Thresholds{MinDescriptionChars: 20}, "abcd efgh ijkl mnopq", false},
		{"default: 14 code points in 26 bytes", Thresholds{}, "éé éé éééééééé", true},
		{"default: 15 code points of multi-byte runes", Thresholds{}, "éé éé ééééééééé", false},
		{"words 5: 4 words", Thresholds{MinDescriptionWords: 5}, "alpha bravo charlie delta", true},
		{"words 5: 5 words", Thresholds{MinDescriptionWords: 5}, "alpha bravo charlie delta echo", false},
	}
	for _, c := range cases {
		rules := descriptionRules(c.th.resolved())
		diags := (&Linter{Rules: rules}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec("x", c.desc)})
		if got := hasCode(diags, "lint.thin_description"); got != c.thin {
			t.Errorf("%s: %q thin=%v, want %v (%v)", c.name, c.desc, got, c.thin, diags)
		}
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

// Spec: §7.3.1 — descriptions whose normalized word sets are equal have
// similarity 1, and every artifact of the group draws a warning naming its
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

// Spec: §7.3.1 — an empty description has an empty word set and overlaps
// nothing, so two empty descriptions draw no lint.colliding_descriptions
// warning.
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

// collidingMessages maps each artifact to its lint.colliding_descriptions
// message.
func collidingMessages(diags []Diagnostic) map[string]string {
	out := map[string]string{}
	for _, d := range diags {
		if d.Code == "lint.colliding_descriptions" {
			out[d.ArtifactID] = d.Message
		}
	}
	return out
}

// Spec: §7.3.1 — "two artifacts whose normalized descriptions have word
// sets with a similarity of 0.7 or higher." Two six-word descriptions that
// differ by one substituted word share 5 of 7 words (71%) and overlap at
// the default threshold; the message names the peer and its percentage.
func TestCollidingDescriptions_ParaphraseAtDefault(t *testing.T) {
	t.Parallel()
	msgs := collidingMessages(runAdvisory([]filesystem.ArtifactRecord{
		rec("finance/close-monthly", "Close the monthly books for finance."),
		rec("finance/close-quarterly", "Close the quarterly books for finance."),
	}))
	if !strings.Contains(msgs["finance/close-monthly"], "description overlaps finance/close-quarterly (71%)") {
		t.Errorf("monthly message = %q", msgs["finance/close-monthly"])
	}
	if !strings.Contains(msgs["finance/close-quarterly"], "description overlaps finance/close-monthly (71%)") {
		t.Errorf("quarterly message = %q", msgs["finance/close-quarterly"])
	}
}

// Spec: §7.3.1 — descriptions that follow one template share most of their
// words: "create" and "merge" variants share 8 of 10 words, 80%.
func TestCollidingDescriptions_TemplateDescriptions(t *testing.T) {
	t.Parallel()
	msgs := collidingMessages(runAdvisory([]filesystem.ArtifactRecord{
		rec("pdf/create", "Use when the user asks to create a pdf"),
		rec("pdf/merge", "Use when the user asks to merge a pdf"),
	}))
	if !strings.Contains(msgs["pdf/create"], "pdf/merge (80%)") {
		t.Errorf("create message = %q", msgs["pdf/create"])
	}
}

// Spec: §7.3.1 — peers are named by descending similarity and then by
// identifier, and more than 10 peers print the first 10 and a count of the
// rest.
func TestCollidingDescriptions_PeerOrderAndCap(t *testing.T) {
	t.Parallel()
	ordered := collidingMessages(runAdvisory([]filesystem.ArtifactRecord{
		rec("m/base", "alpha bravo charlie delta echo foxtrot golf hotel india juliet"),
		rec("a/low", "alpha bravo charlie delta echo foxtrot golf hotel"),
		rec("z/high", "alpha bravo charlie delta echo foxtrot golf hotel india"),
	}))
	if want := "description overlaps z/high (90%), a/low (80%);"; !strings.Contains(ordered["m/base"], want) {
		t.Errorf("base message = %q, want it to contain %q", ordered["m/base"], want)
	}

	var records []filesystem.ArtifactRecord
	for i := 12; i >= 0; i-- {
		records = append(records, rec(fmt.Sprintf("dup/%02d", i), "Reconcile vendor invoices at month-end close."))
	}
	msg := collidingMessages(runAdvisory(records))["dup/00"]
	var peers []string
	for i := 1; i <= 10; i++ {
		peers = append(peers, fmt.Sprintf("dup/%02d (100%%)", i))
	}
	if want := "description overlaps " + strings.Join(peers, ", ") + ", and 2 more;"; !strings.Contains(msg, want) {
		t.Errorf("capped message = %q, want it to contain %q", msg, want)
	}
}

// Spec: §7.3.1 — AllRules() excludes the catalog advisories, and
// AdvisoryRules() returns them. Registry ingest runs AdvisoryRules() over
// the ingested layer, and `podium lint` adds them to its pass through
// IncludeAdvisories.
func TestDescriptionAdvisories_NotInAllRules(t *testing.T) {
	t.Parallel()
	advisories := map[string]bool{
		"lint.thin_description":       true,
		"lint.colliding_descriptions": true,
		"lint.near_duplicate_content": true,
	}
	for _, r := range AllRules() {
		if advisories[r.Code()] {
			t.Errorf("advisory rule %s must not be in AllRules()", r.Code())
		}
	}
	got := map[string]bool{}
	for _, r := range (&Linter{}).AdvisoryRules() {
		got[r.Code()] = true
	}
	for code := range advisories {
		if !got[code] {
			t.Errorf("AdvisoryRules() lacks %s; got %v", code, got)
		}
	}
	if len(got) != len(advisories) {
		t.Errorf("AdvisoryRules() codes = %v, want exactly %v", got, advisories)
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
