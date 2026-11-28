package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/materialize"
)

// ArtifactRecord is one artifact discovered under a layer's tree, with its
// canonical ID, the parsed Artifact (and Skill, when applicable), the
// containing layer, and the bytes of every file in the package.
type ArtifactRecord struct {
	ID            string
	Layer         Layer
	Artifact      *manifest.Artifact
	Skill         *manifest.Skill
	ArtifactBytes []byte
	SkillBytes    []byte
	Resources     map[string][]byte
	// AuthoredBytes is ARTIFACT.md as read from disk. ArtifactBytes and
	// AuthoredBytes differ only for a skill with no ARTIFACT.md, where Walk
	// sets ArtifactBytes to the §4.3.4 synthesized document and AuthoredBytes
	// stays nil. Materialization reads ArtifactBytes; the §4.7.6 digest and
	// the §4.6 no-inheritance and §4.1 removed-type checks read AuthoredBytes,
	// because the digest frames a zero-length ARTIFACT.md for a plain skill.
	// The store's ManifestRecord.Frontmatter carries the same bytes on the
	// registry side.
	AuthoredBytes []byte
	// VersionAssigned reports that the skill declares no usable version
	// (§4.3.4), so the version is assigned.
	VersionAssigned bool
	// AuthoredVersion is the version the package's documents declare, which
	// the §4.7.6 subject name carries when it is in major.minor.patch form:
	// the ARTIFACT.md version, or for a skill the version §4.3.4 resolves. It
	// is empty for an unresolved package and for a skill that declares no
	// version, and unlike Artifact.Version it never holds an assigned version.
	AuthoredVersion string
	// PackageDigest is the §4.7.6 package digest of the package, written
	// sha256:<hex>. Both walks set it on every record, an unparseable one
	// included, so the layer digest lists every package discovery finds.
	PackageDigest string
	// Fields are the §4.7.6 resolved fields the package digest frames. They
	// are the zero value for an unresolved package, which Unparseable marks.
	Fields manifest.ResolvedFields
	// FileSums holds the SHA-256 digest of each file of the package under
	// its slash path relative to the package root, the files the package
	// digest was computed from. `podium attest check --commit` compares
	// them between a working tree and a commit.
	FileSums map[string]attest.Sum
	// entryPathErr is the attest.CheckEntryPath error of a package that holds
	// a file whose path is not valid UTF-8, which leaves it with no subject
	// name. NewPackageRecord sets it.
	entryPathErr error
	// Unparseable is the §4.1 unparseable-manifest reason of an unresolved
	// package: one whose ARTIFACT.md or SKILL.md frontmatter does not parse,
	// or whose ARTIFACT.md declares type: skill beside no SKILL.md. It is
	// empty for every other record. A record that carries it holds only ID,
	// Layer, and PackageDigest. Walk and registry ingest refuse it through
	// Refusal before any rule that reads the parsed fields, so Walk never
	// returns one.
	Unparseable string
}

// UnparseableRecord returns the record of package id in layer l whose
// manifest file, ARTIFACT.md or SKILL.md, does not parse with err. The walk
// keeps the record so the refusal reaches the caller through the same
// channel as the other pre-lint refusals, and so the package still claims its
// directory under the §4.2 discovery rule. Both deployment modes build the
// record here, so they give the same reason.
//
// Spec: §4.1, §4.2, §13.11.3
func UnparseableRecord(id string, l Layer, file string, err error) ArtifactRecord {
	return ArtifactRecord{
		ID:          id,
		Layer:       l,
		Unparseable: layer.UnparseableReason(id+"/"+file, err),
	}
}

