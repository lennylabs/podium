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
// split, decode, or re-encode, and on any rewritten block that does not read
// back as a mapping free of an extends value, because the block it could not
// rewrite is the block that names the parent.
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
			name: "an anchor unrelated to extends survives the strip",
			src:  "---\ntype: agent\nversion: 2.0.0\ntags: &t [alpha, beta]\nother: *t\nextends: shared/parent@1.0.0\n---\n\nbody\n",
			want: []string{"type: agent", "alpha", "other"},
			gone: []string{"extends", "shared/parent", "body"},
		},
		{
			// A merge key the child authored keeps its value. The strip is
			// scoped to the top-level extends key, so a mapping the child
			// merged in survives with whatever the child wrote in it.
			name: "merge-key unrelated to extends is served",
			src:  "---\ndefaults: &d\n  acme_owner: platform-team\ntype: agent\n<<: *d\nextends: shared/parent@1.0.0\n---\n\nbody\n",
			want: []string{"type: agent", "acme_owner: platform-team", "<<: *d"},
			gone: []string{"extends", "shared/parent", "body"},
		},
		{
			// ParseArtifact resolves merge keys, so this child is ingested as
			// an extends child of shared/parent. Deleting the top-level key
			// leaves the operative extends inside the merged mapping, so the
			// rewritten block still resolves a parent and the helper fails
			// closed.
			name: "an extends supplied through a merge key fails closed",
			src:  "---\nbase: &b\n  extends: shared/parent@1.0.0\ntype: agent\n<<: *b\n---\n\nbody\n",
			zero: true,
		},
		{
			// A nested extends the child never merges in is not what the
			// parser resolved, so it is the child's authored text and rides
			// along with the rest of the block.
			name: "an unmerged nested extends is served",
			src:  "---\nbase:\n  extends: shared/parent@1.0.0\ntype: agent\n---\n\nbody\n",
			want: []string{"type: agent", "extends: shared/parent@1.0.0"},
			gone: []string{"body"},
		},
		{
			// Deleting the anchored extends value strands the alias, so the
			// rewritten block is undecodable YAML rather than a parent-free
			// header and the helper fails closed.
			name: "an alias into the deleted extends value fails closed",
			src:  "---\ntype: agent\nextends: &p shared/parent@1.0.0\nnote: *p\n---\n\nbody\n",
			zero: true,
		},
		{
			// The strip works on the YAML node, so a key whose YAML type does
			// not match manifest.Artifact's field never reaches a typed decode
			// and its block is served with the remaining keys intact.
			name: "a key manifest.Artifact cannot type-decode is still served",
			src:  "---\ntype: agent\nversion: 2.0.0\ntags: authored-as-a-scalar\nextends: shared/parent@1.0.0\n---\n\nbody\n",
			want: []string{"type: agent", "tags: authored-as-a-scalar"},
			gone: []string{"extends", "shared/parent", "body"},
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
