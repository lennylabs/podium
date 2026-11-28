package ingest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// faultyStore fails the named attestation methods of the store it wraps.
type faultyStore struct {
	store.Store
	fail      map[string]error
	noReplace bool
}

func (f *faultyStore) err(name string) error { return f.fail[name] }

func (f *faultyStore) ListRootRecords(ctx context.Context) ([]store.RootRecord, error) {
	if err := f.err("ListRootRecords"); err != nil {
		return nil, err
	}
	return f.Store.ListRootRecords(ctx)
}

func (f *faultyStore) ReplaceRootRecord(ctx context.Context, prev *store.RootRecord, next store.RootRecord) (bool, error) {
	if err := f.err("ReplaceRootRecord"); err != nil {
		return false, err
	}
	if f.noReplace {
		// Another process wrote the record between the read and the write.
		_, _ = f.Store.ReplaceRootRecord(ctx, prev, store.RootRecord{Instance: next.Instance, RootDigest: "sha256:other", RootTime: next.RootTime})
		return false, nil
	}
	return f.Store.ReplaceRootRecord(ctx, prev, next)
}

func (f *faultyStore) ListAttestationLinks(ctx context.Context, tenantID string, lf store.LinkFilter) ([]store.AttestationLink, error) {
	if err := f.err("ListAttestationLinks"); err != nil {
		return nil, err
	}
	return f.Store.ListAttestationLinks(ctx, tenantID, lf)
}

func (f *faultyStore) GetAttestation(ctx context.Context, tenantID, digest string) (store.StoredAttestation, error) {
	if err := f.err("GetAttestation"); err != nil {
		return store.StoredAttestation{}, err
	}
	return f.Store.GetAttestation(ctx, tenantID, digest)
}

func (f *faultyStore) WriteAttestations(ctx context.Context, tenantID, layerID, claim string, w store.AttestationWrites) error {
	if err := f.err("WriteAttestations"); err != nil {
		return err
	}
	return f.Store.WriteAttestations(ctx, tenantID, layerID, claim, w)
}

func (f *faultyStore) ListManifestsIncludingDeleted(ctx context.Context, tenantID string) ([]store.ManifestRecord, error) {
	if err := f.err("ListManifestsIncludingDeleted"); err != nil {
		return nil, err
	}
	return f.Store.ListManifestsIncludingDeleted(ctx, tenantID)
}

// Spec: §4.7.11 (Re-evaluation) — a policy change that a cycle finds after
// its claim downgrades the links of a removed signer in that cycle, records
// the event with the cause cycle, and keeps the claim, so the refusal of the
// layer step that follows leaves the downgrade in place.
func TestAttestationReeval_CycleDowngradesAfterAPolicyChange(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	f.mustCycle()
	other := emailSigner()
	other.Identity = "carol@acme.com"
	f.setPolicy("require", other)
	if _, err := f.cycle(); err == nil {
		t.Fatal("a cycle with no bundle of the new signer passed")
	}
	if ev, ok := f.lastEvent("layer.attestation_links_downgraded"); !ok || ev.fields["cause"] != "cycle" {
		t.Errorf("event %+v", ev)
	}
	if got := f.outcome("finance/close"); got != "failed ingest.attestation_missing/artifact/link_downgraded/0" {
		t.Errorf("outcome %s", got)
	}
}

// Spec: §4.7.11 (Re-evaluation, Root record) — store failures of the
// re-evaluation fail the cycle before its snapshot, a root record that
// another process wrote between the read and the write is read again, a
// bundle the store no longer holds is accepted by no requirement, and a
// later claim ends the cycle as superseded.
func TestAttestationReeval_StoreFailures(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name    string
		fail    map[string]error
		change  bool
		wantErr bool
		check   func(t *testing.T, f *attestFixture, res *ingest.Result)
	}{
		{name: "root records unreadable", fail: map[string]error{"ListRootRecords": boom}, wantErr: true},
		{name: "root record not written", fail: map[string]error{"ReplaceRootRecord": boom}, wantErr: true},
		{name: "links unreadable", fail: map[string]error{"ListAttestationLinks": boom}, wantErr: true},
		{name: "bundle unreadable", fail: map[string]error{"GetAttestation": boom}, change: true, wantErr: true},
		{name: "write failed", fail: map[string]error{"WriteAttestations": boom}, change: true, wantErr: true},
		{name: "superseded", fail: map[string]error{"WriteAttestations": store.ErrIngestSuperseded}, change: true,
			check: func(t *testing.T, _ *attestFixture, res *ingest.Result) {
				if !res.Superseded {
					t.Errorf("result %+v", res)
				}
			}},
		{name: "bundle gone", fail: map[string]error{"GetAttestation": store.ErrNotFound}, change: true,
			check: func(t *testing.T, f *attestFixture, _ *ingest.Result) {
				// The re-evaluation downgraded the links; the layer step that
				// follows accepts the bundle again and links it as verified.
				if _, ok := f.lastEvent("layer.attestation_links_downgraded"); !ok {
					t.Errorf("no downgrade event; links %v", f.links())
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttestFixture(t, "warn")
			f.signLayer(time.Minute)
			if tc.change {
				f.mustCycle()
				f.setPolicy("warn", emailSigner(), store.TrustSigner{Issuer: sh.DefaultIssuer, Identity: "dave@acme.com", Instance: "github"})
			}
			inner := f.st
			f.st = &faultyStore{Store: inner, fail: tc.fail}
			res, err := f.cycle()
			f.st = inner
			if (err != nil) != tc.wantErr {
				t.Fatalf("cycle: %v, want an error %v", err, tc.wantErr)
			}
			if tc.check != nil {
				tc.check(t, f, res)
			}
		})
	}
}

