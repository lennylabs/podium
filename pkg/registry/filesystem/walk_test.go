package filesystem

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
)

const skillArtifact = `---
type: skill
version: 1.0.0
---

`

const skillBody = `---
name: %s
description: %s
---

Body for %s.
`

const contextArtifact = `---
type: context
version: 1.0.0
description: %s
---

Body for %s.
`

// Spec: §4.2 Registry Layout — Walk discovers every ARTIFACT.md under each
// layer and emits records keyed by canonical artifact ID (the path under
// the layer root).
func TestWalk_FindsAllArtifactsInLayer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "greetings/hello-world/ARTIFACT.md",
			Content: skillArtifact,
		},
		testharness.WriteTreeOption{
			Path:    "greetings/hello-world/SKILL.md",
			Content: stringf(skillBody, "hello-world", "Say hi", "hello-world"),
		},
		testharness.WriteTreeOption{
			Path:    "greetings/good-morning/ARTIFACT.md",
			Content: skillArtifact,
		},
		testharness.WriteTreeOption{
			Path:    "greetings/good-morning/SKILL.md",
			Content: stringf(skillBody, "good-morning", "Morning hi", "good-morning"),
		},
		testharness.WriteTreeOption{
			Path:    "company-glossary/ARTIFACT.md",
			Content: stringf(contextArtifact, "Glossary", "glossary"),
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	wantIDs := []string{
		"company-glossary",
		"greetings/good-morning",
		"greetings/hello-world",
	}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d artifacts, want %d (%v)", len(got), len(wantIDs), idsOf(got))
	}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("Walk[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

// Spec: §4.3.4 SKILL.md compliance — type: skill artifact missing SKILL.md
// fails Walk with a structured error message.
func TestWalk_SkillMissingSkillMDFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "greetings/hello/ARTIFACT.md",
			Content: skillArtifact,
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = reg.Walk(WalkOptions{})
	if err == nil {
		t.Fatalf("expected error for missing SKILL.md")
	}
	if !strings.Contains(err.Error(), "missing SKILL.md") {
		t.Errorf("error %q missing 'missing SKILL.md'", err)
	}
}

// Spec: §4.4 Bundled Resources — files alongside ARTIFACT.md (and
// SKILL.md for skills) are captured as bundled resources.
func TestWalk_CapturesBundledResources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "finance/run-variance/ARTIFACT.md",
			Content: skillArtifact,
		},
		testharness.WriteTreeOption{
			Path:    "finance/run-variance/SKILL.md",
			Content: stringf(skillBody, "run-variance", "variance", "run-variance"),
		},
		testharness.WriteTreeOption{
			Path:    "finance/run-variance/scripts/variance.py",
			Content: "print('variance')\n",
		},
		testharness.WriteTreeOption{
			Path:    "finance/run-variance/references/explained.md",
			Content: "# explained\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(got))
	}
	rec := got[0]
	if string(rec.Resources["scripts/variance.py"]) != "print('variance')\n" {
		t.Errorf("scripts/variance.py = %q", rec.Resources["scripts/variance.py"])
	}
	if string(rec.Resources["references/explained.md"]) != "# explained\n" {
		t.Errorf("references/explained.md missing")
	}
}

// Spec: §4.6 — two layers contributing the same canonical artifact ID
// without extends: are rejected (default Walk behavior).
// Matrix: §6.10 (ingest.collision)
func TestWalk_CollisionWithoutExtendsFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/x/ARTIFACT.md",
			Content: stringf(contextArtifact, "shared", "shared"),
		},
		testharness.WriteTreeOption{
			Path:    "personal/x/ARTIFACT.md",
			Content: stringf(contextArtifact, "personal", "personal"),
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = reg.Walk(WalkOptions{})
	if err == nil {
		t.Fatalf("expected ingest.collision, got nil")
	}
	if !strings.Contains(err.Error(), "ingest.collision") {
		t.Errorf("error %q missing ingest.collision namespace", err)
	}
}

