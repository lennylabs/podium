package core_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// esrIngest ingests one artifact into its own layer and returns the store.
// Every case here needs a parent in one layer and a child in another, so the
// helper takes both and keeps each test to what it asserts.
func esrIngest(t *testing.T, parent, child string, signer ingest.SignerFunc) *store.Memory {
	t.Helper()
	return esrIngestFiles(t,
		fstest.MapFS{"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parent)}},
		fstest.MapFS{"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(child)}},
		signer)
}

// esrIngestFiles is the multi-file form. A skill authors its prose in SKILL.md
// beside ARTIFACT.md, so the skill case needs two files per artifact.
func esrIngestFiles(t *testing.T, parent, child fstest.MapFS, signer ingest.SignerFunc) *store.Memory {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Signer: signer, Files: parent,
	})
	if err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res.Rejected)
	}
	res, err = ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Signer: signer, Files: child,
	})
	if err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("child not accepted: %+v", res.Rejected)
	}
	return st
}

// esrProseBelowFrontmatter returns whatever a served frontmatter block carries
// after its closing delimiter. A record whose prose lives in ARTIFACT.md
// serves that prose in both Frontmatter and ManifestBody, so an assertion that
// the body is empty has to cover the frontmatter's copy of it too.
func esrProseBelowFrontmatter(t *testing.T, b []byte) string {
	t.Helper()
	const delim = "---"
	s := string(b)
	if !strings.HasPrefix(s, delim+"\n") {
		t.Fatalf("served frontmatter does not open with a %q delimiter:\n%s", delim, s)
	}
	rest := s[len(delim)+1:]
	i := strings.Index(rest, "\n"+delim)
	if i < 0 {
		t.Fatalf("served frontmatter has no closing %q delimiter:\n%s", delim, s)
	}
	after := rest[i+1+len(delim):]
	nl := strings.Index(after, "\n")
	if nl < 0 {
		return ""
	}
	return after[nl+1:]
}

func esrRegistry(st *store.Memory) *core.Registry {
	return core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "L2", Visibility: layer.Visibility{Public: true}, Precedence: 2},
	})
}

// Spec: §4.7.9 — a served signature covers the served content hash. The
// merged record an extends child is served from is assembled starting at the
// root parent's stored row, and the child's coordinates are copied over it.
// The signature was not among the copied fields, so the child was served its
// parent's envelope against its own content hash and verification could not
// succeed. Ingest signs each record over its own hash, so the two never
// corresponded for any extends child.
//
// This fails closed rather than open: materialization refuses the artifact
// with materialize.signature_invalid, which makes signing and extends:
// mutually exclusive in practice. No test paired the two before this one.
func TestExtends_ServedSignatureVerifiesAgainstServedContentHash(t *testing.T) {
	t.Parallel()
	signer := sign.Noop{}
	st := esrIngest(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\nsensitivity: medium\n---\n\nparent body\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nsensitivity: medium\n"+
			"extends: shared/parent@1.x\n---\n\nchild body\n",
		signer.Sign)

	got, err := esrRegistry(st).LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Signature == "" {
		t.Fatal("served signature is empty; the signer should have produced one at ingest")
	}
	if err := sign.EnforceVerification(context.Background(), sign.PolicyAlways, signer,
		manifest.Sensitivity(got.Sensitivity), got.ContentHash, got.Signature); err != nil {
		t.Errorf("the served signature does not verify against the served content hash: %v", err)
	}
}

