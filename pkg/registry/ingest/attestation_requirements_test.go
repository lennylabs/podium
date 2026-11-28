package ingest_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// scanType is the predicate type of the fixture's scanner statements.
const scanType = "https://acme.com/scan/v1"

// scannerEmail is the identity of the fixture's second signer.
const scannerEmail = "scanner@acme.com"

// scanSigner is the second signer, a scanner with an identity of its own on
// the github instance.
func scanSigner() store.TrustSigner {
	return store.TrustSigner{Issuer: sh.DefaultIssuer, Identity: scannerEmail, Instance: "github"}
}

// buildReq is the covering requirement of the fixture's signing workflow.
func buildReq() store.TrustRequirement {
	return store.TrustRequirement{Signers: []store.TrustSigner{emailSigner()}, PredicateType: slsaType, CoversLayer: true}
}

// scanReq is the additional requirement of the scanner.
func scanReq(optional bool) store.TrustRequirement {
	return store.TrustRequirement{Signers: []store.TrustSigner{scanSigner()}, PredicateType: scanType, Optional: optional}
}

// hookArtifact is a hook manifest.
func hookArtifact(desc string) string {
	return "---\ntype: hook\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\nhook_event: pre_tool_use\nhook_action: echo hi\n---\n\n" + desc + "\n"
}

// setRequirements stores the layer with mode, the listed types, and reqs.
func (f *attestFixture) setRequirements(mode string, types []string, reqs ...store.TrustRequirement) {
	f.t.Helper()
	lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	lc.TrustMode, lc.TrustRequirements, lc.TrustRequireTypes = mode, reqs, types
	if err := f.st.PutLayerConfig(context.Background(), lc); err != nil {
		f.t.Fatal(err)
	}
}

// signScan returns a scanner bundle over subjects at offset.
func (f *attestFixture) signScan(subjects []sh.Subject, offset time.Duration) []byte {
	f.t.Helper()
	return f.h.DSSEBundle(sh.Statement(f.t, scanType, subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(f.h.Clock().Add(offset))), sh.WithLeaf(sh.LeafEmail(scannerEmail)))
}

// artifactSubjects returns the artifact subjects of the layer as it stands,
// without the layer subject and without the subjects whose name begins with
// one of the prefixes in skip.
func (f *attestFixture) artifactSubjects(skip ...string) []sh.Subject {
	f.t.Helper()
	var out []sh.Subject
	for _, s := range f.subjects()[1:] {
		if !slices.ContainsFunc(skip, func(p string) bool { return strings.HasPrefix(s.Name, p) }) {
			out = append(out, s)
		}
	}
	return out
}

// rejected renders the rejections of a cycle as id=code/requirement.
func rejected(res *ingest.Result) []string {
	var out []string
	for _, r := range res.Rejected {
		req := "-"
		if n, ok := r.Details["requirement"]; ok {
			b, _ := json.Marshal(n)
			req = string(b)
		}
		out = append(out, r.ArtifactID+"="+r.Code+"/"+req)
	}
	slices.Sort(out)
	return out
}

// Spec: §4.7.11 (Layer trust policy, Artifact step) — under require an
// artifact needs a subject of every requirement: the covering signer's
// statement passes the layer step, and an artifact the scanner did not list
// is dropped with ingest.attestation_missing for requirement 1. The stored
// artifact holds a link per signer, and layer.attestation_verified names the
// requirement that accepted each bundle.
func TestAttestation_SeveralRequirementsUnderRequire(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.setRequirements("require", nil, buildReq(), scanReq(false))
	f.place(f.sign(f.subjects(), time.Minute), f.signScan(f.artifactSubjects("finance/close"), 2*time.Minute))
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	want(t, "rejected", rejected(res), "finance/close=ingest.attestation_missing/1")
	if rec := f.record(); res.Accepted != 1 || rec.Status != "ok" || rec.Dropped != 1 {
		t.Fatalf("accepted %d, record %+v", res.Accepted, rec)
	}
	if got := f.links(); len(got) != 2 || !hasLink(got, "finance/pay github/"+sh.DefaultSAN) || !hasLink(got, "finance/pay github/"+scannerEmail) {
		t.Errorf("links = %v", got)
	}
	if o := f.outcome("finance/pay"); o != "verified" {
		t.Errorf("pay outcome = %s", o)
	}
	ev, _ := f.lastEvent("layer.attestation_verified")
	if !strings.Contains(ev.fields["bundles"], `"requirement":1`) || ev.fields["missing"] != "1" || ev.fields["matched"] != "1" {
		t.Errorf("verified event = %+v", ev.fields)
	}
}