// Spec: §4.6 — "A collision is rejected at ingest unless the
// higher-precedence artifact declares extends: <lower-precedence-id>."
// The higher-precedence (personal) record at canonical ID x declares
// extends: x, so the same-ID collision is permitted and the
// higher-precedence record wins; the extends merge runs at read time.
func TestWalk_CollisionWithExtendsAllowed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/x/ARTIFACT.md",
			Content: stringf(contextArtifact, "shared parent", "shared parent"),
		},
		testharness.WriteTreeOption{
			Path:    "personal/x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 2.0.0\ndescription: overlay child\nextends: x\n---\n\noverlay body\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{})
	if err != nil {
		t.Fatalf("Walk returned %v; the extends exception should permit the same-ID overlay", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d artifacts, want 1 (the overlay)", len(got))
	}
	if got[0].Layer.ID != "personal" {
		t.Errorf("Layer.ID = %q, want personal (the higher-precedence overlay)", got[0].Layer.ID)
	}
}

// Spec: §4.6 — the extends exception is keyed on the COLLIDING id. A
// higher-precedence record that extends some other id is still a
// forbidden silent shadow of the colliding id.
func TestWalk_CollisionExtendsOtherIDStillFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/x/ARTIFACT.md",
			Content: stringf(contextArtifact, "shared", "shared"),
		},
		testharness.WriteTreeOption{
			Path:    "personal/x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 2.0.0\ndescription: shadow\nextends: some/other-id\n---\n\nbody\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := reg.Walk(WalkOptions{}); err == nil || !strings.Contains(err.Error(), "ingest.collision") {
		t.Fatalf("Walk err = %v, want ingest.collision (extends points at a different id)", err)
	}
}

// CollisionPolicyHighestWins keeps the highest-precedence record for each ID
// without checking extends:. The workspace overlay's walk of its own
// directory (pkg/overlay) uses it; §6.4 defines no multi-layer overlay, so
// this pins the policy rather than a spec rule.
func TestWalk_HighestWinsKeepsTopLayer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path: ".registry-config",
			Content: `multi_layer: true
layer_order:
  - team-shared
  - personal
`,
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/x/ARTIFACT.md",
			Content: stringf(contextArtifact, "shared", "shared"),
		},
		testharness.WriteTreeOption{
			Path:    "personal/x/ARTIFACT.md",
			Content: stringf(contextArtifact, "personal", "personal"),
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyHighestWins})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(got))
	}
	if got[0].Layer.ID != "personal" {
		t.Errorf("Layer.ID = %q, want personal (highest precedence)", got[0].Layer.ID)
	}
}

// Spec: §4.6, §13.11.3 — CollisionPolicyDrop reports each drop through
// OnCollision, so Walk refuses the policy without a callback rather than
// dropping an artifact silently. The refusal precedes the layer walk.
//
// Matrix: §6.10 (ingest.collision)
func TestWalk_DropPolicyRequiresOnCollision(t *testing.T) {
	t.Parallel()
	reg := &Registry{}
	got, err := reg.Walk(WalkOptions{CollisionPolicy: CollisionPolicyDrop})
	if err == nil || !strings.Contains(err.Error(), "CollisionPolicyDrop requires OnCollision") {
		t.Fatalf("Walk err = %v, want the missing-OnCollision refusal", err)
	}
	if got != nil {
		t.Errorf("Walk records = %v, want nil on the refusal", idsOf(got))
	}
}

// dropFixtureArtifact is a context artifact whose description names the
// contributing layer, with an optional extends: line and tags, so a test
// can tell which layer's copy survived and what an extends: merge folded in.
func dropFixtureArtifact(desc, extends string, tags ...string) string {
	var b strings.Builder
	b.WriteString("---\ntype: context\nversion: 1.0.0\ndescription: " + desc + "\n")
	if extends != "" {
		b.WriteString("extends: " + extends + "\n")
	}
	if len(tags) > 0 {
		b.WriteString("tags:\n")
		for _, tag := range tags {
			b.WriteString("  - " + tag + "\n")
		}
	}
	b.WriteString("---\n\nBody.\n")
	return b.String()
}

