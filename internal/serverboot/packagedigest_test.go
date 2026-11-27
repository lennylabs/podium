package serverboot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// digestStore wraps a store to count the fill's writes and to fail one of
// its calls on demand.
type digestStore struct {
	store.Store
	sets       int
	markerErr  error
	tenantsErr error
	rowsErr    error
	setErr     error
	setMarkErr error
}

func (d *digestStore) DataMigrationApplied(ctx context.Context, name string) (bool, error) {
	if d.markerErr != nil {
		return false, d.markerErr
	}
	return d.Store.DataMigrationApplied(ctx, name)
}

func (d *digestStore) SetDataMigrationApplied(ctx context.Context, name string, applied bool) error {
	if d.setMarkErr != nil {
		return d.setMarkErr
	}
	return d.Store.SetDataMigrationApplied(ctx, name, applied)
}

func (d *digestStore) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	if d.tenantsErr != nil {
		return nil, d.tenantsErr
	}
	return d.Store.ListTenants(ctx)
}

func (d *digestStore) ListManifestsIncludingDeleted(ctx context.Context, tenantID string) ([]store.ManifestRecord, error) {
	if d.rowsErr != nil {
		return nil, d.rowsErr
	}
	return d.Store.ListManifestsIncludingDeleted(ctx, tenantID)
}

func (d *digestStore) SetPackageDigest(ctx context.Context, tenantID, artifactID, version, contentHash, digest string) error {
	d.sets++
	if d.setErr != nil {
		return d.setErr
	}
	return d.Store.SetPackageDigest(ctx, tenantID, artifactID, version, contentHash, digest)
}

// attestCorpus is the committed attestation corpus layer, whose packages
// cover the YAML forms, the skill layouts, dot-prefixed resources, and a
// zero-length SKILL.md as a resource.
func attestCorpus(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "registries", "attest-corpus")
}

// ingestCorpus ingests the corpus into st under tenant t and returns the
// package digest ingest stored for each row, keyed by artifact ID.
func ingestCorpus(t *testing.T, st store.Store) map[string]string {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{TenantID: "t", LayerID: "corpus", Files: os.DirFS(attestCorpus(t))}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return storedDigests(t, st)
}

// storedDigests returns the stored package digest of every row of tenant t,
// soft-deleted rows included.
func storedDigests(t *testing.T, st store.Store) map[string]string {
	t.Helper()
	rows, err := st.ListManifestsIncludingDeleted(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListManifestsIncludingDeleted: %v", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ArtifactID] = r.PackageDigest
	}
	return out
}

// clearDigests empties every stored digest of tenant t and the completion
// record, as a store written before the column existed holds them.
func clearDigests(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	rows, err := st.ListManifestsIncludingDeleted(ctx, "t")
	if err != nil {
		t.Fatalf("ListManifestsIncludingDeleted: %v", err)
	}
	for _, r := range rows {
		if err := st.SetPackageDigest(ctx, r.TenantID, r.ArtifactID, r.Version, r.ContentHash, ""); err != nil {
			t.Fatalf("clear %s: %v", r.ArtifactID, err)
		}
	}
	if err := st.SetDataMigrationApplied(ctx, store.DataMigrationPackageDigest, false); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
}

