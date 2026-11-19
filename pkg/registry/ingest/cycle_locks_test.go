package ingest

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/store"
)

// orderProvider snapshots files at ref. It signals taken once it has taken
// the snapshot and, when release is non-nil, waits for it before returning.
type orderProvider struct {
	files   fs.FS
	ref     string
	taken   chan struct{}
	release chan struct{}
}

func (p *orderProvider) ID() string                   { return "fake" }
func (p *orderProvider) Trigger() source.TriggerModel { return source.TriggerManual }
func (p *orderProvider) Snapshot(ctx context.Context, _ source.LayerConfig) (*source.Snapshot, error) {
	close(p.taken)
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &source.Snapshot{Reference: p.ref, Files: p.files, CreatedAt: time.Now().UTC()}, nil
}

func orderManifest(desc string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("---\ntype: agent\nversion: 1.0.0\ndescription: " + desc +
		"\nsensitivity: low\n---\n\nbody\n")}
}

// Spec: §7.3.1 (Removed artifacts) — the cycles of one layer that share a
// process's cycle locks do not overlap: a second cycle takes its snapshot
// only after the first has ended, and then reads the mark the first
// recorded, so it removes what its own snapshot no longer holds. The lock
// entry is dropped once no cycle holds it.
func TestSourceIngest_CycleLocksSerializeALayer(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	cfg := store.LayerConfig{TenantID: "t", ID: "team", SourceType: "fake"}
	if err := st.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	locks := NewCycleLocks()
	first := &orderProvider{
		files: fstest.MapFS{"a/ARTIFACT.md": orderManifest("runbook for the ingest worker")},
		ref:   "r1", taken: make(chan struct{}), release: make(chan struct{}),
	}
	second := &orderProvider{
		files: fstest.MapFS{"b/ARTIFACT.md": orderManifest("checklist for the on-call handover")},
		ref:   "r2", taken: make(chan struct{}),
	}
	errs := make(chan error, 2)
	go func() {
		_, err := SourceIngestWithOptions(ctx, st, first, cfg, SourceIngestOptions{CycleLocks: locks})
		errs <- err
	}()
	<-first.taken
	go func() {
		_, err := SourceIngestWithOptions(ctx, st, second, cfg, SourceIngestOptions{CycleLocks: locks})
		errs <- err
	}()
	select {
	case <-second.taken:
		t.Fatal("the second cycle took its snapshot while the first cycle of the layer was running")
	case <-time.After(100 * time.Millisecond):
	}
	close(first.release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("cycle: %v", err)
		}
	}
	if _, err := st.GetManifest(ctx, "t", "a", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetManifest(a) = %v, want removed by the second cycle", err)
	}
	if _, err := st.GetManifest(ctx, "t", "b", "1.0.0"); err != nil {
		t.Errorf("GetManifest(b) = %v, want served", err)
	}
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if len(locks.locks) != 0 {
		t.Errorf("cycle locks hold %d entries after every cycle ended, want 0", len(locks.locks))
	}
}

// Spec: §7.3.1 (Removed artifacts) — a cycle waiting for the layer's lock
// stops at its context's end without running, and other layers never wait.
func TestCycleLocks_WaitEndsWithTheContext(t *testing.T) {
	t.Parallel()
	locks := NewCycleLocks()
	unlock, err := locks.lock(context.Background(), "t", "team")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := locks.lock(ctx, "t", "team"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("lock while held = %v, want the context's deadline error", err)
	}
	other, err := locks.lock(context.Background(), "t", "other")
	if err != nil {
		t.Fatalf("lock of another layer = %v, want it taken at once", err)
	}
	other()
	unlock()
	locks.mu.Lock()
	n := len(locks.locks)
	locks.mu.Unlock()
	if n != 0 {
		t.Errorf("cycle locks hold %d entries, want 0", n)
	}
	var none *CycleLocks
	release, err := none.lock(context.Background(), "t", "team")
	if err != nil {
		t.Fatalf("nil CycleLocks lock = %v, want a no-op", err)
	}
	release()
}

