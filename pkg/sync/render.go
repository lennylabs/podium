package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/materialize"
)

// This file implements the §7.8 render phase of a kind: marketplace target: it
// resolves the publishing identity's effective view by reusing the FetchRecords
// record-fetch path, intersects it with each plugin's scope filter, assigns each
// selected artifact to its plugin in declaration order, selects the per-harness
// §7.8 emitter, renders each harness's marketplace tree, and writes it into the
// output working directory through materialize.Write. Reconciliation reuses the
// sync lock file plus the PodiumOwnedKey/inject stale-file cleanup so a
// re-render is idempotent and drops files for artifacts that left the view.

// RenderOptions are the inputs to Render. Registry comes from the resolved
// marketplace output. Identity is the documentary record of the principal the
// output publishes as (§7.8 "the effective-view principal the render runs as");
// the render does not read it, because the token carries the publishing identity.
// Workdir is the per-output checkout the render writes into (the directory the
// prepare phase placed a clone at). Harnesses and Plugins are the output's
// harness set and plugin list. Token is the publishing identity's registry
// credential, attached on every server-source request so the render reflects
// that identity's effective view (§4.6). HTTPClient is injected by tests; a nil
// client uses the default.
type RenderOptions struct {
	OutputID   string
	Registry   string
	Identity   string
	Token      string
	Workdir    string
	Harnesses  []string
	Plugins    []PluginFilter
	HTTPClient *http.Client
	// Delivery resolves the §4.7.10 delivery check of a server-source render.
	Delivery DeliveryCheckFunc
	// Attestation resolves the §4.7.11 consumer trust policy of the
	// render, the policy of the podium sync process (§7.8 Author
	// attestation).
	Attestation AttestationFunc
}

// RenderResult describes one render. Changed reports whether the render produced
// a diff against the checkout content already present in the working directory
// (the §7.8 $PODIUM_CHANGED signal). ChangedArtifacts lists the canonical IDs
// whose materialized output differs from the checkout, the body of the
// $PODIUM_CHANGE_SUMMARY JSON file. Files is the full set of relative paths the
// render wrote, sorted.
type RenderResult struct {
	OutputID         string
	Changed          bool
	ChangedArtifacts []string
	Files            []string
	// Dropped lists the artifacts the filesystem-source composition dropped
	// under the §4.6 collision rule, the §4.1 unparseable-manifest rule, the
	// §4.6 no-inheritance rule, or the §4.1 removed-type rule (§13.11.3). The render omits them; callers report
	// each entry with its Code and Reason.
	Dropped []layer.Drop
	// Warnings are the §4.7.11 warning texts of the records the render
	// rendered under warn, which callers write on standard error (§7.8).
	Warnings []string
}

// ErrNoEmitter signals that a harness in the output's harness set has no §7.8
// marketplace emitter. ValidateOutput already rejects such a harness at config
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
	record Record
	plugin PluginFilter
}

// Render runs the §7.8 render phase for one marketplace output. It fetches the
// effective view the registry credential authenticates (§4.6), assigns each
// selected artifact to its plugin, renders every harness in the harness set into
// opts.Workdir, and reconciles the result against the prior render through the
// sync lock file.
//
// The render reflects the authenticated token's effective view. opts.Identity is
// the documentary record of the principal the operator publishes as (§7.8
// "the effective-view principal the render runs as"); the token carries the
// publishing identity, so Render does not read opts.Identity.
//
// The harness set may name several Claude surfaces; they share one emitter
// (adapter.EmitterForHarness), so the rendered tree carries one Claude
// marketplace rather than a collision. A record selected by no plugin is omitted
// from the output.
//
// spec: §7.8 (render pipeline), §4.6 (effective view), §7.5.1 (scope filters).
func Render(ctx context.Context, opts RenderOptions) (*RenderResult, error) {
	view, err := fetchView(Options{
		RegistryPath: opts.Registry,
		Token:        opts.Token,
		HTTPClient:   opts.HTTPClient,
		Delivery:     opts.Delivery,
		Attestation:  opts.Attestation,
	})
	if err != nil {
		return nil, fmt.Errorf("publish %q: fetch effective view: %w", opts.OutputID, err)
	}
	records := make([]Record, len(view.records))
	for i, rec := range view.records {
		records[i] = rec.export()
	}

	assignments := assignPlugins(records, opts.Plugins)
	// Spec: §7.8 (Author attestation) — the check of every record a plugin
	// selects decides the target before the render writes a file.
	warnings, err := checkAssigned(ctx, view.records, assignments)
	if err != nil {
		return nil, fmt.Errorf("publish %q: %w", opts.OutputID, err)
	}

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

	res, err := reconcile(opts.Workdir, opts.OutputID, rendered)
	if err != nil {
		return nil, err
	}
	res.Dropped = view.dropped
	res.Warnings = append(view.warnings, warnings...)
	return res, nil
}

