package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// largeContextDoc returns an ARTIFACT.md whose bytes exceed the §4.2 inline
// cutoff, so its canonical document is delivered via the §6.6 presigned
// manifest-body channel.
func largeContextDoc() []byte {
	body := strings.Repeat("glossary line\n", objectstore.InlineCutoff/14+200)
	return []byte("---\ntype: context\nversion: 1.0.0\ndescription: Big glossary.\n---\n\n" + body)
}

// largeSkillRaw returns a SKILL.md whose bytes exceed the inline cutoff.
func largeSkillRaw() []byte {
	body := strings.Repeat("step line\n", objectstore.InlineCutoff/10+200)
	return []byte("---\nname: run\ndescription: Big skill.\n---\n\n" + body)
}

// Spec: §6.6 — ResolveResourceOwner authorizes a presigned manifest-body
// object (keyed by sha256 of the canonical document above the inline cutoff)
// for the same caller/visibility as the artifact, and denies a caller who
// cannot see the owning layer. A sub-cutoff document is never stored
// externally, so its key does not resolve through the body path.
func TestResolveResourceOwner_ManifestBodyVisibility(t *testing.T) {
	t.Parallel()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	doc := largeContextDoc()
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenantID, ArtifactID: "team/secret", Version: "1.0.0",
		ContentHash: "sha256:m", Type: "context", Layer: "private",
		Frontmatter: doc, Body: []byte("glossary line\n"),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "private", Precedence: 1, Visibility: layer.Visibility{Users: []string{"alice"}}},
	})

	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	bob := layer.Identity{Sub: "bob", IsAuthenticated: true}
	key := core.ManifestBodyKey(doc)

	if owner, ok := reg.ResolveResourceOwner(context.Background(), alice, key); !ok || owner != "team/secret" {
		t.Errorf("alice should own the manifest-body object: owner=%q ok=%v", owner, ok)
	}
	if _, ok := reg.ResolveResourceOwner(context.Background(), bob, key); ok {
		t.Errorf("bob cannot see the private layer; the manifest-body object must not resolve")
	}
	// A sub-cutoff document is delivered inline, never stored as an object,
	// so its hash must not authorize an /objects fetch.
	if _, ok := reg.ResolveResourceOwner(context.Background(), alice, core.ManifestBodyKey([]byte("small"))); ok {
		t.Errorf("a below-cutoff document key must not resolve through the body path")
	}
}

// Spec: §6.6 / §4.3.4 — a skill's canonical document is its verbatim
// SKILL.md, so the manifest-body object is keyed on SkillRaw, not the small
// ARTIFACT.md frontmatter. The frontmatter (below the cutoff) is never stored
// externally and its key does not resolve.
func TestResolveResourceOwner_SkillBodyKeyedOnSkillRaw(t *testing.T) {
	t.Parallel()
	const tenantID = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	front := []byte("---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body in SKILL.md -->\n")
	skill := largeSkillRaw()
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenantID, ArtifactID: "team/run", Version: "1.0.0",
		ContentHash: "sha256:m", Type: "skill", Layer: "L",
		Frontmatter: front, SkillRaw: skill, Body: []byte("step line\n"),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	pub := layer.Identity{IsPublic: true}

	if _, ok := reg.ResolveResourceOwner(context.Background(), pub, core.ManifestBodyKey(skill)); !ok {
		t.Errorf("the SKILL.md manifest-body object should resolve for a visible caller")
	}
	if _, ok := reg.ResolveResourceOwner(context.Background(), pub, core.ManifestBodyKey(front)); ok {
		t.Errorf("the below-cutoff ARTIFACT.md frontmatter is not stored externally; its key must not resolve")
	}
}

// Spec: §6.6 — CanonicalManifestDoc selects SKILL.md for a skill and
// ARTIFACT.md for every other type, matching the document the body channel
// delivers.
func TestCanonicalManifestDoc_SelectsByType(t *testing.T) {
	t.Parallel()
	front := []byte("ARTIFACT")
	skill := []byte("SKILL")
	if got := core.CanonicalManifestDoc("skill", front, skill); string(got) != "SKILL" {
		t.Errorf("skill canonical doc = %q, want SKILL", got)
	}
	for _, typ := range []string{"context", "command", "agent", "rule"} {
		if got := core.CanonicalManifestDoc(typ, front, skill); string(got) != "ARTIFACT" {
			t.Errorf("%s canonical doc = %q, want ARTIFACT", typ, got)
		}
	}
}
