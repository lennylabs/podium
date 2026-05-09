// Package sync orchestrates filesystem-source materialization (spec §7.5,
// §13.11): open the filesystem registry, walk every visible artifact in
// the caller's effective view, run the configured HarnessAdapter, and
// write atomically through pkg/materialize.
package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/overlay"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Errors returned by Run. Tests assert against them via errors.Is.
var (
	// ErrNoTarget signals that Options.Target was empty.
	ErrNoTarget = errors.New("sync: target directory not specified")
	// ErrNoRegistry signals that no registry source was configured.
	// Maps to config.no_registry in §6.10. Surfaces when neither
	// Options.RegistryPath nor a discoverable .podium/sync.yaml /
	// PODIUM_REGISTRY env var is set.
	ErrNoRegistry = errors.New("config.no_registry: no registry configured")
)

// Options are the inputs to Run. RegistryPath is the filesystem-source
// registry path (per §13.11). Target is the destination directory where
// adapter output lands. AdapterID selects the HarnessAdapter from the
// registry; the default is "none" (canonical layout pass-through).
type Options struct {
	RegistryPath    string
	Target          string
	AdapterID       string
	AdapterRegistry *adapter.Registry
	DryRun          bool
	// OverlayPath, when non-empty, points at a workspace overlay
	// directory whose records sit at the highest precedence of the
	// effective view (§4.6 / §6.4). Overlay artifacts override the
	// registry's contribution at the same canonical ID.
	OverlayPath string
}

// Result describes what a Run actually did. Used by callers (CLI, tests)
// for reporting.
type Result struct {
	Adapter   string
	Target    string
	Artifacts []ArtifactResult
}

// ArtifactResult is one artifact's contribution to the materialized output.
type ArtifactResult struct {
	ID    string
	Layer string
	Files []string
}

// Run executes one filesystem-source sync. The function does not consult
// any HTTP service; it reads the registry, applies layer composition with
// CollisionPolicyHighestWins (per §4.6), and writes the adapter output to
// Target.
//
// When Options.DryRun is true, Run resolves the artifact set, returns the
// Result, and writes nothing.
func Run(opts Options) (*Result, error) {
	if opts.RegistryPath == "" {
		return nil, ErrNoRegistry
	}
	if opts.Target == "" && !opts.DryRun {
		return nil, ErrNoTarget
	}
	if opts.AdapterID == "" {
		opts.AdapterID = "none"
	}
	if opts.AdapterRegistry == nil {
		opts.AdapterRegistry = adapter.DefaultRegistry()
	}
	a, err := opts.AdapterRegistry.Get(opts.AdapterID)
	if err != nil {
		return nil, err
	}

	reg, err := filesystem.Open(opts.RegistryPath)
	if err != nil {
		return nil, err
	}
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		return nil, err
	}

	// §6.4 workspace overlay: records under OverlayPath sit at the
	// highest precedence; overlay IDs replace the registry's
	// contribution at the same canonical ID.
	if opts.OverlayPath != "" {
		overlayRecords, oerr := overlay.Filesystem{Path: opts.OverlayPath}.Resolve(context.Background())
		if oerr != nil && !errors.Is(oerr, overlay.ErrNoOverlay) {
			return nil, fmt.Errorf("overlay: %w", oerr)
		}
		if len(overlayRecords) > 0 {
			records = mergeOverlay(records, overlayRecords)
		}
	}

	res := &Result{Adapter: a.ID(), Target: opts.Target}

	allFiles := []adapter.File{}
	for _, rec := range records {
		out, err := a.Adapt(adapter.Source{
			ArtifactID:    rec.ID,
			ArtifactBytes: rec.ArtifactBytes,
			SkillBytes:    rec.SkillBytes,
			Resources:     rec.Resources,
		})
		if err != nil {
			return nil, fmt.Errorf("adapter %q failed for %s: %w", a.ID(), rec.ID, err)
		}
		paths := make([]string, len(out))
		for i, f := range out {
			paths[i] = f.Path
		}
		sort.Strings(paths)
		res.Artifacts = append(res.Artifacts, ArtifactResult{
			ID:    rec.ID,
			Layer: rec.Layer.ID,
			Files: paths,
		})
		allFiles = append(allFiles, out...)
	}

	if opts.DryRun {
		return res, nil
	}

	// §7.5 stale-file cleanup: read the prior lock, compute the
	// set of paths this run wrote, and delete anything the prior
	// run wrote that this run didn't. Without this, syncing a
	// registry that drops an artifact leaves the artifact's files
	// behind in the target.
	priorPaths := loadPriorLockPaths(opts.Target)

	if err := materialize.Write(opts.Target, allFiles); err != nil {
		return nil, err
	}

	currentPaths := map[string]bool{}
	for _, f := range allFiles {
		currentPaths[f.Path] = true
	}
	removeStalePaths(opts.Target, priorPaths, currentPaths)

	// Persist the new lock entry list so the next run can repeat
	// the diff. Each artifact records every path it wrote so a
	// future delete is precise.
	lock := &LockFile{
		Target:       opts.Target,
		Harness:      a.ID(),
		LastSyncedAt: time.Now().UTC(),
		LastSyncedBy: "podium-sync",
	}
	for _, art := range res.Artifacts {
		for _, p := range art.Files {
			lock.Artifacts = append(lock.Artifacts, LockArtifact{
				ID:               art.ID,
				Layer:            art.Layer,
				MaterializedPath: p,
			})
		}
	}
	if err := WriteLock(opts.Target, lock); err != nil {
		// Lock write failure is non-fatal; the sync already
		// completed. Operators see the warning via the returned
		// Result + a printed message in the CLI.
		fmt.Fprintf(os.Stderr, "sync: lock write failed: %v\n", err)
	}
	return res, nil
}

// loadPriorLockPaths reads the lock file (if any) and returns the
// set of materialized paths from the previous successful sync.
// Missing lock returns an empty set.
func loadPriorLockPaths(target string) map[string]bool {
	out := map[string]bool{}
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		return out
	}
	for _, a := range lock.Artifacts {
		if a.MaterializedPath != "" {
			out[a.MaterializedPath] = true
		}
	}
	return out
}

// removeStalePaths deletes every prior path that's not in the
// current set. Best-effort: log to stderr on error and continue,
// since a partial cleanup is better than a hard failure that
// rolls back a successful materialize.
func removeStalePaths(target string, prior, current map[string]bool) {
	for p := range prior {
		if current[p] {
			continue
		}
		full := filepath.Join(target, p)
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "sync: stale-file cleanup: %v\n", err)
			continue
		}
		// Try to remove the now-empty parent directory; Remove
		// fails non-empty dirs naturally, which is what we want.
		_ = os.Remove(filepath.Dir(full))
	}
}

// mergeOverlay returns base with each overlay record replacing the
// same-ID base record (or appended when no base record matches).
// Overlay records keep their original Layer so downstream callers
// can identify the override path.
func mergeOverlay(base, overlay []filesystem.ArtifactRecord) []filesystem.ArtifactRecord {
	idx := map[string]int{}
	for i, rec := range base {
		idx[rec.ID] = i
	}
	out := make([]filesystem.ArtifactRecord, len(base))
	copy(out, base)
	for _, rec := range overlay {
		if i, ok := idx[rec.ID]; ok {
			out[i] = rec
			continue
		}
		idx[rec.ID] = len(out)
		out = append(out, rec)
	}
	return out
}