// checkAssigned runs the §4.7.11 attestation check on each record a plugin
// selected, once per ID. A record that the check refuses under require
// fails the render, and a failure under warn is a warning. Records that no
// plugin selects are not checked, so they cannot fail the render.
//
// Spec: §7.8 (Author attestation)
func checkAssigned(ctx context.Context, all []materialRecord, assignments []assigned) ([]string, error) {
	byID := make(map[string]materialRecord, len(all))
	for _, rec := range all {
		byID[rec.ID] = rec
	}
	var warnings []string
	checked := map[string]bool{}
	for _, a := range assignments {
		rec := byID[a.record.ID]
		if checked[rec.ID] || rec.check == nil {
			continue
		}
		checked[rec.ID] = true
		r, err := rec.check(ctx)
		if err != nil {
			return nil, &RecordRefusedError{ID: rec.ID, Err: err}
		}
		for _, w := range r.Warnings {
			warnings = append(warnings, w.WarningText(rec.ID))
		}
	}
	return warnings, nil
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

// removedOwner is the change-set owner attributed to a path present in the
// checkout before the render but absent after it (a stale file the cleanup
// removed), so a pure removal still reports a non-empty change.
const removedOwner = "(removed)"

// assignPlugins intersects the effective view with the plugin scope filters and
// assigns each selected record to the first plugin (in declaration order) whose
// filter selects it (§7.8). A record selected by no plugin is dropped. The
// result preserves the input record order within each plugin so the render is
// deterministic.
func assignPlugins(records []Record, plugins []PluginFilter) []assigned {
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
func pluginSelects(p PluginFilter, rec Record) bool {
	return len(p.ScopeFilter().Select([]Record{rec})) == 1
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
		desc := adapter.PluginDescriptor{Name: a.plugin.Name, Description: a.plugin.Description, Prefix: emitter.ID()}
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
		desc := adapter.PluginDescriptor{Name: p.Name, Description: p.Description, Prefix: emitter.ID()}
		out, err := emitter.Manifest(ctx, marketplaceName, desc)
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
// run. It computes the change set by diffing the render against the checkout
// content already on disk in workdir: the per-path content digest is read from
// the bytes on disk before the write and compared against the bytes on disk
// after the write and the stale-file cleanup, so $PODIUM_CHANGED is true exactly
// when the render altered the working tree (§7.8 "the render produced a diff
// against the checkout"). This holds for a fresh actions/checkout that carries no
// prior-render lock: an unchanged tree reports Changed=false even though no lock
// is present, which the lock-based comparison could not do.
func reconcile(workdir, outputID string, rendered []renderedFile) (*RenderResult, error) {
	// The prior lock is committed to the marketplace repository, so the
	// §7.5.3 recorded-path rule drops every entry the cleanup must not touch
	// before the comparison or the cleanup reads it. A committed .podium link
	// fails the render here, before anything is written.
	prior, err := readPriorLock(workdir)
	if errors.Is(err, ErrStateDirUnusable) {
		return nil, fmt.Errorf("publish %q: %w", outputID, err)
	}
	priorPaths := PriorPaths(dropUnsafeLockPaths(prior))

	files := make([]adapter.File, 0, len(rendered))
	currentPaths := map[string]bool{}
	merge := map[string]string{}
	owner := map[string]string{}
	for _, rf := range rendered {
		f := rf.file
		// §7.5.3: a path the next render would refuse to read back from the
		// lock is neither written nor recorded.
		if !recordable(rf.ownerID, f.Path) {
			continue
		}
		files = append(files, f)
		currentPaths[f.Path] = true
		merge[f.Path] = MergeKindForOp(f.Op)
		owner[f.Path] = rf.ownerID
	}

	// The change set diffs against the checkout, so capture the on-disk digest
	// of every path the render touches (the rendered paths plus the prior-render
	// paths that the cleanup may remove) before any write mutates the tree. A
	// prior-only path comes from the committed lock, so it is read under the
	// cleanup's rule, and a rendered path is read only when it is a regular
	// file. A path that cannot be read counts as altered (§7.5.2) rather than
	// failing the render: a rendered path unreadable before the write has no
	// starting digest, so the comparison reports it under its owner, and an
	// unreadable prior-only path is reported under removedOwner below.
	priorOnly := priorOnlyPaths(currentPaths, priorPaths)
	beforeDigests, _ := tolerantDigests(currentPaths, readRecorded(workdir))
	beforePrior, unobservedBefore := tolerantDigests(priorOnly, readRecorded(workdir))

	// §7.5.3, §7.8: a stale file the cleanup would keep fails the render
	// before anything is written. Publishing would push a tree that still
	// carries an artifact that left the view, and since the lock is not
	// compared (§7.5.2) the publish phase could skip the commit, so every
	// later render would keep the file again. Failing before the write leaves
	// a reused checkout with its tree and lock, so the run after the remedy
	// sees the whole catalog change.
	if left := staleRefusals(workdir, priorPaths, currentPaths); len(left) > 0 {
		return nil, staleFilesKeptError(outputID, left)
	}

	if err := materialize.Write(workdir, files); err != nil {
		return nil, fmt.Errorf("publish %q: write render: %w", outputID, err)
	}

	// A removal can still fail after the write, for example on a read-only
	// directory. The render then fails without rewriting the lock, so the
	// entry stays recorded.
	if left := Reconcile(workdir, priorPaths, currentPaths); len(left) > 0 {
		return nil, staleFilesKeptError(outputID, left)
	}

	// materialize.Write left every rendered path a readable regular file, so
	// this read fails only on a concurrent filesystem mutation; the guard
	// surfaces that rather than reporting a change set against unobservable
	// state. The digests are the §7.5.3 file hashes the lock records, so they
	// are read before the lock is written.
	afterDigests, err := onDiskDigests(workdir, currentPaths)
	if err != nil {
		return nil, fmt.Errorf("publish %q: read checkout after render: %w", outputID, err)
	}

	if err := writeLock(workdir, currentPaths, merge, afterDigests); err != nil {
		return nil, err
	}

	afterPrior, unobservedAfter := tolerantDigests(priorOnly, readRecorded(workdir))

	res := &RenderResult{OutputID: outputID, Files: sortedKeys(currentPaths)}
	res.Changed, res.ChangedArtifacts = changeSet(mergeDigests(beforeDigests, beforePrior), mergeDigests(afterDigests, afterPrior), owner)
	if unobservedBefore || unobservedAfter {
		res.Changed, res.ChangedArtifacts = true, mergeOwner(res.ChangedArtifacts, removedOwner)
	}
	return res, nil
}

// mergeOwner returns the sorted change-set owners with owner added once. An
// unreadable prior-only path is attributed to removedOwner, the owner of a
// path no rendered file owns.
func mergeOwner(ids []string, owner string) []string {
	set := map[string]bool{owner: true}
	for _, id := range ids {
		set[id] = true
	}
	return sortedSet(set)
}

// onDiskDigests reads each path's bytes from workdir and returns its hex SHA-256,
// keyed by the relative path. A path absent from disk is omitted, so a path that
// did not exist before materialization or was removed by the cleanup contributes
// no entry, and the change-set comparison treats it as added or removed. A read
// error other than "not present" is returned, because it means the state of the
// checkout or target directory could not be observed. The read goes through
// readRecorded, so a link at the path is followed only inside the target.
func onDiskDigests(workdir string, paths map[string]bool) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	read := readRecorded(workdir)
	for p := range paths {
		data, err := read(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			// Both callers read the paths the run just wrote as regular
			// files, so this branch runs only on a concurrent change to the
			// target, which the tests cannot stage deterministically.
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		out[p] = digest(data)
	}
	return out, nil
}

// digest returns the per-path content fingerprint the change set compares, the
// §7.5.3 file hash of content (materialize.FileHash). It is computed over the
// bytes on disk in the checkout, including the merged result for a
// config-merge path, so a config-merge write that leaves the file
// byte-identical reports no change. Because the value is the file hash, the
// digest of a standalone path read after the write is what the lock records
// as its file_hash.
func digest(content []byte) string {
	return materialize.FileHash(content)
}

// writeLock persists the render's lock for the next run's reconciliation: one
// LockArtifact per written path recording its config-merge kind in Merge and,
// for a standalone path, the file hash of the bytes the render left on disk in
// FileHash, taken from hashes. The next render reads it back through
// PriorPaths, so the cleanup treats a config-merge path (reconcile Podium's
// entries) differently from a standalone path (remove when the bytes still
// match). Change detection reads the checkout content directly rather than the
// lock, and the ContentHash field, which holds the sha256:<hex> artifact
// content hash everywhere else in pkg/sync, stays unset.
//
// Spec: §7.5.3, §7.8
func writeLock(workdir string, paths map[string]bool, merge, hashes map[string]string) error {
	lock := &LockFile{Target: workdir}
	for _, p := range sortedKeys(paths) {
		lock.Artifacts = append(lock.Artifacts, LockArtifact{
			MaterializedPath: p,
			Merge:            merge[p],
			FileHash:         wholeFileHash(merge[p], hashes[p]),
		})
	}
	if err := WriteLock(workdir, lock); err != nil {
		return fmt.Errorf("publish: write lock: %w", err)
	}
	return nil
}

// changeSet diffs the digests of a marketplace checkout or a `kind: workspace`
// target directory captured before and after materialization and returns whether
// the working tree changed and the sorted canonical IDs of the changed artifacts.
// A path is changed when its on-disk content differs, when it is newly present
// (absent before, present after), or when it is gone (present before, absent
// after). The owner map carries each rendered path's owning artifact ID (or the
// manifest marker for a shared manifest); a path that disappeared is attributed
// to the removed marker, because no rendered file owns it. A nil owner map is
// valid for a caller that reads only the boolean; the returned IDs are then
// meaningless.
func changeSet(before, after, owner map[string]string) (bool, []string) {
	ids := map[string]bool{}
	for p, d := range after {
		if before[p] != d {
			ids[owner[p]] = true
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
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
// count, and the changed artifact identifiers. The marketplace runner writes this
// to a temp file and passes its path to the workflow commands.
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
