package lint

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

const nearDupCode = "lint.near_duplicate_content"

// prose returns n distinct words with the given prefix, as one string.
func prose(prefix string, n int) string {
	return strings.Join(proseWords(prefix, n), " ")
}

func proseWords(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// sharedPrefixBodies returns two bodies of total distinct words each that
// share their first shared words and no later word.
func sharedPrefixBodies(total, shared int) (string, string) {
	common := proseWords("common", shared)
	a := append(append([]string{}, common...), proseWords("left", total-shared)...)
	b := append(append([]string{}, common...), proseWords("right", total-shared)...)
	return strings.Join(a, " "), strings.Join(b, " ")
}

func skillRec(id, body string) filesystem.ArtifactRecord {
	return filesystem.ArtifactRecord{
		ID:       id,
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Version: "1.0.0"},
		Skill:    &manifest.Skill{Name: lastPathSegment(id), Body: body},
	}
}

func contextRec(id, body string) filesystem.ArtifactRecord {
	return filesystem.ArtifactRecord{
		ID:       id,
		Artifact: &manifest.Artifact{Type: manifest.TypeContext, Version: "1.0.0", Body: body},
	}
}

// nearDup runs only lint.near_duplicate_content at threshold th.
func nearDup(records []filesystem.ArtifactRecord, th float64) []Diagnostic {
	return (&Linter{Rules: []Rule{ruleNearDuplicateContent{t: th}}}).Lint(context.Background(), nil, records)
}

func nearDupMessages(diags []Diagnostic) map[string][]string {
	out := map[string][]string{}
	for _, d := range diags {
		if d.Code == nearDupCode {
			out[d.ArtifactID] = append(out[d.ArtifactID], d.Message)
		}
	}
	return out
}

// Spec: §7.3.1 — two SKILL.md bodies that differ in one sentence share 51
// of 61 shingles (83%), so each artifact draws a warning naming its own
// unit, the peer, and the peer's unit.
func TestNearDuplicate_BodiesAcrossArtifacts(t *testing.T) {
	t.Parallel()
	a, b := sharedPrefixBodies(60, 55)
	msgs := nearDupMessages(nearDup([]filesystem.ArtifactRecord{skillRec("ops/a", a), skillRec("ops/b", b)}, DefaultContentSimilarity))
	if got := msgs["ops/a"]; len(got) != 1 || !strings.HasPrefix(got[0], "SKILL.md body nearly duplicates ops/b SKILL.md body (83%); ") {
		t.Errorf("ops/a messages = %q", got)
	}
	if got := msgs["ops/b"]; len(got) != 1 || !strings.Contains(got[0], "nearly duplicates ops/a SKILL.md body (83%); keep one copy, or move the shared text") {
		t.Errorf("ops/b messages = %q", got)
	}
}

// Spec: §7.3.1 — the prose body of a type other than skill is its
// ARTIFACT.md body, which fails against a rule that collects a body only
// when the record carries a SKILL.md.
func TestNearDuplicate_ArtifactBodiesOfOtherTypes(t *testing.T) {
	t.Parallel()
	a, b := sharedPrefixBodies(60, 55)
	msgs := nearDupMessages(nearDup([]filesystem.ArtifactRecord{contextRec("ctx/a", a), contextRec("ctx/b", b)}, DefaultContentSimilarity))
	for id, peer := range map[string]string{"ctx/a": "ctx/b", "ctx/b": "ctx/a"} {
		want := "ARTIFACT.md body nearly duplicates " + peer + " ARTIFACT.md body (83%)"
		if got := msgs[id]; len(got) != 1 || !strings.HasPrefix(got[0], want) {
			t.Errorf("%s messages = %q, want prefix %q", id, got, want)
		}
	}
}

// Spec: §7.3.1, §13.12 — the default content threshold is 0.8. Two 76-word
// bodies sharing their first 68 words share 64 of 80 shingles (exactly 0.8)
// and warn under a zero Linter; sharing 67 words gives 63 of 81 and draws
// nothing.
func TestNearDuplicate_DefaultThreshold(t *testing.T) {
	t.Parallel()
	run := func(shared int) bool {
		a, b := sharedPrefixBodies(76, shared)
		l := &Linter{IncludeAdvisories: true}
		return hasCode(l.Lint(context.Background(), nil, []filesystem.ArtifactRecord{contextRec("ctx/a", a), contextRec("ctx/b", b)}), nearDupCode)
	}
	if !run(68) {
		t.Errorf("64/80 shingles (0.8) did not warn at the default")
	}
	if run(67) {
		t.Errorf("63/81 shingles warned at the default")
	}
}

