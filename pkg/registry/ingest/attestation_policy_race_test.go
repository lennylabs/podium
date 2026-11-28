package ingest_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// hookStore runs a hook once, before the first call of the named method,
// and counts the tenant listings of the versions.
type hookStore struct {
	store.Store
	onList func()
	onGet  func()
	lists  int
}

func (h *hookStore) ListManifestsIncludingDeleted(ctx context.Context, tenantID string) ([]store.ManifestRecord, error) {
	h.lists++
	if fn := h.onList; fn != nil {
		h.onList = nil
		fn()
	}
	return h.Store.ListManifestsIncludingDeleted(ctx, tenantID)
}

func (h *hookStore) GetAttestation(ctx context.Context, tenantID, digest string) (store.StoredAttestation, error) {
	if fn := h.onGet; fn != nil {
		h.onGet = nil
		fn()
	}
	return h.Store.GetAttestation(ctx, tenantID, digest)
}

// carolSigner is a signer the fixture's bundles do not name.
func carolSigner() store.TrustSigner {
	s := emailSigner()
	s.Identity = "carol@acme.com"
	return s
}

// Spec: §4.7.11 (Verified links, Re-evaluation) — a policy change that lands
// while a cycle claimed under the earlier policy is in flight: the cycle's
// link and outcome write is refused, so it stops as superseded, the links
// the change downgraded stay unverified, the outcome stays failed, and a
// version the cycle stored keeps not_evaluated.
func TestAttestationPolicyRace_InFlightCycleWritesNothing(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	f.mustCycle()
	f.write(map[string]string{"finance/extra/ARTIFACT.md": agentArtifact("Extra")})
	f.signLayer(2 * time.Minute)
	inner := f.st
	h := &hookStore{Store: inner}
	h.onList = func() {
		f.st = inner
		f.setPolicy("require", carolSigner())
		f.reevaluate()
		f.st = h
	}
	f.st = h
	res, err := f.cycle()
	f.st = inner
	if err != nil || !res.Superseded {
		t.Fatalf("cycle: %+v %v, want superseded", res, err)
	}
	if got := f.outcome("finance/pay"); !strings.HasPrefix(got, "failed") {
		t.Errorf("outcome %s", got)
	}
	if got := f.outcome("finance/extra"); got != store.OutcomeNotEvaluated {
		t.Errorf("stored version outcome %s", got)
	}
	if links := strings.Join(f.links(), "\n"); strings.Contains(links, " verified ") {
		t.Errorf("links:\n%s", links)
	}
}

// Spec: §4.7.11 (Re-evaluation) — a policy that changed outside the layer
// endpoints, such as a declaration or the trust default changed between two
// starts, is re-evaluated by the start: a removed signer downgrades the
// links and the outcome, and a change to none clears every outcome, each
// recorded with the cause start.
func TestAttestationPolicyRace_StartReevaluatesStoredLayers(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, mode, want string
		signers          []store.TrustSigner
	}{
		{"signer removed", "require", "failed ingest.attestation_missing/artifact/link_downgraded/0", []store.TrustSigner{carolSigner()}},
		{"mode none", "none", store.OutcomeNotEvaluated, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttestFixture(t, "require")
			f.signLayer(time.Minute)
			f.mustCycle()
			f.setPolicy(tc.mode, tc.signers...)
			if err := f.cfg.ReevaluateStoredLayers(ctx, f.st, "tenant-1", f.clock, func(typ, target string, fields map[string]string) {
				f.events = append(f.events, auditRecord{typ, target, fields})
			}); err != nil {
				t.Fatal(err)
			}
			if got := f.outcome("finance/pay"); got != tc.want {
				t.Errorf("outcome %s, want %s", got, tc.want)
			}
			if ev, ok := f.lastEvent("layer.attestation_links_downgraded"); !ok || ev.fields["cause"] != "start" {
				t.Errorf("event %+v", ev)
			}
		})
	}
	failing := &faultyStore{Store: store.NewMemory(), fail: map[string]error{"ListManifestsIncludingDeleted": errFaulty}}
	if err := (&faultyCfg).ReevaluateStoredLayers(ctx, failing, "t", nil, nil); err == nil {
		t.Error("a start that cannot read the versions passed")
	}
}

// Spec: §4.7.11 (Re-evaluation) — two claim-less re-evaluations: one under
// an earlier policy reads a link, a later one downgrades it, and the earlier
// one's update does not apply, because it is conditional on the verdict and
// the key it read; only the applied downgrade is recorded.
func TestAttestationPolicyRace_ReevaluationsDoNotReverify(t *testing.T) {
	ctx := context.Background()
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	f.mustCycle()
	dave := emailSigner()
	dave.Identity = "dave@acme.com"
	f.setPolicy("require", carolSigner(), dave)
	p2, _ := f.st.GetLayerConfig(ctx, "tenant-1", "team")
	f.setPolicy("require", carolSigner())
	p3, _ := f.st.GetLayerConfig(ctx, "tenant-1", "team")
	inner := f.st
	emit := func(typ, target string, fields map[string]string) {
		f.events = append(f.events, auditRecord{typ, target, fields})
	}
	h := &hookStore{Store: inner, onGet: func() {
		if err := f.cfg.Reevaluate(ctx, inner, p3, f.clock, emit); err != nil {
			t.Fatal(err)
		}
	}}
	if err := f.cfg.Reevaluate(ctx, h, p2, f.clock, emit); err != nil {
		t.Fatal(err)
	}
	if links := strings.Join(f.links(), "\n"); strings.Contains(links, " verified ") {
		t.Errorf("links:\n%s", links)
	}
	n := 0
	for _, ev := range f.events {
		if ev.typ == "layer.attestation_links_downgraded" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d downgrade events, want the one whose update applied", n)
	}
}

