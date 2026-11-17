package sync

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
)

// listingSkills is the TestListing_Cost fixture. Each entry's cost under the
// default overhead is the code points of the last ID segment plus those of
// the description plus 5:
//
//	a/alpha  "Draft invoices."      5 + 15 + 5 = 25
//	b/beta   "Résumé builder ✓"     4 + 16 + 5 = 25 (20 bytes)
//	gamma    ""                     5 +  0 + 5 = 10
//	c/delta  agent "Agent."         5 +  6 + 5 = 16
func listingSkills() *Result {
	return &Result{
		Adapter: "claude-code",
		Artifacts: []ArtifactResult{
			{ID: "a/alpha", ListingType: "skill", ListingDescription: "Draft invoices."},
			{ID: "b/beta", ListingType: "skill", ListingDescription: "Résumé builder ✓"},
			{ID: "gamma", ListingType: "skill", ListingDescription: ""},
			{ID: "c/delta", ListingType: "agent", ListingDescription: "Agent."},
		},
	}
}

// budget returns the §7.5.8 defaults of an adapter other than claude-code,
// which leave max_entry_chars unset, with the given overrides applied.
func budget(mut func(*EffectiveListingBudget)) EffectiveListingBudget {
	b := DefaultListingBudget("cursor")
	if mut != nil {
		mut(&b)
	}
	return b
}

// Spec: §7.5.8 — the listing cost is the sum over the entries of the code
// points of the name (the last ID segment), the code points of the
// description, and entry_overhead_chars; the run reports an excess only when
// the cost exceeds max_chars.
func TestListing_Cost(t *testing.T) {
	t.Parallel()
	res := listingSkills()

	rep := Listing(res, budget(nil))
	if rep == nil || rep.CostChars != 60 || rep.Entries != 3 {
		t.Fatalf("skills cost = %+v, want 60 over 3 entries", rep)
	}
	if rep.Harness != "claude-code" || rep.BudgetChars != 8000 || rep.Check != "warning" {
		t.Errorf("report header = %+v", rep)
	}

	withAgent := Listing(res, budget(func(b *EffectiveListingBudget) { b.Types = []string{"skill", "agent"} }))
	if withAgent.CostChars != 76 || withAgent.Entries != 4 {
		t.Errorf("skill+agent cost = %d over %d, want 76 over 4", withAgent.CostChars, withAgent.Entries)
	}

	atBudget := Listing(res, budget(func(b *EffectiveListingBudget) { b.MaxChars = 60; b.Check = ListingCheckError }))
	if atBudget.Over || atBudget.Failed() {
		t.Errorf("cost equal to max_chars: Over=%v Failed=%v, want both false", atBudget.Over, atBudget.Failed())
	}
	over := Listing(res, budget(func(b *EffectiveListingBudget) { b.MaxChars = 59 }))
	if !over.Over {
		t.Errorf("cost one above max_chars: Over = false, want true")
	}

	overhead := Listing(res, budget(func(b *EffectiveListingBudget) { b.EntryOverheadChars = 10 }))
	if overhead.CostChars != 60+3*5 {
		t.Errorf("entry_overhead_chars 10: cost = %d, want %d", overhead.CostChars, 60+3*5)
	}
}

// Spec: §7.5.8 — the defaults are max_chars 8000, entry_overhead_chars 5,
// types [skill], and check warning, for every adapter. max_entry_chars is
// 1536 for claude-code and unset for every other adapter.
func TestDefaultListingBudget(t *testing.T) {
	t.Parallel()
	want := EffectiveListingBudget{MaxChars: 8000, EntryOverheadChars: 5, MaxEntryChars: 0, Types: []string{"skill"}, Check: "warning"}
	wantClaude := want
	wantClaude.MaxEntryChars = 1536
	for _, id := range []string{"cursor", "codex", "gemini", "opencode", "pi"} {
		if got := DefaultListingBudget(id); !reflect.DeepEqual(got, want) {
			t.Errorf("DefaultListingBudget(%s) = %+v, want %+v", id, got, want)
		}
	}
	if got := DefaultListingBudget("claude-code"); !reflect.DeepEqual(got, wantClaude) {
		t.Errorf("DefaultListingBudget(claude-code) = %+v, want %+v", got, wantClaude)
	}
	reg := adapter.DefaultRegistry()
	var nilCfg *MergedConfig
	got, err := nilCfg.ListingBudgetFor("claude-code", reg)
	if err != nil || !reflect.DeepEqual(got, wantClaude) {
		t.Errorf("nil config: %+v, %v; want %+v", got, err, wantClaude)
	}
	got, err = nilCfg.ListingBudgetFor("cursor", reg)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("nil config, cursor: %+v, %v; want %+v", got, err, want)
	}
	max := 100
	other := &MergedConfig{ListingBudgets: map[string]ListingBudget{"codex": {MaxChars: &max}}}
	got, err = other.ListingBudgetFor("claude-code", reg)
	if err != nil || !reflect.DeepEqual(got, wantClaude) {
		t.Errorf("entry for codex only: %+v, %v; want %+v", got, err, wantClaude)
	}
}

