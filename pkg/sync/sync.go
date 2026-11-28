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
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/overlay"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/version"
)

// Errors returned by Run. Tests assert against them via errors.Is.
var (
	// ErrNoTarget signals that Options.Target was empty.
	ErrNoTarget = errors.New("sync: target directory not specified")
	// ErrNoRegistry signals that no registry source was configured: Run
	// returns it when Options.RegistryPath is empty. Maps to
	// config.no_registry in §6.10. Resolving the registry from CLI flags,
	// PODIUM_REGISTRY, or a discovered sync.yaml (the §7.5.2 precedence
	// chain) is the caller's responsibility; pkg/sync reads no environment
	// variables or config files.
	ErrNoRegistry = errors.New("config.no_registry: no registry configured")
	// ErrOfflineCacheMiss signals a §7.4 offline-only sync against a
	// server-source registry. podium sync keeps no offline content cache, so
	// offline-only ("never contact the registry; structured error if cache
	// miss") has nothing local to materialize. The code lives in the §6.10
	// network.* namespace, matching the MCP server.
	ErrOfflineCacheMiss = errors.New("network.offline_cache_miss: offline-only mode cannot reach a server-source registry and podium sync keeps no offline cache")
	// ErrRegistryUnreachable signals a §7.4 always-revalidate sync (the
	// default mode) against an unreachable server-source registry.
	// always-revalidate returns "structured error network.registry_unreachable"
	// when there is no cache; podium sync keeps no offline content cache, so an
	// unreachable server-source registry has nothing to serve and surfaces this
	// namespaced §6.10 code rather than the raw transport message. offline-first
	// is the silent no-op above; offline-only returns ErrOfflineCacheMiss.
	ErrRegistryUnreachable = errors.New("network.registry_unreachable: always-revalidate mode cannot reach the server-source registry and podium sync keeps no offline cache")
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
	// Delivery resolves the §4.7.10 delivery check a server-source load
	// applies to every record. It is called once, at the start of the
	// server-source branch of the effective-view resolution, and never for a
	// filesystem source. A server-source load with a nil Delivery fails closed.
	Delivery DeliveryCheckFunc
	// OnUnparseable selects what a filesystem-source run does with the
	// previous output of an artifact the §4.1 unparseable-manifest rule
	// drops (§13.11.3): keep it (the zero value) or remove it. A server
	// source reports no drop, so the setting has no effect there.
	OnUnparseable OnUnparseable
	// Attestation resolves the §4.7.11 consumer trust policy of the sync
	// (§7.5 Author attestation). It is called once, at the start of the
	// effective-view resolution of either source, before the first registry
	// request or filesystem read. A nil Attestation is a sync with no
	// policy: the well-formed rule and the request binding still run, and
	// every ID resolves to none.
	Attestation AttestationFunc
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
	// AuthoredBytes is the filesystem source's hashing input: the authored
	// ARTIFACT.md as read from disk. It differs from ArtifactBytes only for a
	// skill with no ARTIFACT.md, where ArtifactBytes holds the §4.3.4
	// synthesized document and AuthoredBytes is empty. contentHashFor reads it
	// so this consumer and ingest hash the same bytes (§4.7.6). The server
	// source copies the served frontmatter into it.
	AuthoredBytes []byte
	// ContentHash is the registry's authoritative §6.6 content hash for a
	// server-source record, decoded from the /v1/load_artifact response. The
	// lock pins it verbatim so the committed (id, version, content_hash) triple
	// matches the registry's system-of-record value, which a locally recomputed
	// digest does not for a plain skill, whose served ARTIFACT.md is the
	// §4.3.4 synthesized document while its content hash frames a zero-length
	// ARTIFACT.md. Empty for a filesystem source, which has no separate
	// recorded hash and falls back to contentHashFor.
	// spec: §14.11, §7.5.3.
	ContentHash string
	// refusal is the §4.7.11 well-formed rule or request binding refusal of
	// the record at load. A refused record carries nothing else that a run
	// reads, and a run that selects it skips it (§7.5).
	refusal error
	// check runs the §4.7.11 attestation check of the record, nil for a
	// workspace overlay record, which is exempt (§6.4).
	check recordCheck
	// PackageDigest and Attestations are the lock version 2 members
	// (§7.5.3). The check sets them when it accepts a bundle: the package
	// digest a subject of that bundle lists, which is the walk's digest of
	// a filesystem-source package and the candidate of a served record, so
	// neither is ever a served value.
	PackageDigest string
	Attestations  []LockAttestation
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
	// Dropped lists the artifacts a filesystem-source composition dropped
	// under the §4.6 collision rule, the §4.1 unparseable-manifest rule, the
	// §4.6 no-inheritance rule, or the §4.1 removed-type rule (§13.11.3),
	// and the selected records of either source that the §4.7.11
	// well-formed rule, request binding, or attestation check refused
	// (§7.5), each with its code and details.constraint. Callers render each
	// entry with its Code and Reason. An entry whose PreviousOutputKept is
	// set is a drop whose previous files and lock entries the run kept (a
	// DryRun run reports what a real run would keep).
	Dropped []layer.Drop
	// Offline is set when a §7.4 offline-first sync could not reach the
	// server-source registry and left the existing materialized output in
	// place. Callers surface it as the offline status hosts can present.
	Offline bool
	// Changed reports whether this run altered the bytes on disk of a compared
	// path: a path the run materialized or a materialized path recorded in the
	// prior lock. A path whose bytes differ, a path that appeared, and a path the
	// stale-file cleanup removed each count; the lock file is never compared, and
	// a compared path unreadable before the write, or a prior-lock path unreadable
	// after it, counts as changed. It feeds the
	// $PODIUM_CHANGED variable of a kind: workspace target's publish phase, with
	// the meaning a marketplace target's RenderResult.Changed has. A DryRun run
	// writes nothing and leaves Changed false.
	// Spec: §7.5.2
	Changed bool
	// Warnings are the §4.7.11 warning texts of the run: a failure of the
	// attestation check under warn, an overlay artifact that is not
	// applied, and an overlay package the well-formed rule skips. Callers
	// write each on standard error (§7.5).
	Warnings []string
	// SignerChanges lists each checked artifact whose signers differ from
	// those its prior lock entries record (§7.5.3).
	SignerChanges []SignerChange
}

