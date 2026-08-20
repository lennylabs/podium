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
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L1", Signer: signer,
		Files: fstest.MapFS{"shared/parent/ARTIFACT.md": &fstest.MapFile{Data: []byte(parent)}},
	})
	if err != nil {
		t.Fatalf("ingest parent: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res.Rejected)
	}
	res, err = ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L2", Signer: signer,
		Files: fstest.MapFS{"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(child)}},
	})
	if err != nil {
		t.Fatalf("ingest child: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("child not accepted: %+v", res.Rejected)
	}
	return st
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

// Spec: §4.6 — extends inherits structured frontmatter fields and not the
// markdown body, which manifest.MergeExtends implements by taking the child's
// body unconditionally. The served record guarded that carry-over on a
// non-empty body, so a child that authored no prose was served the root
// parent's, and the record disagreed with the body inside its own merged
// frontmatter. The parent's prose also reaches a requester who may hold no
// access to the parent's layer, which is what §4.6 hidden parents forbids.
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
}
