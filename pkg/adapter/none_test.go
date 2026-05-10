package adapter

import (
	"errors"
	"strings"
	"testing"
)

// Spec: §6.7 Harness Adapters — the none adapter writes the canonical
// layout as-is, with paths rooted at <artifact-id>/.
func TestNone_WritesCanonicalLayout(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "finance/ap/pay-invoice",
		ArtifactBytes: []byte("ARTIFACT body\n"),
		SkillBytes:    []byte("SKILL body\n"),
		Resources: map[string][]byte{
			"scripts/x.py":    []byte("print('x')\n"),
			"references/r.md": []byte("# Reference\n"),
		},
	}
	out, err := None{}.Adapt(src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	wantPaths := []string{
		"finance/ap/pay-invoice/ARTIFACT.md",
		"finance/ap/pay-invoice/SKILL.md",
		"finance/ap/pay-invoice/references/r.md",
		"finance/ap/pay-invoice/scripts/x.py",
	}
	if len(out) != len(wantPaths) {
		t.Fatalf("got %d files, want %d", len(out), len(wantPaths))
	}
	for i, want := range wantPaths {
		if out[i].Path != want {
			t.Errorf("File[%d].Path = %q, want %q", i, out[i].Path, want)
		}
	}
}

// Spec: §6.7 — when no SKILL.md is present (non-skill type), the adapter
// emits ARTIFACT.md plus bundled resources only.
func TestNone_OmitsMissingSkill(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "company-glossary",
		ArtifactBytes: []byte("---\ntype: context\n---\nbody\n"),
	}
	out, err := None{}.Adapt(src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d files, want 1", len(out))
	}
	if out[0].Path != "company-glossary/ARTIFACT.md" {
		t.Errorf("Path = %q", out[0].Path)
	}
}

// Spec: §6.7 — None.ID() returns the literal "none" so PODIUM_HARNESS=none
// resolves through the default registry.
func TestNone_IDIsLiteralNone(t *testing.T) {
	t.Parallel()
	if id := (None{}).ID(); id != "none" {
		t.Errorf("ID = %q, want none", id)
	}
}

// Spec: §6.7 — DefaultRegistry includes the none adapter at minimum.
func TestDefaultRegistry_ContainsNone(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	a, err := r.Get("none")
	if err != nil {
		t.Fatalf("Get(none): %v", err)
	}
	if a.ID() != "none" {
		t.Errorf("got %q, want none", a.ID())
	}
}

// Spec: §6.10 namespacing — Get returns a config.unknown_harness error for
// a missing adapter, and the error message lists registered IDs.
func TestRegistry_GetUnknownReturnsStructured(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	_, err := r.Get("not-an-adapter")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "config.unknown_harness") {
		t.Errorf("error %q missing config.unknown_harness namespace", err)
	}
	if !strings.Contains(err.Error(), "none") {
		t.Errorf("error %q does not list registered adapters", err)
	}
}

// Spec: §9.1 SPI — registering the same adapter ID twice is rejected.
func TestRegistry_DuplicateIDIsRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(None{}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(None{})
	if err == nil {
		t.Fatalf("expected error on duplicate Register, got nil")
	}
	// Use errors.Is for sentinel matching once we move to wrapped errors.
	_ = errors.New
}

// Spec: §6.7 — adapter outputs are sorted deterministically so golden-file
// comparisons remain stable across map iteration orders.
func TestNone_OutputIsDeterministic(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "x",
		ArtifactBytes: []byte("a"),
		Resources: map[string][]byte{
			"z/last.txt":  []byte("z"),
			"a/first.txt": []byte("a"),
			"m/mid.txt":   []byte("m"),
		},
	}
	for i := 0; i < 5; i++ {
		out, err := None{}.Adapt(src)
		if err != nil {
			t.Fatalf("Adapt: %v", err)
		}
		paths := make([]string, len(out))
		for j, f := range out {
			paths[j] = f.Path
		}
		want := []string{
			"x/ARTIFACT.md",
			"x/a/first.txt",
			"x/m/mid.txt",
			"x/z/last.txt",
		}
		for j, w := range want {
			if paths[j] != w {
				t.Errorf("iter %d, paths[%d] = %q, want %q", i, j, paths[j], w)
			}
		}
	}
}
