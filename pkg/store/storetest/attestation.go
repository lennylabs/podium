package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// recordView renders the attestation record of a layer read in one
// comparable string.
func recordView(cfg store.LayerConfig) string {
	r := cfg.AttestationRecord
	mark, checked := "-", "-"
	if r.Mark != nil {
		mark = r.Mark.UTC().Format(time.RFC3339Nano)
	}
	if r.CheckedAt != nil {
		checked = r.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s|%s|%s|%v|%d|%s", mark, r.Commit, r.Status, r.Details, r.Dropped, checked)
}

// layerAttestationRecord covers the §4.7.11 attestation record: the
// conditions of a cycle's record write, the record kept by every layer write
// whatever the caller passes, over a live row and a soft-deleted one, and the
// mark reset written with the claim.
//
// Spec: §4.7.11 (Attestation record, Mark reset), §7.3.1
func layerAttestationRecord(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	t1 := time.Date(2026, 11, 20, 9, 14, 2, 123456789, time.UTC)
	t2, at := t1.Add(time.Hour), t1.Add(2*time.Hour)
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x"}))
	_, _, err := s.ClaimLayerIngest(ctx, "t", "L", "c1")
	must(t, err)
	var got []string
	write := func(claim string, w store.AttestationWrite) {
		err := s.WriteLayerAttestation(ctx, "t", "L", claim, w)
		cfg, rerr := s.GetLayerConfig(ctx, "t", "L")
		must(t, rerr)
		got = append(got, fmt.Sprintf("%v %s", errors.Is(err, store.ErrIngestSuperseded), recordView(cfg)))
	}
	pass := store.AttestationWrite{Status: "ok", CheckedAt: at, Advance: true, Mark: t1, Commit: "aaa"}
	write("c0", pass) // another claim
	write("c1", pass)
	write("c1", pass) // the stored mark moved since the read
	write("c1", store.AttestationWrite{Status: "ingest.attestation_stale", Details: map[string]any{"constraint": "mark", "n": 2}, Dropped: 3, CheckedAt: at})
	mark := t1.Truncate(time.Microsecond)
	write("c1", store.AttestationWrite{Status: "ok", CheckedAt: at, Advance: true, Mark: t2, ReadMark: &mark, ReadCommit: "aaa"})
	// A layer write keeps the record whatever it carries, on a live row and,
	// through an unregister and a restore, on a soft-deleted one.
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/y",
		AttestationRecord: store.AttestationRecord{Status: "forged", Commit: "zzz"}}))
	must(t, s.DeleteLayerConfig(ctx, "t", "L"))
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/z", DeletedAt: &at}))
	must(t, s.RestoreLayerConfig(ctx, "t", "L"))
	kept, err := s.GetLayerConfig(ctx, "t", "L")
	must(t, err)
	got = append(got, recordView(kept))
	// The reset claims the layer, supersedes the earlier claim, and returns
	// the record it replaced.
	claimed, _, prior, err := s.ClaimLayerIngestWithReset(ctx, "t", "L", "c2", "bbb")
	must(t, err)
	got = append(got, recordView(store.LayerConfig{AttestationRecord: prior}), recordView(claimed))
	write("c1", store.AttestationWrite{Status: "ok", CheckedAt: at})
	if _, _, _, err := s.ClaimLayerIngestWithReset(ctx, "t", "absent", "c", ""); !errors.Is(err, store.ErrNotFound) {
		got = append(got, "reset of an absent layer: "+fmt.Sprint(err))
	}
	m2 := "2026-11-20T10:14:02.123456Z"
	m1 := "2026-11-20T09:14:02.123456Z"
	checkedAt := at.Truncate(time.Microsecond).Format(time.RFC3339Nano)
	want := []string{
		"true -|||map[]|0|-",
		"false " + m1 + "|aaa|ok|map[]|0|" + checkedAt,
		"true " + m1 + "|aaa|ok|map[]|0|" + checkedAt,
		"false " + m1 + "|aaa|ingest.attestation_stale|map[constraint:mark n:2]|3|" + checkedAt,
		"false " + m2 + "|aaa|ok|map[]|0|" + checkedAt,
		m2 + "|aaa|ok|map[]|0|" + checkedAt,
		m2 + "|aaa|ok|map[]|0|" + checkedAt,
		"-|bbb|ok|map[]|0|" + checkedAt,
		"true -|bbb|ok|map[]|0|" + checkedAt,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// layerAttestationMigration covers the §13.4 copy of the mark and the commit:
// a target with no row, a target record a cycle wrote, and a record no cycle
// wrote with an earlier mark, a later mark, and a later mark with no commit.
//
// Spec: §13.4, §4.7.11 (Migration copy)
func layerAttestationMigration(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	t1 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	t2, t3 := t1.Add(time.Hour), t1.Add(2*time.Hour)
	layer := func(id string, mark *time.Time, commit string) store.LayerConfig {
		return store.LayerConfig{TenantID: "t", ID: id, SourceType: "local", LocalPath: "/x",
			AttestationRecord: store.AttestationRecord{Mark: mark, Commit: commit}}
	}
	var got []string
	migrate := func(cfg store.LayerConfig) {
		out, err := s.MigrateLayerConfig(ctx, cfg)
		must(t, err)
		read, err := s.GetLayerConfig(ctx, "t", cfg.ID)
		must(t, err)
		got = append(got, string(out)+" "+recordView(read))
	}
	migrate(layer("A", &t1, "aaa"))
	migrate(layer("A", &t2, "bbb"))
	migrate(layer("A", &t1, "ccc"))
	migrate(layer("A", &t3, ""))
	migrate(layer("B", &t1, "aaa"))
	_, _, err := s.ClaimLayerIngest(ctx, "t", "B", "c")
	must(t, err)
	must(t, s.WriteLayerAttestation(ctx, "t", "B", "c", store.AttestationWrite{Status: "ok", CheckedAt: t1}))
	migrate(layer("B", &t2, "bbb"))
	want := strings.Join([]string{
		"written 2026-11-20T09:00:00Z|aaa||map[]|0|-",
		"replaced 2026-11-20T10:00:00Z|bbb||map[]|0|-",
		"left 2026-11-20T10:00:00Z|bbb||map[]|0|-",
		"left 2026-11-20T10:00:00Z|bbb||map[]|0|-",
		"written 2026-11-20T09:00:00Z|aaa||map[]|0|-",
		"left 2026-11-20T09:00:00Z|aaa|ok|map[]|0|2026-11-20T09:00:00Z",
	}, "\n")
	if strings.Join(got, "\n") != want {
		t.Errorf("copies:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// linksView renders links in one comparable string.
func linksView(links []store.AttestationLink) string {
	var parts []string
	for _, l := range links {
		parts = append(parts, fmt.Sprintf("%s@%s %s/%s/%s/%s=%s:%s:%s", l.ArtifactID, l.Version,
			l.PredicateType, l.Instance, l.SignerIssuer, l.SignerIdentity, l.Digest, l.Verdict, l.VerifiedUnder))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// outcomeView renders the attestation outcome of a stored version.
func outcomeView(s store.Store, tenant, id, version string) string {
	rec, err := s.GetManifest(context.Background(), tenant, id, version)
	if err != nil {
		return err.Error()
	}
	f, at := "-", "-"
	if p := rec.AttestationFailure; p != nil {
		f = fmt.Sprintf("%s/%s/%s", p.Code, p.Scope, p.Constraint)
		if p.Requirement != nil {
			f += fmt.Sprintf("/%d", *p.Requirement)
		}
	}
	if rec.AttestationEvaluatedAt != nil {
		at = rec.AttestationEvaluatedAt.UTC().Format(time.RFC3339Nano)
	}
	return rec.AttestationOutcome + " " + f + " " + at
}

// attestationStorage covers the §4.7.11 storage: the outcome members of a
// version and their default, bundles stored once per tenant and digest, links
// replaced by key and in full, the conditional link and outcome updates, the
// claim, the link filters, links of two instances under one issuer and
// identity, the root record replacement rule, tenant isolation, and the
// sweep of a purge.
//
// Spec: §4.7.11 (Attestation outcome, Attestation storage, Root record), §8.4
func attestationStorage(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	mustCreateTenant(t, s, "u")
	at := time.Date(2026, 11, 20, 9, 14, 2, 123456789, time.UTC)
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x"}))
	_, _, err := s.ClaimLayerIngest(ctx, "t", "L", "c1")
	must(t, err)
	two := 2
	manifest := func(tenant, id string, o store.VersionOutcome) store.ManifestRecord {
		return store.ManifestRecord{TenantID: tenant, ArtifactID: id, Version: "1.0.0", ContentHash: "sha256:" + id,
			Type: "skill", Layer: "L", IngestedAt: at, AttestationOutcome: o.Outcome, AttestationFailure: o.Failure,
			AttestationEvaluatedAt: o.EvaluatedAt}
	}
	must(t, s.PutManifest(ctx, manifest("t", "a", store.VersionOutcome{})))
	must(t, s.PutManifest(ctx, manifest("t", "b", store.VersionOutcome{Outcome: store.OutcomeFailed, EvaluatedAt: &at,
		Failure: &store.AttestationFailure{Code: "ingest.attestation_mismatch", Scope: "artifact", Constraint: "digest", Requirement: &two}})))
	must(t, s.PutManifest(ctx, manifest("u", "a", store.VersionOutcome{Outcome: store.OutcomeVerified})))
	idx := int64(7)
	bundle := func(digest, layer string, log *int64) store.StoredAttestation {
		return store.StoredAttestation{Digest: digest, Bundle: []byte("bytes-" + digest), LayerID: layer, SourceRef: "ref",
			PredicateType: "p", Instance: "public-good", SourceRepository: "r", SourceRepositoryOwner: "o",
			SourceRepositoryRef: "refs/heads/main", SignerIssuer: "iss", SignerIdentity: "id", LogIndex: log,
			AttestedAt: at, StoredAt: at}
	}
	link := func(instance, digest, verdict, key string) store.AttestationLink {
		return store.AttestationLink{PredicateType: "p", Instance: instance, SignerIssuer: "iss", SignerIdentity: "id",
			Digest: digest, Verdict: verdict, VerifiedUnder: key, LinkedAt: at}
	}
	verified := store.VersionOutcome{Outcome: store.OutcomeVerified, EvaluatedAt: &at}
	var got []string
	note := func(label string) func(store.AttestationWriteResult, error) {
		return func(r store.AttestationWriteResult, err error) {
			got = append(got, fmt.Sprintf("%s: %v %s", label, err, linksView(r.Updated)))
		}
	}
	note("other claim")(s.WriteAttestations(ctx, "t", "L", "c0", store.AttestationWrites{Bundles: []store.StoredAttestation{bundle("d1", "L", &idx)}}))
	note("other policy")(s.WriteAttestations(ctx, "t", "L", "c1", store.AttestationWrites{Policy: &store.TrustPolicy{Mode: "require"},
		Bundles: []store.StoredAttestation{bundle("d1", "L", &idx)}}))
	note("write")(s.WriteAttestations(ctx, "t", "L", "c1", store.AttestationWrites{Policy: &store.TrustPolicy{},
		Bundles: []store.StoredAttestation{bundle("d1", "L", &idx), bundle("d2", "L", nil)},
		Versions: []store.VersionAttestations{{ArtifactID: "a", Version: "1.0.0", Outcome: &verified,
			Links: []store.AttestationLink{link("public-good", "d1", "verified", "k1"), link("github", "d2", "unverified", "dropped")}}},
	}))
	note("again")(s.WriteAttestations(ctx, "u", "", "", store.AttestationWrites{Bundles: []store.StoredAttestation{bundle("d1", "U", nil)}}))
	note("upsert")(s.WriteAttestations(ctx, "t", "", "", store.AttestationWrites{
		Bundles: []store.StoredAttestation{bundle("d1", "M", nil)},
		Versions: []store.VersionAttestations{
			{ArtifactID: "a", Version: "1.0.0", Links: []store.AttestationLink{link("public-good", "d2", "verified", "k2")},
				Updates: []store.LinkUpdate{{Link: link("github", "d1", "verified", "x"), ReadVerdict: "unverified"},
					{Link: link("github", "d2", "verified", "y"), ReadVerdict: "verified"},
					{Link: link("github", "d2", "verified", "k3"), ReadVerdict: "unverified"}},
				Outcome: &store.VersionOutcome{Outcome: store.OutcomeNotEvaluated}, OutcomeIf: store.OutcomeFailed},
			{ArtifactID: "b", Version: "1.0.0", ReplaceLinks: true, Links: []store.AttestationLink{link("public-good", "d1", "verified", "k1")},
				Outcome: &store.VersionOutcome{Outcome: store.OutcomeVerified, EvaluatedAt: &at}, OutcomeIf: store.OutcomeFailed},
			{ArtifactID: "absent", Version: "1.0.0", Outcome: &verified},
		},
	}))
	got = append(got, outcomeView(s, "t", "a", "1.0.0"), outcomeView(s, "t", "b", "1.0.0"), outcomeView(s, "u", "a", "1.0.0"))
	for _, f := range []store.LinkFilter{{}, {LayerID: "L", Digest: "d1"}, {ArtifactID: "a", Version: "1.0.0"}, {LayerID: "M"}} {
		links, err := s.ListAttestationLinks(ctx, "t", f)
		must(t, err)
		got = append(got, linksView(links))
	}
	a, err := s.GetAttestation(ctx, "t", "d1")
	must(t, err)
	got = append(got, fmt.Sprintf("%s %s %s %s %s %d %s", a.Digest, a.Bundle, a.LayerID, a.SourceRepositoryRef, a.SignerIdentity, *a.LogIndex,
		a.AttestedAt.Format(time.RFC3339Nano)))
	_, err = s.GetAttestation(ctx, "u", "d2")
	got = append(got, fmt.Sprintf("other tenant: %v", err))
	// Root records.
	r1 := store.RootRecord{Instance: "public-good", RootDigest: "sha256:1", RootTime: at}
	r2 := store.RootRecord{Instance: "public-good", RootDigest: "sha256:2", RootTime: at.Add(time.Hour)}
	for _, step := range []struct {
		prev *store.RootRecord
		next store.RootRecord
	}{{nil, r1}, {nil, r2}, {&r2, r1}, {&r1, r2}} {
		ok, err := s.ReplaceRootRecord(ctx, step.prev, step.next)
		must(t, err)
		got = append(got, fmt.Sprint(ok))
	}
	roots, err := s.ListRootRecords(ctx)
	must(t, err)
	for _, r := range roots {
		got = append(got, r.Instance+" "+r.RootDigest+" "+r.RootTime.Format(time.RFC3339Nano))
	}
	// A purge deletes the links of the versions it removes and then each
	// bundle no version links.
	must(t, s.SoftDeleteArtifacts(ctx, "t", "L", []string{"b"}, at, ""))
	_, err = s.PurgeExpiredLayerDeletions(ctx, at.Add(time.Hour))
	must(t, err)
	left, err := s.ListAttestations(ctx, "t")
	must(t, err)
	links, err := s.ListAttestationLinks(ctx, "t", store.LinkFilter{})
	must(t, err)
	got = append(got, fmt.Sprint(len(left), " ", left[0].Digest), linksView(links))
	ts := "2026-11-20T09:14:02.123456Z"
	want := []string{
		"other claim: " + store.ErrIngestSuperseded.Error() + " []",
		"other policy: " + store.ErrIngestSuperseded.Error() + " []",
		"write: <nil> []",
		"again: <nil> []",
		"upsert: <nil> [a@1.0.0 p/github/iss/id=d2:verified:k3]",
		"verified - " + ts,
		"verified - " + ts,
		"verified - -",
		"[a@1.0.0 p/github/iss/id=d2:verified:k3 a@1.0.0 p/public-good/iss/id=d2:verified:k2 b@1.0.0 p/public-good/iss/id=d1:verified:k1]",
		"[b@1.0.0 p/public-good/iss/id=d1:verified:k1]",
		"[a@1.0.0 p/github/iss/id=d2:verified:k3 a@1.0.0 p/public-good/iss/id=d2:verified:k2]",
		"[]",
		"d1 bytes-d1 L refs/heads/main id 7 " + ts,
		"other tenant: " + store.ErrNotFound.Error(),
		"true", "false", "false", "true",
		"public-good sha256:2 2026-11-20T10:14:02.123456Z",
		"1 d2",
		"[a@1.0.0 p/github/iss/id=d2:verified:k3 a@1.0.0 p/public-good/iss/id=d2:verified:k2]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("attestation storage:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