// openDropFixture writes a multi-layer registry whose layer_order is layers
// and whose files map "<layer>/<id>" to ARTIFACT.md content, and opens it.
func openDropFixture(t *testing.T, layers []string, files map[string]string) *Registry {
	t.Helper()
	root := t.TempDir()
	opts := []testharness.WriteTreeOption{{
		Path:    ".registry-config",
		Content: "multi_layer: true\nlayer_order:\n  - " + strings.Join(layers, "\n  - ") + "\n",
	}}
	for path, content := range files {
		opts = append(opts, testharness.WriteTreeOption{Path: path + "/ARTIFACT.md", Content: content})
	}
	testharness.WriteTree(t, root, opts...)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return reg
}

// Spec: §4.6 — under CollisionPolicyDrop an unsanctioned collision drops the
// higher-precedence record, keeps the lower one, reports the drop through
// OnCollision, and leaves every non-colliding artifact in place. A third
// layer is compared against the kept record, so its drop names the first
// contributor as ExistingLayer.
//
// Matrix: §6.10 (ingest.collision)
func TestWalk_DropPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		layers    []string
		files     map[string]string
		wantDesc  string
		wantLayer string
		wantDrops []layer.Collision
	}{
		{
			name:   "two layers without extends keep the lower copy",
			layers: []string{"l1", "l2"},
			files: map[string]string{
				"l1/x": dropFixtureArtifact("from-l1", ""),
				"l2/x": dropFixtureArtifact("from-l2", ""),
			},
			wantDesc:  "from-l1",
			wantLayer: "l1",
			wantDrops: []layer.Collision{{ArtifactID: "x", Layer: "l2", ExistingLayer: "l1"}},
		},
		{
			name:   "three layers without extends drop both higher copies",
			layers: []string{"l1", "l2", "l3"},
			files: map[string]string{
				"l1/x": dropFixtureArtifact("from-l1", ""),
				"l2/x": dropFixtureArtifact("from-l2", ""),
				"l3/x": dropFixtureArtifact("from-l3", ""),
			},
			wantDesc:  "from-l1",
			wantLayer: "l1",
			wantDrops: []layer.Collision{
				{ArtifactID: "x", Layer: "l2", ExistingLayer: "l1"},
				{ArtifactID: "x", Layer: "l3", ExistingLayer: "l1"},
			},
		},
		{
			name:   "higher copy declaring extends on the id is kept",
			layers: []string{"l1", "l2"},
			files: map[string]string{
				"l1/x": dropFixtureArtifact("from-l1", ""),
				"l2/x": dropFixtureArtifact("from-l2", "x"),
			},
			wantDesc:  "from-l2",
			wantLayer: "l2",
		},
		{
			name:   "higher copy declaring extends on another id is dropped",
			layers: []string{"l1", "l2"},
			files: map[string]string{
				"l1/x": dropFixtureArtifact("from-l1", ""),
				"l2/x": dropFixtureArtifact("from-l2", "other/id"),
			},
			wantDesc:  "from-l1",
			wantLayer: "l1",
			wantDrops: []layer.Collision{{ArtifactID: "x", Layer: "l2", ExistingLayer: "l1"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"l1/other": dropFixtureArtifact("other", "")}
			for k, v := range tc.files {
				files[k] = v
			}
			reg := openDropFixture(t, tc.layers, files)
			var drops []layer.Collision
			got, err := reg.Walk(WalkOptions{
				CollisionPolicy: CollisionPolicyDrop,
				OnCollision:     func(c layer.Collision) { drops = append(drops, c) },
			})
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}
			if !reflect.DeepEqual(drops, tc.wantDrops) {
				t.Errorf("OnCollision calls = %+v, want %+v", drops, tc.wantDrops)
			}
			byID := map[string]ArtifactRecord{}
			for _, rec := range got {
				byID[rec.ID] = rec
			}
			if len(got) != 2 {
				t.Fatalf("got %v, want x and other", idsOf(got))
			}
			if _, ok := byID["other"]; !ok {
				t.Errorf("non-colliding artifact missing from %v", idsOf(got))
			}
			x := byID["x"]
			if x.Layer.ID != tc.wantLayer || x.Artifact.Description != tc.wantDesc {
				t.Errorf("x kept from layer %q (%q), want %q (%q)",
					x.Layer.ID, x.Artifact.Description, tc.wantLayer, tc.wantDesc)
			}
		})
	}
}