// Spec: §7.5.8 — under the claude-code defaults an entry whose description is
// longer than 1536 code points is reported as oversized, and one at 1536 is not.
func TestListing_ClaudeCodeDefaultEntryCap(t *testing.T) {
	t.Parallel()
	res := &Result{Adapter: "claude-code", Artifacts: []ArtifactResult{
		{ID: "team/long", ListingType: "skill", ListingDescription: strings.Repeat("a", 1537)},
		{ID: "team/edge", ListingType: "skill", ListingDescription: strings.Repeat("a", 1536)},
	}}
	rep := Listing(res, DefaultListingBudget("claude-code"))
	if rep == nil || len(rep.Oversized) != 1 || rep.Oversized[0].ID != "team/long" || rep.Oversized[0].Chars != 1537 {
		t.Fatalf("Listing oversized = %+v, want only team/long at 1537", rep)
	}
}

// Spec: §7.5.8 — no listing under check: off, for the none adapter, for an
// adapter for which no type in types counts, and for an offline-first run.
func TestListing_NilCases(t *testing.T) {
	t.Parallel()
	types := func(ts ...string) EffectiveListingBudget {
		return budget(func(b *EffectiveListingBudget) { b.Types = ts })
	}
	nilCases := []struct {
		name    string
		res     *Result
		b       EffectiveListingBudget
		wantNil bool
	}{
		{"nil result", nil, budget(nil), true},
		{"check off", &Result{Adapter: "claude-code"}, budget(func(b *EffectiveListingBudget) { b.Check = "off" }), true},
		{"none", &Result{Adapter: "none"}, budget(nil), true},
		{"claude-desktop skill", &Result{Adapter: "claude-desktop"}, types("skill"), true},
		{"claude-cowork skill", &Result{Adapter: "claude-cowork"}, types("skill"), true},
		{"hermes skill", &Result{Adapter: "hermes"}, types("skill"), true},
		{"opencode hook", &Result{Adapter: "opencode"}, types("hook"), true},
		{"claude-desktop rule", &Result{Adapter: "claude-desktop"}, types("rule"), true},
		{"offline", &Result{Adapter: "claude-code", Offline: true}, budget(nil), true},
		{"claude-code empty", &Result{Adapter: "claude-code"}, budget(nil), false},
		{"claude-code rule", &Result{Adapter: "claude-code"}, types("rule"), false},
		{"claude-code hook", &Result{Adapter: "claude-code"}, types("hook"), false},
	}
	for _, tc := range nilCases {
		got := Listing(tc.res, tc.b)
		if (got == nil) != tc.wantNil {
			t.Errorf("%s: Listing = %+v, want nil=%v", tc.name, got, tc.wantNil)
		}
	}
}