// Refusal classifies the record against the per-artifact rules both
// deployment modes apply before lint, in the order the spec fixes: the §4.1
// unparseable-manifest rule, then the §4.6 no-inheritance rule, then the §4.1
// removed-type rule. Neither later rule can be evaluated without the parsed
// frontmatter, so an unparseable record always reports
// manifest_unparseable. It returns the refusing rule's constraint and reason,
// or ok false when no rule refuses the record. AuthoredBytes is nil for a
// plain skill, so its synthesized document is never tested for the extends
// key or a removed type.
//
// Spec: §4.1, §4.6, §7.3.1, §13.11.3
func (r *ArtifactRecord) Refusal() (constraint, reason string, ok bool) {
	if r.Unparseable != "" {
		return layer.ManifestUnparseableConstraint, r.Unparseable, true
	}
	return layer.RemovedFeature(r.AuthoredBytes, r.Type())
}

// CanonicalID is the path under the layer root, separated by "/".
// Equivalent to ArtifactRecord.ID; provided as a method for callers
// that want the explicit name.
func (r *ArtifactRecord) CanonicalID() string { return r.ID }

// Type returns the record's parsed artifact type, or the empty type when the
// record carries no parsed ARTIFACT.md.
func (r *ArtifactRecord) Type() manifest.ArtifactType {
	if r.Artifact == nil {
		return ""
	}
	return r.Artifact.Type
}

// Walk walks every layer in r and returns the discovered artifacts in a
// stable order: layer order first, alphabetical canonical-ID within each
// layer.
//
// An ARTIFACT.md or SKILL.md whose frontmatter does not parse is refused
// under the §4.1 unparseable-manifest rule, an ARTIFACT.md that declares
// extends: is refused under the §4.6 no-inheritance rule, an ARTIFACT.md
// whose type is command or context is refused under the §4.1 removed-type
// rule, and two layers that contribute the same canonical ID collide (§4.6).
// Under every policy except CollisionPolicyHighestWins, Walk removes each
// refused record before it applies the collision rule, reporting it through
// OnDrop when OnDrop is set and failing the walk when it is not. A package
// that cannot hold an artifact at all, such as a root-level manifest with no
// canonical ID (§4.2), and an I/O failure still fail the walk.
// WalkOptions.CollisionPolicy selects the outcome of a collision: an
// ingest.collision error (the default), a drop reported through OnDrop
// (filesystem-source sync, §13.11.3), or highest-precedence-wins (the
// workspace overlay's walk of its own directory, §6.4), which applies no
// ingest rule and fails on an unparseable manifest it cannot serve.
func (r *Registry) Walk(opts WalkOptions) ([]ArtifactRecord, error) {
	if opts.CollisionPolicy == CollisionPolicyDrop && opts.OnDrop == nil {
		return nil, errors.New("filesystem: CollisionPolicyDrop requires OnDrop")
	}
	all := []ArtifactRecord{}
	for _, layer := range r.Layers {
		records, fsys, err := walkLayer(layer)
		if err != nil {
			return nil, err
		}
		if opts.OnLayer != nil {
			opts.OnLayer(layer, records, fsys)
		}
		all = append(all, records...)
	}

	// spec: §13.11.3 — the filesystem source applies the same §4.1 and §4.6
	// rules as the server, so a source materializes the artifacts a server
	// stores from it.
	kept, refused, err := refuseInvalidArtifacts(all, opts)
	if err != nil {
		return nil, err
	}
	records, collisions, err := dedupe(kept, opts)
	if err != nil {
		return nil, err
	}
	reportDrops(opts, append(refused, collisions...))
	// Synthesis follows composition, so every caller receives a plain
	// skill's synthesized ARTIFACT.md.
	if err := synthesizeSkillDocuments(records); err != nil {
		return nil, err
	}
	return records, nil
}