// Spec: §4.7.11 (Re-evaluation) — a cycle under none reads the tenant's
// versions once: the cycle's own write clears the outcomes of the versions
// no longer in the tree.
func TestAttestationPolicyRace_NoneCycleReadsTheVersionsOnce(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	f.mustCycle()
	f.write(map[string]string{"finance/extra/ARTIFACT.md": agentArtifact("Extra")})
	f.signLayer(2 * time.Minute)
	f.mustCycle()
	f.setPolicy("none")
	h := &hookStore{Store: f.st}
	f.st = h
	f.mustCycle()
	if h.lists != 1 {
		t.Errorf("a cycle under none read the versions %d times", h.lists)
	}
	f.st = h.Store
	if got := f.outcome("finance/extra"); got != store.OutcomeNotEvaluated {
		t.Errorf("outcome %s", got)
	}
}

// Spec: §4.7.11 (Re-evaluation) — a requirement accepts a link only through
// a signer of the instance the link is keyed on: when the only accepting
// signer moves to another instance that trusts the same root, the link
// keyed on the first instance is downgraded rather than kept.
func TestAttestationPolicyRace_InstanceOfTheLink(t *testing.T) {
	f := newAttestFixture(t, "require")
	root, err := sign.ParseAttestationRoot(f.h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Instances["mirror"] = sign.AttestationInstance{Name: "mirror", Root: root}
	f.signLayer(time.Minute)
	f.mustCycle()
	moved := emailSigner()
	moved.Instance = "mirror"
	f.setPolicy("require", moved)
	f.reevaluate()
	for _, l := range f.links() {
		if !strings.Contains(l, "github/") || !strings.HasSuffix(l, " unverified keyed=false") {
			t.Errorf("link %s", l)
		}
	}
}

// Spec: §4.7.11 (Verified links) — a version a cycle restores from a
// soft-deleted row holds only the links that cycle gives it, so the links of
// its earlier life do not return with it.
func TestAttestationPolicyRace_RestoredVersionDropsItsOldLinks(t *testing.T) {
	f := newAttestFixture(t, "warn")
	f.signLayer(time.Minute)
	f.mustCycle()
	body := agentArtifact("Pay an invoice")
	if err := removeAll(f.dir, "finance/pay"); err != nil {
		t.Fatal(err)
	}
	f.place()
	f.mustCycle()
	if !strings.Contains(strings.Join(f.links(), "\n"), "finance/pay ") {
		t.Fatalf("the removed version holds no link to keep: %v", f.links())
	}
	f.write(map[string]string{"finance/pay/ARTIFACT.md": body})
	f.mustCycle()
	for _, l := range f.links() {
		if strings.HasPrefix(l, "finance/pay ") {
			t.Errorf("the restored version kept %s", l)
		}
	}
}

// Spec: §4.7.11 (Verified links) — the policy a cycle's write compares is
// the stored form of the policy the claim read, so an unchanged policy reads
// as unchanged on SQLite for identities that the JSON encoder escapes or
// replaces.
func TestAttestationPolicyRace_UnchangedPolicyRoundTripsOnSQLite(t *testing.T) {
	ctx := context.Background()
	for name, ident := range map[string]string{"ascii": "alice@acme.com", "html": "<a&b>@acme.com", "invalid utf8": "al\xffice@acme.com", "line separator": "a b"} {
		t.Run(name, func(t *testing.T) {
			st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "p.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if err := st.CreateTenant(ctx, store.Tenant{ID: "tenant-1", Name: "tenant-1"}); err != nil {
				t.Fatal(err)
			}
			lc := store.LayerConfig{TenantID: "tenant-1", ID: "team", SourceType: "local", LocalPath: t.TempDir(), TrustMode: "require",
				TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "https://i", Identity: ident, Instance: "github"}}}}}
			if err := st.PutLayerConfig(ctx, lc); err != nil {
				t.Fatal(err)
			}
			claim, _, claimed, err := ingest.ClaimLayer(ctx, st, "tenant-1", "team")
			if err != nil {
				t.Fatal(err)
			}
			p := store.PolicyOf(claimed)
			if _, err := st.WriteAttestations(ctx, "tenant-1", "team", claim, store.AttestationWrites{Policy: &p}); err != nil {
				t.Errorf("an unchanged policy %q: %v", ident, err)
			}
		})
	}
}