// Spec: §4.7.11 (Artifact step, Attestation outcome) — under warn an
// artifact that fails an additional requirement is stored with the outcome
// failed, the code of the artifact step, and the position of the
// requirement, beside a verified link of the covering requirement.
func TestAttestation_SeveralRequirementsUnderWarn(t *testing.T) {
	f := newAttestFixture(t, "warn")
	f.setRequirements("warn", nil, buildReq(), scanReq(false))
	scanned := f.artifactSubjects()
	for i := range scanned {
		if strings.HasPrefix(scanned[i].Name, "finance/close") {
			scanned[i].SHA256 = strings.Repeat("0", 64)
		}
	}
	f.place(f.sign(f.subjects(), time.Minute), f.signScan(scanned, 2*time.Minute))
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if res.Accepted != 2 || len(res.Rejected) != 0 || f.record().Dropped != 0 {
		t.Fatalf("accepted %d, rejected %v", res.Accepted, res.Rejected)
	}
	want(t, "outcomes", []string{f.outcome("finance/close"), f.outcome("finance/pay")},
		"failed ingest.attestation_mismatch/artifact/digest/1", "verified")
	if got := f.links(); len(got) != 3 || !hasLink(got, "finance/close github/"+sh.DefaultSAN) {
		t.Errorf("links = %v", got)
	}
}

// Spec: §4.7.11 (Layer trust policy, Artifact step, Verified links) — an
// optional requirement fails nothing: without its bundle every artifact is
// verified, and with a bundle that lists one artifact that artifact gains
// the scanner's link and the other is neither dropped nor reported.
func TestAttestation_OptionalRequirement(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.setRequirements("require", nil, buildReq(), scanReq(true))
	f.signLayer(time.Minute)
	res, err := f.cycle()
	if err != nil || res.Accepted != 2 || len(res.Rejected) != 0 {
		t.Fatalf("without a scan bundle: %+v, %v", res, err)
	}
	f.place(f.sign(f.subjects(), 2*time.Minute), f.signScan(f.artifactSubjects("finance/close"), 3*time.Minute))
	res, err = f.cycle()
	if err != nil || len(res.Rejected) != 0 || f.record().Dropped != 0 {
		t.Fatalf("with a scan bundle: %+v, %v", res, err)
	}
	want(t, "outcomes", []string{f.outcome("finance/close"), f.outcome("finance/pay")}, "verified", "verified")
	if got := f.links(); len(got) != 3 || !hasLink(got, "finance/pay github/"+scannerEmail) || hasLink(got, "finance/close github/"+scannerEmail) {
		t.Errorf("links = %v", got)
	}
}

// Spec: §4.7.11 (Layer step, Policy rules) — the requirement that sets
// covers_layer covers the layer wherever it stands: with the scanner first,
// a directory that holds only the scanner's bundle fails the layer step as
// no_accepted_bundle, with the reason the covering signer gave; with both
// bundles the cycle passes and an artifact the scanner did not list fails
// requirement 0.
func TestAttestation_CoveringRequirementByMarker(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.setRequirements("require", nil, scanReq(false), buildReq())
	f.place(f.signScan(f.subjects(), time.Minute))
	_, err := f.cycle()
	code, details := refusal(t, err)
	b, _ := json.Marshal(details["bundles"])
	if code != ingest.CodeAttestationMissing || details["constraint"] != "no_accepted_bundle" || !strings.Contains(string(b), `"reason":"identity"`) {
		t.Fatalf("refusal %s %s", code, b)
	}
	f.place(f.sign(f.subjects(), 2*time.Minute), f.signScan(f.artifactSubjects("finance/pay"), 3*time.Minute))
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	want(t, "rejected", rejected(res), "finance/pay=ingest.attestation_missing/0")
}