// Spec: §7.3.1 — at a threshold of 1.0 byte-identical bodies still warn,
// through their group, while a pair differing in one sentence does not.
func TestNearDuplicate_ThresholdOne(t *testing.T) {
	t.Parallel()
	rules := (&Linter{Thresholds: Thresholds{ContentSimilarity: 1.0}}).AdvisoryRules()
	lint := func(records ...filesystem.ArtifactRecord) []Diagnostic {
		return (&Linter{Rules: rules}).Lint(context.Background(), nil, records)
	}
	body := prose("same", 60)
	if msgs := nearDupMessages(lint(skillRec("s/a", body), skillRec("s/b", body))); len(msgs["s/a"]) != 1 || len(msgs["s/b"]) != 1 {
		t.Errorf("identical bodies at 1.0: %v", msgs)
	}
	a, b := sharedPrefixBodies(60, 55)
	if hasCode(lint(skillRec("s/a", a), skillRec("s/b", b)), nearDupCode) {
		t.Errorf("one-sentence-changed pair warned at 1.0")
	}
}

// sharedResourceRecords returns n context artifacts that each bundle the
// same resource of the given word count at references/license.md.
func sharedResourceRecords(n, words int) []filesystem.ArtifactRecord {
	res := []byte(prose("license", words))
	out := make([]filesystem.ArtifactRecord, n)
	for i := range out {
		r := contextRec(fmt.Sprintf("pkg/a%03d", i), "")
		r.Resources = map[string][]byte{"references/license.md": res}
		out[i] = r
	}
	return out
}

// Spec: §7.3.1 — a file bundled unchanged in many artifacts forms one group
// and no pair, so its cost is linear in the number of artifacts, and each
// artifact names 10 peers and a count of the rest. The join assertion fails
// against a join that skips groupIdentical, which returns 500 groups and
// 124,750 pairs; the warnings alone cannot tell the two apart.
func TestNearDuplicate_SharedResourceCluster(t *testing.T) {
	t.Parallel()
	records := sharedResourceRecords(500, 200)
	units, sets, groups, pairs, err := nearDuplicateJoin(context.Background(), records, DefaultContentSimilarity)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 500 || len(sets) != 500 || len(groups) != 1 || len(groups[0].members) != 500 || len(pairs) != 0 {
		t.Fatalf("join: %d units, %d groups, %d pairs; want 500, 1 group of 500, 0", len(units), len(groups), len(pairs))
	}
	msgs := nearDupMessages(nearDup(records, DefaultContentSimilarity))
	if len(msgs) != 500 {
		t.Fatalf("%d artifacts warned, want 500", len(msgs))
	}
	var peers []string
	for i := 1; i <= 10; i++ {
		peers = append(peers, fmt.Sprintf("pkg/a%03d references/license.md (100%%)", i))
	}
	want := "references/license.md nearly duplicates " + strings.Join(peers, ", ") + ", and 489 more; keep one copy if the artifacts can share it"
	if got := msgs["pkg/a000"]; len(got) != 1 || got[0] != want {
		t.Errorf("pkg/a000 messages = %q\nwant %q", got, want)
	}
}

// Spec: §7.3.1 — a bundled resource that is valid UTF-8 with no NUL byte is
// a unit and draws the resource remedy; a resource holding a NUL byte and
// one that is not valid UTF-8 are not compared.
func TestNearDuplicate_ResourceUnits(t *testing.T) {
	t.Parallel()
	text := prose("guide", 60)
	resources := func() map[string][]byte {
		return map[string][]byte{
			"references/guide.md": []byte(text),
			"assets/blob.bin":     []byte(text + "\x00"),
			"assets/latin1.txt":   []byte(text + " \xff"),
		}
	}
	a, b := contextRec("docs/a", ""), contextRec("docs/b", "")
	a.Resources, b.Resources = resources(), resources()
	msgs := nearDupMessages(nearDup([]filesystem.ArtifactRecord{a, b}, DefaultContentSimilarity))
	want := "references/guide.md nearly duplicates docs/b references/guide.md (100%); keep one copy if the artifacts can share it"
	if got := msgs["docs/a"]; len(got) != 1 || got[0] != want {
		t.Errorf("docs/a messages = %q, want [%q]", got, want)
	}
	for _, ms := range msgs {
		for _, m := range ms {
			if strings.Contains(m, "blob.bin") || strings.Contains(m, "latin1.txt") {
				t.Errorf("binary resource compared: %q", m)
			}
		}
	}
}