// Spec: §13.4, §4.7.6 — the fill computes, from each stored row's manifest,
// SKILL.md, and resource content hashes, the package digest ingest stored for
// the same package, on the memory and SQLite stores, for every package of the
// corpus that ingest stores, a soft-deleted row included. A second run over
// a store whose record was removed writes no row.
func TestFillPackageDigests_EqualsTheIngestDigest(t *testing.T) {
	t.Parallel()
	factories := map[string]func(t *testing.T) store.Store{
		"memory": func(*testing.T) store.Store { return store.NewMemory() },
		"sqlite": func(t *testing.T) store.Store {
			st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "podium.db"))
			if err != nil {
				t.Fatalf("OpenSQLite: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			return st
		},
	}
	for name, open := range factories {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			st := &digestStore{Store: open(t)}
			want := ingestCorpus(t, st)
			if len(want) == 0 {
				t.Fatalf("ingest stored no row")
			}
			for id, d := range want {
				if d == "" {
					t.Errorf("ingest stored %s without a package digest", id)
				}
			}
			if err := st.SoftDeleteArtifacts(ctx, "t", "corpus", []string{"forms/anchor"}, time.Now(), ""); err != nil {
				t.Fatalf("SoftDeleteArtifacts: %v", err)
			}
			clearDigests(t, st)

			if err := fillPackageDigests(ctx, st); err != nil {
				t.Fatalf("fill: %v", err)
			}
			got := storedDigests(t, st)
			applied, err := st.DataMigrationApplied(ctx, store.DataMigrationPackageDigest)
			if err != nil || !applied {
				t.Errorf("completion record = %v, %v, want recorded", applied, err)
			}
			for id, d := range want {
				if got[id] != d {
					t.Errorf("%s: fill stored %q, ingest stored %q", id, got[id], d)
				}
			}

			st.sets = 0
			if err := st.SetDataMigrationApplied(ctx, store.DataMigrationPackageDigest, false); err != nil {
				t.Fatalf("clear marker: %v", err)
			}
			if err := fillPackageDigests(ctx, st); err != nil || st.sets != 0 {
				t.Errorf("second fill: err %v, %d writes, want no write", err, st.sets)
			}
		})
	}
}

// Spec: §13.4 — a start that finds the completion record runs no fill, so a
// row stored without a digest afterwards, as a process on an earlier release
// stores one, keeps none.
func TestFillPackageDigests_SkipsAfterCompletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &digestStore{Store: store.NewMemory()}
	ingestCorpus(t, st)
	clearDigests(t, st)
	if err := st.SetDataMigrationApplied(ctx, store.DataMigrationPackageDigest, true); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	st.sets = 0
	if err := fillPackageDigests(ctx, st); err != nil || st.sets != 0 {
		t.Fatalf("fill after completion: err %v, %d writes, want none", err, st.sets)
	}
	for id, d := range storedDigests(t, st) {
		if d != "" {
			t.Errorf("%s gained digest %q after completion", id, d)
		}
	}
}

// sumHex is the sha256:<hex> content hash of b.
func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// putLegacyRow stores a row with the given documents and resources, sealed
// with its content hash and carrying no package digest.
func putLegacyRow(t *testing.T, st store.Store, id string, artifactMD, skillMD []byte, resources map[string][]byte, refs []store.ResourceRef) store.ManifestRecord {
	t.Helper()
	if refs == nil {
		for p, b := range resources {
			refs = append(refs, store.ResourceRef{Path: p, ContentHash: sumHex(b), Size: int64(len(b)), Inline: b})
		}
	}
	rec := store.ManifestRecord{
		TenantID: "t", ArtifactID: id, Version: "1.0.0", Type: "skill", Layer: "L",
		Sensitivity: "high", Frontmatter: artifactMD, SkillRaw: skillMD, Resources: refs,
		ContentHash: "sha256:" + version.CanonicalContentHash(artifactMD, skillMD, resources),
	}
	if err := st.PutManifest(context.Background(), rec); err != nil {
		t.Fatalf("PutManifest %s: %v", id, err)
	}
	return rec
}