// Spec: §4.7.11 (Policy that became invalid) — a stored policy with several
// requirements and no covering marker, which the endpoints refuse, refuses
// each cycle with ingest.attestation_policy_invalid and the constraint
// policy_rule, and stores nothing.
func TestAttestation_StoredPolicyWithoutCoveringRequirementIsRefused(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.setRequirements("warn", nil, scanReq(false), store.TrustRequirement{Signers: []store.TrustSigner{emailSigner()}})
	f.signLayer(time.Minute)
	_, err := f.cycle()
	code, details := refusal(t, err)
	if code != ingest.CodeAttestationPolicyInvalid || details["constraint"] != "policy_rule" || len(f.stored()) != 0 {
		t.Fatalf("refusal %s %v, stored %v", code, details, f.stored())
	}
	if c := f.cfg.PolicyConstraint(f.claimed()); c != "policy_rule" {
		t.Errorf("served constraint = %q", c)
	}
}

// hasLink reports whether a rendered link begins with prefix.
func hasLink(links []string, prefix string) bool {
	return slices.ContainsFunc(links, func(l string) bool { return strings.HasPrefix(l, prefix) })
}

// claimed returns the stored layer.
func (f *attestFixture) claimed() store.LayerConfig {
	f.t.Helper()
	lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	return lc
}

// Spec: §4.7.11 (Layer trust policy, Artifact step, Attestation outcome) —
// under warn an artifact of a type trust_require_types lists that fails a
// requirement is dropped as under require, while an agent that fails it is
// stored as failed. A hook unchanged from a stored version that a later
// cycle drops leaves that version serving with the outcome failed.
func TestAttestation_RequiredTypeFailingTheArtifactStep(t *testing.T) {
	f := newAttestFixture(t, "warn")
	f.write(map[string]string{"finance/guard/ARTIFACT.md": hookArtifact("Guard the books")})
	f.setRequirements("warn", []string{"hook"}, buildReq(), scanReq(false))
	f.place(f.sign(f.subjects(), time.Minute), f.signScan(f.artifactSubjects(), 2*time.Minute))
	f.mustCycle()
	if o := f.outcome("finance/guard"); o != "verified" {
		t.Fatalf("hook outcome = %s", o)
	}
	f.place(f.sign(f.subjects(), 3*time.Minute))
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	want(t, "rejected", rejected(res), "finance/guard=ingest.attestation_missing/1")
	if f.record().Dropped != 1 || !slices.Contains(f.stored(), "finance/guard") {
		t.Fatalf("record %+v, stored %v", f.record(), f.stored())
	}
	want(t, "outcomes", []string{f.outcome("finance/guard"), f.outcome("finance/pay")},
		"failed ingest.attestation_missing/artifact//1", "failed ingest.attestation_missing/artifact//1")
}

