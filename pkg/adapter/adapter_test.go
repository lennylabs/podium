package adapter

import (
	"context"
	"testing"
)

// Spec: §6.7 Plugin descriptor, §9.1 — the Source carries a PluginDescriptor
// for marketplace emitters (§7.8). The project-files mode leaves it unset, and
// a zero descriptor must produce the same output as a Source that never names
// the field, so existing project-files adapters are unaffected by the contract
// change. A zero PluginDescriptor compares equal to the explicit zero value, so
// the two Sources are byte-identical for every field the project-files adapters
// read.
func TestSource_ZeroPluginDescriptorLeavesProjectFilesUnchanged(t *testing.T) {
	t.Parallel()

	resources := map[string][]byte{
		"scripts/x.py":    []byte("print('x')\n"),
		"references/r.md": []byte("# Reference\n"),
	}
	// A Source from a project-files caller that never sets Plugin.
	projectFiles := Source{
		ArtifactID:    "finance/ap/pay-invoice",
		ArtifactBytes: []byte("---\ntype: skill\n---\nbody\n"),
		SkillBytes:    []byte("SKILL body\n"),
		Resources:     resources,
	}
	// The same Source with the descriptor written out as its zero value.
	zeroDescriptor := projectFiles
	zeroDescriptor.Plugin = PluginDescriptor{}

	if projectFiles.Plugin != (PluginDescriptor{}) {
		t.Fatalf("project-files Source.Plugin = %+v, want zero value", projectFiles.Plugin)
	}

	// Every project-files adapter must produce identical output for the two
	// Sources, because a zero descriptor carries no plugin information.
	adapters := []HarnessAdapter{
		None{},
		ClaudeCode{},
		Cursor{},
		Codex{},
		Gemini{},
		OpenCode{},
		Pi{},
		Hermes{},
	}
	for _, a := range adapters {
		a := a
		t.Run(a.ID(), func(t *testing.T) {
			t.Parallel()
			gotUnset, err := a.Adapt(context.Background(), projectFiles)
			if err != nil {
				t.Fatalf("Adapt (unset descriptor): %v", err)
			}
			gotZero, err := a.Adapt(context.Background(), zeroDescriptor)
			if err != nil {
				t.Fatalf("Adapt (zero descriptor): %v", err)
			}
			assertSameFiles(t, gotUnset, gotZero)
		})
	}
}

// Spec: §6.7 — a zero PluginDescriptor has empty Name, Description, and Prefix
// so a project-files adapter reads no plugin information from it.
func TestPluginDescriptor_ZeroValueIsEmpty(t *testing.T) {
	t.Parallel()
	var d PluginDescriptor
	if d.Name != "" || d.Description != "" || d.Prefix != "" {
		t.Errorf("zero PluginDescriptor = %+v, want all fields empty", d)
	}
}

// assertSameFiles fails when two adapter outputs differ in length, path, mode,
// op, key, or content. The project-files adapters return files in deterministic
// order, so a positional comparison is sufficient.
func assertSameFiles(t *testing.T, got, want []File) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("file count = %d, want %d", len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Path != w.Path {
			t.Errorf("File[%d].Path = %q, want %q", i, g.Path, w.Path)
		}
		if g.Mode != w.Mode {
			t.Errorf("File[%d].Mode = %o, want %o", i, g.Mode, w.Mode)
		}
		if g.Op != w.Op {
			t.Errorf("File[%d].Op = %d, want %d", i, g.Op, w.Op)
		}
		if g.Key != w.Key {
			t.Errorf("File[%d].Key = %q, want %q", i, g.Key, w.Key)
		}
		if string(g.Content) != string(w.Content) {
			t.Errorf("File[%d].Content for %q differs", i, g.Path)
		}
	}
}
