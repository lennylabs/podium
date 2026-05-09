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
// Phase: 0
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
// Phase: 0
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
// Phase: 0
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
// Phase: 0
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

// Spec: §4.6 — sync's effective-view composition uses
// CollisionPolicyHighestWins so the highest-precedence layer wins.
// Phase: 0
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
// Phase: 0
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

// Helpers used by these tests only.

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
