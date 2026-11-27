package ingest_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// digestFailStore fails every package digest write.
type digestFailStore struct {
	store.Store
	sets int
}

func (d *digestFailStore) SetPackageDigest(context.Context, string, string, string, string, string) error {
	d.sets++
	return errors.New("store down")
}

// digestLayer is a layer with an agent that declares its version and a
// skill whose version the registry assigns.
func digestLayer() fstest.MapFS {
	return fstest.MapFS{
		"ops/runbook/ARTIFACT.md":  {Data: []byte("---\ntype: agent\nversion: 1.0.0\ndescription: A runbook.\n---\n\nBody.\n")},
		"ops/runbook/notes.txt":    {Data: []byte("notes\n")},
		"ops/triage/SKILL.md":      {Data: []byte("---\nname: triage\ndescription: Triage an incident.\n---\n\nSteps.\n")},
		"ops/triage/scripts/a.sh":  {Data: []byte("echo a\n")},
		"ops/triage/.config/x.yml": {Data: []byte("k: v\n")},
	}
}

// rowDigests returns the stored package digest of every live row of tenant
// t by artifact ID.
func rowDigests(t *testing.T, st store.Store) map[string]store.ManifestRecord {
	t.Helper()
	rows, err := st.ListManifests(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	out := map[string]store.ManifestRecord{}
	for _, r := range rows {
		out[r.ArtifactID] = r
	}
	return out
}

// Spec: §13.4 — an ingest that finds a version unchanged stores the package
// digest on a row that holds none, which is a row a process on an earlier
// release stored after the fill, and a failed write leaves the cycle's
// outcome unchanged.
func TestIngest_UnchangedVersionGainsItsPackageDigest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	req := ingest.Request{TenantID: "t", LayerID: "L", Files: digestLayer()}
	if _, err := ingest.Ingest(ctx, st, req); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	first := rowDigests(t, st)
	for _, r := range first {
		if err := st.SetPackageDigest(ctx, "t", r.ArtifactID, r.Version, r.ContentHash, ""); err != nil {
			t.Fatalf("clear %s: %v", r.ArtifactID, err)
		}
	}

	failing := &digestFailStore{Store: st}
	failed, err := ingest.Ingest(ctx, failing, req)
	if err != nil || failed.Idempotent != len(first) || failing.sets != len(first) {
		t.Fatalf("Ingest with failing digest writes = %+v, %v (%d writes), want every version unchanged", failed, err, failing.sets)
	}

	res, err := ingest.Ingest(ctx, st, req)
	if err != nil {
		t.Fatalf("reingest: %v", err)
	}
	again := rowDigests(t, st)
	for id, r := range first {
		if r.PackageDigest == "" || again[id].PackageDigest != r.PackageDigest || again[id].ContentHash != r.ContentHash {
			t.Errorf("%s: digest after reingest %q, at ingest %q", id, again[id].PackageDigest, r.PackageDigest)
		}
	}
	if res.Idempotent != len(first) || len(first) != 2 {
		t.Errorf("reingest = %+v over %d rows, want two unchanged versions", res, len(first))
	}
}
