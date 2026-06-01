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
	// ErrOfflineCacheMiss signals a §7.4 offline-only sync against a
	// server-source registry. podium sync keeps no offline content cache, so
	// offline-only ("never contact the registry; structured error if cache
	// miss") has nothing local to materialize. The code lives in the §6.10
	// network.* namespace, matching the MCP server (F-7.4.3, F-7.4.5).
	ErrOfflineCacheMiss = errors.New("network.offline_cache_miss: offline-only mode cannot reach a server-source registry and podium sync keeps no offline cache")
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
	// registry's contribution at the same canonical ID. The consumer merges
	// it client-side for both registry sources: the developer's overlay
	// directory is local, so a server source cannot see it.
	OverlayPath string
	// HTTPClient, when set, is used for server-source requests. Nil uses a
	// default client with a bounded timeout. Tests inject a stub here.
	HTTPClient *http.Client
	// Token is the caller credential attached as Authorization: Bearer on
	// every server-source registry API request (a §6.3.2 injected-session-token
	// or a §6.3.1 oauth-device-code access token). Empty reaches the registry
	// anonymously. It applies only to a server source; a filesystem source is
	// read locally with no request to authenticate.
	//
	// spec: §6.3.2, §14.11 — CI supplies a runtime-issued JWT via
	// PODIUM_SESSION_TOKEN_FILE so podium sync authenticates against the
	// remote registry the same way the MCP server and read CLI do.
	Token string
	// Scope narrows the effective view per §7.5.1 (--include / --exclude /
	// --type). The empty filter materializes the full effective view. The
	// resolved scope is persisted into the lock (§7.5.3).
	Scope ScopeFilter
	// Profile is the active profile name (§7.5.3). Persisted into the lock so
	// override, save-as, and profile edit can default to it.
	Profile string
	// PreserveToggles selects the §7.5.4 toggle semantics. When true (watch
	// mode and override re-materialization), Run reads the prior lock's
	// toggles, applies them on top of the scoped set (add fetched, remove
	// dropped), and carries them into the new lock. When false (a manual
	// one-shot sync), toggles are cleared, which is the operator's
	// "reset to baseline" gesture.
	PreserveToggles bool
	// LastSyncedBy is the §7.5.3 lock provenance field: one of "full"
	// (manual one-shot sync), "watch" (watcher-driven rerun), or "override"
	// (override-driven materialization). An empty value defaults to "full".
	LastSyncedBy string
	// CacheMode is the §7.4 PODIUM_CACHE_MODE the sync applies to a
	// server-source registry: "always-revalidate" (default; fetch every run),
	// "offline-first" (tolerate an unreachable server, leaving the existing
	// materialized output in place), or "offline-only" (never contact the
	// server). It is a no-op for a filesystem source, which is read locally and
	// is always reachable. An empty value behaves as always-revalidate.
	CacheMode string
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
// for reporting. Profile and Scope echo the resolved §7.5.1 scope and active
// profile so the §7.5 --json dry-run envelope can emit them.
type Result struct {
	Adapter   string
	Profile   string
	Target    string
	Scope     ScopeFilter
	Artifacts []ArtifactResult
	// Skipped lists the canonical IDs of artifacts excluded from this
	// run because their target_harnesses (§4.3) does not include the
	// active adapter. Recorded so callers can report what was dropped
	// rather than silently omitting it.
	Skipped []string
	// Offline is set when a §7.4 offline-first sync could not reach the
	// server-source registry and left the existing materialized output in
	// place. Callers surface it as the offline status hosts can present.
	Offline bool
}

