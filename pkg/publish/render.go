package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/sync"
)

// This file implements the §7.8 render phase of `podium publish`: it resolves
// the publishing identity's effective view by reusing the pkg/sync record-fetch
// path, intersects it with each plugin's scope filter, assigns each selected
// artifact to its plugin in declaration order, selects the per-harness §7.8
// emitter, renders each harness's marketplace tree, and writes it into the
// output working directory through materialize.Write. Reconciliation reuses the
// sync lock file plus the PodiumOwnedKey/inject stale-file cleanup so a
// re-render is idempotent and drops files for artifacts that left the view.

// RenderOptions are the inputs to Render. Registry and Identity come from the
// resolved marketplace output (the §7.8 publishing identity whose effective view
// the render reflects); Workdir is the per-output checkout the render writes into
// (the directory the prepare phase placed a clone at). Harnesses and Plugins are
// the output's harness set and plugin list. Token is the publishing identity's
// registry credential, attached on every server-source request so the render
// reflects that identity's effective view (§4.6). HTTPClient is injected by
// tests; a nil client uses the pkg/sync default.
type RenderOptions struct {
	OutputID   string
	Registry   string
	Identity   string
	Token      string
	Workdir    string
	Harnesses  []string
	Plugins    []PluginFilter
	HTTPClient *http.Client
}

// RenderResult describes one render. Changed reports whether the render wrote a
// tree that differs from the prior render (the §7.8 $PODIUM_CHANGED signal).
// ChangedArtifacts lists the canonical IDs whose materialized output changed or
// was removed since the prior render, the body of the $PODIUM_CHANGE_SUMMARY
// JSON file. Files is the full set of relative paths the render wrote, sorted.
type RenderResult struct {
	OutputID         string
	Changed          bool
	ChangedArtifacts []string
	Files            []string
}

// ErrNoEmitter signals that a harness in the output's harness set has no §7.8
// marketplace emitter. Resolve already rejects such a harness at config
// validation, so Render returning it indicates a caller bypassed validation.
type ErrNoEmitter struct {
	Harness string
	Err     error
}

func (e *ErrNoEmitter) Error() string {
	return fmt.Sprintf("publish: harness %q has no marketplace emitter: %v", e.Harness, e.Err)
}

func (e *ErrNoEmitter) Unwrap() error { return e.Err }

// assigned pairs a record with the plugin it was assigned to. A record matches
// at most one plugin (the first in declaration order whose scope filter selects
// it), so the publishing pipeline renders an artifact into exactly one plugin
// per harness (§7.8 "evaluating the plugin filters in declaration order").
type assigned struct {
	record sync.Record
	plugin PluginFilter
}

