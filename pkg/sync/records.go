package sync

import (
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
)

// Record is one source-neutral artifact ready for a HarnessAdapter or a §7.8
// marketplace emitter. It is the exported projection of the internal
// materialRecord: `podium publish` (pkg/publish) reads the same effective view
// as `podium sync` by calling FetchRecords, so a published marketplace reflects
// the publishing identity's effective view (§4.6) exactly as a workspace sync
// would. The fields mirror the adapter.Source inputs an emitter consumes.
type Record struct {
	// ID is the canonical artifact path under the registry root.
	ID string
	// LayerID is the layer the artifact resolved from.
	LayerID string
	// Artifact is the parsed manifest. It is nil when the served frontmatter
	// did not parse, so callers that gate on the §4.3 fields guard for nil.
	Artifact *manifest.Artifact
	// ArtifactBytes is the verbatim ARTIFACT.md frontmatter (and body, for a
	// non-skill type) the adapter or emitter renders from.
	ArtifactBytes []byte
	// SkillBytes is the verbatim SKILL.md (frontmatter+body) for a skill,
	// empty otherwise.
	SkillBytes []byte
	// Resources are bundled non-manifest files keyed by relative path inside
	// the artifact directory.
	Resources map[string][]byte
	// ContentHash is the registry's authoritative §6.6 content hash for a
	// server-source record, empty for a filesystem source.
	ContentHash string
}

// Select returns the records the scope filter selects, applying the §7.5.1
// include, exclude, and type globs over the canonical artifact IDs. An empty
// filter selects every record (ScopeFilter.IsEmpty). It is the Record-typed form
// of the internal filterMaterial used by Run, so `podium publish` intersects a
// plugin's scope filter with the effective view through the same glob semantics
// `podium sync` applies to a target scope.
func (f ScopeFilter) Select(records []Record) []Record {
	if f.IsEmpty() {
		return records
	}
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		if len(f.Include) > 0 && !matchesAny(rec.ID, f.Include) {
			continue
		}
		if matchesAny(rec.ID, f.Exclude) {
			continue
		}
		if len(f.Types) > 0 {
			ty := ""
			if rec.Artifact != nil {
				ty = string(rec.Artifact.Type)
			}
			if !containsType(f.Types, ty) {
				continue
			}
		}
		out = append(out, rec)
	}
	return out
}

// FetchRecords resolves the caller's effective view from the registry source in
// opts and returns the source-neutral records, dispatching on the §7.5.2 source
// rule: an http(s):// RegistryPath reads the effective view over HTTP with
// opts.Token, every other value reads the local filesystem registry. It is the
// exported entry point `podium publish` uses to read the same view `podium sync`
// materializes, so the two consumers stay byte-equivalent for the same identity.
//
// The §6.4 workspace overlay in opts.OverlayPath applies identically to both
// sources. Scope and toggles are not applied here: FetchRecords returns the full
// effective view, and the caller narrows it (publish intersects each plugin's
// scope filter; sync applies its target scope).
//
// spec: §7.5.2 (source dispatch), §7.8 (publish reads the same view as sync).
func FetchRecords(opts Options) ([]Record, error) {
	if opts.RegistryPath == "" {
		return nil, ErrNoRegistry
	}
	internal, err := resolveRecords(opts)
	if err != nil {
		return nil, err
	}
	out := make([]Record, len(internal))
	for i, rec := range internal {
		out[i] = Record{
			ID:            rec.ID,
			LayerID:       rec.LayerID,
			Artifact:      rec.Artifact,
			ArtifactBytes: rec.ArtifactBytes,
			SkillBytes:    rec.SkillBytes,
			Resources:     rec.Resources,
			ContentHash:   rec.ContentHash,
		}
	}
	return out, nil
}

// Reconcile removes from target every materialized path the prior lock recorded
// that the current render did not write, reusing the §7.5 stale-file cleanup: a
// standalone path is deleted and its empty parents pruned, and a §6.7
// config-merge path (the PodiumOwnedKey JSON manifests, the inject blocks) is
// reconciled in place so the operator's other entries survive. It is the
// exported form of the internal sync cleanup so `podium publish` reconciles a
// marketplace tree on re-render through the same code path `podium sync` uses,
// giving an idempotent re-render and stale-file removal (§7.8 reconciliation).
//
// prior maps each prior materialized path to its config-merge kind ("json",
// "inject", or "" for a standalone file), as PriorMergeKinds returns from a
// lock. current is the set of paths the render wrote this time.
func Reconcile(target string, prior map[string]string, current map[string]bool) {
	removeStalePaths(target, prior, current)
}

// PriorMergeKinds returns the materialized paths a lock recorded, each mapped to
// its §6.7 config-merge kind, for the Reconcile prior argument. A nil lock
// returns an empty map.
func PriorMergeKinds(lock *LockFile) map[string]string {
	return lockMergeKinds(lock)
}

// MergeKindForOp maps an adapter.FileOp to the lock's config-merge kind string
// ("json", "inject", or "" for a standalone write), so a publish caller records
// the same Merge value in its lock that sync does and Reconcile treats the path
// identically on the next render.
func MergeKindForOp(op adapter.FileOp) string {
	return mergeKind(op)
}