// ArtifactResult is one artifact's contribution to the materialized output.
// Version and Type come from the parsed manifest (empty when the frontmatter
// did not parse) and feed the §7.5 --json artifact entries.
type ArtifactResult struct {
	ID      string
	Version string
	Type    string
	Layer   string
	Files   []string
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

	// §7.4 cache modes apply to a server-source registry; a filesystem
	// registry is read locally and is always reachable, so the mode is a no-op
	// there (offline-only's "cache only" is trivially satisfied when no network
	// call happens).
	if isServerSource(opts.RegistryPath) && opts.CacheMode == "offline-only" {
		// §7.4: "never contact the registry; structured error if cache miss."
		// podium sync keeps no offline content cache, so a server-source sync
		// has nothing local to materialize (F-7.4.3).
		return nil, ErrOfflineCacheMiss
	}

	// §7.5.2 dispatch: a URL routes to the Podium server, every other value
	// to the local filesystem registry. The full effective view is resolved
	// first; scope and toggles narrow it below.
	all, err := resolveRecords(opts)
	if err != nil {
		// §7.4 offline-first: "no error; serve cached results silently." When
		// the server-source registry is unreachable, leave the already
		// materialized output untouched and report a no-op offline result
		// rather than failing the sync (F-7.4.3).
		if opts.CacheMode == "offline-first" && errors.As(err, new(*serverUnreachableError)) {
			return offlineFirstNoop(opts, a.ID()), nil
		}
		return nil, err
	}

	// The prior lock carries the §7.5.5 toggles (applied only in watch /
	// override mode) and the materialized paths used for §7.5 stale cleanup.
	priorLock, _ := ReadLock(opts.Target)
	var toggles LockToggles
	if opts.PreserveToggles && priorLock != nil {
		toggles = priorLock.Toggles
	}

	// §7.5.1 scope + §7.5.5 toggles select the records to materialize.
	records := selectRecords(opts.Scope, all, toggles)

	res := &Result{Adapter: a.ID(), Target: opts.Target, Profile: opts.Profile, Scope: opts.Scope}

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
		version, artType := "", ""
		if rec.Artifact != nil {
			version = rec.Artifact.Version
			artType = string(rec.Artifact.Type)
		}
		res.Artifacts = append(res.Artifacts, ArtifactResult{
			ID:      rec.ID,
			Version: version,
			Type:    artType,
			Layer:   rec.LayerID,
			Files:   paths,
		})
		allFiles = append(allFiles, out...)
	}

	if opts.DryRun {
		return res, nil
	}

	// §7.5 stale-file cleanup: compute the paths this run wrote and delete
	// anything the prior run wrote that this run didn't. Without this,
	// syncing a registry that drops an artifact leaves the artifact's files
	// behind in the target.
	priorPaths := lockPaths(priorLock)

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
	//
	// spec: §7.5.3 — last_synced_by is one of "full | watch | override".
	// Run writes the value the caller selected (manual sync defaults to
	// "full"; the watcher passes "watch"; override passes "override").
	syncedBy := opts.LastSyncedBy
	if syncedBy == "" {
		syncedBy = "full"
	}
	lock := &LockFile{
		Target:  opts.Target,
		Harness: a.ID(),
		Profile: opts.Profile,
		Scope: LockScope{
			Include: opts.Scope.Include,
			Exclude: opts.Scope.Exclude,
			Type:    opts.Scope.Types,
		},
		LastSyncedAt: time.Now().UTC(),
		LastSyncedBy: syncedBy,
		Toggles:      toggles,
	}
	for _, art := range res.Artifacts {
		for _, p := range art.Files {
			lock.Artifacts = append(lock.Artifacts, LockArtifact{
				ID:               art.ID,
				Version:          art.Version,
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

// offlineFirstNoop builds the §7.4 offline-first result for a sync whose
// server-source registry was unreachable. It writes nothing and runs no
// stale-file cleanup, leaving the existing materialized output in place; the
// returned Result echoes the prior lock so callers see the served (cached)
// state and the Offline flag.
func offlineFirstNoop(opts Options, adapterID string) *Result {
	res := &Result{Adapter: adapterID, Target: opts.Target, Profile: opts.Profile, Scope: opts.Scope, Offline: true}
	prior, _ := ReadLock(opts.Target)
	if prior == nil {
		return res
	}
	idx := map[string]int{}
	for _, la := range prior.Artifacts {
		i, ok := idx[la.ID]
		if !ok {
			i = len(res.Artifacts)
			idx[la.ID] = i
			res.Artifacts = append(res.Artifacts, ArtifactResult{ID: la.ID, Version: la.Version, Layer: la.Layer})
		}
		res.Artifacts[i].Files = append(res.Artifacts[i].Files, la.MaterializedPath)
	}
	return res
}

// resolveRecords dispatches on the registry source (§7.5.2) and returns the
// source-neutral records to materialize. A server URL reads the effective
// view over HTTP; any other value reads the local filesystem registry.
func resolveRecords(opts Options) ([]materialRecord, error) {
	var records []materialRecord
	var err error
	if isServerSource(opts.RegistryPath) {
		records, err = fetchServerRecords(context.Background(), opts)
	} else {
		records, err = filesystemRecords(opts)
	}
	if err != nil {
		return nil, err
	}
	// §6.4 workspace overlay: records under OverlayPath sit at the highest
	// precedence for both registry sources. The consumer (podium sync) merges
	// the overlay client-side because the developer's overlay directory is
	// local; a server source has no access to it.
	if opts.OverlayPath != "" {
		records, err = applyOverlay(records, opts.OverlayPath)
		if err != nil {
			return nil, err
		}
	}
	return records, nil
}

// applyOverlay resolves the §6.4 workspace overlay and merges its records on
// top of base at the source-neutral materialRecord level. An overlay record
// replaces the same-ID base record (or appends when no base record matches),
// matching the highest-precedence semantics of §4.6. ErrNoOverlay (the
// directory is absent) leaves base unchanged.
func applyOverlay(base []materialRecord, overlayPath string) ([]materialRecord, error) {
	overlayRecords, oerr := overlay.Filesystem{Path: overlayPath}.Resolve(context.Background())
	if oerr != nil && !errors.Is(oerr, overlay.ErrNoOverlay) {
		return nil, fmt.Errorf("overlay: %w", oerr)
	}
	if len(overlayRecords) == 0 {
		return base, nil
	}
	idx := map[string]int{}
	for i, rec := range base {
		idx[rec.ID] = i
	}
	out := make([]materialRecord, len(base))
	copy(out, base)
	for _, rec := range overlayRecords {
		mr := materialRecord{
			ID:            rec.ID,
			LayerID:       rec.Layer.ID,
			Artifact:      rec.Artifact,
			ArtifactBytes: rec.ArtifactBytes,
			SkillBytes:    rec.SkillBytes,
			Resources:     rec.Resources,
		}
		if i, ok := idx[rec.ID]; ok {
			out[i] = mr
			continue
		}
		idx[rec.ID] = len(out)
		out = append(out, mr)
	}
	return out, nil
}

// filesystemRecords reads the filesystem registry and converts the result to
// source-neutral records. The §6.4 workspace overlay is merged by the caller
// (resolveRecords) so both registry sources honor it identically.
func filesystemRecords(opts Options) ([]materialRecord, error) {
	reg, err := filesystem.Open(opts.RegistryPath)
	if err != nil {
		return nil, err
	}
	// spec: §13.11.3 — filesystem source runs the same composer and extends:
	// resolver as the server so materialization produces equivalent output for
	// the same artifact directory (the §13.11.6 migration equivalence).
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
		ResolveExtends:  true,
	})
	if err != nil {
		return nil, err
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

// lockPaths returns the set of materialized paths recorded in a lock.
// A nil lock returns an empty set.
func lockPaths(lock *LockFile) map[string]bool {
	out := map[string]bool{}
	if lock == nil {
		return out
	}
	for _, a := range lock.Artifacts {
		if a.MaterializedPath != "" {
			out[a.MaterializedPath] = true
		}
	}
	return out
}

// selectRecords computes the materialization set from the full effective view
// (§7.5.1 scope, then §7.5.5 toggles). The scoped set is taken first; each
// toggles.add ID is then pulled from the full view when the caller can see it
// (an invisible ID is silently absent, matching §7.5.5 visibility), and each
// toggles.remove ID is dropped. Toggles are empty for a manual sync, so the
// result is exactly the scoped set there.
func selectRecords(scope ScopeFilter, all []materialRecord, toggles LockToggles) []materialRecord {
	scoped := scope.filterMaterial(all)

	byID := make(map[string]materialRecord, len(all))
	for _, r := range all {
		byID[r.ID] = r
	}

	included := make(map[string]bool, len(scoped))
	out := make([]materialRecord, 0, len(scoped)+len(toggles.Add))
	for _, r := range scoped {
		included[r.ID] = true
		out = append(out, r)
	}
	for _, t := range toggles.Add {
		if included[t.ID] {
			continue
		}
		if r, ok := byID[t.ID]; ok {
			included[t.ID] = true
			out = append(out, r)
		}
	}
	if len(toggles.Remove) > 0 {
		drop := make(map[string]bool, len(toggles.Remove))
		for _, t := range toggles.Remove {
			drop[t.ID] = true
		}
		kept := out[:0]
		for _, r := range out {
			if !drop[r.ID] {
				kept = append(kept, r)
			}
		}
		out = kept
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
