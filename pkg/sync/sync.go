// Package sync orchestrates materialization (spec §7.5, §13.11) for both
// registry sources: a filesystem registry (walk the local layers) and a
// server registry (read the effective view over the §7.5 HTTP API). Both
// run the configured HarnessAdapter and write atomically through
// pkg/materialize. The source is chosen per §7.5.2 dispatch: an http(s)
// URL routes to the server, every other value to the filesystem.
package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
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

// Options are the inputs to Run. RegistryPath is the registry source: an
// http(s):// URL routes to a Podium server (§7.5 server-source), every other
// value is a filesystem-registry path (§13.11). Target is the destination
// directory where adapter output lands. AdapterID selects the HarnessAdapter
// from the registry; the default is "none" (canonical layout pass-through).
type Options struct {
	RegistryPath    string
	Target          string
	AdapterID       string
	AdapterRegistry *adapter.Registry
	DryRun          bool
	// OverlayPath, when non-empty, points at a workspace overlay
	// directory whose records sit at the highest precedence of the
	// effective view (§4.6 / §6.4). Overlay artifacts override the
	// registry's contribution at the same canonical ID. Applies to the
	// filesystem source; a server source composes the overlay server-side.
	OverlayPath string
	// HTTPClient, when set, is used for server-source requests. Nil uses a
	// default client with a bounded timeout. Tests inject a stub here.
	HTTPClient *http.Client
}

// materialRecord is a source-neutral artifact ready for the HarnessAdapter.
// The filesystem source builds it from on-disk files; the server source
// builds it from the registry's HTTP responses, mirroring the MCP server's
// server-source delivery (§2.2): ArtifactBytes is the served frontmatter,
// SkillBytes is frontmatter+body for skills, Resources are the inline and
// fetched large resources. Artifact carries the parsed manifest so the
// §4.3 target_harnesses gate runs; it may be nil when the frontmatter does
// not parse.
type materialRecord struct {
	ID            string
	LayerID       string
	Artifact      *manifest.Artifact
	ArtifactBytes []byte
	SkillBytes    []byte
	Resources     map[string][]byte
}

// Result describes what a Run actually did. Used by callers (CLI, tests)
// for reporting.
type Result struct {
	Adapter   string
	Target    string
	Artifacts []ArtifactResult
	// Skipped lists the canonical IDs of artifacts excluded from this
	// run because their target_harnesses (§4.3) does not include the
	// active adapter. Recorded so callers can report what was dropped
	// rather than silently omitting it.
	Skipped []string
}

// ArtifactResult is one artifact's contribution to the materialized output.
type ArtifactResult struct {
	ID    string
	Layer string
	Files []string
}

// Run executes one sync. The registry source is dispatched per §7.5.2: an
// http(s):// URL reads the caller's effective view over the §7.5 HTTP API; a
// filesystem path reads the registry directly, applies layer composition
// with CollisionPolicyHighestWins (per §4.6), and writes the adapter output
// to Target. Both paths run the configured HarnessAdapter and the §7.5
// stale-file cleanup against the same lock file.
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

	// §7.5.2 dispatch: a URL routes to the Podium server, every other value
	// to the local filesystem registry.
	records, err := resolveRecords(opts)
	if err != nil {
		return nil, err
	}

	res := &Result{Adapter: a.ID(), Target: opts.Target}

	allFiles := []adapter.File{}
	for _, rec := range records {
		// §4.3 target_harnesses: an artifact that opts out of this
		// adapter is not materialized for it. Skip it before adapting
		// so its files are neither written nor counted as current
		// (stale-file cleanup then removes any prior output for it).
		if rec.Artifact != nil && !manifest.TargetsHarness(rec.Artifact.TargetHarnesses, a.ID()) {
			res.Skipped = append(res.Skipped, rec.ID)
			continue
		}
		out, err := a.Adapt(context.Background(), adapter.Source{
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
			Layer: rec.LayerID,
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

// resolveRecords dispatches on the registry source (§7.5.2) and returns the
// source-neutral records to materialize. A server URL reads the effective
// view over HTTP; any other value reads the local filesystem registry.
func resolveRecords(opts Options) ([]materialRecord, error) {
	if isServerSource(opts.RegistryPath) {
		return fetchServerRecords(context.Background(), opts)
	}
	return filesystemRecords(opts)
}

// filesystemRecords reads the filesystem registry, applies the §6.4
// workspace overlay, and converts the result to source-neutral records.
func filesystemRecords(opts Options) ([]materialRecord, error) {
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

	out := make([]materialRecord, 0, len(records))
	for _, rec := range records {
		out = append(out, materialRecord{
			ID:            rec.ID,
			LayerID:       rec.Layer.ID,
			Artifact:      rec.Artifact,
			ArtifactBytes: rec.ArtifactBytes,
			SkillBytes:    rec.SkillBytes,
			Resources:     rec.Resources,
		})
	}
	return out, nil
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
