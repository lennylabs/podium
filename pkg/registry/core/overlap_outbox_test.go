package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// readCountingIndex is failingIndex with a counted stored-vector read.
type readCountingIndex struct {
	*failingIndex
	reads int
}

func (r *readCountingIndex) StoredVector(ctx context.Context, tenant, id, version string) ([]float32, string, error) {
	r.reads++
	return r.Memory.StoredVector(ctx, tenant, id, version)
}

// Spec: §7.3.1 — the outbox-write computation queries with the vector the
// outbox row carries when the configured model produced it, and reads no
// stored vector. A write with no vector or with a vector of another model
// reads the stored vector, and a read the backend answers with no row is
// returned as unreadable, neither computed nor failed, with no failure log.
// Any other failure is still counted and logged.
func TestComputeOverlapAtWrite(t *testing.T) {
	logs := captureLog(t)
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	idx := &readCountingIndex{failingIndex: f.idx}
	f.reg.WithVectorSearch(idx, overlapEmbedder{table: overlapVectors, model: "m1"})
	f.put("a", "1.0.0", "L", "alpha", "", false)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	key := vector.RowKey{ArtifactID: "a", Version: "1.0.0"}

	res, err := f.reg.ComputeOverlapAtWrite(f.ctx, []OverlapWrite{{Key: key, Vector: overlapVectors["alpha"], Model: "m1"}})
	if err != nil || res.Computed != 1 || res.Failed != 0 || len(res.Unreadable) != 0 || idx.reads != 0 {
		t.Fatalf("with the written vector: %+v, %v, %d reads; want a computed with no read", res, err, idx.reads)
	}
	if got := ownedSummary(f.records()); got != "a>b" {
		t.Errorf("records = %q, want a>b", got)
	}

	for name, w := range map[string]OverlapWrite{
		"no vector":     {Key: key},
		"another model": {Key: key, Vector: overlapVectors["alpha"], Model: "m0"},
	} {
		reads := idx.reads
		res, err := f.reg.ComputeOverlapAtWrite(f.ctx, []OverlapWrite{w})
		if err != nil || res.Computed != 0 || res.Failed != 0 || len(res.Unreadable) != 1 || res.Unreadable[0] != key || idx.reads != reads+1 {
			t.Errorf("%s: %+v, %v, %d reads; want a read that finds no row and a unreadable", name, res, err, idx.reads-reads)
		}
	}
	if strings.Contains(logs.String(), "stored-vector read") {
		t.Errorf("an unreadable stored vector was logged as a failure:\n%s", logs.String())
	}

	f.idx.fail = true
	res, err = f.reg.ComputeOverlapAtWrite(f.ctx, []OverlapWrite{{Key: key, Vector: overlapVectors["alpha"], Model: "m1"}})
	if err != nil || res.Failed != 1 || len(res.Unreadable) != 0 || !strings.Contains(logs.String(), "neighbour query for a@1.0.0 failed") {
		t.Errorf("with a failing query: %+v, %v; want one logged failure", res, err)
	}
	f.idx.fail = false
	off := New(f.st, "t", nil)
	if _, err := off.ComputeOverlapAtWrite(f.ctx, []OverlapWrite{{Key: key}}); !errors.Is(err, ErrOverlapUnavailable) {
		t.Errorf("with the check off: %v, want ErrOverlapUnavailable", err)
	}
}

// failingOutboxStore is the memory store whose outbox key listing fails.
type failingOutboxStore struct{ *store.Memory }

func (failingOutboxStore) ListVectorPendingKeys(context.Context, string) ([]store.VectorPendingKey, error) {
	return nil, errors.New("outbox unreachable")
}

// outboxOnlyStore hides every capability of the memory store but the Store
// interface, so it has no vector outbox.
type outboxOnlyStore struct{ store.Store }

// Spec: §7.3.1 — a sweep pass skips a stale artifact whose current version
// has a row in the outbox and counts it in Pending, as neither computed nor
// failed. A row of an earlier version does not skip the served one, a store
// with no outbox has nothing pending, a failed outbox listing fails the
// pass, and the rebuild computes every served artifact it can and counts an
// unwritten one as failed.
func TestRefreshOverlap_SkipsUnwrittenVectors(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	pend := func(id, ver, desc string) {
		m := store.ManifestRecord{TenantID: "t", ArtifactID: id, Version: ver, ContentHash: "sha256:" + id + ver,
			Type: "agent", Description: desc, Layer: "L", IngestedAt: overlapNow,
			Frontmatter: []byte("---\ntype: agent\nversion: " + ver + "\ndescription: " + desc + "\n---\n")}
		p := store.VectorPending{TenantID: "t", ArtifactID: id, Version: ver, Text: desc, EnqueuedAt: overlapNow, NextRetryAt: overlapNow.Add(time.Hour)}
		if err := f.st.PutManifestWithVectorPending(f.ctx, m, p); err != nil {
			t.Fatalf("PutManifestWithVectorPending %s: %v", id, err)
		}
	}
	pend("c", "1.0.0", "charlie")
	pend("d", "0.9.0", "delta")
	f.put("d", "1.0.0", "L", "delta", "", true)

	res, err := f.reg.RefreshOverlap(f.ctx)
	if err != nil || res != (OverlapSweepResult{OverlapComputeResult: OverlapComputeResult{Computed: 3}, Pending: 1}) {
		t.Fatalf("RefreshOverlap = %+v, %v; want a, b, and d computed and c pending", res, err)
	}
	if again, err := f.reg.RefreshOverlap(f.ctx); err != nil || again != (OverlapSweepResult{Pending: 1}) {
		t.Errorf("second RefreshOverlap = %+v, %v; want only c pending", again, err)
	}

	rebuilt, err := f.reg.RebuildOverlap(f.ctx)
	if err != nil || rebuilt != (OverlapComputeResult{Computed: 3, Failed: 1}) {
		t.Errorf("RebuildOverlap = %+v, %v; want a, b, and d computed and c failed", rebuilt, err)
	}

	bare := New(outboxOnlyStore{f.st}, "t", nil).WithVectorSearch(f.idx, overlapEmbedder{table: overlapVectors, model: "m1"}).WithOverlapChecks(f.reg.overlap.thresholds, f.reg.overlap.semantic)
	if res, err := bare.RefreshOverlap(f.ctx); err != nil || res.Pending != 0 || res.Failed != 1 {
		t.Errorf("RefreshOverlap with no outbox = %+v, %v; want c attempted and failed", res, err)
	}

	broken := New(failingOutboxStore{f.st}, "t", nil).WithVectorSearch(f.idx, overlapEmbedder{table: overlapVectors, model: "m1"}).WithOverlapChecks(f.reg.overlap.thresholds, f.reg.overlap.semantic)
	if _, err := broken.RefreshOverlap(f.ctx); err == nil || !strings.Contains(err.Error(), "list the vector outbox: outbox unreachable") {
		t.Errorf("RefreshOverlap with a failing outbox listing = %v, want the listing error", err)
	}

	if err := f.st.MarkVectorPendingDone(f.ctx, "t", "c", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.embedAndUpsert(f.ctx, store.ManifestRecord{TenantID: "t", ArtifactID: "c", Version: "1.0.0", Type: "agent", Description: "charlie"}); err != nil {
		t.Fatal(err)
	}
	if res, err := f.reg.RefreshOverlap(f.ctx); err != nil || res != (OverlapSweepResult{OverlapComputeResult: OverlapComputeResult{Computed: 1}}) {
		t.Errorf("RefreshOverlap after the write = %+v, %v; want c computed", res, err)
	}
}