// Spec: §7.3.1 — "A unit of fewer than 50 words is not compared, and two
// units of one artifact are not compared."
func TestNearDuplicate_SkipsShortAndSameArtifact(t *testing.T) {
	t.Parallel()
	short := prose("short", 49)
	if diags := nearDup([]filesystem.ArtifactRecord{skillRec("s/a", short), skillRec("s/b", short)}, DefaultContentSimilarity); len(diags) != 0 {
		t.Errorf("49-word bodies compared: %v", diags)
	}
	body := prose("self", 60)
	r := skillRec("s/self", body)
	r.Resources = map[string][]byte{"references/copy.md": []byte(body)}
	if diags := nearDup([]filesystem.ArtifactRecord{r}, DefaultContentSimilarity); len(diags) != 0 {
		t.Errorf("units of one artifact compared: %v", diags)
	}
}

// Spec: §7.3.1, §4.3.4 — an artifact that suppresses the code draws no
// warning, and its peer still names it.
func TestNearDuplicate_OneSidedSuppress(t *testing.T) {
	t.Parallel()
	body := prose("dup", 60)
	a, b := contextRec("ctx/a", body), contextRec("ctx/b", body)
	a.Artifact.LintSuppress = []string{nearDupCode}
	msgs := nearDupMessages(nearDup([]filesystem.ArtifactRecord{a, b}, DefaultContentSimilarity))
	if len(msgs["ctx/a"]) != 0 {
		t.Errorf("suppressed artifact warned: %q", msgs["ctx/a"])
	}
	if got := msgs["ctx/b"]; len(got) != 1 || !strings.Contains(got[0], "ctx/a ARTIFACT.md body (100%)") {
		t.Errorf("peer does not name the suppressing artifact: %q", got)
	}
}

// Spec: §7.3.1 — a cancelled pass returns no diagnostics.
func TestNearDuplicate_Cancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := prose("dup", 60)
	got := ruleNearDuplicateContent{t: DefaultContentSimilarity}.Check(ctx, nil, []filesystem.ArtifactRecord{contextRec("a", body), contextRec("b", body)})
	if len(got) != 0 {
		t.Errorf("cancelled pass returned %v", got)
	}
}

// stubRule emits fixed diagnostics.
type stubRule struct{ diags []Diagnostic }

func (stubRule) Code() string { return nearDupCode }

func (s stubRule) Check(context.Context, *filesystem.Registry, []filesystem.ArtifactRecord) []Diagnostic {
	return s.diags
}

// Spec: §7.3.1 — diagnostics are ordered by artifact identifier, then by
// code, then by message. A stable sort without the message key keeps the
// emission order, which this test reverses.
func TestLint_TieOrderByMessage(t *testing.T) {
	t.Parallel()
	stub := stubRule{diags: []Diagnostic{
		warn("x", nearDupCode, "b unit"),
		warn("x", nearDupCode, "a unit"),
	}}
	got := (&Linter{Rules: []Rule{stub}}).Lint(context.Background(), nil, nil)
	if len(got) != 2 || got[0].Message != "a unit" || got[1].Message != "b unit" {
		t.Errorf("Lint order = %v, want messages ascending", got)
	}
}