// Spec: §4.7.11 (Layer step, Attestation record) — under warn a failed layer
// step withholds every artifact of a listed type: a changed hook stores no
// version and is reported with the layer step's code, a hook the snapshot
// omits is not removed, an unchanged hook keeps its outcome, the agents are
// stored as failed, and attestation_dropped counts the withheld artifact.
func TestAttestation_FailedLayerStepWithholdsRequiredTypes(t *testing.T) {
	f := newAttestFixture(t, "warn")
	f.write(map[string]string{
		"finance/guard/ARTIFACT.md": hookArtifact("Guard the books"),
		"finance/audit/ARTIFACT.md": hookArtifact("Audit the books"),
		"finance/keep/ARTIFACT.md":  hookArtifact("Keep the books"),
	})
	f.setRequirements("warn", []string{"hook"}, buildReq())
	f.signLayer(time.Minute)
	f.mustCycle()
	// An unsigned state: one hook changed, one removed, one unchanged, and a
	// changed agent.
	if err := os.RemoveAll(filepath.Join(f.dir, "finance", "audit")); err != nil {
		t.Fatal(err)
	}
	f.write(map[string]string{
		"finance/guard/ARTIFACT.md": strings.Replace(hookArtifact("Guard the books"), "echo hi", "curl evil", 1),
		"finance/pay/ARTIFACT.md":   strings.Replace(agentArtifact("Pay an invoice"), "1.0.0", "1.1.0", 1),
	})
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	want(t, "rejected", rejected(res), "finance/guard=ingest.attestation_mismatch/-", "finance/keep=ingest.attestation_mismatch/-")
	if rec := f.record(); rec.Status != ingest.CodeAttestationMismatch || rec.Dropped != 2 || len(res.Removed) != 0 {
		t.Fatalf("record %+v, removed %v", rec, res.Removed)
	}
	if !slices.Contains(f.stored(), "finance/audit") || f.outcome("finance/guard") != "verified" || f.outcome("finance/keep") != "verified" {
		t.Errorf("stored %v, guard %s, keep %s", f.stored(), f.outcome("finance/guard"), f.outcome("finance/keep"))
	}
	rec, err := f.st.GetManifest(context.Background(), "tenant-1", "finance/guard", "1.0.0")
	if err != nil || strings.Contains(string(rec.Body), "curl evil") {
		t.Errorf("the changed hook was stored: %v", err)
	}
	pay, err := f.st.GetManifest(context.Background(), "tenant-1", "finance/pay", "1.1.0")
	if err != nil || pay.AttestationOutcome != "failed" || pay.AttestationFailure.Scope != "layer" {
		t.Errorf("changed agent = %+v, %v", pay.AttestationFailure, err)
	}
}

// Spec: §4.7.11 (Artifact step), §4.7.11 (Layer step) — a party who removes
// the bundle of a second signer from a signed state drops each changed
// artifact that needs it, keeps each unchanged one that an older bundle of
// that signer lists, and the removals of the signed state apply, which is
// the state a lagging second signer produces.
func TestAttestation_WithheldBundleOfASecondSigner(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.write(map[string]string{"finance/old/ARTIFACT.md": agentArtifact("Old")})
	f.setRequirements("require", nil, buildReq(), scanReq(false))
	oldScan := f.signScan(f.artifactSubjects(), 2*time.Minute)
	f.place(f.sign(f.subjects(), time.Minute), oldScan)
	f.mustCycle()
	if err := os.RemoveAll(filepath.Join(f.dir, "finance", "old")); err != nil {
		t.Fatal(err)
	}
	f.write(map[string]string{"finance/pay/ARTIFACT.md": strings.Replace(agentArtifact("Pay an invoice"), "1.0.0", "1.1.0", 1)})
	// The signing run signed the new state, and its scan bundle is withheld:
	// only the older scan bundle stays in the directory.
	f.place(f.sign(f.subjects(), 3*time.Minute), oldScan)
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	want(t, "rejected", rejected(res), "finance/pay=ingest.attestation_mismatch/1")
	want(t, "removed", res.Removed, "finance/old")
	if rec := f.record(); rec.Status != "ok" || rec.Dropped != 1 || f.outcome("finance/close") != "verified" || f.outcome("finance/pay") != "verified" {
		t.Errorf("record %+v, close %s, pay@1.0.0 %s", rec, f.outcome("finance/close"), f.outcome("finance/pay"))
	}
}