// ArtifactResult is one artifact's contribution to the materialized output.
// Version and Type come from the parsed manifest (empty when the frontmatter
// did not parse) and feed the §7.5 --json artifact entries.
type ArtifactResult struct {
	ID          string
	Version     string
	Type        string
	ContentHash string
	Layer       string
	Files       []string
	// ListingType and ListingDescription feed the §7.5.8 listing estimate.
	// ListingType is the manifest type, or skill when the ARTIFACT.md does
	// not parse and the SKILL.md does. ListingDescription is the SKILL.md
	// description for a skill and the ARTIFACT.md description otherwise.
	ListingType        string
	ListingDescription string
	// UserInvoked reports a skill whose resolved §4.3.4 invocation is user.
	// Run sets it from the record's resolved artifact, and Listing never
	// counts such a skill as an entry (§7.5.8). It is not part of the
	// --json artifacts array.
	UserInvoked bool
	// PackageDigest and Attestations are the lock version 2 members each
	// lock entry of the artifact records (§7.5.3).
	PackageDigest string
	Attestations  []LockAttestation
}

// listingEntry returns the §7.5.8 listing type and description of rec. A
// harness lists a skill under its SKILL.md description, which can differ from
// the ARTIFACT.md one, so a skill reads its SKILL.md. A record whose SKILL.md
// does not parse lists an empty description.
//
// Spec: §7.5.8
func listingEntry(rec materialRecord) (listingType, description string) {
	if rec.Artifact != nil && rec.Artifact.Type != manifest.TypeSkill {
		return string(rec.Artifact.Type), rec.Artifact.Description
	}
	skill, err := manifest.ParseSkill(rec.SkillBytes)
	switch {
	case err == nil:
		return string(manifest.TypeSkill), skill.Description
	case rec.Artifact != nil:
		return string(manifest.TypeSkill), ""
	default:
		return "", ""
	}
}

