package publish

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// ErrIdentityMismatch signals that the resolved registry credential does not
// authenticate as the output's declared publishing identity (§7.8). Publishing
// fails closed on a mismatch, because the published marketplace reflects the
// authenticated principal's effective view (§4.6): a token whose principal can
// see restricted layers would render them into the output under an identity the
// operator did not intend. Callers assert against it via errors.Is.
var ErrIdentityMismatch = errors.New("publish.identity_mismatch")

// verifyPublishIdentity binds the output's declared publishing identity to the
// resolved registry credential before the render reads the effective view
// (§7.8). The published marketplace reflects the authenticated principal's
// effective view (§4.6), so the principal the token authenticates as must equal
// the declared identity; a public marketplace is published under an identity
// scoped to the artifacts intended for it. The check is fail-closed.
//
// It applies only when both an identity is declared and the source is a server,
// because the principal is the token's subject:
//
//   - An empty identity declares no principal to bind, so the check is a no-op
//     and the token alone governs visibility.
//   - A filesystem source has no authenticated principal: it resolves the local
//     view directly with no token, so a declared identity is purely documentary
//     and the check is a no-op.
//   - A server source with no token would reach the registry anonymously, so a
//     declared identity cannot be honored; the render must not silently publish
//     the anonymous (public) view under the declared identity, so it fails
//     closed.
//   - A server source with a token must authenticate as the declared identity:
//     the token's sub or email claim equals identity, else it fails closed.
func verifyPublishIdentity(registry, identity, token string) error {
	if identity == "" || !sync.IsServerSource(registry) {
		return nil
	}
	if token == "" {
		return fmt.Errorf("%w: identity %q is declared but no registry credential resolved; set PODIUM_TOKEN to the credential for that principal",
			ErrIdentityMismatch, identity)
	}
	sub, email, err := tokenPrincipal(token)
	if err != nil {
		return fmt.Errorf("%w: identity %q is declared but the registry credential is not a decodable token: %v",
			ErrIdentityMismatch, identity, err)
	}
	if identity == sub || identity == email {
		return nil
	}
	return fmt.Errorf("%w: identity %q does not match the credential principal (sub=%q email=%q); the token would publish that principal's effective view",
		ErrIdentityMismatch, identity, sub, email)
}

// tokenPrincipal decodes the sub and email claims from a JWT credential's
// payload without verifying its signature: the registry verifies the token on
// every call (§6.3.2), so the publish-side check binds the declared identity to
// the claims the verified token carries rather than re-validating the signature
// client-side. It mirrors the claim decode `podium login` prints the resolved
// identity with (cmd/podium/login.go decodeIdentity). A token that is not a JWT,
// or one carrying neither claim, returns an error so the caller fails closed.
func tokenPrincipal(token string) (sub, email string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", "", errors.New("not a JWT (expected a header.payload.signature triple)")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", fmt.Errorf("decode payload: %w", err)
		}
	}
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", fmt.Errorf("parse claims: %w", err)
	}
	if claims.Sub == "" && claims.Email == "" {
		return "", "", errors.New("token carries neither a sub nor an email claim")
	}
	return claims.Sub, claims.Email, nil
}

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
	// Bind the configured publishing identity to the resolved credential before
	// any fetch, so a mismatch fails closed without reading the effective view.
	if err := verifyPublishIdentity(opts.Registry, opts.Identity, opts.Token); err != nil {
		return nil, fmt.Errorf("publish %q: %w", opts.OutputID, err)
	}

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

