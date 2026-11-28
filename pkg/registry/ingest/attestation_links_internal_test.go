package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.11 (Attestation outcome) — the outcome a cycle sets: none for a
// cycle with no configuration, not_evaluated under none, none for an
// unchanged version after a failed layer step, and the failure with the
// requirement for the scope artifact.
func TestVersionOutcome_Branches(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	var nilCycle *AttestationCycle
	none := &AttestationCycle{mode: "none"}
	req := &AttestationCycle{mode: "require"}
	v := &attestationVerdict{outcomes: map[string]AttestationOutcome{
		"a": {Outcome: OutcomeFailed, Code: CodeAttestationMismatch, Scope: "artifact", Constraint: "digest", Requirement: 1},
		"b": {Outcome: OutcomeFailed, Code: CodeAttestationMissing, Scope: "layer", Constraint: "no_bundle"},
	}}
	failedStep := &attestationVerdict{failure: &AttestationError{Code: CodeAttestationMissing}, outcomes: v.outcomes}
	if nilCycle.versionOutcome(v, "a", false, now) != nil || req.versionOutcome(nil, "a", false, now) != nil ||
		req.versionOutcome(failedStep, "a", true, now) != nil || req.versionOutcome(v, "absent", false, now) != nil {
		t.Error("an outcome where none applies")
	}
	if o := none.versionOutcome(nil, "a", true, now); o == nil || o.Outcome != store.OutcomeNotEvaluated {
		t.Errorf("none: %+v", o)
	}
	a := req.versionOutcome(v, "a", false, now)
	b := req.versionOutcome(v, "b", false, now)
	if a.Failure == nil || a.Failure.Requirement == nil || *a.Failure.Requirement != 1 || b.Failure.Requirement != nil || !a.EvaluatedAt.Equal(now) {
		t.Errorf("artifact %+v %+v, layer %+v", a, a.Failure, b.Failure)
	}
}

// Spec: §4.7.11 (Attestation outcome) — the stored version a dropped
// artifact is unchanged from: the live row of the layer with its content
// hash at its version, and at any version for a registry-assigned one.
func TestUnchangedRow(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	req := Request{TenantID: "t", LayerID: "L"}
	recs, err := walkLayer(fstest.MapFS{"a/x/ARTIFACT.md": {Data: []byte("---\ntype: agent\nversion: 1.0.0\ndescription: X\n---\n\nX.\n")}}, "L")
	if err != nil || len(recs) != 1 {
		t.Fatalf("walk: %v %d", err, len(recs))
	}
	rec := recs[0]
	mr, err := manifestRecordFor(rec, "t", "L", now)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string][]store.ManifestRecord{"a/x": {
		{ArtifactID: "a/x", Version: "0.9.0", Layer: "L", ContentHash: mr.ContentHash},
		{ArtifactID: "a/x", Version: "1.0.0", Layer: "M", ContentHash: mr.ContentHash},
	}}
	if _, ok := unchangedRow(rec, req, rows, now); ok {
		t.Error("matched another version or another layer")
	}
	rows["a/x"] = append(rows["a/x"], store.ManifestRecord{ArtifactID: "a/x", Version: "1.0.0", Layer: "L", ContentHash: mr.ContentHash})
	if row, ok := unchangedRow(rec, req, rows, now); !ok || row.Version != "1.0.0" {
		t.Errorf("row %+v %v", row, ok)
	}
	rec.VersionAssigned = true
	if row, ok := unchangedRow(rec, req, rows, now); !ok || row.Version != "0.9.0" {
		t.Errorf("assigned: %+v %v", row, ok)
	}
	var p *linkPlan
	p.version(&rec, "a/x", "1.0.0", nil, nil)
	p.outcome(rows["a/x"][0], &store.VersionOutcome{Outcome: store.OutcomeVerified})
	if err := p.write(context.Background(), nil, ""); err != nil {
		t.Error(err)
	}
	q := &linkPlan{cycle: &AttestationCycle{mode: "require"}}
	q.outcome(store.ManifestRecord{AttestationOutcome: store.OutcomeVerified}, &store.VersionOutcome{Outcome: store.OutcomeVerified})
	q.outcome(store.ManifestRecord{}, nil)
	if links, changed := q.links(&filesystem.ArtifactRecord{ID: "a b/x"}, nil); links != nil || changed || len(q.versions) != 0 {
		t.Errorf("links %v %v, versions %v", links, changed, q.versions)
	}
}

// Spec: §4.7.11 (Artifact step, Attestation outcome) — an artifact that an
// additional requirement refuses under require is dropped, and the stored
// version it is unchanged from takes the failed outcome of that
// requirement. The policy rules admit one requirement in this release, so
// the cycle is built past its policy check.
func TestIngest_DroppedUnchangedVersionTakesTheFailedOutcome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := sh.New(t)
	root, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := "---\ntype: agent\nversion: 1.0.0\ndescription: Pay\n---\n\nPay.\n"
	if err := os.MkdirAll(filepath.Join(dir, "finance", "pay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "finance", "pay", "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Ingest(ctx, st, Request{TenantID: "t", LayerID: "L", Files: source.ConfinedFS(dir)}); err != nil {
		t.Fatal(err)
	}
	content, err := LayerContent(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	subjects := []sh.Subject{{Name: ".podium/layer", SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		subjects = append(subjects, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	bundle := h.DSSEBundle(sh.Statement(t, "https://slsa.dev/provenance/v1", subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(time.Minute))))
	if err := os.MkdirAll(filepath.Join(dir, ".podium", "attestations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".podium", "attestations", attest.BundleFileName(bundle)), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	lc := store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: dir, TrustMode: "require",
		TrustRequirements: []store.TrustRequirement{
			{Signers: []store.TrustSigner{{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN, Instance: "github"}}},
			{Signers: []store.TrustSigner{{Issuer: sh.DefaultIssuer, Identity: "carol@acme.com", Instance: "github"}}},
		}}
	if err := st.PutLayerConfig(ctx, lc); err != nil {
		t.Fatal(err)
	}
	cfg := &AttestationConfig{Instances: map[string]sign.AttestationInstance{"github": {Name: "github", Root: root}}}
	claim, _, claimed, err := ClaimLayer(ctx, st, "t", "L")
	if err != nil {
		t.Fatal(err)
	}
	att := cfg.Cycle(claimed, claim)
	att.checked = true
	res, err := Ingest(ctx, st, Request{TenantID: "t", LayerID: "L", IngestClaim: claim, Files: source.ConfinedFS(dir), Attestation: att})
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.GetManifest(ctx, "t", "finance/pay", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	f := row.AttestationFailure
	if len(res.Rejected) != 1 || res.Rejected[0].Code != CodeAttestationMissing || row.AttestationOutcome != store.OutcomeFailed ||
		f == nil || f.Scope != "artifact" || f.Requirement == nil || *f.Requirement != 1 {
		t.Fatalf("rejected %+v, outcome %s %+v", res.Rejected, row.AttestationOutcome, f)
	}
}