// Run executes one sync. The registry source is dispatched per §7.5.2: an
// http(s):// URL reads the caller's effective view over the §7.5 HTTP API; a
// filesystem path reads the registry directly, applies layer composition
// with CollisionPolicyDrop (§4.6, §13.11.3), recording each dropped artifact
// on Result.Dropped and materializing the rest, and writes the adapter output
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
	if err := opts.OnUnparseable.check(); err != nil {
		return nil, err
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
		// has nothing local to materialize.
		return nil, ErrOfflineCacheMiss
	}

	// §7.5.2 dispatch: a URL routes to the Podium server, every other value
	// to the local filesystem registry. The full effective view is resolved
	// first; scope and toggles narrow it below.
	view, err := resolveRecords(opts)
	if err != nil {
		var unreachable *serverUnreachableError
		if errors.As(err, &unreachable) {
			// §7.4 offline-first: "no error; serve cached results silently."
			// When the server-source registry is unreachable, leave the already
			// materialized output untouched and report a no-op offline result
			// rather than failing the sync.
			if opts.CacheMode == "offline-first" {
				return offlineFirstNoop(opts, a.ID()), nil
			}
			// §7.4 always-revalidate (the default): "if no cache, structured
			// error network.registry_unreachable." podium sync keeps no offline
			// content cache, so an unreachable server-source registry has
			// nothing to serve and surfaces the namespaced code rather than the
			// raw transport message. offline-only never reaches here:
			// it returned ErrOfflineCacheMiss above without dialing.
			return nil, fmt.Errorf("%w: %v", ErrRegistryUnreachable, unreachable.err)
		}
		return nil, err
	}

	// The prior lock carries the §7.5.5 toggles (applied only in watch /
	// override mode) and the materialized paths used for §7.5 stale cleanup.
	// The §7.5.3 recorded-path rule drops every entry whose path the cleanup
	// must not touch before any consumer reads the lock, so the entry is
	// neither cleaned up nor kept nor written into the next lock.
	// A .podium that is a symbolic link or not a directory fails the run here,
	// before anything is written: the lock could neither be read nor written,
	// and a run that went on would orphan every later stale file. Any other
	// lock read error is named on standard error and leaves the run without
	// a prior lock.
	priorLock, err := readPriorLock(opts.Target)
	if errors.Is(err, ErrStateDirUnusable) {
		return nil, err
	}
	priorLock = dropUnsafeLockPaths(priorLock)
	var toggles LockToggles
	if opts.PreserveToggles && priorLock != nil {
		toggles = priorLock.Toggles
	}

	// §7.5.1 scope + §7.5.5 toggles select the records to materialize.
	res := &Result{Adapter: a.ID(), Target: opts.Target, Profile: opts.Profile, Scope: opts.Scope,
		Dropped: view.dropped, Warnings: view.warnings}
	// Spec: §7.5 — the well-formed rule, the request binding, and the
	// attestation check decide each selected record before anything is
	// written. A refused record is skipped and its previous output kept.
	records, refused := checkSelected(context.Background(), selectRecords(opts.Scope, view.records, toggles), priorLock, view.att, res)
	all := withoutIDs(view.records, refused)

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
		// §6.9 "Adapter cannot translate an artifact": refuse to
		// materialize a field the selected harness has no §6.7.1
		// equivalent for (a ✗ cell). The MCP server load_artifact path
		// runs the same guard before adapting (cmd/podium-mcp/main.go),
		// so both canonical-Adapt consumers fail an untranslatable
		// artifact identically (§2.2) instead of one writing a verbatim
		// copy and the other silently producing no output.
		if terr := adapter.TranslationError(a.ID(), rec.Artifact); terr != nil {
			return nil, fmt.Errorf("adapter %q cannot translate %s: %w", a.ID(), rec.ID, terr)
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
		stripHarnessConfigPrefix(opts.Target, out)
		out = dropUnrecordablePaths(rec.ID, out)
		paths := make([]string, len(out))
		for i, f := range out {
			paths[i] = f.Path
		}
		sort.Strings(paths)
		ver, artType := "", ""
		if rec.Artifact != nil {
			ver = rec.Artifact.Version
			artType = string(rec.Artifact.Type)
		}
		listingType, listingDesc := listingEntry(rec)
		res.Artifacts = append(res.Artifacts, ArtifactResult{
			ID:                 rec.ID,
			Version:            ver,
			Type:               artType,
			ContentHash:        lockContentHash(rec),
			Layer:              rec.LayerID,
			Files:              paths,
			ListingType:        listingType,
			ListingDescription: listingDesc,
			UserInvoked:        rec.Artifact.UserInvoked(),
			PackageDigest:      rec.PackageDigest,
			Attestations:       rec.Attestations,
		})
		allFiles = append(allFiles, out...)
	}

	// §13.11.3: a drop does not stop the sync. The caller reports each
	// dropped artifact, and the rest is materialized, stale-file cleanup
	// runs, and the lock omits the dropped copy. The exception, under the keep
	// setting, is a drop under the unparseable-manifest rule of an artifact the
	// prior lock records whose output still belongs in the target: its previous
	// files and lock entries are kept. It is computed after the run's output is
	// known, because an artifact that now writes a kept path ends the keep, and
	// before the dry-run return, so a dry run reports the drops a real run
	// would keep.
	kept := keepPreviousOutput(res.Dropped, priorLock, keepRun{
		adapterID: a.ID(),
		policy:    opts.OnUnparseable,
		scope:     opts.Scope,
		toggles:   toggles,
		resolved:  all,
		files:     allFiles,
		target:    opts.Target,
		refused:   refused,
	})

	if opts.DryRun {
		return res, nil
	}

	// §7.5 stale-file cleanup: compute the paths this run wrote and delete
	// anything the prior run wrote that this run didn't. Without this,
	// syncing a registry that drops an artifact leaves the artifact's files
	// behind in the target. The prior entries carry the merge kinds (§6.7
	// config-merge), so a shared config file is reconciled rather than
	// deleted, and the file hashes, so a standalone file is deleted only while
	// it holds the bytes sync wrote (§7.5.3).
	priorPaths := lockPriorPaths(priorLock)

	// fileMerge records the §6.7 config-merge kind per path written this run:
	// "json" for OpMergeJSON, "inject" for OpInject, empty for a standalone
	// file. It feeds both the stale-file cleanup and the lock entries.
	// currentPaths is built before the write because writeTarget snapshots
	// those paths before materializing them.
	fileMerge := map[string]string{}
	currentPaths := map[string]bool{}
	for _, f := range allFiles {
		currentPaths[f.Path] = true
		fileMerge[f.Path] = mergeKind(f.Op)
	}
	changed, fileHashes, err := writeTarget(opts.Target, allFiles, currentPaths, priorPaths, kept)
	if err != nil {
		return nil, err
	}

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
		Profile: nullProfile(opts.Profile),
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
				Type:             art.Type,
				ContentHash:      art.ContentHash,
				Layer:            art.Layer,
				MaterializedPath: p,
				FileHash:         wholeFileHash(fileMerge[p], fileHashes[p]),
				Merge:            fileMerge[p],
				PackageDigest:    art.PackageDigest,
				Attestations:     art.Attestations,
			})
		}
	}
	// §13.11.3: a kept artifact's entries carry over unchanged, so the next
	// run still records its previous output.
	lock.Artifacts = append(lock.Artifacts, kept.entries...)
	res.Changed = changed
	if err := WriteLock(opts.Target, lock); err != nil {
		// An unusable .podium is never silent (§7.5.3): without the lock the
		// next run cannot clean up what this one wrote. The prior-lock read
		// already refuses it, so this covers a .podium replaced during the
		// run.
		if errors.Is(err, ErrStateDirUnusable) {
			return nil, err
		}
		// Any other lock write failure is non-fatal; the sync already
		// completed. Operators see the warning via the returned
		// Result + a printed message in the CLI.
		fmt.Fprintf(os.Stderr, "sync: lock write failed: %v\n", err)
	}
	return res, nil
}