// Spec: §4.7.11 (Root record) — a root record that another process wrote
// between this process's read and its write is read again, and this process
// is behind it.
func TestAttestationReeval_RootRecordRace(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	inner := f.st
	f.st = &faultyStore{Store: inner, noReplace: true}
	f.mustCycle()
	f.st = inner
	roots, err := f.st.ListRootRecords(context.Background())
	if err != nil || len(roots) != 1 || roots[0].RootDigest != "sha256:other" {
		t.Fatalf("roots %+v %v", roots, err)
	}
	for _, l := range f.links() {
		if !strings.HasSuffix(l, " verified keyed=false") {
			t.Errorf("a process behind the record keyed its link: %s", l)
		}
	}
}

// Spec: §4.7.11 (Re-evaluation) — a policy change re-evaluates nothing for
// a layer whose policy is invalid or that no configuration verifies, and a
// change to none that cannot read the versions of the layer fails.
func TestAttestationReeval_PolicyChangeEdges(t *testing.T) {
	ctx := context.Background()
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	f.mustCycle()
	lc, _ := f.st.GetLayerConfig(ctx, "tenant-1", "team")
	var nilCfg *ingest.AttestationConfig
	if err := nilCfg.Reevaluate(ctx, f.st, lc, nil, nil); err != nil {
		t.Errorf("nil configuration: %v", err)
	}
	bad := lc
	bad.TrustRequirements[0].Signers[0].Instance = "staging"
	if err := f.cfg.Reevaluate(ctx, f.st, bad, nil, nil); err != nil {
		t.Errorf("invalid policy: %v", err)
	}
	none := lc
	none.TrustMode = "none"
	failing := &faultyStore{Store: f.st, fail: map[string]error{"ListManifestsIncludingDeleted": errors.New("boom")}}
	if err := f.cfg.Reevaluate(ctx, failing, none, nil, nil); err == nil {
		t.Error("a change to none that cannot read the versions passed")
	}
	// Without an audit emitter the downgrade is logged.
	if err := f.cfg.Reevaluate(ctx, f.st, none, nil, nil); err != nil {
		t.Errorf("change to none: %v", err)
	}
	if strings.Contains(strings.Join(f.links(), "\n"), " verified ") {
		t.Errorf("links %v", f.links())
	}
}

// Spec: §4.7.11 (Attestation storage) — a bundle of an instance that keeps a
// transparency log is stored with its log index.
func TestAttestationLinks_LogIndexStored(t *testing.T) {
	f := newAttestFixture(t, "require")
	root, err := sign.ParseAttestationRoot(f.h.TrustedRootJSON(sh.WithCTLogKey()))
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Instances["rekor"] = sign.AttestationInstance{Name: "rekor", Root: root, TransparencyLog: true, CertificateTransparency: true}
	s := emailSigner()
	s.Instance = "rekor"
	f.setPolicy("require", s)
	f.place(f.h.DSSEBundle(sh.Statement(t, slsaType, f.subjects()...), sh.WithSCT(), sh.WithRekorV1Entry()))
	f.mustCycle()
	as, err := f.st.ListAttestations(context.Background(), "tenant-1")
	if err != nil || len(as) != 1 || as[0].LogIndex == nil {
		t.Fatalf("bundles %+v %v", as, err)
	}
}

// Spec: §4.7.11 (Verified links) — a link plan that cannot read the stored
// links, or cannot write, fails the cycle; a later claim refusing the write
// is reported by the removal check.
func TestAttestationLinks_PlanStoreFailures(t *testing.T) {
	f := newAttestFixture(t, "none")
	f.signLayer(time.Minute)
	inner := f.st
	f.st = &faultyStore{Store: inner, fail: map[string]error{"WriteAttestations": errors.New("boom")}}
	if _, err := f.cycle(); err == nil {
		t.Error("a failed link write passed")
	}
	f.st = &faultyStore{Store: inner, fail: map[string]error{"WriteAttestations": store.ErrIngestSuperseded}}
	if _, err := f.cycle(); err != nil {
		t.Errorf("a superseded link write: %v", err)
	}
	f.st = inner
	f.cfg = nil
	f.mustCycle()
}
