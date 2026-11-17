package sync

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/spi"
)

// invocationSkillMD is a plain SKILL.md whose description is longer than the
// max_entry_chars the listing tests set. meta is the metadata.invocation
// value, or empty for none.
func invocationSkillMD(name, meta string) string {
	md := "---\nname: " + name + "\ndescription: The " + name + " skill describes itself at length.\n"
	if meta != "" {
		md += "metadata:\n  invocation: \"" + meta + "\"\n"
	}
	return md + "---\n\nBody.\n"
}

// synthesizedSkillFM is the ARTIFACT.md the registry serves for a plain skill.
const synthesizedSkillFM = "---\ntype: skill\nversion: 0.0.0-auto.0\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"

// listingInvocationBudget counts skills only and flags every description
// longer than 10 code points.
func listingInvocationBudget() EffectiveListingBudget {
	return budget(func(b *EffectiveListingBudget) {
		b.Types = []string{"skill"}
		b.MaxEntryChars = 10
	})
}

// assertTwoListingEntries checks that only model and upper, the skills whose
// resolved invocation is any, are listing entries.
func assertTwoListingEntries(t *testing.T, harness string, res *Result) {
	t.Helper()
	rep := Listing(res, listingInvocationBudget())
	if rep == nil {
		t.Fatalf("%s: no listing report", harness)
	}
	cost := 0
	for _, name := range []string{"model", "upper"} {
		cost += len(name) + len("The "+name+" skill describes itself at length.") + DefaultListingBudget().EntryOverheadChars
	}
	var oversized []string
	for _, o := range rep.Oversized {
		oversized = append(oversized, o.ID)
	}
	if rep.Entries != 2 || rep.CostChars != cost || !reflect.DeepEqual(oversized, []string{"s/model", "s/upper"}) {
		t.Errorf("%s: listing = entries %d, cost %d, oversized %v; want 2, %d, [s/model s/upper]",
			harness, rep.Entries, rep.CostChars, oversized, cost)
	}
}

// Spec: §7.5.8, §4.3.4 — a skill whose resolved invocation is user is never a
// listing entry on any adapter, whether the field is stated in SKILL.md
// metadata or in ARTIFACT.md. An invalid value such as User resolves to any
// and counts. The registry-backed record resolves the same way.
func TestListing_UserInvokedSkillNotCounted(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/s/model/SKILL.md", Content: invocationSkillMD("model", "")},
		testharness.WriteTreeOption{Path: "team/s/userm/SKILL.md", Content: invocationSkillMD("userm", "user")},
		testharness.WriteTreeOption{Path: "team/s/usera/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\ninvocation: user\n---\n"},
		testharness.WriteTreeOption{Path: "team/s/usera/SKILL.md", Content: invocationSkillMD("usera", "")},
		testharness.WriteTreeOption{Path: "team/s/upper/SKILL.md", Content: invocationSkillMD("upper", "User")},
	)
	for _, harness := range []string{"opencode", "gemini", "claude-code", "codex"} {
		res, err := Run(Options{RegistryPath: registry, AdapterID: harness, DryRun: true})
		if err != nil {
			t.Fatalf("%s Run: %v", harness, err)
		}
		assertTwoListingEntries(t, harness, res)
	}

	srv := newStubRegistry(t, map[string]stubArtifact{
		"s/model": {typ: "skill", layer: "team", frontmatter: synthesizedSkillFM, skillRaw: invocationSkillMD("model", "")},
		"s/userm": {typ: "skill", layer: "team", frontmatter: synthesizedSkillFM, skillRaw: invocationSkillMD("userm", "user")},
		"s/usera": {typ: "skill", layer: "team", frontmatter: "---\ntype: skill\nversion: 1.0.0\ninvocation: user\n---\n", skillRaw: invocationSkillMD("usera", "")},
		"s/upper": {typ: "skill", layer: "team", frontmatter: synthesizedSkillFM, skillRaw: invocationSkillMD("upper", "User")},
	})
	res, err := Run(Options{RegistryPath: srv.URL, Delivery: neverDelivery, Target: t.TempDir(), AdapterID: "opencode", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	assertTwoListingEntries(t, "opencode (server source)", res)
}

// Spec: §6.9, §4.3.4 — the registry-backed record resolves a plain skill's
// metadata.invocation, so the guard names invocation: user beside type: skill
// on a harness where both cells are ✗, in the message and in
// details.unsupported.
func TestRun_ServerSourceGuardNamesInvocation(t *testing.T) {
	t.Parallel()
	srv := newStubRegistry(t, map[string]stubArtifact{
		"ops/ping": {typ: "skill", layer: "team", frontmatter: synthesizedSkillFM, skillRaw: invocationSkillMD("ping", "user")},
	})
	_, err := Run(Options{RegistryPath: srv.URL, Delivery: neverDelivery, Target: t.TempDir(), AdapterID: "hermes", HTTPClient: srv.Client()})
	var serr *spi.Error
	if !errors.As(err, &serr) {
		t.Fatalf("Run error = %v, want a materialize.untranslatable *spi.Error", err)
	}
	want := []string{"invocation: user", "type: skill"}
	if got := serr.Details["unsupported"]; !reflect.DeepEqual(got, want) {
		t.Errorf("details.unsupported = %#v, want %v", got, want)
	}
	if !strings.Contains(err.Error(), "invocation: user, type: skill") {
		t.Errorf("message = %q, want it to name invocation: user, type: skill", err)
	}
}
