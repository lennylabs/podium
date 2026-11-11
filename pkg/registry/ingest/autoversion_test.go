package ingest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

const avTenant = "tenant-1"

// avSkill is a SKILL.md for the package x that declares no metadata.version,
// so ingest assigns its version. body varies the content hash.
func avSkill(body string) string {
	return "---\nname: x\ndescription: x skill\n---\n\n" + body + "\n"
}

// avHash is the §4.7.6 content hash of a plain skill package holding only
// skill.
func avHash(skill string) string {
	return "sha256:" + version.CanonicalContentHash(nil, []byte(skill), map[string][]byte{})
}

// avPlainFS is a layer snapshot holding the plain skill x.
func avPlainFS(skill string) fstest.MapFS {
	return fstest.MapFS{"x/SKILL.md": &fstest.MapFile{Data: []byte(skill)}}
}

// avStore returns a memory store with the test tenant created.
func avStore(t *testing.T) *store.Memory {
	t.Helper()
	s := store.NewMemory()
	if err := s.CreateTenant(context.Background(), store.Tenant{ID: avTenant, Name: avTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return s
}

// avRow is a stored row of x for the wrapper to insert and for the
// assignVersion table.
func avRow(layerID, v, hash string, at time.Time) store.ManifestRecord {
	return store.ManifestRecord{
		TenantID: avTenant, ArtifactID: "x", Version: v, ContentHash: hash,
		Type: "skill", Layer: layerID, IngestedAt: at,
	}
}

// interveningStore wraps a store and runs a hook before each GetManifest,
// PutManifest, and ListManifestsIncludingDeleted, so a test can place a
// competing row between the ingest's listing and its write deterministically,
// as a concurrent ingest would. Ingest calls the store from one goroutine, so
// the counters need no lock.
type interveningStore struct {
	store.Store
	beforeGet  func(ctx context.Context, id, v string)
	beforePut  func(ctx context.Context, mr store.ManifestRecord) error
	beforeList func(call int) error
	gets, puts int
	lists      int
}

func (s *interveningStore) GetManifest(ctx context.Context, tenantID, id, v string) (store.ManifestRecord, error) {
	s.gets++
	if s.beforeGet != nil {
		s.beforeGet(ctx, id, v)
	}
	return s.Store.GetManifest(ctx, tenantID, id, v)
}

func (s *interveningStore) PutManifest(ctx context.Context, mr store.ManifestRecord) error {
	s.puts++
	if s.beforePut != nil {
		if err := s.beforePut(ctx, mr); err != nil {
			return err
		}
	}
	return s.Store.PutManifest(ctx, mr)
}

func (s *interveningStore) ListManifestsIncludingDeleted(ctx context.Context, tenantID string) ([]store.ManifestRecord, error) {
	s.lists++
	if s.beforeList != nil {
		if err := s.beforeList(s.lists); err != nil {
			return nil, err
		}
	}
	return s.Store.ListManifestsIncludingDeleted(ctx, tenantID)
}

// seed stores mr in the wrapped store, bypassing the hooks.
func (s *interveningStore) seed(t *testing.T, mr store.ManifestRecord) {
	t.Helper()
	if err := s.Store.PutManifest(context.Background(), mr); err != nil {
		t.Fatalf("seed %s@%s: %v", mr.ArtifactID, mr.Version, err)
	}
}

// avIngest ingests files into layerID against st and fails the test on an
// abort.
func avIngest(t *testing.T, st store.Store, layerID string, files fstest.MapFS) *Result {
	t.Helper()
	res, err := Ingest(context.Background(), st, Request{TenantID: avTenant, LayerID: layerID, Files: files})
	if err != nil {
		t.Fatalf("ingest %s: %v", layerID, err)
	}
	return res
}

// assertStoredHash fails unless x@v is stored with hash.
func assertStoredHash(t *testing.T, st store.Store, v, hash string) {
	t.Helper()
	got, err := st.GetManifest(context.Background(), avTenant, "x", v)
	if err != nil {
		t.Fatalf("GetManifest x@%s: %v", v, err)
	}
	if got.ContentHash != hash {
		t.Errorf("x@%s hash = %s, want %s", v, got.ContentHash, hash)
	}
}

// assertDuplicateRejection fails unless res holds exactly the §7.3.1
// duplicate-content rejection of x and nothing else.
func assertDuplicateRejection(t *testing.T, res *Result) {
	t.Helper()
	if res.Accepted != 0 || res.Idempotent != 0 || len(res.Conflicts) != 0 || len(res.Rejected) != 1 {
		t.Fatalf("result = %+v, want one rejection only", res)
	}
	rj := res.Rejected[0]
	want := map[string]any{"constraint": autoVersionDuplicateConstraint}
	if rj.ArtifactID != "x" || rj.Code != layer.InvalidArtifactCode || rj.Reason != autoVersionDuplicateReason ||
		!reflect.DeepEqual(rj.Details, want) {
		t.Errorf("rejection = %+v, want x %s with details %v", rj, layer.InvalidArtifactCode, want)
	}
}

// A plain skill is assigned 0.0.0-auto.0, unchanged bytes are idempotent at
// that version, changed bytes take the next N, and a revert to the first
// bytes is refused while `latest` keeps the newer row.
// Spec: §7.3.1, §4.7.6
func TestIngest_AutoVersionLifecycle(t *testing.T) {
	t.Parallel()
	st := avStore(t)
	first, second := avSkill("first"), avSkill("second")

	if res := avIngest(t, st, "L", avPlainFS(first)); res.Accepted != 1 || res.Ingested[0].Version != "0.0.0-auto.0" {
		t.Fatalf("first ingest = %+v, want x@0.0.0-auto.0 accepted", res)
	}
	assertStoredHash(t, st, "0.0.0-auto.0", avHash(first))
	if res := avIngest(t, st, "L", avPlainFS(first)); res.Idempotent != 1 || res.Ingested[0].Version != "0.0.0-auto.0" {
		t.Fatalf("unchanged ingest = %+v, want x@0.0.0-auto.0 idempotent", res)
	}
	if res := avIngest(t, st, "L", avPlainFS(second)); res.Accepted != 1 || len(res.Conflicts) != 0 ||
		res.Ingested[0].Version != "0.0.0-auto.1" {
		t.Fatalf("changed ingest = %+v, want x@0.0.0-auto.1 accepted", res)
	}
	assertDuplicateRejection(t, avIngest(t, st, "L", avPlainFS(first)))
	if _, err := st.GetManifest(context.Background(), avTenant, "x", "0.0.0-auto.2"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("revert stored x@0.0.0-auto.2 (err=%v), want no row", err)
	}
}

// (a) A competing row at the assigned version, which the pre-check finds,
// makes the ingest re-read the listing and commit the next N with no
// conflict.
// Spec: §7.3.1, §4.7.6
func TestIngest_AutoVersionPreCheckConflictRederives(t *testing.T) {
	t.Parallel()
	skill := avSkill("mine")
	st := &interveningStore{Store: avStore(t)}
	st.beforeGet = func(_ context.Context, id, v string) {
		if st.gets == 1 && id == "x" && v == "0.0.0-auto.0" {
			st.seed(t, avRow("L", v, "sha256:competing", time.Now()))
		}
	}

	res := avIngest(t, st, "L", avPlainFS(skill))
	if res.Accepted != 1 || len(res.Conflicts) != 0 || res.Ingested[0].Version != "0.0.0-auto.1" {
		t.Fatalf("result = %+v, want x@0.0.0-auto.1 accepted with no conflict", res)
	}
	assertStoredHash(t, st, "0.0.0-auto.1", avHash(skill))
	if st.lists != 2 {
		t.Errorf("listings = %d, want the initial read and one re-read", st.lists)
	}
}

// (b) A competing row that lands between the pre-check and the commit makes
// the commit fail with ErrImmutableViolation, and the ingest re-derives N and
// commits it. Signing and the resource upload run once across the attempts.
// Spec: §7.3.1, §4.7.6, §4.7.9
func TestIngest_AutoVersionCommitConflictRederives(t *testing.T) {
	t.Parallel()
	skill := avSkill("mine")
	st := &interveningStore{Store: avStore(t)}
	st.beforePut = func(_ context.Context, mr store.ManifestRecord) error {
		if st.puts == 1 {
			st.seed(t, avRow("L", mr.Version, "sha256:competing", time.Now()))
		}
		return nil
	}
	var signs, uploads int
	res, err := Ingest(context.Background(), st, Request{
		TenantID: avTenant, LayerID: "L",
		Files: fstest.MapFS{
			"x/SKILL.md":        &fstest.MapFile{Data: []byte(skill)},
			"x/scripts/run.sh":  &fstest.MapFile{Data: []byte("echo run\n")},
			"x/references/a.md": &fstest.MapFile{Data: []byte("ref\n")},
		},
		Signer: func(context.Context, string) (string, error) {
			signs++
			return "envelope", nil
		},
		ResourcePut: func(context.Context, string, []byte, string) error {
			uploads++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 || len(res.Conflicts) != 0 || res.Ingested[0].Version != "0.0.0-auto.1" {
		t.Fatalf("result = %+v, want x@0.0.0-auto.1 accepted with no conflict", res)
	}
	if st.puts != 2 || signs != 1 || uploads != 2 {
		t.Errorf("puts=%d signs=%d uploads=%d, want 2 commits, 1 signature, and 2 resource uploads", st.puts, signs, uploads)
	}
	got, err := st.GetManifest(context.Background(), avTenant, "x", "0.0.0-auto.1")
	if err != nil || got.Signature != "envelope" {
		t.Errorf("x@0.0.0-auto.1 = %+v (err=%v), want the signed row", got, err)
	}
}

// (c) A commit that keeps failing with ErrImmutableViolation ends after the
// bounded attempts with the existing conflict report and no row.
// Spec: §7.3.1
func TestIngest_AutoVersionExhaustedAttemptsReportConflict(t *testing.T) {
	t.Parallel()
	skill := avSkill("mine")
	st := &interveningStore{Store: avStore(t)}
	st.beforePut = func(context.Context, store.ManifestRecord) error { return store.ErrImmutableViolation }

	res := avIngest(t, st, "L", avPlainFS(skill))
	want := ConflictReport{ArtifactID: "x", Version: "0.0.0-auto.0", NewHash: avHash(skill)}
	if res.Accepted != 0 || len(res.Rejected) != 0 || len(res.Conflicts) != 1 || res.Conflicts[0] != want {
		t.Fatalf("result = %+v, want the single conflict %+v", res, want)
	}
	if st.puts != autoVersionAttempts || st.lists != autoVersionAttempts {
		t.Errorf("puts=%d lists=%d, want %d commits and %d listings",
			st.puts, st.lists, autoVersionAttempts, autoVersionAttempts)
	}
	if _, err := st.GetManifest(context.Background(), avTenant, "x", "0.0.0-auto.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("x@0.0.0-auto.0 stored (err=%v), want no row", err)
	}
}

// (d) A failed listing aborts the call, both the initial read and the
// re-read after a conflict, because neither the §4.6 collision check nor the
// version assignment can be evaluated without it.
// Spec: §7.3.1, §4.6
func TestIngest_AutoVersionListingErrorAborts(t *testing.T) {
	t.Parallel()
	boom := errors.New("store down")
	cases := []struct {
		name     string
		failCall int
		compete  bool
	}{
		{name: "initial listing", failCall: 1},
		{name: "re-read after a conflict", failCall: 2, compete: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &interveningStore{Store: avStore(t)}
			st.beforeList = func(call int) error {
				if call == tc.failCall {
					return boom
				}
				return nil
			}
			if tc.compete {
				st.beforeGet = func(_ context.Context, id, v string) {
					if st.gets == 1 {
						st.seed(t, avRow("L", v, "sha256:competing", time.Now()))
					}
				}
			}
			_, err := Ingest(context.Background(), st, Request{TenantID: avTenant, LayerID: "L", Files: avPlainFS(avSkill("mine"))})
			if err == nil || !strings.HasPrefix(err.Error(), "ingest: list manifests:") || !errors.Is(err, boom) {
				t.Fatalf("err = %v, want an ingest: list manifests: error wrapping the store failure", err)
			}
		})
	}
}

// (f) A duplicate that only the re-read listing reveals is rejected rather
// than committed: the competing ingest stored another layer's row carrying
// the incoming bytes.
// Spec: §7.3.1, §4.7.6
func TestIngest_AutoVersionDuplicateRevealedByReread(t *testing.T) {
	t.Parallel()
	skill := avSkill("mine")
	st := &interveningStore{Store: avStore(t)}
	st.beforeGet = func(_ context.Context, id, v string) {
		if st.gets == 1 {
			now := time.Now()
			st.seed(t, avRow("M", "0.0.0-auto.0", "sha256:competing", now))
			st.seed(t, avRow("M", "0.0.0-auto.1", avHash(skill), now.Add(time.Second)))
		}
	}

	assertDuplicateRejection(t, avIngest(t, st, "L", avPlainFS(skill)))
	rows, err := st.Store.ListManifestsIncludingDeleted(context.Background(), avTenant)
	if err != nil {
		t.Fatalf("ListManifestsIncludingDeleted: %v", err)
	}
	for _, r := range rows {
		if r.Layer == "L" {
			t.Errorf("row %s@%s stored for L, want none", r.ArtifactID, r.Version)
		}
	}
}

// (e) assignVersion over hand-built rows of one artifact ID.
// Spec: §7.3.1
func TestAssignVersion(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	deleted := t0.Add(time.Hour)
	tomb := func(r store.ManifestRecord) store.ManifestRecord {
		r.DeletedAt = &deleted
		return r
	}
	const h1, h2, h3 = "sha256:h1", "sha256:h2", "sha256:h3"
	twoLayers := []store.ManifestRecord{
		avRow("base", "0.0.0-auto.0", h1, t0),
		avRow("team", "0.0.0-auto.1", h2, t0.Add(time.Minute)),
	}
	cases := []struct {
		name      string
		rows      []store.ManifestRecord
		layerID   string
		hash      string
		want      string
		duplicate bool
	}{
		{name: "no rows", layerID: "L", hash: h1, want: "0.0.0-auto.0"},
		{
			name:    "layer's latest row carries the hash",
			rows:    []store.ManifestRecord{avRow("L", "0.0.0-auto.0", h2, t0), avRow("L", "0.0.0-auto.1", h1, t0.Add(time.Minute))},
			layerID: "L", hash: h1, want: "0.0.0-auto.1",
		},
		{
			name:    "layer's latest row carries another hash",
			rows:    []store.ManifestRecord{avRow("L", "0.0.0-auto.0", h1, t0), avRow("L", "0.0.0-auto.3", h2, t0.Add(time.Minute))},
			layerID: "L", hash: h3, want: "0.0.0-auto.4",
		},
		{
			name:    "older row of the layer carries the hash",
			rows:    []store.ManifestRecord{avRow("L", "0.0.0-auto.0", h1, t0), avRow("L", "1.0.0", h2, t0.Add(time.Minute))},
			layerID: "L", hash: h1, duplicate: true,
		},
		{
			name:    "ingest-time tie resolves to the higher version",
			rows:    []store.ManifestRecord{avRow("L", "0.0.0-auto.1", h1, t0), avRow("L", "0.0.0-auto.0", h2, t0)},
			layerID: "L", hash: h1, want: "0.0.0-auto.1",
		},
		{
			name:    "soft-deleted row of the layer carries the hash",
			rows:    []store.ManifestRecord{tomb(avRow("L", "0.0.0-auto.0", h1, t0))},
			layerID: "L", hash: h1, duplicate: true,
		},
		{
			name:    "soft-deleted row of another layer holds the maximum N",
			rows:    []store.ManifestRecord{avRow("L", "0.0.0-auto.0", h1, t0), tomb(avRow("M", "0.0.0-auto.5", h2, t0))},
			layerID: "L", hash: h3, want: "0.0.0-auto.6",
		},
		{
			name:    "non-deleted row of another layer carries the hash",
			rows:    []store.ManifestRecord{avRow("M", "0.0.0-auto.0", h1, t0)},
			layerID: "L", hash: h1, duplicate: true,
		},
		{name: "two layers, base reuses", rows: twoLayers, layerID: "base", hash: h1, want: "0.0.0-auto.0"},
		{name: "two layers, team reuses", rows: twoLayers, layerID: "team", hash: h2, want: "0.0.0-auto.1"},
		{name: "two layers, base changes", rows: twoLayers, layerID: "base", hash: h3, want: "0.0.0-auto.2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, dup := assignVersion(tc.rows, tc.layerID, tc.hash)
			if got != tc.want || dup != tc.duplicate {
				t.Errorf("assignVersion = (%q, %v), want (%q, %v)", got, dup, tc.want, tc.duplicate)
			}
		})
	}
}