// stripHarnessConfigPrefix removes the leading path segment from each
// adapter-emitted file when that segment already names the sync target's final
// directory component. The §7.5/§7.5.3/§14.11 model points --target at the
// harness config directory itself (e.g. ./build/.claude/) and records each
// materialized_path relative to it (agents/pay-invoice.md). Harness adapters
// prefix that same config directory onto every emitted path (.claude/agents/…),
// so a --target that already ends in .claude/ would otherwise produce a doubled
// .claude/.claude/ tree on disk and a lock recording .claude/agents/… instead
// of the spec's agents/…. Neutral buckets whose leading segment differs from the
// target directory (.podium/, .mcp.json, AGENTS.md) are left untouched.
// spec: §7.5.3, §14.11.
func stripHarnessConfigPrefix(target string, files []adapter.File) {
	base := filepath.Base(filepath.Clean(target))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return
	}
	for i := range files {
		// adapter.File.Path is slash-separated (built with path.Join), so the
		// first segment is everything up to the first "/".
		seg, rest, found := strings.Cut(files[i].Path, "/")
		if found && rest != "" && seg == base {
			files[i].Path = rest
		}
	}
}

// lockContentHash returns the §7.5.3 content_hash to pin in the lock for a
// record. A server source carries the registry's authoritative §6.6 value
// (decoded from /v1/load_artifact), used verbatim so the committed lock matches
// the registry's system-of-record hash even when the served bytes do not
// reproduce it, as for a plain skill served the §4.3.4 synthesized document. A
// filesystem source has no recorded hash, so it is computed from the authored
// bytes.
// spec: §14.11, §7.5.3.
func lockContentHash(rec materialRecord) string {
	if rec.ContentHash != "" {
		return rec.ContentHash
	}
	return contentHashFor(rec)
}

// contentHashFor computes the §7.5.3 content_hash for a filesystem-source
// record over the authored ARTIFACT.md bytes rather than the §4.3.4
// synthesized document the walk writes into ArtifactBytes for a plain skill,
// so this consumer, ingest, and the §6.6 check hash the same bytes (§4.7.6).
// The record supplies the other slots as before: the SKILL.md bytes when the
// artifact carries one, and every bundled resource, inline and large alike.
// The result carries the spec's "sha256:<hex>" prefix, which
// CanonicalContentHash omits. spec: §4.7.6, §7.5.3, §14.11.
func contentHashFor(rec materialRecord) string {
	return "sha256:" + version.CanonicalContentHash(rec.AuthoredBytes, rec.SkillBytes, rec.Resources)
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
			res.Artifacts = append(res.Artifacts, ArtifactResult{ID: la.ID, Version: la.Version, ContentHash: la.ContentHash, Layer: la.Layer})
		}
		res.Artifacts[i].Files = append(res.Artifacts[i].Files, la.MaterializedPath)
	}
	return res
}

