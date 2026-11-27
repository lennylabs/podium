package ingest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/spi"
	"github.com/lennylabs/podium/pkg/store"
)

// slsaType is the predicate type the fixture signs.
const slsaType = "https://slsa.dev/provenance/v1"

// auditRecord is one §8.1 event a cycle emitted.
type auditRecord struct {
	typ    string
	target string
	fields map[string]string
}

// attestFixture is a local layer under a trust policy whose one signer is
// the harness's default email identity on a GitHub-style instance, which has
// no transparency log and takes an RFC 3161 timestamp as its time.
type attestFixture struct {
	t      *testing.T
	h      *sh.Harness
	dir    string
	st     store.Store
	cfg    *ingest.AttestationConfig
	clock  *clock.Frozen
	events []auditRecord
}

// newAttestFixture writes a layer with a domain and two agents and registers
// it under mode.
func newAttestFixture(t *testing.T, mode string) *attestFixture {
	t.Helper()
	h := sh.New(t)
	root, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	if err != nil {
		t.Fatal(err)
	}
	f := &attestFixture{
		t: t, h: h, dir: t.TempDir(), st: newStore(t),
		cfg:   &ingest.AttestationConfig{Instances: map[string]sign.AttestationInstance{"github": {Name: "github", Root: root}}},
		clock: clock.NewFrozen(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)),
	}
	f.write(map[string]string{
		"finance/DOMAIN.md":         "---\ndescription: Finance\n---\n",
		"finance/pay/ARTIFACT.md":   agentArtifact("Pay an invoice"),
		"finance/close/ARTIFACT.md": agentArtifact("Close the books"),
	})
	f.setPolicy(mode, emailSigner())
	return f
}

// emailSigner is the harness's default signer on the github instance.
func emailSigner() store.TrustSigner {
	return store.TrustSigner{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN, Instance: "github"}
}

// setPolicy stores the layer with mode and one requirement of signers.
func (f *attestFixture) setPolicy(mode string, signers ...store.TrustSigner) {
	f.t.Helper()
	lc := store.LayerConfig{TenantID: "tenant-1", ID: "team", SourceType: "local", LocalPath: f.dir, TrustMode: mode}
	if len(signers) > 0 {
		lc.TrustRequirements = []store.TrustRequirement{{Signers: signers, PredicateType: slsaType}}
	}
	if err := f.st.PutLayerConfig(context.Background(), lc); err != nil {
		f.t.Fatal(err)
	}
}

// write writes files under the layer directory.
func (f *attestFixture) write(files map[string]string) {
	f.t.Helper()
	for p, body := range files {
		full := filepath.Join(f.dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
}

// subjects returns the layer subject and the artifact subjects of the
// layer as it stands, with the layer subject first.
func (f *attestFixture) subjects() []sh.Subject {
	f.t.Helper()
	content, err := ingest.LayerContent(os.DirFS(f.dir))
	if err != nil {
		f.t.Fatal(err)
	}
	out := []sh.Subject{{Name: ".podium/layer", SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		out = append(out, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	return out
}

// sign returns a bundle over subjects whose timestamp lies at offset from
// the harness clock, inside the validity of the leaf.
func (f *attestFixture) sign(subjects []sh.Subject, offset time.Duration, opts ...sh.BundleOpt) []byte {
	f.t.Helper()
	all := append([]sh.BundleOpt{sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(f.h.Clock().Add(offset)))}, opts...)
	return f.h.DSSEBundle(sh.Statement(f.t, slsaType, subjects...), all...)
}

// place replaces the attestation directory with bundles.
func (f *attestFixture) place(bundles ...[]byte) {
	f.t.Helper()
	dir := filepath.Join(f.dir, ".podium", "attestations")
	if err := os.RemoveAll(dir); err != nil {
		f.t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	for _, b := range bundles {
		if err := os.WriteFile(filepath.Join(dir, attest.BundleFileName(b)), b, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
}

// signLayer places one bundle over the current layer at offset.
func (f *attestFixture) signLayer(offset time.Duration) {
	f.t.Helper()
	f.place(f.sign(f.subjects(), offset))
}

// cycle claims the layer and runs one ingest cycle against it, with the
// attestation steps the claimed layer's policy selects.
func (f *attestFixture) cycle() (*ingest.Result, error) {
	f.t.Helper()
	return f.cycleWith(nil)
}

// cycleWith runs a cycle whose attestation steps carry lineage.
func (f *attestFixture) cycleWith(lineage ingest.LineageFunc) (*ingest.Result, error) {
	f.t.Helper()
	ctx := context.Background()
	claim, _, claimed, err := ingest.ClaimLayer(ctx, f.st, "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	att := f.cfg.Cycle(claimed, claim)
	if att != nil && lineage != nil {
		att.WithLineage("ref", lineage)
	}
	return ingest.Ingest(ctx, f.st, ingest.Request{
		TenantID: "tenant-1", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(f.dir), Clock: f.clock, Attestation: att,
		AuditEmit: func(typ, target string, fields map[string]string) {
			f.events = append(f.events, auditRecord{typ, target, fields})
		},
	})
}

// record returns the stored attestation record of the layer.
func (f *attestFixture) record() store.AttestationRecord {
	f.t.Helper()
	lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	return lc.AttestationRecord
}

// stored returns the IDs of the live manifests the store holds.
func (f *attestFixture) stored() []string {
	f.t.Helper()
	rows, err := f.st.ListManifests(context.Background(), "tenant-1")
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.ArtifactID)
	}
	return out
}

// lastEvent returns the last audit event of type typ.
func (f *attestFixture) lastEvent(typ string) (auditRecord, bool) {
	for i := len(f.events) - 1; i >= 0; i-- {
		if f.events[i].typ == typ {
			return f.events[i], true
		}
	}
	return auditRecord{}, false
}

// refusal returns the code and the details of an attestation refusal, and
// fails the test when err is none.
func refusal(t *testing.T, err error) (string, map[string]any) {
	t.Helper()
	if !errors.Is(err, ingest.ErrAttestationRefused) {
		t.Fatalf("error = %v, want an attestation refusal", err)
	}
	e, ok := spi.AsError(err)
	if !ok {
		t.Fatalf("refusal %v carries no envelope", err)
	}
	return e.Code, e.Details
}