// Spec: §7.5.8, §6.7.1 — a type counts for an adapter unless the matrix
// grades it ✗; a type the matrix does not grade counts everywhere.
func TestListing_CountsOnlyWrittenTypes(t *testing.T) {
	t.Parallel()
	hermes := &Result{
		Adapter: "hermes",
		Artifacts: []ArtifactResult{
			{ID: "s/skill-one", ListingType: "skill", ListingDescription: "Skill."},
			{ID: "r/rule-one", ListingType: "rule", ListingDescription: "Rule."},
		},
	}
	rep := Listing(hermes, budget(func(b *EffectiveListingBudget) { b.Types = []string{"skill", "rule"} }))
	if rep == nil || rep.Entries != 1 || rep.CostChars != len("rule-one")+len("Rule.")+5 {
		t.Errorf("hermes skill+rule = %+v, want the rule alone", rep)
	}

	ext := &Result{
		Adapter:   "claude-code",
		Artifacts: []ArtifactResult{{ID: "w/flow", ListingType: "workflow", ListingDescription: "Flow."}},
	}
	rep = Listing(ext, budget(func(b *EffectiveListingBudget) { b.Types = []string{"workflow"} }))
	if rep == nil || rep.Entries != 1 {
		t.Errorf("extension type on claude-code = %+v, want one entry", rep)
	}
	if !typeCounts("skill", "acme-harness") {
		t.Errorf("a custom adapter outside the matrix must count every type")
	}
}

// Spec: §7.5.8 — with max_entry_chars set, each entry whose description has
// more code points than it is reported, in identifier order.
func TestListing_Oversized(t *testing.T) {
	t.Parallel()
	// Multi-byte runes make a byte count exceed the code-point count.
	long, exact := strings.Repeat("é", 101), strings.Repeat("é", 100)
	res := &Result{
		Adapter: "claude-code",
		Artifacts: []ArtifactResult{
			{ID: "z/long", ListingType: "skill", ListingDescription: long},
			{ID: "a/long", ListingType: "skill", ListingDescription: long},
			{ID: "m/exact", ListingType: "skill", ListingDescription: exact},
		},
	}
	unset := Listing(res, budget(nil))
	if len(unset.Oversized) != 0 || unset.MaxEntryChars != 0 {
		t.Errorf("max_entry_chars unset: %+v", unset)
	}
	set := Listing(res, budget(func(b *EffectiveListingBudget) { b.MaxEntryChars = 100 }))
	want := []OversizedEntry{{ID: "a/long", Chars: 101}, {ID: "z/long", Chars: 101}}
	if !reflect.DeepEqual(set.Oversized, want) {
		t.Errorf("Oversized = %+v, want %+v", set.Oversized, want)
	}
	if set.MaxEntryChars != 100 {
		t.Errorf("MaxEntryChars = %d, want 100", set.MaxEntryChars)
	}
}

// Spec: §7.5.8 — only check: error with an excess or an oversized entry fails
// the run; a nil report neither reports nor fails.
func TestListing_FailedAndReported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rep              *ListingReport
		reported, failed bool
	}{
		{nil, false, false},
		{&ListingReport{Check: "error"}, false, false},
		{&ListingReport{Check: "error", Over: true}, true, true},
		{&ListingReport{Check: "error", Oversized: []OversizedEntry{{ID: "a", Chars: 2}}}, true, true},
		{&ListingReport{Check: "warning", Over: true}, true, false},
		{&ListingReport{Check: "warning", Oversized: []OversizedEntry{{ID: "a", Chars: 2}}}, true, false},
	}
	for i, tc := range cases {
		if got := tc.rep.Reported(); got != tc.reported {
			t.Errorf("case %d: Reported = %v, want %v", i, got, tc.reported)
		}
		if got := tc.rep.Failed(); got != tc.failed {
			t.Errorf("case %d: Failed = %v, want %v", i, got, tc.failed)
		}
	}
}

