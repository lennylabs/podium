package filesystem

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
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

// Spec: §4.6 — sync's effective-view composition uses
// CollisionPolicyHighestWins so the highest-precedence layer wins.
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