// resolveRecords dispatches on the registry source (§7.5.2) and returns the
// source-neutral records to materialize. A server URL reads the effective
// view over HTTP; any other value reads the local filesystem registry. The
// second return lists the artifacts a filesystem-source composition dropped
// under the §4.6 collision rule, the §4.6 no-inheritance rule, or the §4.1
// removed-type rule (§13.11.3). A
// server source contributes none, because ingest already rejected the artifact
// (§7.3.1).
func resolveRecords(opts Options) (resolvedView, error) {
	var view resolvedView
	var err error
	if isServerSource(opts.RegistryPath) {
		// Spec: §4.7.10, §7.5 — this is the one resolution point of the
		// delivery check and of the consumer trust policy. It runs before
		// the first registry request, so a resolution refusal sends
		// nothing, and it runs whenever a server source is loaded,
		// including an effective view that lists no artifact.
		check, cerr := deliveryCheck(opts.Delivery)
		if cerr != nil {
			return view, cerr
		}
		if view.att, err = resolveAttestation(opts.Attestation); err != nil {
			return view, err
		}
		view.records, err = fetchServerRecords(context.Background(), opts, check, view.att)
	} else {
		// Spec: §7.5, §13.11.3 — a filesystem source resolves the policy
		// before its first read of the layer directories.
		if view.att, err = resolveAttestation(opts.Attestation); err != nil {
			return view, err
		}
		view.records, view.dropped, err = filesystemRecords(opts, view.att)
	}
	if err != nil {
		return view, err
	}
	// §6.4 workspace overlay: records under OverlayPath sit at the highest
	// precedence for both registry sources. The consumer (podium sync) merges
	// the overlay client-side because the developer's overlay directory is
	// local; a server source has no access to it.
	if opts.OverlayPath != "" {
		view.records, view.warnings, err = applyOverlay(view.records, opts.OverlayPath, view.att)
		if err != nil {
			return view, err
		}
	}
	return view, nil
}

// resolvedView is the effective view a load resolved: the records, the
// artifacts a filesystem-source composition dropped, the warnings of the
// workspace overlay, and the consumer trust policy the load resolved.
type resolvedView struct {
	records  []materialRecord
	dropped  []layer.Drop
	warnings []string
	att      *Attestation
}

// withoutIDs returns the records whose ID ids does not hold.
func withoutIDs(records []materialRecord, ids map[string]bool) []materialRecord {
	if len(ids) == 0 {
		return records
	}
	out := make([]materialRecord, 0, len(records))
	for _, rec := range records {
		if !ids[rec.ID] {
			out = append(out, rec)
		}
	}
	return out
}