// Spec: §7.5.8 — a skill lists its SKILL.md description, which can differ
// from its ARTIFACT.md description, and every other type lists its
// ARTIFACT.md description.
func TestRun_ListingDescription(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/docs/writer/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\ndescription: Artifact-level description.\n---\n\n"},
		testharness.WriteTreeOption{Path: "team/docs/writer/SKILL.md",
			Content: "---\nname: writer\ndescription: Skill-level description.\n---\n\nBody.\n"},
		testharness.WriteTreeOption{Path: "team/ops/pager/ARTIFACT.md",
			Content: "---\ntype: agent\nversion: 1.0.0\ndescription: Page the on-call engineer.\n---\n\nAgent body.\n"},
		testharness.WriteTreeOption{Path: "team/ops/style/ARTIFACT.md",
			Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\ndescription: Follow the house style.\n---\n\nRule body.\n"},
	)
	res, err := Run(Options{RegistryPath: registry, AdapterID: "claude-code", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string][2]string{}
	for _, a := range res.Artifacts {
		got[a.ID] = [2]string{a.ListingType, a.ListingDescription}
	}
	want := map[string][2]string{
		"docs/writer": {"skill", "Skill-level description."},
		"ops/pager":   {"agent", "Page the on-call engineer."},
		"ops/style":   {"rule", "Follow the house style."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listing entries = %v, want %v", got, want)
	}
}

// Spec: §7.5.8 — a record whose ARTIFACT.md does not parse still lists as a
// skill when its SKILL.md parses; a skill whose SKILL.md does not parse lists
// an empty description; a record with neither counts as no type.
func TestListingEntry_UnparsedManifests(t *testing.T) {
	t.Parallel()
	skill := []byte("---\nname: writer\ndescription: Skill-level description.\n---\n\nBody.\n")
	cases := []struct {
		name     string
		rec      materialRecord
		typ, des string
	}{
		{"artifact unparsed, skill parses", materialRecord{SkillBytes: skill}, "skill", "Skill-level description."},
		{"skill unparsed", materialRecord{Artifact: &manifest.Artifact{Type: manifest.TypeSkill}, SkillBytes: []byte("no frontmatter")}, "skill", ""},
		{"neither parses", materialRecord{}, "", ""},
	}
	for _, tc := range cases {
		typ, des := listingEntry(tc.rec)
		if typ != tc.typ || des != tc.des {
			t.Errorf("%s: listingEntry = %q, %q; want %q, %q", tc.name, typ, des, tc.typ, tc.des)
		}
	}
}

// Spec: §7.5.2 — each key-level invalid form, an entry for none, and an entry
// for an unregistered adapter wrap ErrInvalidListingBudget and name the
// adapter and, for a key, the key. An adapter with no entry takes the
// defaults, and an invalid entry for another adapter does not affect it.
func TestListingBudgetFor_Invalid(t *testing.T) {
	t.Parallel()
	reg := adapter.DefaultRegistry()
	zero, empty, maybe := 0, "", "maybe"
	cases := []struct {
		lb  ListingBudget
		key string
	}{
		{ListingBudget{MaxChars: &zero}, "max_chars"},
		{ListingBudget{EntryOverheadChars: &zero}, "entry_overhead_chars"},
		{ListingBudget{MaxEntryChars: &zero}, "max_entry_chars"},
		{ListingBudget{Types: []string{}}, "types"},
		{ListingBudget{Check: &maybe}, "check"},
		{ListingBudget{Check: &empty}, "check"},
	}
	for _, tc := range cases {
		m := &MergedConfig{ListingBudgets: map[string]ListingBudget{"claude-code": tc.lb}}
		_, err := m.ListingBudgetFor("claude-code", reg)
		assertInvalidBudget(t, err, "listing_budgets.claude-code."+tc.key)
	}

	ws := t.TempDir()
	writeScope(t, ws, "sync.yaml", "listing_budgets:\n  claude-code:\n    check: error\n")
	writeScope(t, ws, "sync.local.yaml", "listing_budgets:\n  claude-code:\n    check: \"\"\n")
	merged, _, err := LoadMergedConfig(ws, "")
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	_, err = merged.ListingBudgetFor("claude-code", reg)
	assertInvalidBudget(t, err, "listing_budgets.claude-code.check")

	max := 100
	m := &MergedConfig{ListingBudgets: map[string]ListingBudget{
		"none":         {MaxChars: &max},
		"acme-harness": {MaxChars: &max},
		"codex":        {Check: &maybe},
	}}
	_, err = m.ListingBudgetFor("none", reg)
	assertInvalidBudget(t, err, "listing_budgets.none: the none adapter feeds no harness listing")
	_, err = m.ListingBudgetFor("acme-harness", reg)
	assertInvalidBudget(t, err, "listing_budgets.acme-harness: no adapter of that name is registered")

	clean := &MergedConfig{ListingBudgets: map[string]ListingBudget{"codex": {Check: &maybe}}}
	for _, id := range []string{"acme-harness", "claude-code"} {
		got, err := clean.ListingBudgetFor(id, nil)
		if err != nil || !reflect.DeepEqual(got, DefaultListingBudget(id)) {
			t.Errorf("ListingBudgetFor(%q) with no entry = %+v, %v; want the defaults", id, got, err)
		}
	}
}

// Spec: §7.5.2 — the block and each entry must be mappings, a null block or
// entry sets nothing, and each mistyped value is named by its resolved YAML
// type.
func TestListingBudgets_TypeErrors(t *testing.T) {
	t.Parallel()
	errCases := map[string]string{
		"listing_budgets: abc\n":                                             "want a mapping keyed by adapter name, got a string",
		"listing_budgets: [1]\n":                                             "got a list",
		"listing_budgets: {claude-code: 5}\n":                                "claude-code: want a mapping, got an integer",
		"listing_budgets: {claude-code: {max_chars: 9223372036854775808}}\n": "claude-code.max_chars: integer 9223372036854775808 is out of range",
		"listing_budgets: {claude-code: {max_chars: 2001-12-14}}\n":          "claude-code.max_chars: want an integer, got a timestamp",
		"listing_budgets: {claude-code: {max_chars: {a: 1}}}\n":              "claude-code.max_chars: want an integer, got a mapping",
		"listing_budgets: {claude-code: {max_chars: true}}\n":                "got a boolean",
		"listing_budgets: {claude-code: {max_chars: 1.5}}\n":                 "got a float",
		"listing_budgets: {claude-code: {check: [a]}}\n":                     "claude-code.check: want a string, got a list",
		"listing_budgets: {claude-code: {types: [~]}}\n":                     "claude-code.types: want a list of strings, got an item that is null",
		"listing_budgets: {claude-code: {check: !!binary b2Zm}}\n":           "claude-code.check: want a string, got a binary value",
		"listing_budgets: {claude-code: {types: [!!binary c2tpbGw=]}}\n":     "claude-code.types: want a list of strings, got an item that is a binary value",
		"listing_budgets: {claude-code: {check: !local off}}\n":              "claude-code.check: want a string, got a value tagged !local",
		"listing_budgets: {claude-code: {}, claude-code: {}}\n":              "already defined",
		"listing_budgets: {claude-code: {check: off, check: off}}\n":         "claude-code: yaml: unmarshal errors",
	}
	for doc, want := range errCases {
		cfg := &SyncConfig{}
		if err := yaml.Unmarshal([]byte(doc), cfg); err != nil {
			t.Fatalf("%q: Unmarshal: %v", doc, err)
		}
		_, err := cfg.listingBudgets()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: listingBudgets error = %v, want %q", doc, err, want)
		}
	}
	for _, doc := range []string{"listing_budgets:\n", "listing_budgets: {claude-code: ~}\n", "defaults: {}\n"} {
		cfg := &SyncConfig{}
		if err := yaml.Unmarshal([]byte(doc), cfg); err != nil {
			t.Fatalf("%q: Unmarshal: %v", doc, err)
		}
		got, err := cfg.listingBudgets()
		if err != nil {
			t.Errorf("%q: listingBudgets error = %v", doc, err)
		}
		for id, lb := range got {
			if !reflect.DeepEqual(lb, ListingBudget{}) {
				t.Errorf("%q: entry %s = %+v, want no key set", doc, id, lb)
			}
		}
	}
	// A key that is not a string names an entry by its scalar text, and a
	// collection key, which has no scalar text, by its type and line.
	cfg := &SyncConfig{}
	if err := yaml.Unmarshal([]byte("listing_budgets:\n  1: ~\n  ? [a]\n  : ~\n"), cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, err := cfg.listingBudgets()
	if err != nil {
		t.Fatalf("listingBudgets: %v", err)
	}
	for _, id := range []string{"1", "a list at line 3"} {
		if _, ok := got[id]; !ok {
			t.Errorf("listingBudgets = %v, want an entry keyed %q", got, id)
		}
	}
}

// assertInvalidBudget fails unless err wraps ErrInvalidListingBudget and
// carries want.
func assertInvalidBudget(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, ErrInvalidListingBudget) {
		t.Errorf("error = %v, want ErrInvalidListingBudget naming %q", err, want)
		return
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to name %q", err, want)
	}
}

// writeScope writes a §7.5.2 scope file under ws/.podium.
func writeScope(t *testing.T, ws, name, content string) {
	t.Helper()
	dir := filepath.Join(ws, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