// Spec: §4.6 — a record CollisionPolicyDrop drops is also removed from the
// extends: resolver's input, so a higher layer's extends: merges onto the
// kept lower copy rather than onto the dropped middle copy.
//
// Matrix: §6.10 (ingest.collision)
func TestWalk_DropPolicyDroppedRecordIsNotAParent(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2", "l3"}, map[string]string{
		"l1/x": dropFixtureArtifact("from-l1", "", "l1-tag"),
		"l2/x": dropFixtureArtifact("from-l2", "", "l2-tag"),
		"l3/x": dropFixtureArtifact("from-l3", "x", "l3-tag"),
	})
	var drops []layer.Collision
	got, err := reg.Walk(WalkOptions{
		CollisionPolicy: CollisionPolicyDrop,
		ResolveExtends:  true,
		OnCollision:     func(c layer.Collision) { drops = append(drops, c) },
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []layer.Collision{{ArtifactID: "x", Layer: "l2", ExistingLayer: "l1"}}
	if !reflect.DeepEqual(drops, want) {
		t.Errorf("OnCollision calls = %+v, want %+v", drops, want)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want x only", idsOf(got))
	}
	a, err := manifest.ParseArtifact(got[0].ArtifactBytes)
	if err != nil {
		t.Fatalf("ParseArtifact(merged): %v", err)
	}
	if a.Description != "from-l3" {
		t.Errorf("Description = %q, want from-l3", a.Description)
	}
	if !contains(a.Tags, "l1-tag") || !contains(a.Tags, "l3-tag") || contains(a.Tags, "l2-tag") {
		t.Errorf("Tags = %v, want the union of l1-tag and l3-tag without l2-tag", a.Tags)
	}
	if strings.Contains(string(got[0].ArtifactBytes), "from-l2") {
		t.Errorf("merged bytes carry the dropped copy's description:\n%s", got[0].ArtifactBytes)
	}
}

// Spec: §13.11 — single-layer mode walks the layer rooted at the registry
// path, not at any subdirectory.
func TestWalk_SingleLayerArtifactIDsAreRelativeToRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "company-glossary/ARTIFACT.md",
			Content: stringf(contextArtifact, "Glossary", "glossary"),
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(got))
	}
	if got[0].ID != "company-glossary" {
		t.Errorf("ID = %q, want company-glossary", got[0].ID)
	}
}

// Spec: §4.2 — ValidateCanonicalID enforces the shared canonical-ID
// invariants: a non-empty directory path with no "@" in any segment. Domain
// directories such as "_shared" are allowed (the spec's own layout uses one).
func TestValidateCanonicalID(t *testing.T) {
	t.Parallel()
	ok := []string{
		"finance",
		"finance/ap/pay-invoice",
		"_shared/payment-helpers/routing-validator",
		"engineering/platform/code-change-pr",
	}
	for _, id := range ok {
		if err := ValidateCanonicalID(id); err != nil {
			t.Errorf("ValidateCanonicalID(%q) = %v, want nil", id, err)
		}
	}
	bad := map[string]string{
		"":              "subdirectory",
		"pay@v2":        "@",
		"finance/p@y/x": "@",
		"a//b":          "empty path segment",
	}
	for id, want := range bad {
		err := ValidateCanonicalID(id)
		if err == nil {
			t.Errorf("ValidateCanonicalID(%q) = nil, want error containing %q", id, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateCanonicalID(%q) = %v, want substring %q", id, err, want)
		}
	}
}

// Spec: §4.2 — a root-level ARTIFACT.md has no directory path under the layer
// root, so Walk rejects it (an empty canonical ID is unaddressable).
func TestWalk_RootLevelArtifactRejected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "ARTIFACT.md",
			Content: stringf(contextArtifact, "Root", "root"),
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err = reg.Walk(WalkOptions{}); err == nil {
		t.Fatalf("expected error for root-level ARTIFACT.md")
	} else if !strings.Contains(err.Error(), "subdirectory") {
		t.Errorf("error %q missing 'subdirectory'", err)
	}
}

