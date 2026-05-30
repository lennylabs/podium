package adapter

import (
	"strings"
	"testing"
)

// allAdapterIDs lists the §6.7 built-in adapter values that ship in
// DefaultRegistry. Used by the conformance tests below.
var allAdapterIDs = []string{
	"none",
	"claude-code",
	"claude-desktop",
	"claude-cowork",
	"cursor",
	"codex",
	"gemini",
	"opencode",
	"pi",
	"hermes",
}

// Spec: §6.7 Harness Adapters — DefaultRegistry exposes every built-in
// adapter listed in the §6.7 table.
func TestDefaultRegistry_AllAdaptersRegistered(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	for _, id := range allAdapterIDs {
		a, err := r.Get(id)
		if err != nil {
			t.Errorf("Get(%q): %v", id, err)
			continue
		}
		if a.ID() != id {
			t.Errorf("Get(%q).ID() = %q", id, a.ID())
		}
	}
}

// Spec: §6.7 — every built-in adapter produces output for a basic
// context-type artifact and emits files in deterministic order.
func TestEveryAdapter_ProducesDeterministicOutput(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "company/glossary",
		ArtifactBytes: []byte("---\ntype: context\nversion: 1.0.0\n---\n\nbody\n"),
		Resources: map[string][]byte{
			"r/x.md": []byte("x"),
			"r/y.md": []byte("y"),
		},
	}
	r := DefaultRegistry()
	for _, id := range allAdapterIDs {
		a, err := r.Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		runs := make([][]File, 5)
		for i := 0; i < 5; i++ {
			runs[i], err = a.Adapt(src)
			if err != nil {
				t.Fatalf("%s.Adapt: %v", id, err)
			}
		}
		// All runs must produce the same file paths in the same order.
		ref := paths(runs[0])
		for i := 1; i < len(runs); i++ {
			got := paths(runs[i])
			if !equalSlices(ref, got) {
				t.Errorf("%s: non-deterministic output: %v vs %v", id, ref, got)
			}
		}
	}
}

// Spec: §6.7 — the rule-aware adapters write type: rule artifacts to
// the adapter-native rules directory.
// Matrix: §4.3 (always, claude-code)
// Matrix: §4.3 (explicit, cursor)
// Matrix: §4.3 (explicit, opencode)
// Matrix: §4.3 (explicit, pi)
// Matrix: §4.3 (explicit, hermes)
// Matrix: §4.3 (always, codex)
func TestRuleAdapters_PlaceUnderNativeRulesDir(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "ts-style",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nrules\n"),
	}
	want := map[string]string{
		"claude-code": ".claude/rules/ts-style.md",
		"codex":       ".codex/rules/ts-style.md",
		"cursor":      ".cursor/rules/ts-style.mdc",
		"opencode":    ".opencode/rules/ts-style.md",
		"pi":          ".pi/rules/ts-style.md",
		"hermes":      ".claude/rules/ts-style.md",
	}
	r := DefaultRegistry()
	for id, expected := range want {
		a, err := r.Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		out, err := a.Adapt(src)
		if err != nil {
			t.Fatalf("%s.Adapt: %v", id, err)
		}
		found := false
		for _, f := range out {
			if f.Path == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: expected rule at %q, got: %v", id, expected, paths(out))
		}
	}
}

// Spec: §6.7 — every adapter rejects nothing; even an empty Source
// produces zero or more files without erroring.
func TestEveryAdapter_DoesNotErrorOnEmptySource(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	for _, id := range allAdapterIDs {
		a, _ := r.Get(id)
		if _, err := a.Adapt(Source{ArtifactID: "x", ArtifactBytes: []byte{}}); err != nil {
			t.Errorf("%s.Adapt(empty): %v", id, err)
		}
	}
}

// Spec: §6.7 sandbox contract — adapters must not produce paths that
// escape the conventional roots; every output is rooted at the
// artifact ID or under a recognized harness-native top-level.
func TestEveryAdapter_OutputPathsAreSafe(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "x/y",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\n"),
		SkillBytes:    []byte("---\nname: y\ndescription: y\n---\n\nbody\n"),
		Resources: map[string][]byte{
			"scripts/run.py": []byte("print(1)\n"),
		},
	}
	r := DefaultRegistry()
	for _, id := range allAdapterIDs {
		a, _ := r.Get(id)
		out, err := a.Adapt(src)
		if err != nil {
			t.Fatalf("%s.Adapt: %v", id, err)
		}
		for _, f := range out {
			if strings.HasPrefix(f.Path, "/") {
				t.Errorf("%s: absolute path %q", id, f.Path)
			}
			if strings.Contains(f.Path, "..") {
				t.Errorf("%s: parent escape %q", id, f.Path)
			}
		}
	}
}

func paths(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
