package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.4, §13.10 (Upgrade path), §4.7.11 (Attestation storage) —
// migrate-to-standard copies each migrated version with its attestation
// outcome, replaces its links on the target with the source's, and copies
// the bundles those links reference. A soft-deleted version, which the
// migration does not carry, takes neither its links nor a bundle only it
// links.
func TestPumpStore_CopiesAttestationsOfMigratedVersions(t *testing.T) {
	ctx := context.Background()
	srcDB := filepath.Join(t.TempDir(), "source.db")
	src, err := store.OpenSQLite(srcDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(src.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}))
	for _, id := range []string{"alpha", "beta", "gone"} {
		must(src.PutManifest(ctx, store.ManifestRecord{TenantID: "default", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Layer: "team", AttestationOutcome: store.OutcomeVerified, AttestationEvaluatedAt: &at}))
	}
	must(src.SoftDeleteArtifacts(ctx, "default", "team", []string{"gone"}, at, ""))
	link := func(digest string) store.AttestationLink {
		return store.AttestationLink{PredicateType: "p", Instance: "github", SignerIssuer: "iss", SignerIdentity: "id",
			Digest: digest, Verdict: store.VerdictVerified, VerifiedUnder: "k", LinkedAt: at}
	}
	bundle := func(digest string) store.StoredAttestation {
		return store.StoredAttestation{Digest: digest, Bundle: []byte(digest), LayerID: "team", AttestedAt: at, StoredAt: at}
	}
	must(src.WriteAttestations(ctx, "default", "", "", store.AttestationWrites{
		Bundles: []store.StoredAttestation{bundle("d1"), bundle("d3")},
		Versions: []store.VersionAttestations{
			{ArtifactID: "alpha", Version: "1.0.0", Links: []store.AttestationLink{link("d1")}},
			{ArtifactID: "gone", Version: "1.0.0", Links: []store.AttestationLink{link("d3")}},
		},
	}))
	plan, err := planMigration(ctx, srcDB)
	must(err)
	target := store.NewMemory()
	// A link the target holds for beta, which the source does not link.
	must(target.WriteAttestations(ctx, "default", "", "", store.AttestationWrites{
		Bundles:  []store.StoredAttestation{bundle("d2")},
		Versions: []store.VersionAttestations{{ArtifactID: "beta", Version: "1.0.0", Links: []store.AttestationLink{link("d2")}}},
	}))
	must(pumpStore(ctx, plan, target))
	links, err := target.ListAttestationLinks(ctx, "default", store.LinkFilter{})
	must(err)
	bundles, err := target.ListAttestations(ctx, "default")
	must(err)
	alpha, err := target.GetManifest(ctx, "default", "alpha", "1.0.0")
	must(err)
	var got []string
	for _, l := range links {
		got = append(got, l.ArtifactID+"="+l.Digest)
	}
	for _, b := range bundles {
		got = append(got, "bundle "+b.Digest)
	}
	got = append(got, fmt.Sprint(alpha.AttestationOutcome))
	want := "alpha=d1\nbundle d1\nbundle d2\nverified"
	if strings.Join(got, "\n") != want {
		t.Errorf("target:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// attFailStore fails the attestation reads and writes it names.
type attFailStore struct {
	store.Store
	links, bundles, write bool
}

func (s attFailStore) ListAttestationLinks(ctx context.Context, t string, f store.LinkFilter) ([]store.AttestationLink, error) {
	if s.links {
		return nil, errors.New("links down")
	}
	return s.Store.ListAttestationLinks(ctx, t, f)
}

func (s attFailStore) ListAttestations(ctx context.Context, t string) ([]store.StoredAttestation, error) {
	if s.bundles {
		return nil, errors.New("bundles down")
	}
	return s.Store.ListAttestations(ctx, t)
}

func (s attFailStore) WriteAttestations(ctx context.Context, t, l, c string, w store.AttestationWrites) error {
	if s.write {
		return errors.New("write down")
	}
	return s.Store.WriteAttestations(ctx, t, l, c, w)
}

// Spec: §13.4 — a source whose links or bundles cannot be read, and a target
// that cannot store them, stop the migration with an error that names them.
func TestPumpStore_AttestationFailures(t *testing.T) {
	ctx := context.Background()
	src := store.NewMemory()
	for _, s := range []attFailStore{{Store: src, links: true}, {Store: src, bundles: true}} {
		if _, err := planAttestations(ctx, s, "default", nil); err == nil {
			t.Errorf("planAttestations with %+v passed", s)
		}
	}
	plan := &migrationPlan{tenants: []tenantPlan{{tenant: store.Tenant{ID: "default", Name: "default"}}}}
	if err := pumpStore(ctx, plan, attFailStore{Store: store.NewMemory(), write: true}); err == nil || !strings.Contains(err.Error(), "copy attestations of default") {
		t.Errorf("pumpStore = %v", err)
	}
}
