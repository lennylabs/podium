package integration

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
)

// Spec: §6.6 step 5 — "The MCP server writes the adapted output atomically to a
// host-configured destination path (.tmp + rename), ensuring the destination
// either contains a complete copy or nothing." §6.7 names the sandbox contract
// the writer enforces. §7.5 / §13.11 run materialization through pkg/sync,
// which writes through pkg/materialize.
//
// podium sync is single-writer-per-target: each target directory has one
// materializer. pkg/materialize.Write stages each file to a fixed "<path>.tmp"
// sibling and renames it into place. Two writers that share one target also
// share that staging path, so concurrent same-target writers can corrupt the
// committed tree (a writer can rename another writer's half-written or
// truncated ".tmp" into the final path). That is the documented single-writer
// limit, not a guarantee the code makes, so these tests do not assert
// corruption-free mid-race output for one shared target. They assert the
// invariants the design does provide: concurrent writers to DISTINCT targets
// never interfere, and a clean single-writer pass after contention converges
// the target to the complete, correct tree. No materialize test exercises
// concurrent writers today. Run these under -race.

// syncTargetFiles is a small, internally consistent file set an adapter could
// emit for one artifact tree. The token marks which writer produced the bytes.
func syncTargetFiles(token string) []adapter.File {
	body := []byte(strings.Repeat(token, 4096))
	return []adapter.File{
		{Path: "a.txt", Content: body},
		{Path: filepath.Join("nested", "b.txt"), Content: body},
		{Path: filepath.Join("nested", "deep", "c.txt"), Content: body},
	}
}

// Spec: §6.6 step 5 — materialize.Write holds no shared mutable global state,
// so concurrent materializations into DISTINCT target directories all succeed
// and each lands its complete tree with no staged ".tmp" siblings left behind.
// This is the supported topology: one writer owns each target. A data race in
// the writer would surface here under -race; a stray global would corrupt one
// of the independent targets.
func TestSyncConcurrent_DistinctTargetsAllComplete(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	const writers, rounds = 8, 200
	var errs atomic.Int64

	ready := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each writer owns a distinct target directory under one parent.
			target := filepath.Join(parent, "writer", string(rune('a'+w)))
			files := syncTargetFiles("x")
			<-ready
			for r := 0; r < rounds; r++ {
				if err := materialize.Write(target, files); err != nil {
					errs.Add(1)
					t.Errorf("writer %d round %d: %v", w, r, err)
				}
			}
		}(w)
	}
	close(ready)
	wg.Wait()

	if errs.Load() != 0 {
		t.Fatalf("%d Write errors across distinct targets; want 0 (each target has a single writer)", errs.Load())
	}

	// Every target holds its complete tree and nothing partial: each file has
	// the exact expected content and no ".tmp" staging sibling survived.
	want := syncTargetFiles("x")
	for w := 0; w < writers; w++ {
		target := filepath.Join(parent, "writer", string(rune('a'+w)))
		assertTreeCompleteNoTmp(t, target, want)
	}
}

// Spec: §6.6 step 5 — two writers sharing ONE target is the unsupported
// topology (a manual `podium sync` racing the --watch loop). They share the
// fixed "<path>.tmp" staging path, so during contention the committed tree can
// hold a truncated or cross-writer-mixed file; that is the single-writer limit
// the design states, so the mid-race state is not asserted corruption-free.
// The invariant this asserts is convergence: once contention ends, a single
// clean materialize pass (the next non-racing sync) restores the target to the
// complete, correct tree the .tmp+rename design guarantees for a sole writer,
// with no torn file and no leftover ".tmp" sibling. This is the recovery
// property that makes a transient sync collision self-healing.
func TestSyncConcurrent_SameTargetConvergesOnCleanPass(t *testing.T) {
	t.Parallel()
	target := t.TempDir()

	const rounds = 300
	// Two writers race on the same target, each emitting a self-consistent set
	// marked with its own token. Collisions on the shared "<path>.tmp" can make
	// a Write error or leave the tree mixed/partial; tolerate it.
	var writers sync.WaitGroup
	for _, token := range []string{"A", "B"} {
		writers.Add(1)
		go func(token string) {
			defer writers.Done()
			files := syncTargetFiles(token)
			for r := 0; r < rounds; r++ {
				_ = materialize.Write(target, files)
			}
		}(token)
	}
	writers.Wait()

	// Contention has ended. A single clean materialize pass (the canonical
	// single-writer sync) must converge the target: every file lands with its
	// exact content and no staging temporary survives. The grounding's recovery
	// claim is that a transient two-writer collision is repaired by the next
	// non-racing sync; this asserts exactly that.
	final := syncTargetFiles("F")
	if err := materialize.Write(target, final); err != nil {
		t.Fatalf("clean single-writer pass after contention: %v "+
			"(the target did not converge: a non-racing sync must repair a transient collision)", err)
	}
	assertTreeCompleteNoTmp(t, target, final)
}

// assertTreeCompleteNoTmp confirms every expected file is present with exact
// content and no ".tmp" staging sibling remains.
func assertTreeCompleteNoTmp(t *testing.T, target string, want []adapter.File) {
	t.Helper()
	for _, f := range want {
		p := filepath.Join(target, f.Path)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if string(got) != string(f.Content) {
			t.Errorf("%s content mismatch: got %d bytes, want %d", p, len(got), len(f.Content))
		}
		if _, statErr := os.Stat(p + ".tmp"); !os.IsNotExist(statErr) {
			t.Errorf("staging temporary %s.tmp left behind", p)
		}
	}
}
