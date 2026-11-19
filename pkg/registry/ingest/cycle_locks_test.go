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

// Spec: §7.3.1 (Removed artifacts) — a cycle whose layer cannot be read
// before the snapshot fails rather than running without the ingest mark.
func TestSourceIngest_LayerReadFailureStopsTheCycle(t *testing.T) {
	t.Parallel()
	st := failingLayerRead{Memory: store.NewMemory()}
	prov := &orderProvider{files: fstest.MapFS{}, ref: "r1", taken: make(chan struct{})}
	_, err := SourceIngestWithOptions(context.Background(), st, prov, store.LayerConfig{TenantID: "t", ID: "team"}, SourceIngestOptions{})
	if err == nil || !errors.Is(err, errLayerRead) {
		t.Errorf("SourceIngestWithOptions = %v, want the layer read error", err)
	}
}

var errLayerRead = errors.New("layer read failed")

// failingLayerRead is a store whose layer read fails.
type failingLayerRead struct{ *store.Memory }

func (failingLayerRead) GetLayerConfig(context.Context, string, string) (store.LayerConfig, error) {
	return store.LayerConfig{}, errLayerRead
}