// applyOverlay resolves the §6.4 workspace overlay and merges its records on
// top of base at the source-neutral materialRecord level. An overlay record
// replaces the same-ID base record (or appends when no base record matches),
// per the §6.4 overlay exception, and the overlay applies no ingest rule.
// ErrNoOverlay (the directory is absent) leaves base unchanged.
//
// Each overlay package meets the §4.7.11 well-formed rule, and an overlay
// artifact applies only to the IDs att admits it for (§7.5 Author
// attestation). A package that is skipped leaves the base record of its ID
// in place and is returned as a warning.
func applyOverlay(base []materialRecord, overlayPath string, att *Attestation) ([]materialRecord, []string, error) {
	overlayRecords, oerr := overlay.Filesystem{Path: overlayPath}.Resolve(context.Background())
	if oerr != nil && !errors.Is(oerr, overlay.ErrNoOverlay) {
		return nil, nil, fmt.Errorf("overlay: %w", oerr)
	}
	if len(overlayRecords) == 0 {
		return base, nil, nil
	}
	var warnings []string
	idx := map[string]int{}
	for i, rec := range base {
		idx[rec.ID] = i
	}
	out := make([]materialRecord, len(base))
	copy(out, base)
	for _, rec := range overlayRecords {
		if err := wellFormedPackage(rec.ID, rec.Resources); err != nil {
			warnings = append(warnings, fmt.Sprintf("overlay package %s skipped: %v", attest.ReportString(rec.ID), err))
			continue
		}
		if ok, warning := att.overlayApplies(rec.ID); !ok {
			if warning != "" {
				warnings = append(warnings, warning)
			}
			continue
		}
		mr := materialRecord{
			ID:            rec.ID,
			LayerID:       rec.Layer.ID,
			Artifact:      rec.Artifact,
			ArtifactBytes: rec.ArtifactBytes,
			AuthoredBytes: rec.AuthoredBytes,
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
	return out, warnings, nil
}

// filesystemRecords reads the filesystem registry and converts the result to
// source-neutral records. The §6.4 workspace overlay is merged by the caller
// (resolveRecords) so both registry sources honor it identically. The
// second return lists the artifacts dropped under the §4.6 collision rule,
// the §4.6 no-inheritance rule, or the §4.1 removed-type rule (§13.11.3).
func filesystemRecords(opts Options, att *Attestation) ([]materialRecord, []layer.Drop, error) {
	reg, err := filesystem.Open(opts.RegistryPath)
	if err != nil {
		return nil, nil, err
	}
	layers := map[string]*fsLayer{}
	// spec: §13.11.3 — the filesystem source runs the same §4.6 collision
	// rule as the server, so materialization produces equivalent output for
	// the same artifact directory (the §13.11.6 migration equivalence). A
	// cross-layer collision drops the higher-precedence artifact, which a
	// server would have rejected at ingest, and the walk continues with the
	// rest. Each artifact whose ARTIFACT.md declares extends: (§4.6) or a
	// removed type (§4.1) is dropped before the collision rule, as a server
	// never stores it.
	var dropped []layer.Drop
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyDrop,
		OnDrop:          func(d layer.Drop) { dropped = append(dropped, d) },
		// Spec: §13.11.3 — the layer digest lists the packages the walk
		// read, so the digested bytes are the bytes the run writes.
		OnLayer: func(l filesystem.Layer, all []filesystem.ArtifactRecord, fsys fs.FS) {
			layers[l.ID] = &fsLayer{layer: l, records: all, fsys: fsys}
		},
	})
	if err != nil {
		return nil, nil, err
	}

	checker := att.Checker(nil)
	out := make([]materialRecord, 0, len(records))
	for _, rec := range records {
		mr := materialRecord{
			ID:            rec.ID,
			LayerID:       rec.Layer.ID,
			Artifact:      rec.Artifact,
			ArtifactBytes: rec.ArtifactBytes,
			AuthoredBytes: rec.AuthoredBytes,
			SkillBytes:    rec.SkillBytes,
			Resources:     rec.Resources,
		}
		// Spec: §13.11.3 — the well-formed rule applies to every package
		// under every mode; the check runs on the packages a run selects.
		if mr.refusal = wellFormedPackage(rec.ID, rec.Resources); mr.refusal == nil {
			mr.check = localCheck(checker, att, layers[rec.Layer.ID], rec)
		}
		out = append(out, mr)
	}
	return out, dropped, nil
}

// lockPriorPaths returns the materialized paths recorded in a lock, each
// mapped to the entry that records it. The stale-file cleanup reads the
// entry's §6.7 config-merge kind ("json", "inject", or "" for a standalone
// file) and its §7.5.3 file_hash. A path several entries record, such as a
// config-merge file several artifacts contribute to, maps to the last of them.
// A nil lock returns an empty map.
func lockPriorPaths(lock *LockFile) map[string]LockArtifact {
	out := map[string]LockArtifact{}
	if lock == nil {
		return out
	}
	for _, a := range lock.Artifacts {
		if a.MaterializedPath != "" {
			out[a.MaterializedPath] = a
		}
	}
	return out
}

// wholeFileHash returns the file_hash a lock entry records for a path written
// with the config-merge kind merge, whose bytes on disk after the write hash
// to hash. Only a standalone path, which sync writes whole, records one: the
// cleanup never deletes a config-merge path, so it needs none.
//
// Spec: §7.5.3
func wholeFileHash(merge, hash string) string {
	if merge != "" {
		return ""
	}
	return hash
}