// Spec: §4.2 — "@" is reserved as the reference version/hash delimiter, so a
// directory name containing "@" is an invalid canonical-ID segment.
func TestWalk_AtSegmentRejected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "finance/pay@v2/ARTIFACT.md",
			Content: stringf(contextArtifact, "Pay", "pay"),
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err = reg.Walk(WalkOptions{}); err == nil {
		t.Fatalf("expected error for '@' in a canonical-ID segment")
	} else if !strings.Contains(err.Error(), "@") {
		t.Errorf("error %q missing '@'", err)
	}
}

// Spec: §4.2/§4.4 — a nested artifact package is its own leaf. Its files,
// including its own ARTIFACT.md, are not captured as the parent's bundled
// resources; the nested artifact is discovered as a separate record.
func TestWalk_NestedArtifactNotCapturedAsResource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "outer/ARTIFACT.md",
			Content: stringf(contextArtifact, "Outer", "outer"),
		},
		testharness.WriteTreeOption{
			Path:    "outer/notes.md",
			Content: "outer notes\n",
		},
		testharness.WriteTreeOption{
			Path:    "outer/inner/ARTIFACT.md",
			Content: stringf(contextArtifact, "Inner", "inner"),
		},
		testharness.WriteTreeOption{
			Path:    "outer/inner/data.txt",
			Content: "inner data\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	byID := map[string]ArtifactRecord{}
	for _, r := range got {
		byID[r.ID] = r
	}
	outer, ok := byID["outer"]
	if !ok {
		t.Fatalf("missing outer record (got %v)", idsOf(got))
	}
	if _, ok := byID["outer/inner"]; !ok {
		t.Fatalf("missing nested outer/inner record (got %v)", idsOf(got))
	}
	if _, ok := outer.Resources["notes.md"]; !ok {
		t.Errorf("outer missing its own resource notes.md: %v", resourceKeys(outer.Resources))
	}
	for k := range outer.Resources {
		if strings.HasPrefix(k, "inner/") {
			t.Errorf("outer captured nested-artifact file %q as a resource", k)
		}
	}
	if _, ok := byID["outer/inner"].Resources["data.txt"]; !ok {
		t.Errorf("nested artifact missing its resource data.txt: %v", resourceKeys(byID["outer/inner"].Resources))
	}
}

// Helpers used by these tests only.

func resourceKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func idsOf(records []ArtifactRecord) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.ID
	}
	return out
}

// stringf is a tiny fmt.Sprintf without importing fmt at the test level
// (kept stylistic; not load-bearing).
func stringf(format string, args ...string) string {
	return formatStrings(format, args...)
}

func formatStrings(format string, args ...string) string {
	out := format
	i := 0
	for {
		idx := indexPercent(out, "%s")
		if idx < 0 || i >= len(args) {
			break
		}
		out = out[:idx] + args[i] + out[idx+2:]
		i++
	}
	return out
}

func indexPercent(s, target string) int {
	for i := 0; i+len(target) <= len(s); i++ {
		if s[i:i+len(target)] == target {
			return i
		}
	}
	return -1
}