// Render runs the §7.8 render phase for one marketplace output. It fetches the
// publishing identity's effective view, assigns each selected artifact to its
// plugin, renders every harness in the harness set into opts.Workdir, and
// reconciles the result against the prior render through the sync lock file.
//
// The harness set may name several Claude surfaces; they share one emitter
// (adapter.EmitterForHarness), so the rendered tree carries one Claude
// marketplace rather than a collision. A record selected by no plugin is omitted
// from the output.
//
// spec: §7.8 (render pipeline), §4.6 (effective view), §7.5.1 (scope filters).
func Render(ctx context.Context, opts RenderOptions) (*RenderResult, error) {
	records, err := sync.FetchRecords(sync.Options{
		RegistryPath: opts.Registry,
		Token:        opts.Token,
		HTTPClient:   opts.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("publish %q: fetch effective view: %w", opts.OutputID, err)
	}

	assignments := assignPlugins(records, opts.Plugins)

	// Resolve a distinct emitter per harness. The Claude surfaces collapse to
	// one emitter keyed by its ID ("claude"), so a harness set naming more than
	// one of them renders one Claude marketplace.
	emitters, err := resolveEmitters(opts.Harnesses)
	if err != nil {
		return nil, err
	}

	rendered, err := renderAll(ctx, emitters, assignments, opts.OutputID)
	if err != nil {
		return nil, err
	}

	return reconcile(opts.Workdir, opts.OutputID, rendered)
}

// renderedFile pairs an emitted file with the canonical artifact ID that
// produced it. A shared manifest entry (a root marketplace.json fragment) has no
// single artifact owner; ownerID is the "(manifest)" marker for it, so a
// manifest-only change is still reported in the change set.
type renderedFile struct {
	file    adapter.File
	ownerID string
}

// manifestOwner is the change-set owner attributed to a shared manifest entry,
// which no single artifact owns.
const manifestOwner = "(manifest)"

// removedOwner is the change-set owner attributed to a path a prior render wrote
// that this render did not, so a pure removal still reports a non-empty change.
const removedOwner = "(removed)"

// assignPlugins intersects the effective view with the plugin scope filters and
// assigns each selected record to the first plugin (in declaration order) whose
// filter selects it (§7.8). A record selected by no plugin is dropped. The
// result preserves the input record order within each plugin so the render is
// deterministic.
func assignPlugins(records []sync.Record, plugins []PluginFilter) []assigned {
	out := make([]assigned, 0, len(records))
	for _, rec := range records {
		for _, p := range plugins {
			if pluginSelects(p, rec) {
				out = append(out, assigned{record: rec, plugin: p})
				break
			}
		}
	}
	return out
}

// pluginSelects reports whether the plugin's scope filter selects rec. It reuses
// the sync scope-filter machinery (PluginFilter.ScopeFilter) so plugin selection
// and sync selection apply identical §7.5.1 glob semantics. An empty filter
// selects every record, matching ScopeFilter.IsEmpty.
func pluginSelects(p PluginFilter, rec sync.Record) bool {
	return len(p.ScopeFilter().Select([]sync.Record{rec})) == 1
}

// resolveEmitters maps the harness set to its distinct emitters, keyed by the
// emitter ID so harnesses that share a format (the Claude surfaces) resolve to
// one emitter. The returned map keys the emitter ID, and the subtree prefix is
// the emitter ID, so each harness's plugin content lives under
// <emitter-id>/<plugin>/... in the output repository.
func resolveEmitters(harnesses []string) (map[string]adapter.MarketplaceEmitter, error) {
	out := map[string]adapter.MarketplaceEmitter{}
	for _, h := range harnesses {
		e, err := adapter.EmitterForHarness(h)
		if err != nil {
			return nil, &ErrNoEmitter{Harness: h, Err: err}
		}
		out[e.ID()] = e
	}
	return out, nil
}

// renderAll renders every emitter's marketplace tree for the assignments and
// returns the merged file set. Per emitter it renders each artifact's component
// files (once per artifact) and each plugin's manifest entry (once per plugin),
// so an N-artifact plugin yields one manifest entry rather than N duplicates
// (§7.8). marketplaceName is the output identifier the manifest carries.
func renderAll(ctx context.Context, emitters map[string]adapter.MarketplaceEmitter, assignments []assigned, marketplaceName string) ([]renderedFile, error) {
	// Emitter IDs in deterministic order so the merged file set is stable.
	ids := make([]string, 0, len(emitters))
	for id := range emitters {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var all []renderedFile
	for _, id := range ids {
		emitter := emitters[id]
		files, err := renderEmitter(ctx, emitter, assignments, marketplaceName)
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
	}
	return all, nil
}

// renderEmitter renders one emitter's tree: each artifact's component files
// under its plugin subtree, then each plugin's manifest entry once. The plugin
// descriptor carries the emitter ID as the harness subtree prefix. Each emitted
// file carries its owner: a component file is owned by its source artifact, and a
// manifest fragment is the shared manifestOwner.
func renderEmitter(ctx context.Context, emitter adapter.MarketplaceEmitter, assignments []assigned, marketplaceName string) ([]renderedFile, error) {
	var files []renderedFile
	seenPlugin := map[string]bool{}
	// Components, one render per assigned artifact.
	for _, a := range assignments {
		desc := adapter.PluginDescriptor{Name: a.plugin.Name, Prefix: emitter.ID()}
		out, err := emitter.Component(ctx, adapter.Source{
			ArtifactID:    a.record.ID,
			ArtifactBytes: a.record.ArtifactBytes,
			SkillBytes:    a.record.SkillBytes,
			Resources:     a.record.Resources,
			Plugin:        desc,
		})
		if err != nil {
			return nil, fmt.Errorf("publish: emitter %q component for %s: %w", emitter.ID(), a.record.ID, err)
		}
		for _, f := range out {
			files = append(files, renderedFile{file: f, ownerID: a.record.ID})
		}
		seenPlugin[a.plugin.Name] = true
	}
	// Manifests, one render per plugin that contributed at least one artifact,
	// in declaration order so the listing is deterministic.
	for _, p := range pluginsInOrder(assignments) {
		if !seenPlugin[p.Name] {
			continue
		}
		desc := adapter.PluginDescriptor{Name: p.Name, Prefix: emitter.ID()}
		out, err := emitter.Manifest(marketplaceName, desc)
		if err != nil {
			return nil, fmt.Errorf("publish: emitter %q manifest for plugin %q: %w", emitter.ID(), p.Name, err)
		}
		for _, f := range out {
			files = append(files, renderedFile{file: f, ownerID: manifestOwner})
		}
	}
	return files, nil
}

// pluginsInOrder returns the distinct plugins the assignments reference, in the
// order they first appear. Assignments preserve the plugin declaration order
// (assignPlugins evaluates plugins in declaration order), so the result is the
// declaration order restricted to the plugins with at least one artifact.
func pluginsInOrder(assignments []assigned) []PluginFilter {
	var out []PluginFilter
	seen := map[string]bool{}
	for _, a := range assignments {
		if seen[a.plugin.Name] {
			continue
		}
		seen[a.plugin.Name] = true
		out = append(out, a.plugin)
	}
	return out
}

// reconcile writes the rendered file set into workdir, removes the files a prior
// render wrote that this render did not, and persists the new lock for the next
// run. It computes the change set by comparing the per-path content digests of
// this render against the prior lock, so $PODIUM_CHANGED is true when any path's
// content differs, a path was added, or a prior path is gone (§7.8).
func reconcile(workdir, outputID string, rendered []renderedFile) (*RenderResult, error) {
	prior, _ := sync.ReadLock(workdir)
	priorDigests := lockDigests(prior)

	files := make([]adapter.File, len(rendered))
	for i, rf := range rendered {
		files[i] = rf.file
	}

	if err := materialize.Write(workdir, files); err != nil {
		return nil, fmt.Errorf("publish %q: write render: %w", outputID, err)
	}

	currentPaths := map[string]bool{}
	currentDigests := map[string]string{}
	merge := map[string]string{}
	owner := map[string]string{}
	for _, rf := range rendered {
		f := rf.file
		currentPaths[f.Path] = true
		// Several fragments may target one config-merge path (a hook event's
		// handler list, a manifest's plugin array); fold their digests so the
		// path's fingerprint accounts for every contribution.
		currentDigests[f.Path] = digest([]byte(currentDigests[f.Path] + digest(f.Content)))
		merge[f.Path] = sync.MergeKindForOp(f.Op)
		owner[f.Path] = rf.ownerID
	}

	sync.Reconcile(workdir, sync.PriorMergeKinds(prior), currentPaths)

	if err := writeLock(workdir, currentDigests, merge); err != nil {
		return nil, err
	}

	res := &RenderResult{OutputID: outputID, Files: sortedKeys(currentPaths)}
	res.Changed, res.ChangedArtifacts = changeSet(priorDigests, currentDigests, owner)
	return res, nil
}

// digest returns the hex SHA-256 of content, the per-path content fingerprint
// the change set compares. A config-merge file's on-disk content is the merged
// result rather than the fragment bytes, so the digest of the emitted fragment
// is a stable proxy: the same fragment merges to the same result against the
// same prior, and a changed fragment changes the digest.
func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// lockDigests reads the per-path content digests a prior render stored in the
// lock (in the ContentHash field of each LockArtifact, keyed by MaterializedPath).
// A nil lock returns an empty map, so the first render reports every path as
// added and Changed is true.
func lockDigests(lock *sync.LockFile) map[string]string {
	out := map[string]string{}
	if lock == nil {
		return out
	}
	for _, a := range lock.Artifacts {
		if a.MaterializedPath != "" {
			out[a.MaterializedPath] = a.ContentHash
		}
	}
	return out
}

// writeLock persists the render's lock: one LockArtifact per written path,
// recording the path's content digest in ContentHash and its config-merge kind
// in Merge. The next render reads it back through sync.ReadLock for
// reconciliation and change detection.
func writeLock(workdir string, digests, merge map[string]string) error {
	lock := &sync.LockFile{Target: workdir}
	for _, p := range sortedKeys(mapKeys(digests)) {
		lock.Artifacts = append(lock.Artifacts, sync.LockArtifact{
			MaterializedPath: p,
			ContentHash:      digests[p],
			Merge:            merge[p],
		})
	}
	if err := sync.WriteLock(workdir, lock); err != nil {
		return fmt.Errorf("publish: write lock: %w", err)
	}
	return nil
}

// changeSet compares the prior and current per-path digests and returns whether
// the render changed and the sorted canonical IDs of the changed artifacts. An
// artifact is changed when any of its files was added, removed, or has a
// different digest. The owner map carries each current path's owning artifact ID
// (or the manifest marker for a shared manifest); a path the prior render wrote
// that this render did not is attributed to the removed marker.
func changeSet(prior, current, owner map[string]string) (bool, []string) {
	ids := map[string]bool{}
	for p, d := range current {
		if prior[p] != d {
			ids[owner[p]] = true
		}
	}
	for p := range prior {
		if _, ok := current[p]; !ok {
			ids[removedOwner] = true
		}
	}
	if len(ids) == 0 {
		return false, nil
	}
	return true, sortedSet(ids)
}

// ChangeSummaryJSON renders the change set as the $PODIUM_CHANGE_SUMMARY file
// body (§7.8): a JSON object with the output ID, whether the render changed, the
// count, and the changed artifact identifiers. The publish CLI writes this to a
// temp file and passes its path to the workflow commands.
func (r *RenderResult) ChangeSummaryJSON() []byte {
	body := map[string]any{
		"output":  r.OutputID,
		"changed": r.Changed,
		"count":   len(r.ChangedArtifacts),
		"artifacts": func() []string {
			if r.ChangedArtifacts == nil {
				return []string{}
			}
			return r.ChangedArtifacts
		}(),
	}
	b, _ := json.MarshalIndent(body, "", "  ")
	return append(b, '\n')
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSet(m map[string]bool) []string {
	return sortedKeys(m)
}

func mapKeys(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