// Spec: §7.3.1 (Removed artifacts) — a cycle whose claim fails, or whose
// claim check before its writes fails, stops with that error before its
// snapshot or its first write, rather than running unordered.
func TestSourceIngest_ClaimFailureStopsTheCycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemory()
	if err := mem.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	cfg := store.LayerConfig{TenantID: "t", ID: "team", SourceType: "fake"}
	if err := mem.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	claimProv := &orderProvider{files: fstest.MapFS{}, ref: "r1", taken: make(chan struct{})}
	_, err := SourceIngestWithOptions(ctx, failingClaim{Memory: mem}, claimProv, cfg, SourceIngestOptions{})
	select {
	case <-claimProv.taken:
		t.Error("the cycle took its snapshot after its claim failed")
	default:
	}
	if !errors.Is(err, errClaim) {
		t.Errorf("SourceIngestWithOptions with a failing claim = %v, want the claim error", err)
	}
	checkProv := &orderProvider{files: fstest.MapFS{"a/ARTIFACT.md": orderManifest("runbook for the ingest worker")}, ref: "r1", taken: make(chan struct{})}
	_, err = SourceIngestWithOptions(ctx, failingClaimCheck{Memory: mem}, checkProv, cfg, SourceIngestOptions{})
	if !errors.Is(err, errClaim) {
		t.Errorf("SourceIngestWithOptions with a failing claim check = %v, want the check error", err)
	}
	if _, err := mem.GetManifest(ctx, "t", "a", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetManifest(a) = %v, want nothing stored after the failed check", err)
	}
}

// Spec: §7.3.1 (Removed artifacts) — a cycle superseded after its check
// before the writes stops at the removal check: it removes nothing and
// records nothing. A cycle superseded after its removal records nothing, and
// the reference stays as the later claim leaves it.
func TestSourceIngest_ClaimTakenDuringTheCycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, at := range []string{"removal", "record"} {
		mem := store.NewMemory()
		if err := mem.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
			t.Fatal(err)
		}
		cfg := store.LayerConfig{TenantID: "t", ID: "team", SourceType: "fake"}
		if err := mem.PutLayerConfig(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		base := &orderProvider{files: fstest.MapFS{"a/ARTIFACT.md": orderManifest("runbook for the ingest worker")}, ref: "r0", taken: make(chan struct{})}
		if _, err := SourceIngestWithOptions(ctx, mem, base, cfg, SourceIngestOptions{}); err != nil {
			t.Fatal(err)
		}
		st := &claimTaker{Memory: mem, at: at}
		gone := &orderProvider{files: fstest.MapFS{}, ref: "r1", taken: make(chan struct{})}
		res, err := SourceIngestWithOptions(ctx, st, gone, cfg, SourceIngestOptions{})
		if err != nil {
			t.Fatalf("%s: %v", at, err)
		}
		_, getErr := mem.GetManifest(ctx, "t", "a", "1.0.0")
		lc, _ := mem.GetLayerConfig(ctx, "t", "team")
		switch at {
		case "removal":
			if !res.Superseded || len(res.Removed) != 0 || getErr != nil {
				t.Errorf("removal: result %+v, GetManifest(a) %v; want superseded with a still served", res, getErr)
			}
		case "record":
			if res.Superseded || len(res.Removed) != 1 || !errors.Is(getErr, store.ErrNotFound) {
				t.Errorf("record: result %+v, GetManifest(a) %v; want a removed", res, getErr)
			}
		}
		if lc.LastIngestedRef != "r0" {
			t.Errorf("%s: last_ingested_ref = %q, want r0 left by the superseded cycle", at, lc.LastIngestedRef)
		}
	}
}

// claimTaker is a store on which another cycle claims the layer just before
// the cycle's removal check ("removal") or its record ("record").
type claimTaker struct {
	*store.Memory
	at     string
	checks int
}

func (s *claimTaker) SoftDeleteArtifacts(ctx context.Context, tenantID, layerID string, ids []string, at time.Time, claim string) error {
	s.checks++
	if s.at == "removal" && s.checks == 2 {
		if _, err := s.ClaimLayerIngest(ctx, tenantID, layerID, "later"); err != nil {
			return err
		}
	}
	return s.Memory.SoftDeleteArtifacts(ctx, tenantID, layerID, ids, at, claim)
}

func (s *claimTaker) RecordLayerIngest(ctx context.Context, tenantID, layerID, claim, ref string, at *time.Time) error {
	if s.at == "record" {
		if _, err := s.ClaimLayerIngest(ctx, tenantID, layerID, "later"); err != nil {
			return err
		}
	}
	return s.Memory.RecordLayerIngest(ctx, tenantID, layerID, claim, ref, at)
}

var errClaim = errors.New("claim failed")

// failingClaim is a store whose ingest claim fails.
type failingClaim struct{ *store.Memory }

func (failingClaim) ClaimLayerIngest(context.Context, string, string, string) (store.LayerConfig, error) {
	return store.LayerConfig{}, errClaim
}

// failingClaimCheck is a store whose claim check fails with an error other
// than a later claim.
type failingClaimCheck struct{ *store.Memory }

func (failingClaimCheck) SoftDeleteArtifacts(context.Context, string, string, []string, time.Time, string) error {
	return errClaim
}