// manifest.MergeExtends assigns the child's body unconditionally
// (pkg/manifest/merge.go), the filesystem resolver folds through it, and
// docs/authoring/extends.md states that the child's prose replaces the
// parent's. The served record guarded that carry-over on a non-empty body, so
// a child that authored no prose was served the root parent's. For a type
// whose prose lives in ARTIFACT.md, as this fixture's type: agent does, that
// also left the record's Body disagreeing with the body inside its own merged
// frontmatter. For type: skill the two sources differ by construction, because
// ingest stores the SKILL.md body on the record's Body and the ARTIFACT.md
// bytes as its Frontmatter. No spec section states which body an extends child
// serves, so this test cites no spec section.
func TestExtends_EmptyChildBodyDoesNotServeTheParentProse(t *testing.T) {
	t.Parallel()
	st := esrIngest(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nsecret parent prose\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n",
		nil)

	got, err := esrRegistry(st).LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if strings.Contains(got.ManifestBody, "secret parent prose") {
		t.Errorf("the child was served the parent's body:\n%s", got.ManifestBody)
	}
	if strings.Contains(string(got.Frontmatter), "secret parent prose") {
		t.Errorf("the merged frontmatter carries the parent's body:\n%s", got.Frontmatter)
	}
	// The positive form: a served body of any origin fails, so a truncated or
	// otherwise parent-derived body cannot pass the absence checks above.
	if body := strings.TrimSpace(got.ManifestBody); body != "" {
		t.Errorf("the child authored no prose, so the served body must be empty; got:\n%s", body)
	}
	if prose := strings.TrimSpace(esrProseBelowFrontmatter(t, got.Frontmatter)); prose != "" {
		t.Errorf("the merged frontmatter carries prose below its block:\n%s", prose)
	}
}

// The paired arm: a child that authors its own prose is served that prose.
// Dropping the guard must not turn into dropping the body, so the two cases
// move together. Like the case above this pins manifest.MergeExtends
// (pkg/manifest/merge.go) and docs/authoring/extends.md rather than a spec
// section, because no section states which body an extends child serves.
func TestExtends_NonEmptyChildBodyIsServedOverTheParentProse(t *testing.T) {
	t.Parallel()
	st := esrIngest(t,
		"---\ntype: agent\nversion: 1.0.0\ndescription: parent\n---\n\nsecret parent prose\n",
		"---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nchild prose\n",
		nil)

	got, err := esrRegistry(st).LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if strings.TrimSpace(got.ManifestBody) != "child prose" {
		t.Errorf("served body = %q, want the child's authored prose", got.ManifestBody)
	}
	if strings.Contains(got.ManifestBody, "secret parent prose") {
		t.Errorf("the served body carries the parent's prose:\n%s", got.ManifestBody)
	}
	if prose := strings.TrimSpace(esrProseBelowFrontmatter(t, got.Frontmatter)); prose != "child prose" {
		t.Errorf("merged frontmatter prose = %q, want the child's authored prose", prose)
	}
}

// The skill arm. A skill's prose lives in SKILL.md, which ingest stores on the
// record's Body while the ARTIFACT.md bytes become its Frontmatter
// (pkg/registry/ingest/ingest.go), so the two sources differ by construction
// and this case asserts nothing about the frontmatter's prose. What the served
// record owes a skill child whose SKILL.md carries no prose is its own empty
// body rather than the root parent's SKILL.md prose.
func TestExtends_EmptySkillChildBodyDoesNotServeTheParentProse(t *testing.T) {
	t.Parallel()
	st := esrIngestFiles(t,
		fstest.MapFS{
			"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(
				"---\ntype: skill\nversion: 1.0.0\n---\n")},
			"shared/parent/SKILL.md": &fstest.MapFile{Data: []byte(
				"---\nname: parent\ndescription: The parent skill.\n---\n\nsecret parent prose\n")},
		},
		fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(
				"---\ntype: skill\nversion: 2.0.0\nextends: shared/parent@1.x\n---\n")},
			"finance/child/SKILL.md": &fstest.MapFile{Data: []byte(
				"---\nname: child\ndescription: The child skill.\n---\n")},
		},
		nil)

	got, err := esrRegistry(st).LoadArtifact(context.Background(), publicID, "finance/child", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if strings.Contains(got.ManifestBody, "secret parent prose") {
		t.Errorf("the skill child was served the parent's SKILL.md body:\n%s", got.ManifestBody)
	}
	if body := strings.TrimSpace(got.ManifestBody); body != "" {
		t.Errorf("the skill child authored no prose, so the served body must be empty; got:\n%s", body)
	}
}