// refuseInvalidArtifacts applies the §4.1 unparseable-manifest rule, the §4.6
// no-inheritance rule, and the §4.1 removed-type rule to all, which is in
// layer order and then canonical-ID order. Under CollisionPolicyHighestWins
// it applies none of them, because the workspace overlay serves an artifact
// as authored (§6.4), and it fails on the first unparseable record, which
// the overlay has nothing to serve from. Under every other policy it removes
// each record that ArtifactRecord.Refusal refuses. With OnDrop set, it
// returns the removed records' drops in the order it met them; with OnDrop
// nil, the first such record fails the walk.
//
// Spec: §4.1, §4.6, §13.11.3
func refuseInvalidArtifacts(all []ArtifactRecord, opts WalkOptions) ([]ArtifactRecord, []layer.Drop, error) {
	if opts.CollisionPolicy == CollisionPolicyHighestWins {
		return all, nil, firstUnparseable(all)
	}
	var drops []layer.Drop
	kept := make([]ArtifactRecord, 0, len(all))
	for i := range all {
		rec := all[i]
		constraint, reason, refused := rec.Refusal()
		if !refused {
			kept = append(kept, rec)
			continue
		}
		if opts.OnDrop == nil {
			return nil, nil, fmt.Errorf("%s: %q in layer %q: %s",
				layer.InvalidArtifactCode, rec.ID, rec.Layer.ID, reason)
		}
		drops = append(drops, layer.RemovedFeatureDrop(rec.ID, constraint, reason))
	}
	return kept, drops, nil
}

// firstUnparseable returns the reason of the first record in all whose
// manifest does not parse, as an error, or nil when every manifest parses.
//
// Spec: §4.1, §6.4
func firstUnparseable(all []ArtifactRecord) error {
	for i := range all {
		if all[i].Unparseable != "" {
			return errors.New(all[i].Unparseable)
		}
	}
	return nil
}

// assignSourceSkillFields applies the filesystem-source values to a skill
// record after its version is resolved. An assigned version becomes
// 0.0.0-auto.0, because a filesystem source keeps no history from which to
// derive N; VersionAssigned stays true so lint still skips the required and
// semver checks. A skill with no ARTIFACT.md takes its description from
// SKILL.md, the value the server admits for a plain-skill row, so both modes
// serve the same plain-skill description (§11).
//
// Spec: §13.11.3, §4.3.4
func assignSourceSkillFields(rec *ArtifactRecord) {
	if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill {
		return
	}
	if rec.VersionAssigned {
		rec.Artifact.Version = manifest.AutoVersion(0)
	}
	if len(rec.AuthoredBytes) == 0 && rec.Skill != nil {
		rec.Artifact.Description = rec.Skill.Description
	}
}

// synthesizeSkillDocuments sets the ArtifactBytes of every skill record with
// no ARTIFACT.md to the synthesized document of §4.3.4, so adapters dispatch
// a plain skill as a skill in every mode. Walk calls it on the slice dedupe
// returns, and refuseInvalidArtifacts reads AuthoredBytes, so a plain skill's
// synthesized document is never tested for the extends key or a removed type. The version is a
// validated metadata.version or the auto form, so an error marks a broken
// invariant rather than an authoring fault.
//
// Spec: §13.11.3, §4.3.4, §4.6
func synthesizeSkillDocuments(records []ArtifactRecord) error {
	for i := range records {
		rec := &records[i]
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill || len(rec.AuthoredBytes) != 0 {
			continue
		}
		doc, err := manifest.SynthesizedSkillDocument(rec.Artifact.Version)
		if err != nil {
			return fmt.Errorf("filesystem: synthesize ARTIFACT.md for %s: %w", rec.ID, err)
		}
		rec.ArtifactBytes = doc
	}
	return nil
}

// reportDrops passes each drop to opts.OnDrop in order. A non-empty drops
// slice arises under CollisionPolicyDrop, which Walk refuses without OnDrop,
// and for a pre-lint refusal (the §4.1 unparseable-manifest rule, the §4.6
// no-inheritance rule, or the §4.1 removed-type rule) under any refusing
// policy with OnDrop set, because a refusal with OnDrop nil fails the walk
// before reportDrops runs.
func reportDrops(opts WalkOptions, drops []layer.Drop) {
	for _, d := range drops {
		opts.OnDrop(d)
	}
}