// TestWalk_ResourceSetIsEveryFileUnderThePackageRoot covers
// spec: §4.4, §4.7.6 — the bundled-resource set is every file under the
// package root, dot-prefixed names included, other than the package root's
// ARTIFACT.md, the package root's SKILL.md for a skill, and the files of a
// nested package. The complete key set is asserted, rather than the absence
// of one prefix, so a hidden-name skip or a base-name SKILL.md skip added to
// captureResources fails here instead of silently moving every stored hash.
func TestWalk_ResourceSetIsEveryFileUnderThePackageRoot(t *testing.T) {
	t.Parallel()
	const crlfNotes = "line one\r\nline two\r\n"
	const hiddenNote = "hidden note body\n"
	const toolingConfig = "{\"tool\":\"config\"}\n"
	const referencesSkill = "reference skill body\n"
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "outer/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{
			Path:    "outer/SKILL.md",
			Content: stringf(skillBody, "outer", "Outer skill", "outer"),
		},
		testharness.WriteTreeOption{Path: "outer/notes.md", Content: crlfNotes},
		testharness.WriteTreeOption{Path: "outer/.hidden-note", Content: hiddenNote},
		testharness.WriteTreeOption{Path: "outer/.tooling/config.json", Content: toolingConfig},
		testharness.WriteTreeOption{Path: "outer/references/SKILL.md", Content: referencesSkill},
		testharness.WriteTreeOption{
			Path:    "outer/inner/ARTIFACT.md",
			Content: stringf(contextArtifact, "Inner", "inner"),
		},
		testharness.WriteTreeOption{Path: "outer/inner/data.txt", Content: "inner data\n"},
		testharness.WriteTreeOption{
			Path:    "outer/.nested/ARTIFACT.md",
			Content: stringf(contextArtifact, "Nested", "nested"),
		},
		testharness.WriteTreeOption{Path: "outer/.nested/note.txt", Content: "nested note\n"},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := reg.Walk(WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	byID := map[string]ArtifactRecord{}
	for _, r := range got {
		byID[r.ID] = r
	}
	outer, ok := byID["outer"]
	if !ok {
		t.Fatalf("missing outer record (got %v)", idsOf(got))
	}
	want := map[string]string{
		"notes.md":             crlfNotes,
		".hidden-note":         hiddenNote,
		".tooling/config.json": toolingConfig,
		"references/SKILL.md":  referencesSkill,
	}
	if len(outer.Resources) != len(want) {
		t.Fatalf("outer resource keys = %v, want %v", resourceKeys(outer.Resources), resourceKeys(toBytes(want)))
	}
	for k, v := range want {
		body, ok := outer.Resources[k]
		if !ok {
			t.Errorf("outer missing resource %q (got %v)", k, resourceKeys(outer.Resources))
			continue
		}
		if string(body) != v {
			t.Errorf("outer resource %q = %q, want %q", k, body, v)
		}
	}
	if string(outer.SkillBytes) != stringf(skillBody, "outer", "Outer skill", "outer") {
		t.Errorf("outer SkillBytes = %q, want the package root's SKILL.md", outer.SkillBytes)
	}
	inner, ok := byID["outer/inner"]
	if !ok {
		t.Fatalf("missing nested outer/inner record (got %v)", idsOf(got))
	}
	if len(inner.Resources) != 1 || string(inner.Resources["data.txt"]) != "inner data\n" {
		t.Errorf("outer/inner resources = %v, want exactly data.txt", resourceKeys(inner.Resources))
	}
	if _, ok := byID["outer/.nested"]; ok {
		t.Errorf("a dot-prefixed directory was discovered as an artifact (got %v)", idsOf(got))
	}
}

func toBytes(m map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = []byte(v)
	}
	return out
}

// Spec: §4.6 — a record with no parsed ARTIFACT.md declares no extends:, so
// the collision check treats it as an unsanctioned overlay.
func TestExtendsOf(t *testing.T) {
	t.Parallel()
	if got := extendsOf(ArtifactRecord{}); got != "" {
		t.Errorf("extendsOf(no artifact) = %q, want empty", got)
	}
	rec := ArtifactRecord{Artifact: &manifest.Artifact{Extends: "x@1.0.0"}}
	if got := extendsOf(rec); got != "x@1.0.0" {
		t.Errorf("extendsOf = %q, want x@1.0.0", got)
	}
}
