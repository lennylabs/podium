package main

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// synthesizedComment is the body line of the §4.3.4 synthesized ARTIFACT.md.
const synthesizedComment = "<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"

// synthesizedSkillDocument returns the §4.3.4 synthesized ARTIFACT.md for v.
func synthesizedSkillDocument(t *testing.T, v string) string {
	t.Helper()
	doc, err := manifest.SynthesizedSkillDocument(v)
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument(%q): %v", v, err)
	}
	return string(doc)
}

// Spec: §4.3.4 — manifestBodyOf strips the frontmatter so BM25 indexes
// prose, and indexes nothing for the synthesized ARTIFACT.md of a skill with
// no ARTIFACT.md.
func TestManifestBodyOf_StripsFrontmatter(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		// frontmatter present
		"---\nfoo: bar\n---\nBody here\n": "\nBody here\n",
		"---\n---\nplain":                 "\nplain",
		// no frontmatter
		"plain text": "plain text",
		// empty
		"": "",
		// frontmatter without closing fence — returned as-is
		"---\nno-close": "---\nno-close",
		// the synthesized ARTIFACT.md of a skill with no ARTIFACT.md has no
		// authored prose (§4.3.4)
		synthesizedSkillDocument(t, "0.0.0-auto.0"): "",
		// an authored ARTIFACT.md with the same pointer comment under other
		// frontmatter keeps its body
		"---\nversion: 1.0.0\ntype: skill\n---\n" + synthesizedComment: "\n" + synthesizedComment,
	}
	for in, want := range cases {
		rec := filesystem.ArtifactRecord{ArtifactBytes: []byte(in)}
		got := manifestBodyOf(rec)
		if got != want {
			t.Errorf("input %q → %q, want %q", in, got, want)
		}
	}
}

func TestOverlayTagsMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		have, req []string
		want      bool
	}{
		{nil, nil, true},
		{[]string{"a", "b"}, []string{"a"}, true},
		{[]string{"a", "b"}, []string{"a", "b"}, true},
		{[]string{"a", "b"}, []string{"c"}, false},
		{[]string{"a"}, []string{"a", "b"}, false},
		{nil, []string{"x"}, false},
	}
	for i, c := range cases {
		if got := overlayTagsMatch(c.have, c.req); got != c.want {
			t.Errorf("case %d: overlayTagsMatch(%v, %v) = %v, want %v",
				i, c.have, c.req, got, c.want)
		}
	}
}

func TestTagsArg_ParsesBothShapes(t *testing.T) {
	t.Parallel()
	if got := tagsArg(map[string]any{}); got != nil {
		t.Errorf("missing key → %v", got)
	}
	if got := tagsArg(map[string]any{"tags": "a,b,c"}); strings.Join(got, ",") != "a,b,c" {
		t.Errorf("string → %v", got)
	}
	if got := tagsArg(map[string]any{"tags": []any{"a", "b", 7}}); strings.Join(got, ",") != "a,b" {
		t.Errorf("[]any → %v", got)
	}
}

func TestTopKArg_Defaults(t *testing.T) {
	t.Parallel()
	if got := topKArg(map[string]any{}); got != 10 {
		t.Errorf("default = %d, want 10", got)
	}
	if got := topKArg(map[string]any{"top_k": float64(7)}); got != 7 {
		t.Errorf("float → %d", got)
	}
	if got := topKArg(map[string]any{"top_k": 5}); got != 5 {
		t.Errorf("int → %d", got)
	}
}

func TestOverlayMatch_FindsByID(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{
		overlay: []filesystem.ArtifactRecord{
			{ID: "a"},
			{ID: "b", Artifact: &manifest.Artifact{Version: "1.0.0"}},
		},
	}
	if rec := srv.overlayMatch("b"); rec == nil || rec.ID != "b" {
		t.Errorf("overlayMatch = %+v", rec)
	}
	if rec := srv.overlayMatch("nope"); rec != nil {
		t.Errorf("overlayMatch(nope) = %+v, want nil", rec)
	}
}