// dedupe applies the §4.6 collision rule to all, which is in layer order.
// It returns one record per canonical ID and, under CollisionPolicyDrop, the
// drop of each record the rule removed, in the order it met them. It calls no
// callback, so Walk reports every drop in one pass.
//
// Spec: §4.6, §13.11.3
func dedupe(all []ArtifactRecord, opts WalkOptions) (deduped []ArtifactRecord, collisions []layer.Drop, err error) {
	collisionError := opts.CollisionPolicy == CollisionPolicyDefault ||
		opts.CollisionPolicy == CollisionPolicyError
	byID := map[string]int{}
	deduped = make([]ArtifactRecord, 0, len(all))
	for _, rec := range all {
		idx, seen := byID[rec.ID]
		if !seen {
			byID[rec.ID] = len(deduped)
			deduped = append(deduped, rec)
			continue
		}
		// rec is the higher-precedence record (later layers override
		// earlier), and every same-ID pair is a forbidden silent shadow.
		switch {
		case collisionError:
			return nil, nil, fmt.Errorf("%s: artifact %q present in layers %q and %q",
				layer.CollisionCode, rec.ID, deduped[idx].Layer.ID, rec.Layer.ID)
		case opts.CollisionPolicy == CollisionPolicyDrop:
			// The comparison is against the kept record, so a third
			// layer's drop collides with the first contributor.
			collisions = append(collisions, layer.Collision{ArtifactID: rec.ID, Layer: rec.Layer.ID, ExistingLayer: deduped[idx].Layer.ID}.Drop())
			continue
		}
		deduped[idx] = rec
	}
	return deduped, collisions, nil
}

// CollisionPolicy controls how Walk handles two layers contributing the
// same canonical artifact ID.
type CollisionPolicy int

// CollisionPolicy values.
const (
	// CollisionPolicyDefault is alias for CollisionPolicyError.
	CollisionPolicyDefault CollisionPolicy = iota
	// CollisionPolicyError makes Walk return ingest.collision on duplicate
	// IDs across layers (§4.6), and fail on a manifest that does not parse
	// (§4.1) or an ARTIFACT.md that declares extends: (§4.6) or a removed
	// type (§4.1) when OnDrop is nil.
	CollisionPolicyError
	// CollisionPolicyHighestWins keeps the highest-precedence layer's
	// record and drops earlier ones. Used by the workspace overlay's walk
	// of its own directory (pkg/overlay, §6.4), which applies no ingest
	// rule: neither the §4.6 collision rule, the §4.6 no-inheritance rule,
	// nor the §4.1 removed-type rule. A manifest that does not parse fails
	// the walk, because the overlay has no parsed artifact to serve.
	CollisionPolicyHighestWins
	// CollisionPolicyDrop keeps the lower-precedence record, drops every
	// higher-precedence one with the same ID, and reports it through
	// WalkOptions.OnDrop (§4.6, §13.11.3). Used by filesystem-source sync.
	CollisionPolicyDrop
)

// WalkOptions configures Walk behavior.
type WalkOptions struct {
	CollisionPolicy CollisionPolicy
	// OnDrop receives each record the walk drops: a pre-lint refusal (the
	// §4.1 unparseable-manifest rule, then the §4.6 no-inheritance rule,
	// then the §4.1 removed-type rule, with details.constraint
	// manifest_unparseable, extends_removed, or type_removed) under
	// CollisionPolicyDefault, CollisionPolicyError, and CollisionPolicyDrop,
	// and a collision drop under CollisionPolicyDrop only. Walk refuses
	// CollisionPolicyDrop when it is nil, so no drop goes unreported. Under
	// CollisionPolicyDefault or CollisionPolicyError with OnDrop nil, a
	// manifest that does not parse and an ARTIFACT.md that declares extends:
	// or a removed type fail the walk.
	// Walk calls it synchronously in the calling goroutine.
	OnDrop func(layer.Drop)
	// OnLayer, when set, receives each layer with every package the walk
	// found in it, before any refusal, and the layer's tree read through
	// its resolved root. Filesystem-source sync computes the §4.7.6 layer
	// digest from them and reads the layer's attestation directory, so the
	// package digests the layer digest lists are those of the bytes the
	// walk read and returns (§13.11.3). Walk calls it synchronously, once
	// per layer, in layer order.
	OnLayer func(l Layer, records []ArtifactRecord, fsys fs.FS)
}

