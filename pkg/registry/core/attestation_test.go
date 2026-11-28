package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// attStore fails the named reads of the store it wraps.
type attStore struct {
	store.Store
	fail map[string]bool
}

var errAttStore = errors.New("store down")

func (s *attStore) ListAttestationLinks(ctx context.Context, tenant string, f store.LinkFilter) ([]store.AttestationLink, error) {
	if s.fail["links"] {
		return nil, errAttStore
	}
	return s.Store.ListAttestationLinks(ctx, tenant, f)
}

func (s *attStore) GetAttestation(ctx context.Context, tenant, digest string) (store.StoredAttestation, error) {
	if s.fail["bundle"] {
		return store.StoredAttestation{}, errAttStore
	}
	if s.fail["bundleGone"] {
		return store.StoredAttestation{}, store.ErrNotFound
	}
	return s.Store.GetAttestation(ctx, tenant, digest)
}

func (s *attStore) ListAttestations(ctx context.Context, tenant string) ([]store.StoredAttestation, error) {
	if s.fail["bundles"] {
		return nil, errAttStore
	}
	return s.Store.ListAttestations(ctx, tenant)
}

func (s *attStore) GetLayerConfig(ctx context.Context, tenant, id string) (store.LayerConfig, error) {
	if s.fail["layer"] {
		return store.LayerConfig{}, errAttStore
	}
	return s.Store.GetLayerConfig(ctx, tenant, id)
}

func (s *attStore) ListManifests(ctx context.Context, tenant string) ([]store.ManifestRecord, error) {
	if s.fail["manifests"] {
		return nil, errAttStore
	}
	return s.Store.ListManifests(ctx, tenant)
}

// attFixture stores a verified version of a public layer with one link.
func attFixture(t *testing.T) (*attStore, string) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	m := storetest.Seal(t, store.ManifestRecord{TenantID: "t", ArtifactID: "a/x", Version: "1.0.0", Type: "agent", Layer: "L",
		AttestationOutcome: store.OutcomeVerified, AttestationEvaluatedAt: &at}, nil, nil)
	if err := st.PutManifest(ctx, m); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:ab0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"
	if err := st.WriteAttestations(ctx, "t", "", "", store.AttestationWrites{
		Bundles: []store.StoredAttestation{{Digest: digest, Bundle: []byte("bundle"), AttestedAt: at, StoredAt: at}},
		Versions: []store.VersionAttestations{{ArtifactID: "a/x", Version: "1.0.0", Links: []store.AttestationLink{{
			PredicateType: "p", Instance: "github", SignerIssuer: "iss", SignerIdentity: "id", Digest: digest,
			Verdict: store.VerdictVerified, VerifiedUnder: "k", LinkedAt: at}}}},
	}); err != nil {
		t.Fatal(err)
	}
	return &attStore{Store: st, fail: map[string]bool{}}, digest
}

// Spec: §7.2 (Attestation members), §4.7.11 (Served outcome), §7.3.5
// (Serving bundles), §4.7.8 — a load fails when the links or the layer of a
// verified version cannot be read; a verified version of a layer the store
// no longer holds is served as failed; the bundle endpoint fails on a read
// error and answers not found for a bundle the store no longer holds; and
// the quota read counts bundle bytes.
func TestAttestationServing_ReadFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, digest := attFixture(t)
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithAttestationPolicyCheck(func(store.LayerConfig) string { return "" })
	id := layer.Identity{IsPublic: true}
	res, err := reg.LoadArtifact(ctx, id, "a/x", core.LoadArtifactOptions{})
	if err != nil || res.AttestationOutcome == nil || res.AttestationOutcome.Outcome != store.OutcomeFailed ||
		res.AttestationOutcome.Failure.Constraint != "policy_rule" || len(res.Attestations) != 1 || res.Attestations[0].AttestedAt == "" {
		t.Fatalf("load of a version whose layer the store does not hold: %+v %v", res, err)
	}
	for _, f := range []string{"links", "layer"} {
		st.fail = map[string]bool{f: true}
		if _, err := reg.LoadArtifact(ctx, id, "a/x", core.LoadArtifactOptions{}); !errors.Is(err, core.ErrUnavailable) {
			t.Errorf("%s: load = %v", f, err)
		}
	}
	for f, want := range map[string]error{"links": core.ErrUnavailable, "bundle": core.ErrUnavailable, "bundleGone": core.ErrNotFound, "manifests": core.ErrUnavailable} {
		st.fail = map[string]bool{f: true}
		if _, err := reg.LoadAttestation(ctx, id, digest); !errors.Is(err, want) {
			t.Errorf("%s: LoadAttestation = %v, want %v", f, err, want)
		}
	}
	st.fail = map[string]bool{}
	if _, err := reg.LoadAttestation(ctx, id, "sha256:ZZ0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("a digest that is not hexadecimal: %v", err)
	}
	q, err := reg.Quota(ctx)
	if err != nil || q.StorageBytes < int64(len("bundle")) {
		t.Errorf("quota %+v %v", q, err)
	}
	st.fail = map[string]bool{"bundles": true}
	if _, err := reg.Quota(ctx); err == nil {
		t.Error("a quota read that cannot list the bundles passed")
	}
}