// removedOwner is the change-set owner attributed to a path present in the
// checkout before the render but absent after it (a stale file the cleanup
// removed), so a pure removal still reports a non-empty change.
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
	prior, _ := sync.ReadLock(workdir)
	priorMerge := sync.PriorMergeKinds(prior)

	files := make([]adapter.File, len(rendered))
	currentPaths := map[string]bool{}
	merge := map[string]string{}
	owner := map[string]string{}
	for i, rf := range rendered {
		f := rf.file
		files[i] = f
		currentPaths[f.Path] = true
		merge[f.Path] = sync.MergeKindForOp(f.Op)
		owner[f.Path] = rf.ownerID
	}

	// The change set diffs against the checkout, so capture the on-disk digest
	// of every path the render touches (the rendered paths plus the prior-render
	// paths that the cleanup may remove) before any write mutates the tree.
	diffPaths := unionPaths(currentPaths, priorMerge)
	beforeDigests, err := onDiskDigests(workdir, diffPaths)
	if err != nil {
		return nil, fmt.Errorf("publish %q: read checkout before render: %w", outputID, err)
	}

	if err := materialize.Write(workdir, files); err != nil {
		return nil, fmt.Errorf("publish %q: write render: %w", outputID, err)
	}

	sync.Reconcile(workdir, priorMerge, currentPaths)

	if err := writeLock(workdir, currentPaths, merge); err != nil {
		return nil, err
	}

	// The pre-write read of the same paths succeeded and materialize.Write left
	// every rendered path a readable regular file, so this read fails only on a
	// concurrent filesystem mutation; the guard surfaces that rather than
	// reporting a change set against unobservable state.
	afterDigests, err := onDiskDigests(workdir, diffPaths)
	if err != nil {
		return nil, fmt.Errorf("publish %q: read checkout after render: %w", outputID, err)
	}

	res := &RenderResult{OutputID: outputID, Files: sortedKeys(currentPaths)}
	res.Changed, res.ChangedArtifacts = changeSet(beforeDigests, afterDigests, owner)
	return res, nil
}

// onDiskDigests reads each path's bytes from workdir and returns its hex SHA-256,
// keyed by the relative path. A path absent from disk is omitted, so a path that
// did not exist before the render or was removed by the cleanup contributes no
// entry, and the change-set comparison treats it as added or removed. A read
// error other than "not present" is returned, because it means the checkout
// state could not be observed.
func onDiskDigests(workdir string, paths map[string]bool) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	for p := range paths {
		data, err := os.ReadFile(filepath.Join(workdir, filepath.FromSlash(p)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		out[p] = digest(data)
	}
	return out, nil
}

// digest returns the hex SHA-256 of content, the per-path content fingerprint the
// change set compares. It is computed over the bytes on disk in the checkout,
// including the merged result for a config-merge path, so a config-merge write
// that leaves the file byte-identical reports no change.
func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// unionPaths returns the set of relative paths to diff for change detection: the
// paths this render wrote, plus the prior-render paths the cleanup may remove, so
// a pure removal is observed as a change against the checkout.
func unionPaths(current map[string]bool, priorMerge map[string]string) map[string]bool {
	out := make(map[string]bool, len(current)+len(priorMerge))
	for p := range current {
		out[p] = true
	}
	for p := range priorMerge {
		out[p] = true
	}
	return out
}

// writeLock persists the render's lock for the next run's reconciliation: one
// LockArtifact per written path recording its config-merge kind in Merge. The
// next render reads it back through sync.PriorMergeKinds so the cleanup treats a
// config-merge path (reconcile Podium's entries) differently from a standalone
// path (remove). Change detection reads the checkout content directly rather than
// the lock, so the lock carries no per-file content fingerprint and leaves the
// ContentHash field, which holds the sha256:<hex> artifact content hash
// everywhere else in pkg/sync, unset.
func writeLock(workdir string, paths map[string]bool, merge map[string]string) error {
	lock := &sync.LockFile{Target: workdir}
	for _, p := range sortedKeys(paths) {
		lock.Artifacts = append(lock.Artifacts, sync.LockArtifact{
			MaterializedPath: p,
			Merge:            merge[p],
		})
	}
	if err := sync.WriteLock(workdir, lock); err != nil {
		return fmt.Errorf("publish: write lock: %w", err)
	}
	return nil
}

// changeSet diffs the checkout digests captured before and after the render and
// returns whether the working tree changed and the sorted canonical IDs of the
// changed artifacts. A path is changed when its on-disk content differs, when it
// is newly present (absent before, present after), or when it is gone (present
// before, absent after). The owner map carries each rendered path's owning
// artifact ID (or the manifest marker for a shared manifest); a path that
// disappeared is attributed to the removed marker, because no rendered file owns
// it.
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