// Spec: §13.4, §4.7.6 — a stored skill row whose SKILL.md does not parse,
// which an earlier release could store, receives the package digest of an
// unresolved package: the digest the walk records for the same files, and
// the state under which admission serves the row's stored values. A row
// whose files give no entry list keeps no digest, including one that held a
// digest before, and does not hold the completion record back.
func TestFillPackageDigests_LegacyRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &digestStore{Store: store.NewMemory()}
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	artifactMD := []byte("---\ntype: skill\nversion: 1.0.0\nsensitivity: high\n---\n")
	brokenSkill := []byte("---\nname: [unclosed\n---\nbody\n")
	notes := []byte("notes\n")
	putLegacyRow(t, st, "skills/legacy", artifactMD, brokenSkill, map[string][]byte{"notes.md": notes}, nil)
	dup := []store.ResourceRef{
		{Path: "a.txt", ContentHash: sumHex(notes), Size: int64(len(notes))},
		{Path: "a.txt", ContentHash: sumHex(notes), Size: int64(len(notes))},
	}
	putLegacyRow(t, st, "rules/dup", []byte("---\ntype: rule\n---\n"), nil, map[string][]byte{"a.txt": notes}, dup)
	stale := putLegacyRow(t, st, "rules/upper", []byte("---\ntype: rule\n---\n"), nil, map[string][]byte{"a.txt": notes},
		[]store.ResourceRef{{Path: "a.txt", ContentHash: "sha256:" + "AB" + sumHex(notes)[9:], Size: int64(len(notes))}})
	if err := st.SetPackageDigest(ctx, "t", stale.ArtifactID, stale.Version, stale.ContentHash, "sha256:stale"); err != nil {
		t.Fatalf("seed stale digest: %v", err)
	}

	if err := fillPackageDigests(ctx, st); err != nil {
		t.Fatalf("fill: %v", err)
	}
	walked := filesystem.NewPackageRecord("skills/legacy", filesystem.Layer{ID: "L"}, true, true, map[string][]byte{
		manifest.ArtifactFile: artifactMD, manifest.SkillFile: brokenSkill, "notes.md": notes,
	})
	got := storedDigests(t, st)
	applied, _ := st.DataMigrationApplied(ctx, store.DataMigrationPackageDigest)
	if walked.Artifact != nil || got["skills/legacy"] != walked.PackageDigest || got["rules/dup"] != "" || got["rules/upper"] != "" || !applied {
		t.Errorf("legacy digest %q (walk %q, resolved %v), dup %q, upper %q, recorded %v; want the unresolved walk digest, two empty digests, and a record",
			got["skills/legacy"], walked.PackageDigest, walked.Artifact != nil, got["rules/dup"], got["rules/upper"], applied)
	}
}

// Spec: §13.4 — a failed write holds the completion record back and lets
// the start continue, a row another writer changed does not hold it back,
// and a failure to read the record, list the rows, or record completion
// fails the start.
func TestFillPackageDigests_Failures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	boom := errors.New("store down")
	cases := []struct {
		name     string
		arm      func(*digestStore)
		wantErr  bool
		recorded bool
	}{
		{"marker read", func(d *digestStore) { d.markerErr = boom }, true, false},
		{"tenants", func(d *digestStore) { d.tenantsErr = boom }, true, false},
		{"rows", func(d *digestStore) { d.rowsErr = boom }, true, false},
		{"write", func(d *digestStore) { d.setErr = boom }, false, false},
		{"superseded", func(d *digestStore) { d.setErr = store.ErrImmutableViolation }, false, true},
		{"removed", func(d *digestStore) { d.setErr = store.ErrNotFound }, false, true},
		{"record", func(d *digestStore) { d.setMarkErr = boom }, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &digestStore{Store: store.NewMemory()}
			ingestCorpus(t, st)
			clearDigests(t, st)
			tc.arm(st)
			err := fillPackageDigests(ctx, st)
			applied, _ := st.Store.DataMigrationApplied(ctx, store.DataMigrationPackageDigest)
			if (err != nil) != tc.wantErr || applied != tc.recorded {
				t.Errorf("err %v, recorded %v; want error %v, recorded %v", err, applied, tc.wantErr, tc.recorded)
			}
		})
	}
}

// The corpus package digests are the attest package's own, so a change to
// the fill's entry rule shows here before it reaches a store.
func TestStoredRowDigest_MatchesThePackage(t *testing.T) {
	t.Parallel()
	artifactMD := []byte("---\ntype: rule\nversion: 1.0.0\n---\n")
	body := []byte("x")
	rec := store.ManifestRecord{
		Frontmatter: artifactMD,
		Resources:   []store.ResourceRef{{Path: "SKILL.md", ContentHash: sumHex(nil)}, {Path: "a.txt", ContentHash: sumHex(body)}},
	}
	got, err := storedRowDigest(rec)
	want := filesystem.NewPackageRecord("rules/r", filesystem.Layer{ID: "L"}, true, true, map[string][]byte{
		manifest.ArtifactFile: artifactMD, manifest.SkillFile: nil, "a.txt": body,
	}).PackageDigest
	if err != nil || got != want || got == attest.PackageDigest(attest.Package{}) {
		t.Errorf("storedRowDigest = %q, %v; want the walk digest %q", got, err, want)
	}
}