// walkLayer enumerates every artifact package in a single layer. A
// directory is a package when it directly holds ARTIFACT.md, or when it
// directly holds SKILL.md and no directory between it and the layer root is a
// package; the second form is a type: skill artifact with no ARTIFACT.md.
// DOMAIN.md files are ignored at this stage; phase 8 adds domain composition.
// The walk reads the layer through its resolved root (layerRoot), so a layer
// path that is a symbolic link is walked as the directory it names, as the
// fs.FS of the same path is.
//
// Spec: §4.2
func walkLayer(layer Layer) ([]ArtifactRecord, fs.FS, error) {
	root, err := layerRoot(layer)
	if err != nil {
		return nil, nil, err
	}
	fsys := os.DirFS(root)
	packages := map[string]bool{}
	var records []ArtifactRecord
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && path != root {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		rec, found, err := loadPackage(layer, root, fsys, packages, rel, path)
		if err != nil || !found {
			return err
		}
		packages[rel] = true
		records = append(records, rec)
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, fsys, nil
}

// layerRoot returns the layer's path with every symbolic link resolved. The
// package walk and the layer file list both read the layer through it, so a
// layer path that is a link gives the walk the same tree that os.DirFS of the
// path gives the ingest walk.
//
// Spec: §4.7.6
func layerRoot(l Layer) (string, error) {
	root, err := filepath.EvalSymlinks(l.Path)
	if err != nil {
		return "", fmt.Errorf("filesystem: resolve layer %q root: %w", l.ID, err)
	}
	return root, nil
}

// loadPackage applies the §4.2 discovery rule to the directory dir, whose
// slash path under the resolved layer root is rel, and loads the package it
// holds.
// found is false when dir is not a package. It reads every file of the
// package once, the root manifests included, and resolves and digests the
// package through NewPackageRecord, as registry ingest does.
//
// Spec: §4.1, §4.2, §4.7.6
func loadPackage(layer Layer, root string, fsys fs.FS, packages map[string]bool, rel, dir string) (rec ArtifactRecord, found bool, err error) {
	artifact, skill, err := PackageManifests(fsys, rel)
	if err != nil {
		return ArtifactRecord{}, false, err
	}
	if !artifact && (!skill || WithinPackage(packages, rel)) {
		return ArtifactRecord{}, false, nil
	}
	id, err := canonicalID(root, dir)
	if err != nil {
		return ArtifactRecord{}, false, err
	}
	files, err := packageFiles(root, dir)
	if err != nil {
		return ArtifactRecord{}, false, err
	}
	rec = NewPackageRecord(id, layer, artifact, skill, files)
	assignSourceSkillFields(&rec)
	return rec, true, nil
}

// VersionControlName is the name of the version-control data a package can
// hold: a Git repository directory, or the .git file of a submodule or a
// linked worktree.
const VersionControlName = ".git"

// SkipVersionControl reports whether the walk entry name below a package root
// is version-control data, which §4.4 leaves out of the bundled resource set
// together with every file below it. Only the exact name counts: a Git
// working tree lists its repository directory as exactly .git on its own
// platform, and every other name that resolves to .git stays in the set,
// where lint.resource_path_reserved refuses it.
//
// Spec: §4.4
func SkipVersionControl(name string) bool {
	return name == VersionControlName
}

// packageFiles returns every file under dir, the package directory below
// layerRoot, keyed by slash path relative to dir: the root manifests and the
// §4.4 bundled resources, without the files of a nested package and without
// version-control data. NewPackageRecord separates the manifests from the
// resources.
//
// Spec: §4.4, §4.7.6
func packageFiles(layerRoot, dir string) (map[string][]byte, error) {
	fsys := os.DirFS(layerRoot)
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != dir && SkipVersionControl(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			return skipNestedPackage(fsys, layerRoot, path)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// canonicalID converts a filesystem directory path under a layer root to
// a canonical artifact ID using forward slashes, enforcing the §4.2
// canonical-ID invariants via ValidateCanonicalID.
func canonicalID(layerRoot, dir string) (string, error) {
	rel, err := filepath.Rel(layerRoot, dir)
	if err != nil {
		return "", err
	}
	id := filepath.ToSlash(rel)
	if id == "." {
		// A root-level manifest has no directory path under the root.
		id = ""
	}
	if err := ValidateCanonicalID(id); err != nil {
		return "", fmt.Errorf("%w (layer root: %q)", err, layerRoot)
	}
	return id, nil
}

// skipNestedPackage returns filepath.SkipDir when the subdirectory dir of a
// package is a nested package that discovery finds: it carries its own
// ARTIFACT.md and its path from the layer root has no dot-prefixed segment.
// Directories are domain paths and the leaves are artifact packages, so a
// nested package's files belong to that artifact rather than to the
// enclosing one's bundled resources. A subdirectory holding only SKILL.md
// stays inside the enclosing package, and its SKILL.md is a bundled resource.
// So does a directory below a dot-prefixed directory of the package, whatever
// it holds, because discovery never visits it and no other package owns its
// files.
//
// Spec: §4.2, §4.4
func skipNestedPackage(fsys fs.FS, layerRoot, dir string) error {
	rel, err := filepath.Rel(layerRoot, dir)
	if err != nil {
		return err
	}
	if !Discoverable(filepath.ToSlash(rel)) {
		return nil
	}
	artifact, _, err := PackageManifests(fsys, filepath.ToSlash(rel))
	if err != nil {
		return err
	}
	if artifact {
		return filepath.SkipDir
	}
	return nil
}

// ValidateCanonicalID enforces the §4.2 canonical-ID invariants that every
// artifact-walk path shares: the ID is the non-empty directory path under
// the registry root, and no path segment contains "@". A root-level
// ARTIFACT.md or SKILL.md yields an empty ID and is rejected, so every artifact has an
// addressable canonical home. "@" is reserved as the reference delimiter in
// the "<id>@<semver>" and "<id>@sha256:<hash>" grammar; allowing it inside a
// segment makes a reference split ambiguously. No segment names .git or
// .podium under the §7.5.3 aliasing rule: package discovery skips a
// dot-prefixed directory, but a filesystem resolves GIT~1 or a .git that
// carries a code point HFS+ ignores to .git, and a consumer writes the ID as
// a path (the canonical layout, the .podium/resources/<id>/ bucket), so such
// an ID would reach a repository's .git or Podium's own state. The
// filesystem-source walk, the workspace overlay walk, and the server ingest
// walk all call this so they share one invariant.
//
// Spec: §4.2, §7.5.3
func ValidateCanonicalID(id string) error {
	if id == "" {
		return errors.New("artifact must live in a subdirectory of the layer (a root-level ARTIFACT.md or SKILL.md has no canonical ID)")
	}
	for _, seg := range strings.Split(id, "/") {
		if seg == "" {
			return fmt.Errorf("canonical ID %q has an empty path segment", id)
		}
		if strings.Contains(seg, "@") {
			return fmt.Errorf("canonical ID segment %q must not contain '@' (reserved for the @version or @sha256 suffix)", seg)
		}
		if materialize.AliasesDotGit(seg) || materialize.AliasesDotPodium(seg) {
			return fmt.Errorf("canonical ID segment %q names .git or .podium on some filesystem", seg)
		}
	}
	return nil
}
