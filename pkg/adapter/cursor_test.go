package adapter

import (
	"strings"
	"testing"
)

// cursorMDCFor adapts a single rule artifact through the cursor adapter
// and returns the .mdc content.
func cursorMDCFor(t *testing.T, artifact string) string {
	t.Helper()
	out, err := Cursor{}.Adapt(Source{ArtifactID: "style/rule", ArtifactBytes: []byte(artifact)})
	if err != nil {
		t.Fatalf("Cursor.Adapt: %v", err)
	}
	for _, f := range out {
		if strings.HasSuffix(f.Path, ".mdc") {
			return string(f.Content)
		}
	}
	t.Fatalf("no .mdc file in output: %v", paths(out))
	return ""
}

// Spec: §6.7 — the cursor adapter writes .cursor/rules/<name>.mdc with
// alwaysApply / globs / description derived from rule_mode (F-6.7.4).

func TestCursor_RuleModeAlwaysAlwaysApply(t *testing.T) {
	t.Parallel()
	got := cursorMDCFor(t, "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\ndescription: A rule.\n---\n\nRule prose.\n")
	if !strings.HasPrefix(got, "---\n") {
		t.Errorf(".mdc must open with frontmatter:\n%s", got)
	}
	if !strings.Contains(got, "alwaysApply: true") {
		t.Errorf("always rule must emit alwaysApply: true:\n%s", got)
	}
	if !strings.Contains(got, "Rule prose.") {
		t.Errorf(".mdc must carry the rule prose:\n%s", got)
	}
	// The canonical field names must not leak into the native .mdc.
	if strings.Contains(got, "rule_mode") {
		t.Errorf("canonical rule_mode leaked into cursor .mdc:\n%s", got)
	}
}

func TestCursor_RuleModeGlobGlobs(t *testing.T) {
	t.Parallel()
	got := cursorMDCFor(t, "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\nrule_globs: \"src/**/*.ts,src/**/*.tsx\"\n---\n\nTS rules.\n")
	if !strings.Contains(got, "globs: src/**/*.ts,src/**/*.tsx") {
		t.Errorf("glob rule must emit globs from rule_globs:\n%s", got)
	}
	if strings.Contains(got, "alwaysApply") {
		t.Errorf("glob rule must not emit alwaysApply:\n%s", got)
	}
}

func TestCursor_RuleModeAutoDescription(t *testing.T) {
	t.Parallel()
	got := cursorMDCFor(t, "---\ntype: rule\nversion: 1.0.0\nrule_mode: auto\nrule_description: Apply when migrating databases.\n---\n\nDB rules.\n")
	if !strings.Contains(got, "description: Apply when migrating databases.") {
		t.Errorf("auto rule must emit description from rule_description:\n%s", got)
	}
}

func TestCursor_RuleModeExplicitNoAutoApplyKey(t *testing.T) {
	t.Parallel()
	got := cursorMDCFor(t, "---\ntype: rule\nversion: 1.0.0\nrule_mode: explicit\ndescription: A rule.\n---\n\nExplicit prose.\n")
	for _, key := range []string{"alwaysApply", "globs:", "description:"} {
		if strings.Contains(got, key) {
			t.Errorf("explicit rule must not emit %q:\n%s", key, got)
		}
	}
	if !strings.Contains(got, "Explicit prose.") {
		t.Errorf(".mdc must carry the rule prose:\n%s", got)
	}
}

// The retired "claude-cursor adapter" label must not reappear.
func TestCursor_NoStrayLabel(t *testing.T) {
	t.Parallel()
	got := cursorMDCFor(t, "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nbody\n")
	if strings.Contains(got, "claude-cursor") {
		t.Errorf("stray 'claude-cursor' label present:\n%s", got)
	}
}

// A type: rule whose full manifest fails to parse (here mcpServers is a
// scalar where a list is required) still reaches the .mdc branch via the
// lightweight type scan; cursorRuleBody falls back to wrapping the
// canonical bytes so the .mdc is never empty.
func TestCursor_ParseFallbackNonEmpty(t *testing.T) {
	t.Parallel()
	got := cursorMDCFor(t, "---\ntype: rule\nversion: 1.0.0\nmcpServers: not-a-list\n---\n\nbody\n")
	if strings.TrimSpace(got) == "" {
		t.Errorf("cursor .mdc must not be empty on parse fallback")
	}
}