// Spec: §4.7.11 (Re-evaluation) — a re-evaluation that downgrades the link
// of an optional requirement leaves the outcome verified, and one that
// downgrades the link of a requirement that is not optional sets failed
// with that requirement's position.
func TestAttestation_ReevaluationReadsRequirementsThatAreNotOptional(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.setRequirements("require", nil, buildReq(), scanReq(true))
	f.place(f.sign(f.subjects(), time.Minute), f.signScan(f.artifactSubjects(), 2*time.Minute))
	f.mustCycle()
	other := scanReq(true)
	other.Signers[0].Identity = "bob@acme.com"
	f.setRequirements("require", nil, buildReq(), other)
	f.reevaluate()
	if o := f.outcome("finance/pay"); o != "verified" || !slices.ContainsFunc(f.links(), func(l string) bool {
		return strings.Contains(l, scannerEmail) && strings.Contains(l, "unverified")
	}) {
		t.Fatalf("after the optional downgrade: %s %v", o, f.links())
	}
	f.setRequirements("require", nil, buildReq(), scanReq(false))
	f.mustCycle()
	other.Optional = false
	f.setRequirements("require", nil, buildReq(), other)
	f.reevaluate()
	if o := f.outcome("finance/pay"); o != "failed ingest.attestation_missing/artifact/link_downgraded/1" {
		t.Errorf("after the downgrade of requirement 1: %s", o)
	}
}

// Spec: §4.7.11 (Layer trust policy) — a scan requirement is met by a
// statement of its type whatever its predicate records, so a statement that
// records findings is accepted and the artifact is stored as verified.
func TestAttestation_ScanStatementWithFindingsMeetsTheRequirement(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.setRequirements("require", nil, buildReq(), scanReq(false))
	var st map[string]any
	if err := json.Unmarshal(sh.Statement(t, scanType, f.artifactSubjects()...), &st); err != nil {
		t.Fatal(err)
	}
	st["predicate"] = map[string]any{"findings": []any{map[string]any{"severity": "critical", "rule": "secret-in-body"}}}
	payload, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	scan := f.h.DSSEBundle(payload, sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(f.h.Clock().Add(2*time.Minute))), sh.WithLeaf(sh.LeafEmail(scannerEmail)))
	f.place(f.sign(f.subjects(), time.Minute), scan)
	res, err := f.cycle()
	if err != nil || res.Accepted != 2 || len(res.Rejected) != 0 || f.outcome("finance/pay") != "verified" {
		t.Fatalf("cycle: %+v, %v", res, err)
	}
}

// Spec: §4.7.11 (Artifact step) — an artifact of a listed type that a
// failed layer step withholds under warn stays in the set the cycle's lint
// reads, so an agent whose prose references it by its canonical ID is
// stored; the same reference fails lint once the hook leaves the snapshot.
func TestAttestation_WithheldRequiredTypeStillResolvesReferences(t *testing.T) {
	f := newAttestFixture(t, "warn")
	referencing := func(version string) string {
		return strings.Replace(strings.Replace(agentArtifact("Pay an invoice"), "1.0.0", version, 1), "\n\n", "\n\nSee [the guard](finance/guard).\n\n", 1)
	}
	f.write(map[string]string{"finance/guard/ARTIFACT.md": hookArtifact("Guard the books"), "finance/pay/ARTIFACT.md": referencing("1.0.0")})
	f.setRequirements("warn", []string{"hook"}, buildReq())
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	want(t, "rejected", rejected(res), "finance/guard=ingest.attestation_missing/-")
	if len(res.LintFailures) != 0 || !slices.Contains(f.stored(), "finance/pay") || slices.Contains(f.stored(), "finance/guard") {
		t.Fatalf("lint failures %v, stored %v", res.LintFailures, f.stored())
	}
	if err := os.RemoveAll(filepath.Join(f.dir, "finance", "guard")); err != nil {
		t.Fatal(err)
	}
	f.write(map[string]string{"finance/pay/ARTIFACT.md": referencing("1.1.0")})
	if res, _ = f.cycle(); len(res.LintFailures) != 1 || res.LintFailures[0].ArtifactID != "finance/pay" {
		t.Fatalf("an unresolved reference: %+v", res.LintFailures)
	}
}