// catalogFixture holds a description at 10/14 similarity to 12 variants
// that do not pair with each other, and an artifact whose body and two
// resources each nearly duplicate a unit of another artifact. The input
// order differs from the identifier order.
func catalogFixture() []filesystem.ArtifactRecord {
	base := proseWords("word", 12)
	var records []filesystem.ArtifactRecord
	for k := 11; k >= 0; k-- {
		v := append([]string{}, base...)
		v[k] = fmt.Sprintf("fresh%da", k)
		v[(k+1)%12] = fmt.Sprintf("fresh%db", k)
		records = append(records, rec(fmt.Sprintf("desc/v%02d", k), strings.Join(v, " ")))
	}
	records = append(records, rec("desc/base", strings.Join(base, " ")))

	bodyA, bodyB := sharedPrefixBodies(60, 55)
	resA, resB := sharedPrefixBodies(70, 65)
	refA, refB := sharedPrefixBodies(80, 75)
	src := skillRec("units/source", bodyA)
	src.Resources = map[string][]byte{"references/a.md": []byte(resA), "references/b.md": []byte(refA)}
	other := skillRec("units/other", bodyB)
	other.Resources = map[string][]byte{"references/a.md": []byte(resB), "references/b.md": []byte(refB)}
	return append([]filesystem.ArtifactRecord{other}, append(records, src)...)
}

// Spec: §7.3.1, §11 — "Reordering the input produces the same
// diagnostics." The fixture's description draws 12 peers of equal
// similarity, so a peerSummary that breaks ties by set index or group order
// names different peers when the record order changes.
func TestCatalogAdvisories_OrderIndependent(t *testing.T) {
	t.Parallel()
	records := catalogFixture()
	run := func(rs []filesystem.ArtifactRecord) []Diagnostic {
		return (&Linter{Rules: (&Linter{}).AdvisoryRules()}).Lint(context.Background(), nil, rs)
	}
	want := run(records)

	reversed := make([]filesystem.ArtifactRecord, len(records))
	for i, r := range records {
		reversed[len(records)-1-i] = r
	}
	shuffled := append([]filesystem.ArtifactRecord{}, records...)
	rand.New(rand.NewSource(3)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	for name, rs := range map[string][]filesystem.ArtifactRecord{"reversed": reversed, "shuffled": shuffled} {
		if got := run(rs); !reflect.DeepEqual(got, want) {
			t.Errorf("%s input changed the diagnostics:\ngot  %v\nwant %v", name, got, want)
		}
	}

	baseMsg := collidingMessages(want)["desc/base"]
	if !strings.HasPrefix(baseMsg, "description overlaps desc/v00 (71%), desc/v01 (71%)") || !strings.Contains(baseMsg, "desc/v09 (71%), and 2 more;") {
		t.Errorf("base message = %q", baseMsg)
	}
	if got := nearDupMessages(want)["units/source"]; len(got) != 3 {
		t.Errorf("units/source near-duplicate warnings = %q, want 3", got)
	}
	for k := 0; k < 12; k++ {
		id := fmt.Sprintf("desc/v%02d", k)
		if m := collidingMessages(want)[id]; !strings.HasPrefix(m, "description overlaps desc/base (71%);") {
			t.Errorf("%s message = %q, want only desc/base", id, m)
		}
	}
}

func benchmarkRecords(n int, body func(i int, rng *rand.Rand) filesystem.ArtifactRecord) []filesystem.ArtifactRecord {
	rng := rand.New(rand.NewSource(1))
	out := make([]filesystem.ArtifactRecord, n)
	for i := range out {
		out[i] = body(i, rng)
	}
	return out
}

// BenchmarkNearDuplicateContent runs the rule over 10,000 artifacts with
// distinct 300-word bodies drawn from a 20,000-word vocabulary.
func BenchmarkNearDuplicateContent(b *testing.B) {
	records := benchmarkRecords(10000, func(i int, rng *rand.Rand) filesystem.ArtifactRecord {
		words := make([]string, 300)
		for k := range words {
			words[k] = fmt.Sprintf("v%d", rng.Intn(20000))
		}
		return contextRec(fmt.Sprintf("bench/a%05d", i), strings.Join(words, " "))
	})
	rule := ruleNearDuplicateContent{t: DefaultContentSimilarity}
	b.ResetTimer()
	for range b.N {
		rule.Check(context.Background(), nil, records)
	}
}

// BenchmarkNearDuplicateContent_SharedResource runs the rule over 10,000
// artifacts that each bundle one 1,600-word resource.
func BenchmarkNearDuplicateContent_SharedResource(b *testing.B) {
	records := sharedResourceRecords(10000, 1600)
	rule := ruleNearDuplicateContent{t: DefaultContentSimilarity}
	b.ResetTimer()
	for range b.N {
		rule.Check(context.Background(), nil, records)
	}
}
