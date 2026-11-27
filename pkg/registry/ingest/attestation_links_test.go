package ingest_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// links renders the stored links of the fixture's tenant, one per line, as
// version, key, digest prefix, verdict, and whether a key is recorded.
func (f *attestFixture) links() []string {
	f.t.Helper()
	ls, err := f.st.ListAttestationLinks(context.Background(), "tenant-1", store.LinkFilter{})
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, l := range ls {
		out = append(out, fmt.Sprintf("%s %s/%s %s %s keyed=%v", l.ArtifactID, l.Instance, l.SignerIdentity,
			short(l.Digest), l.Verdict, l.VerifiedUnder != ""))
	}
	return out
}

// short returns the first 12 characters of a digest's hexadecimal part.
func short(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// outcome renders the stored attestation outcome of id@1.0.0.
func (f *attestFixture) outcome(id string) string {
	f.t.Helper()
	rec, err := f.st.GetManifest(context.Background(), "tenant-1", id, "1.0.0")
	if err != nil {
		f.t.Fatal(err)
	}
	if p := rec.AttestationFailure; p != nil {
		req := "-"
		if p.Requirement != nil {
			req = fmt.Sprint(*p.Requirement)
		}
		return fmt.Sprintf("%s %s/%s/%s/%s", rec.AttestationOutcome, p.Code, p.Scope, p.Constraint, req)
	}
	return rec.AttestationOutcome
}

// bundles returns the digest prefixes of the stored bundles.
func (f *attestFixture) bundles() []string {
	f.t.Helper()
	as, err := f.st.ListAttestations(context.Background(), "tenant-1")
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, a := range as {
		out = append(out, short(a.Digest))
	}
	return out
}

// reevaluate runs the re-evaluation a policy change runs.
func (f *attestFixture) reevaluate() {
	f.t.Helper()
	lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.cfg.Reevaluate(context.Background(), f.st, lc, f.clock, func(typ, target string, fields map[string]string) {
		f.events = append(f.events, auditRecord{typ, target, fields})
	}); err != nil {
		f.t.Fatal(err)
	}
}

// mustCycle runs a cycle and fails the test on an error.
func (f *attestFixture) mustCycle() {
	f.t.Helper()
	if _, err := f.cycle(); err != nil {
		f.t.Fatalf("cycle: %v", err)
	}
}

// want compares got with the expected lines.
func want(t *testing.T, label string, got []string, lines ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(lines, "\n") {
		t.Errorf("%s:\n%s\nwant:\n%s", label, strings.Join(got, "\n"), strings.Join(lines, "\n"))
	}
}

// Spec: §4.7.11 (Verified links, Attestation storage, Attestation outcome) —
// a passed cycle links each version to the accepted bundle, stores the
// bundle once, and sets the outcome verified; an unchanged layer signed
// again keeps its link and stores no new bundle.
func TestAttestationLinks_VerifiedLinksAndOutcome(t *testing.T) {
	f := newAttestFixture(t, "require")
	first := f.sign(f.subjects(), time.Minute)
	f.place(first)
	f.mustCycle()
	d := short(attest.AttestationDigest(first))
	want(t, "links", f.links(),
		"finance/close github/"+sh.DefaultSAN+" "+d+" verified keyed=true",
		"finance/pay github/"+sh.DefaultSAN+" "+d+" verified keyed=true")
	want(t, "bundles", f.bundles(), d)
	if got := f.outcome("finance/pay"); got != "verified" {
		t.Errorf("outcome %s", got)
	}
	a, err := f.st.GetAttestation(context.Background(), "tenant-1", attest.AttestationDigest(first))
	if err != nil || string(a.Bundle) != string(first) || a.LayerID != "team" || a.Instance != "github" ||
		a.SignerIdentity != sh.DefaultSAN || a.PredicateType != slsaType || a.LogIndex != nil || !a.AttestedAt.Equal(f.h.Clock().Add(time.Minute)) {
		t.Errorf("stored bundle %+v, %v", a, err)
	}
	// A later signing run of the unchanged layer: the stored link is kept.
	f.place(first, f.sign(f.subjects(), 2*time.Minute))
	f.mustCycle()
	want(t, "links after a second signing run", f.links(),
		"finance/close github/"+sh.DefaultSAN+" "+d+" verified keyed=true",
		"finance/pay github/"+sh.DefaultSAN+" "+d+" verified keyed=true")
	want(t, "bundles after a second signing run", f.bundles(), d)
}

// Spec: §4.7.11 (Attestation outcome) — under warn, a version the cycle
// stores after a failed layer step is failed with the scope layer, and an
// unchanged version keeps its outcome and its link.
func TestAttestationLinks_WarnOutcomes(t *testing.T) {
	f := newAttestFixture(t, "warn")
	f.signLayer(time.Minute)
	f.mustCycle()
	f.write(map[string]string{"finance/extra/ARTIFACT.md": agentArtifact("Extra")})
	f.mustCycle()
	want(t, "outcomes", []string{f.outcome("finance/pay"), f.outcome("finance/extra")},
		"verified", "failed ingest.attestation_mismatch/layer/layer_digest/-")
	if n := len(f.links()); n != 2 {
		t.Errorf("links %v", f.links())
	}
}

// Spec: §4.7.11 (Re-evaluation) — a signer removed from the policy: the
// re-evaluation of the policy change downgrades its links, sets the outcome
// failed with link_downgraded, and records layer.attestation_links_downgraded;
// a later cycle reads no stored bundle again, and a re-signed layer links a
// verified bundle again.
func TestAttestationLinks_RemovedSignerDowngrades(t *testing.T) {
	f := newAttestFixture(t, "warn")
	f.signLayer(time.Minute)
	f.mustCycle()
	other := emailSigner()
	other.Identity = "carol@acme.com"
	f.setPolicy("warn", other)
	f.reevaluate()
	for _, l := range f.links() {
		if !strings.HasSuffix(l, "unverified keyed=false") {
			t.Errorf("link %s", l)
		}
	}
	if got := f.outcome("finance/pay"); got != "failed ingest.attestation_missing/artifact/link_downgraded/0" {
		t.Errorf("outcome %s", got)
	}
	ev, ok := f.lastEvent("layer.attestation_links_downgraded")
	if !ok || ev.fields["cause"] != "policy_change" || !strings.Contains(ev.fields["digests"], "sha256:") {
		t.Errorf("event %+v", ev)
	}
	// The next cycle selects no link, because none is verified; under warn
	// it stores nothing new and keeps the failed outcome of the unchanged
	// versions.
	f.mustCycle()
	if got := f.outcome("finance/pay"); !strings.HasPrefix(got, "failed") {
		t.Errorf("outcome after a cycle %s", got)
	}
	// Signed by the new signer, the layer is linked and verified again.
	f.place(f.sign(f.subjects(), 3*time.Minute, sh.WithLeaf(sh.LeafEmail("carol@acme.com"))))
	f.mustCycle()
	if got := f.outcome("finance/pay"); got != "verified" {
		t.Errorf("outcome after re-signing %s", got)
	}
	if !strings.Contains(strings.Join(f.links(), "\n"), "github/carol@acme.com") {
		t.Errorf("links %v", f.links())
	}
}

// countingStore counts the stored bundles a store returns.
type countingStore struct {
	store.Store
	reads atomic.Int64
}

func (c *countingStore) GetAttestation(ctx context.Context, tenantID, digest string) (store.StoredAttestation, error) {
	c.reads.Add(1)
	return c.Store.GetAttestation(ctx, tenantID, digest)
}

// Spec: §4.7.11 (Re-evaluation, Policy key) — a cycle that follows no change
// of policy or root record reads no stored bundle; a policy change that
// keeps the signer re-verifies each distinct bundle once and records the new
// key.
func TestAttestationLinks_ReevaluationReadsOnlyAfterAChange(t *testing.T) {
	f := newAttestFixture(t, "require")
	cs := &countingStore{Store: f.st}
	f.st = cs
	f.signLayer(time.Minute)
	f.mustCycle()
	f.mustCycle()
	if n := cs.reads.Load(); n != 0 {
		t.Fatalf("a cycle with no change read %d bundles", n)
	}
	s := emailSigner()
	f.setPolicy("require", s, store.TrustSigner{Issuer: sh.DefaultIssuer, Identity: "dave@acme.com", Instance: "github"})
	f.reevaluate()
	if n := cs.reads.Load(); n != 1 {
		t.Fatalf("the re-evaluation read %d bundles, want 1", n)
	}
	for _, l := range f.links() {
		if !strings.HasSuffix(l, " verified keyed=true") {
			t.Errorf("link %s", l)
		}
	}
	f.mustCycle()
	if n := cs.reads.Load(); n != 1 {
		t.Errorf("a cycle after the re-evaluation read %d more bundles", n-1)
	}
}

// Spec: §4.7.11 (Root record, Re-evaluation) — a process behind the root
// record downgrades only a link no signer matches and leaves every other
// selected link with its recorded key; a process ahead of the record
// replaces it.
func TestAttestationLinks_BehindTheRootRecord(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	f.mustCycle()
	ctx := context.Background()
	roots, _ := f.st.ListRootRecords(ctx)
	if len(roots) != 1 || roots[0].Instance != "github" || !strings.HasPrefix(roots[0].RootDigest, "sha256:") {
		t.Fatalf("root records %+v", roots)
	}
	// Another process holds a newer root for the instance.
	newer := store.RootRecord{Instance: "github", RootDigest: "sha256:newer", RootTime: f.h.Clock().Add(time.Hour)}
	if ok, err := f.st.ReplaceRootRecord(ctx, &roots[0], newer); !ok || err != nil {
		t.Fatalf("replace: %v %v", ok, err)
	}
	// A policy change that keeps the signer: the links match a signer, so a
	// process behind the record leaves them with their key.
	f.setPolicy("require", emailSigner(), store.TrustSigner{Issuer: sh.DefaultIssuer, Identity: "dave@acme.com", Instance: "github"})
	f.reevaluate()
	before := f.links()
	for _, l := range before {
		if !strings.HasSuffix(l, " verified keyed=true") {
			t.Errorf("link %s", l)
		}
	}
	// A signer that left the policy: the certificate matches no signer.
	f.setPolicy("require", store.TrustSigner{Issuer: sh.DefaultIssuer, Identity: "dave@acme.com", Instance: "github"})
	f.reevaluate()
	for _, l := range f.links() {
		if !strings.HasSuffix(l, " unverified keyed=false") {
			t.Errorf("link %s", l)
		}
	}
	if roots, _ := f.st.ListRootRecords(ctx); roots[0].RootDigest != "sha256:newer" {
		t.Errorf("a process behind the record replaced it: %+v", roots)
	}
}

// Spec: §4.7.11 (Re-evaluation, Unverified links) — a change to none
// downgrades every verified link and sets every outcome to not_evaluated; a
// cycle under none links the bundles whose signature, chain, and time verify
// as unverified, and none whose certificate another authority issued; a
// return to require links verified bundles again.
func TestAttestationLinks_ModeNoneAndBack(t *testing.T) {
	f := newAttestFixture(t, "require")
	signed := f.sign(f.subjects(), time.Minute)
	f.place(signed)
	f.mustCycle()
	f.setPolicy("none", emailSigner())
	f.reevaluate()
	want(t, "outcomes under none", []string{f.outcome("finance/pay"), f.outcome("finance/close")}, "not_evaluated", "not_evaluated")
	for _, l := range f.links() {
		if !strings.HasSuffix(l, " unverified keyed=false") {
			t.Errorf("link %s", l)
		}
	}
	// A bundle whose certificate another authority issued is not linked.
	forged := sh.New(t).DSSEBundle(sh.Statement(t, slsaType, f.subjects()...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(f.h.Clock().Add(2*time.Minute))), sh.WithLeaf(sh.LeafEmail("mallory@acme.com")))
	f.place(signed, forged)
	f.mustCycle()
	d := short(attest.AttestationDigest(signed))
	want(t, "links under none", f.links(),
		"finance/close github/"+sh.DefaultSAN+" "+d+" unverified keyed=false",
		"finance/pay github/"+sh.DefaultSAN+" "+d+" unverified keyed=false")
	if r := f.record(); r.Status != "ok" {
		t.Errorf("a cycle under none wrote the record: %+v", r)
	}
	f.setPolicy("require", emailSigner())
	f.mustCycle()
	want(t, "links back under require", f.links(),
		"finance/close github/"+sh.DefaultSAN+" "+d+" verified keyed=true",
		"finance/pay github/"+sh.DefaultSAN+" "+d+" verified keyed=true")
	if got := f.outcome("finance/pay"); got != "verified" {
		t.Errorf("outcome %s", got)
	}
}

// Spec: §4.7.11 (Unverified links) — under none a directory over the bundle
// limit changes no link, a file over the size limit is not linked, and the
// keys beyond the link limit go to the bundles with the latest attested
// times.
func TestAttestationLinks_NoneLimits(t *testing.T) {
	f := newAttestFixture(t, "none")
	var bundles [][]byte
	for i := 0; i < store.MaxAttestationLinks+1; i++ {
		bundles = append(bundles, f.sign(f.subjects(), time.Duration(i+1)*10*time.Second,
			sh.WithLeaf(sh.LeafEmail(fmt.Sprintf("signer%02d@acme.com", i)))))
	}
	f.place(bundles...)
	f.mustCycle()
	links := f.links()
	if len(links) != 2*store.MaxAttestationLinks || strings.Contains(strings.Join(links, "\n"), "signer00@") {
		t.Fatalf("links %d: %v", len(links), links)
	}
	f.cfg.MaxBundles = 2
	f.place(bundles[0])
	f.place(bundles[:3]...)
	f.mustCycle()
	if got := len(f.links()); got != 2*store.MaxAttestationLinks {
		t.Errorf("a directory over the limit changed the links: %d", got)
	}
	f.cfg.MaxBundles, f.cfg.MaxBundleBytes = 0, 100
	f.place(bundles[0])
	f.mustCycle()
	if got := f.links(); len(got) != 0 {
		t.Errorf("an oversized bundle was linked: %v", got)
	}
}

// Spec: §4.7.11 (Attestation storage) — two instances whose certificates
// carry one issuer and identity hold separate links.
func TestAttestationLinks_TwoInstancesOneIdentity(t *testing.T) {
	f := newAttestFixture(t, "require")
	h2 := sh.New(t)
	root2, err := sign.ParseAttestationRoot(h2.TrustedRootJSON(sh.GitHubStyleRoot()...))
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Instances["staging"] = sign.AttestationInstance{Name: "staging", Root: root2}
	second := emailSigner()
	second.Instance = "staging"
	f.setPolicy("require", emailSigner(), second)
	f.place(f.sign(f.subjects(), time.Minute),
		h2.DSSEBundle(sh.Statement(t, slsaType, f.subjects()...), sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h2.Clock().Add(time.Minute)))))
	f.mustCycle()
	got := strings.Join(f.links(), "\n")
	if strings.Count(got, "finance/pay ") != 2 || !strings.Contains(got, "github/") || !strings.Contains(got, "staging/") {
		t.Errorf("links:\n%s", got)
	}
}