// mergeKind maps an adapter.FileOp to the lock's config-merge kind string.
func mergeKind(op adapter.FileOp) string {
	switch op {
	case adapter.OpMergeJSON:
		return "json"
	case adapter.OpInject:
		return "inject"
	default:
		return ""
	}
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
	// The materialization set is returned in ascending canonical artifact ID
	// order (§7.5), which is the order both registry sources can produce and
	// the order the merge fold, Result.Artifacts, and the lock all inherit.
	//
	// It selects no artifact: a canonical-ID collision is resolved by Walk
	// before the records arrive (pkg/registry/filesystem/walk.go), and
	// applyOverlay replaces in place. It does decide the fold order inside a
	// shared merge target, and there the key is not the canonical ID: mcpName
	// keys the mcpServers entry by the artifact's name: frontmatter
	// (pkg/adapter/layout.go), so two distinct IDs can write one key. How a
	// shared target composes once the fragments arrive in this order is stated
	// in §7.5. Filesystem mode previously folded in layer-precedence order and
	// server mode in ID order; this makes both fold in ID order, which is the
	// only order a server source can produce, since the sync manifest carries
	// layer IDs and never the tenant's layer_order:.
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// writeTarget materializes files into target, reconciles the prior lock's
// stale paths, and reports whether any compared path changed on disk.
// Spec: §7.5.2 — $PODIUM_CHANGED compares the bytes on disk of the paths
// the run materializes and the prior lock's materialized paths, at the start
// of materialization (after the prepare phase) and after the stale-file
// cleanup. A config-merge or inject path is compared as the whole merged file.
// The before-snapshot reads every compared path tolerantly: materialize.Write
// replaces a standalone file by rename without reading it, so an unreadable
// file must not turn a sync that succeeds into a failure; it only makes the
// result true. A prior-only path is shared with the operator and cleaned up
// best-effort by removeStalePaths, so it is read tolerantly after the write
// as well. A current path unreadable after the write fails the sync closed,
// because the run just wrote it. kept is the previous output of the artifacts
// the run keeps (§13.11.3): its standalone paths are not removed and its
// config-merge entries survive the reconciliation. A prior-only path comes
// from the lock, so it is read under the rule the cleanup applies (§7.5.3): a
// path the cleanup refuses cannot be read and counts as altered. A current
// path is read under the same rule, so a link at a materialized path is
// followed only to a file inside the target.
//
// The second result maps each current path to the digest of its bytes on disk
// after the write, which is the §7.5.3 file hash (materialize.FileHash) the
// lock records for a standalone path.
func writeTarget(target string, files []adapter.File, current map[string]bool, prior map[string]LockArtifact, kept keptOutput) (bool, map[string]string, error) {
	priorOnly := priorOnlyPaths(current, prior)
	beforeCurrent, unobservedCurrent := tolerantDigests(current, readRecorded(target))
	beforePrior, unobservedPrior := tolerantDigests(priorOnly, readRecorded(target))

	if err := materialize.WriteKeeping(target, files, kept.keepFunc()); err != nil {
		return false, nil, err
	}
	// A workspace sync keeps a stale file the hash check refuses and only
	// reports it (§7.5.3); removeStalePaths has already named each one.
	_ = removeStalePaths(target, prior, current, kept)

	afterCurrent, err := onDiskDigests(target, current)
	if err != nil {
		return false, nil, fmt.Errorf("sync: read target after materialize: %w", err)
	}
	afterPrior, unobservedAfter := tolerantDigests(priorOnly, readRecorded(target))
	if unobservedCurrent || unobservedPrior || unobservedAfter {
		return true, afterCurrent, nil
	}
	changed, _ := changeSet(mergeDigests(beforeCurrent, beforePrior), mergeDigests(afterCurrent, afterPrior), nil)
	return changed, afterCurrent, nil
}

// priorOnlyPaths returns the prior-lock paths the current run does not
// materialize: the paths the stale-file cleanup reconciles.
func priorOnlyPaths(current map[string]bool, prior map[string]LockArtifact) map[string]bool {
	out := map[string]bool{}
	for p := range prior {
		if !current[p] {
			out[p] = true
		}
	}
	return out
}

// readRecorded returns a reader of a compared path below workdir through
// materialize.ReadRecorded: a symbolic link, at a directory or at the path
// itself, is followed only when it resolves inside the target to a path the
// §7.5.3 rule accepts, so a compared path cannot direct the read at a file
// outside the target, a device, or a FIFO, while a link such as GEMINI.md to
// AGENTS.md reads the file it names.
//
// Spec: §7.5.2, §7.5.3
func readRecorded(workdir string) func(rel string) ([]byte, error) {
	return func(rel string) ([]byte, error) {
		return materialize.ReadRecorded(workdir, rel)
	}
}

// tolerantDigests is the non-failing form of onDiskDigests for the
// comparison. A path absent from disk contributes no entry, as in
// onDiskDigests; any other read error is reported through the unobservable
// flag rather than returned, so the caller counts the run as changed instead
// of failing it.
func tolerantDigests(paths map[string]bool, read func(rel string) ([]byte, error)) (digests map[string]string, unobservable bool) {
	digests = make(map[string]string, len(paths))
	for p := range paths {
		data, err := read(p)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				unobservable = true
			}
			continue
		}
		digests[p] = digest(data)
	}
	return digests, unobservable
}

// mergeDigests returns the union of two digest maps keyed by disjoint path
// sets: the current paths and the prior-only paths of one snapshot.
func mergeDigests(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}

