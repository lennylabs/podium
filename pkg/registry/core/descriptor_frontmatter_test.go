package core

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// spec: §7.6.1 — the search-result descriptor built from a manifest record
// carries the record's frontmatter so search_artifacts can return it in the
// documented JSON schema.
func TestDescriptorOf_SearchCarriesFrontmatter(t *testing.T) {
	fm := "---\ntype: skill\nversion: 1.0.0\n---\n"
	d := descriptorOf(store.ManifestRecord{
		ArtifactID:  "finance/run",
		Type:        "skill",
		Version:     "1.0.0",
		Frontmatter: []byte(fm),
	})
	if d.Frontmatter != fm {
		t.Errorf("Frontmatter = %q, want %q", d.Frontmatter, fm)
	}
}

// spec: §5 / §7.6.1 — the stored manifest bytes are the full ARTIFACT.md
// source (frontmatter plus prose body). A search descriptor carries only the
// frontmatter block; the body stays at the registry until load_artifact, so it
// must not ride along. Regression test (the prior fix cleared the whole
// frontmatter to keep the body out, which also dropped the documented field).
func TestDescriptorOf_SearchDropsBody(t *testing.T) {
	full := "---\ntype: skill\nversion: 1.0.0\ndescription: do the thing\n---\n\nThe full prose body lives here.\n"
	d := descriptorOf(store.ManifestRecord{
		ArtifactID:  "finance/run",
		Type:        "skill",
		Version:     "1.0.0",
		Frontmatter: []byte(full),
	})
	if d.Frontmatter == "" {
		t.Fatal("search descriptor dropped the frontmatter entirely")
	}
	if got := d.Frontmatter; got != "---\ntype: skill\nversion: 1.0.0\ndescription: do the thing\n---\n" {
		t.Errorf("frontmatter block wrong: %q", got)
	}
	if strings.Contains(d.Frontmatter, "prose body") {
		t.Errorf("manifest body leaked into search frontmatter: %q", d.Frontmatter)
	}
}

// Spec: §4.6 hidden parents — frontmatterBlockHidingParent removes the
// top-level extends key and keeps every other authored key, including one
// manifest.Artifact does not declare. It fails closed on any input it cannot
// split, decode, or re-encode, because the block it could not rewrite is the
// block that names the parent.
func TestFrontmatterBlockHidingParent(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // substrings the result must contain
		gone []string // substrings the result must not contain
		zero bool
	}{
		{
			name: "drops extends and keeps undeclared keys",
			src:  "---\ntype: agent\nversion: 2.0.0\nacme_owner: platform-team\nextends: shared/parent@1.0.0\n---\n\nbody\n",
			want: []string{"type: agent", "acme_owner: platform-team"},
			gone: []string{"extends", "shared/parent", "body"},
		},
		{
			name: "no extends key leaves the mapping intact",
			src:  "---\ntype: agent\nversion: 2.0.0\n---\n\nbody\n",
			want: []string{"type: agent", "version: 2.0.0"},
		},
		{
			name: "unsplittable source fails closed",
			src:  "extends: shared/parent@1.0.0\n\nno frontmatter fence\n",
			zero: true,
		},
		{
			name: "undecodable header fails closed",
			src:  "---\nextends: shared/parent@1.0.0\n  bad: [unclosed\n---\n\nbody\n",
			zero: true,
		},
		{
			name: "non-mapping header fails closed",
			src:  "---\n- extends: shared/parent@1.0.0\n---\n\nbody\n",
			zero: true,
		},
		{
			name: "merge-key declared extends fails closed",
			src:  "---\nbase: &b\n  extends: shared/parent@1.0.0\ntype: agent\n<<: *b\n---\n\nbody\n",
			zero: true,
		},
		{
			name: "anchored extends value with a sibling alias fails closed",
			src:  "---\ntype: agent\nextends: &p shared/parent@1.0.0\nnote: *p\n---\n\nbody\n",
			zero: true,
		},
		{
			name: "empty header fails closed",
			src:  "---\n---\n\nbody\n",
			zero: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := frontmatterBlockHidingParent([]byte(tc.src))
			if tc.zero {
				if got != "" {
					t.Fatalf("got %q, want empty (fail closed)", got)
				}
				return
			}
			if !strings.HasPrefix(got, "---\n") || !strings.HasSuffix(got, "\n---\n") {
				t.Errorf("result is not a fenced frontmatter block: %q", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("result %q is missing %q", got, w)
				}
			}
			for _, g := range tc.gone {
				if strings.Contains(got, g) {
					t.Errorf("result %q still contains %q", got, g)
				}
			}
		})
	}
}