// removeStalePaths reconciles every prior path that this run did not write.
// A standalone path is deleted only while its bytes hash to the file_hash its
// entry records: the lock can come from another party, and a forged entry
// could otherwise name any file the operator wrote inside the target, while a
// materialized file the operator edited would lose the edits. An entry with no
// file_hash, a file whose bytes differ, and a file that cannot be read are
// left in place, named on standard error, and returned, so the §7.8 render can
// fail rather than publish a tree that still carries them. A workspace sync
// writes a lock that records only what it wrote or keeps, so the entry leaves
// that lock. The paths are visited in sorted order so the report reads the
// same on every run. A §6.7 config-merge path is shared with the operator, so
// it is reconciled in place (Podium's tagged entries or inject blocks are
// stripped) rather than deleted, which preserves the operator's other entries
// when the last contributing artifact is removed. Best-effort:
// log to stderr on error and continue, since a partial cleanup is better than
// a hard failure that rolls back a successful materialize. The previous output
// in kept (§13.11.3) is not stale: its standalone paths stay on disk, and the
// reconciliation of a config-merge path leaves its entries in place.
//
// Every prior path comes from a lock another party can write (§7.5.3, §7.8),
// so the cleanup applies the recorded-path rule here as well as when the lock
// is read, which covers a caller of the exported Reconcile that passes paths
// it did not take through dropUnsafeLockPaths. The removal and the rewrite go
// through materialize.RemoveStale and materialize.RewriteStale, which accept
// the directory links the writer accepts and refuse a link that leaves the
// target, a link or a directory at the path, and a path that resolves into
// .git or the sync state; a refused path is skipped with a warning. The
// removal prunes the now-empty parent directories up to the target root,
// because a nested layout (a skill's .claude/skills/<name>/, a bundled
// resource under .podium/resources/<id>/) leaves several empty ancestors when
// its only file is removed.
//
// Spec: §7.5.3
func removeStalePaths(target string, prior map[string]LockArtifact, current map[string]bool, kept keptOutput) []StaleFileKept {
	var live []os.FileInfo
	var left []StaleFileKept
	for _, p := range slices.Sorted(maps.Keys(prior)) {
		entry := prior[p]
		if current[p] || kept.standalone[p] {
			continue
		}
		if err := materialize.CheckRecordedPath(p); err != nil {
			fmt.Fprintf(os.Stderr, "sync: stale-file cleanup: skipping lock entry %q: %v\n", p, err)
			continue
		}
		if live == nil {
			live = liveFiles(target, current, kept)
		}
		if aliasesLive(target, p, live) {
			continue
		}
		if entry.Merge != "" {
			reconcileOrphanConfig(target, p, entry.Merge, kept.keepFunc())
			continue
		}
		if err := materialize.RemoveStale(target, p, entry.FileHash); err != nil {
			fmt.Fprintf(os.Stderr, "sync: stale-file cleanup: skipping %q: %v\n", p, err)
			if reason := keptReason(err); reason != "" {
				left = append(left, StaleFileKept{Path: p, Reason: reason})
			}
		}
	}
	return left
}

// liveFiles returns the file information of every path the run wrote and
// every standalone path it keeps (§13.11.3), following links as the writer
// does. The result is never nil, so the caller computes it once.
func liveFiles(target string, current map[string]bool, kept keptOutput) []os.FileInfo {
	out := make([]os.FileInfo, 0, len(current)+len(kept.standalone))
	for _, set := range []map[string]bool{current, kept.standalone} {
		for p := range set {
			if info, err := os.Stat(filepath.Join(target, filepath.FromSlash(p))); err == nil {
				out = append(out, info)
			}
		}
	}
	return out
}

// aliasesLive reports whether the prior-lock path p names, through a link
// inside the target, the same file as a path the run wrote or keeps. The
// cleanup follows such links (§7.5.3), so a stale entry such as alias/a with
// alias linked to real would otherwise remove or strip the output the run
// just wrote at real/a; such an entry is not stale and is skipped. The
// os.Stat follows links but reads only metadata.
//
// Spec: §7.5.3
func aliasesLive(target, p string, live []os.FileInfo) bool {
	info, err := os.Stat(filepath.Join(target, filepath.FromSlash(p)))
	if err != nil {
		return false
	}
	for _, l := range live {
		if os.SameFile(info, l) {
			return true
		}
	}
	return false
}

// reconcileOrphanConfig strips Podium's contribution from the config-merge
// file at rel below target whose last contributing artifact is gone, leaving
// the operator's content in place (§6.7 "removing the artifact removes its
// entry"). The file is left on disk because the operator may own the rest of
// it. A missing file is a no-op. The entries of an artifact keep reports stay;
// a nil keep strips them all. The rewrite goes through
// materialize.RewriteStale, so a path the stale-file cleanup refuses is left
// untouched (§7.5.3).
func reconcileOrphanConfig(target, rel, merge string, keep func(id string) bool) {
	var strip func([]byte) []byte
	switch merge {
	case "json":
		strip = func(data []byte) []byte { return materialize.StripPodiumOwnedBytes(data, keep) }
	case "inject":
		strip = func(data []byte) []byte { return materialize.StripPodiumBlocks(data, rel, keep) }
	default:
		return
	}
	if err := materialize.RewriteStale(target, rel, strip); err != nil {
		fmt.Fprintf(os.Stderr, "sync: config reconcile: skipping %q: %v\n", rel, err)
	}
}
