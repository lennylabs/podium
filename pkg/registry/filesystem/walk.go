package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
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
	// the §4.6 no-inheritance check read AuthoredBytes, because the digest
	// frames a zero-length ARTIFACT.md for a plain skill. The store's
	// ManifestRecord.Frontmatter carries the same bytes on the registry side.
	AuthoredBytes []byte
	// VersionAssigned reports that the skill declares no usable version
	// (§4.3.4), so the version is assigned.
	VersionAssigned bool
}

// ResolveSkillVersion sets a skill record's version by the §4.3.4
// precedence, the ARTIFACT.md version and then a major.minor.patch SKILL.md
// metadata.version, and sets VersionAssigned when neither declares one. A
// record of any other type is unchanged.
//
// Spec: §4.3.4
func (r *ArtifactRecord) ResolveSkillVersion() {
	if r.Artifact == nil || r.Artifact.Type != manifest.TypeSkill {
		return
	}
	r.Artifact.Version = manifest.ResolveSkillVersion(r.Artifact, r.Skill)
	r.VersionAssigned = r.Artifact.Version == ""
}

// CanonicalID is the path under the layer root, separated by "/".
// Equivalent to ArtifactRecord.ID; provided as a method for callers
// that want the explicit name.
func (r *ArtifactRecord) CanonicalID() string { return r.ID }

// Walk walks every layer in r and returns the discovered artifacts in a
// stable order: layer order first, alphabetical canonical-ID within each
// layer.
//
// Per §4.6, an ARTIFACT.md that declares extends: is refused, and two layers
// that contribute the same canonical ID collide. Under every policy except
// CollisionPolicyHighestWins, Walk removes each refused record before it
// applies the collision rule, reporting it through OnDrop when OnDrop is set
// and failing the walk when it is not. WalkOptions.CollisionPolicy selects
// the outcome of a collision: an ingest.collision error (the default), a drop
// reported through OnDrop (filesystem-source sync, §13.11.3), or
// highest-precedence-wins (the workspace overlay's walk of its own
// directory, §6.4), which applies no ingest rule.
func (r *Registry) Walk(opts WalkOptions) ([]ArtifactRecord, error) {
	if opts.CollisionPolicy == CollisionPolicyDrop && opts.OnDrop == nil {
		return nil, errors.New("filesystem: CollisionPolicyDrop requires OnDrop")
	}
	all := []ArtifactRecord{}
	for _, layer := range r.Layers {
		records, err := walkLayer(layer)
		if err != nil {
			return nil, err
		}
		all = append(all, records...)
	}

	// spec: §13.11.3 — the filesystem source applies the same §4.6 rules as
	// the server, so a source materializes the artifacts a server stores
	// from it.
	kept, refused, err := refuseExtends(all, opts)
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

// refuseExtends applies the §4.6 no-inheritance rule to all, which is in
// layer order and then canonical-ID order. Under CollisionPolicyHighestWins
// it returns all unchanged, because the workspace overlay serves an artifact
// as authored (§6.4). Under every other policy it removes each record whose
// authored ARTIFACT.md declares extends:. With OnDrop set, it returns the
// removed records' drops in the order it met them; with OnDrop nil, the first
// such record fails the walk. AuthoredBytes is nil for a plain skill, so its
// synthesized document is never tested for the key.
//
// Spec: §4.6, §13.11.3
func refuseExtends(all []ArtifactRecord, opts WalkOptions) ([]ArtifactRecord, []layer.Drop, error) {
	if opts.CollisionPolicy == CollisionPolicyHighestWins {
		return all, nil, nil
	}
	var drops []layer.Drop
	kept := make([]ArtifactRecord, 0, len(all))
	for _, rec := range all {
		if !manifest.DeclaresExtends(rec.AuthoredBytes) {
			kept = append(kept, rec)
			continue
		}
		if opts.OnDrop == nil {
			return nil, nil, fmt.Errorf("%s: %q in layer %q: %s",
				layer.InvalidArtifactCode, rec.ID, rec.Layer.ID, layer.ExtendsRemovedReason)
		}
		drops = append(drops, layer.ExtendsRemovedDrop(rec.ID))
	}
	return kept, drops, nil
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
// returns, and refuseExtends reads AuthoredBytes, so a plain skill's
// synthesized document is never tested for the extends key. The version is a
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
// and for a §4.6 no-inheritance refusal under any refusing policy with OnDrop
// set, because a refusal with OnDrop nil fails the walk before reportDrops
// runs.
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
	// IDs across layers (§4.6), and fail on an ARTIFACT.md that declares
	// extends: when OnDrop is nil.
	CollisionPolicyError
	// CollisionPolicyHighestWins keeps the highest-precedence layer's
	// record and drops earlier ones. Used by the workspace overlay's walk
	// of its own directory (pkg/overlay, §6.4), which applies no ingest
	// rule: neither the §4.6 collision rule nor the §4.6 no-inheritance rule.
	CollisionPolicyHighestWins
	// CollisionPolicyDrop keeps the lower-precedence record, drops every
	// higher-precedence one with the same ID, and reports it through
	// WalkOptions.OnDrop (§4.6, §13.11.3). Used by filesystem-source sync.
	CollisionPolicyDrop
)

// WalkOptions configures Walk behavior.
type WalkOptions struct {
	CollisionPolicy CollisionPolicy
	// OnDrop receives each record the walk drops: an extends_removed drop
	// (§4.6 no-inheritance rule) under CollisionPolicyDefault,
	// CollisionPolicyError, and CollisionPolicyDrop, and a collision drop
	// under CollisionPolicyDrop only. Walk refuses CollisionPolicyDrop when it
	// is nil, so no drop goes unreported. Under CollisionPolicyDefault or
	// CollisionPolicyError with OnDrop nil, an ARTIFACT.md that declares
	// extends: fails the walk. Walk calls it synchronously in the calling
	// goroutine.
	OnDrop func(layer.Drop)
}

// walkLayer enumerates every artifact package in a single layer. A
// directory is a package when it directly holds ARTIFACT.md, or when it
// directly holds SKILL.md and no directory between it and the layer root is a
// package; the second form is a type: skill artifact with no ARTIFACT.md.
// DOMAIN.md files are ignored at this stage; phase 8 adds domain composition.
//
// Spec: §4.2
func walkLayer(layer Layer) ([]ArtifactRecord, error) {
	fsys := os.DirFS(layer.Path)
	packages := map[string]bool{}
	var records []ArtifactRecord
	walkErr := filepath.WalkDir(layer.Path, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && path != layer.Path {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(layer.Path, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		rec, found, err := loadPackage(layer, fsys, packages, rel, path)
		if err != nil || !found {
			return err
		}
		packages[rel] = true
		records = append(records, rec)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

// loadPackage applies the §4.2 discovery rule to the directory dir, whose
// slash path under the layer root is rel, and loads the package it holds.
// found is false when dir is not a package.
//
// Spec: §4.2
func loadPackage(layer Layer, fsys fs.FS, packages map[string]bool, rel, dir string) (rec ArtifactRecord, found bool, err error) {
	artifact, skill, err := PackageManifests(fsys, rel)
	if err != nil {
		return ArtifactRecord{}, false, err
	}
	switch {
	case artifact:
		rec, err = loadArtifactRecord(layer, filepath.Join(dir, "ARTIFACT.md"))
	case skill && !WithinPackage(packages, rel):
		rec, err = loadPlainSkillRecord(layer, dir)
	default:
		return ArtifactRecord{}, false, nil
	}
	if err != nil {
		return ArtifactRecord{}, false, err
	}
	return rec, true, nil
}

// loadArtifactRecord reads ARTIFACT.md and (for skills) SKILL.md from the
// containing directory, parses both, captures every other file in the
// directory tree as a bundled resource, and returns the assembled record.
func loadArtifactRecord(layer Layer, artifactPath string) (ArtifactRecord, error) {
	dir := filepath.Dir(artifactPath)
	id, err := canonicalID(layer.Path, dir)
	if err != nil {
		return ArtifactRecord{}, err
	}
	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		return ArtifactRecord{}, err
	}
	a, err := manifest.ParseArtifact(artifactBytes)
	if err != nil {
		return ArtifactRecord{}, fmt.Errorf("%s: %w", id, err)
	}
	rec := ArtifactRecord{
		ID:            id,
		Layer:         layer,
		Artifact:      a,
		ArtifactBytes: artifactBytes,
		AuthoredBytes: artifactBytes,
		Resources:     map[string][]byte{},
	}

	if a.Type == manifest.TypeSkill {
		skillPath := filepath.Join(dir, "SKILL.md")
		skillBytes, serr := os.ReadFile(skillPath)
		if errors.Is(serr, os.ErrNotExist) {
			return ArtifactRecord{}, fmt.Errorf("%s: type: skill missing SKILL.md", id)
		}
		if serr != nil {
			return ArtifactRecord{}, serr
		}
		s, err := manifest.ParseSkill(skillBytes)
		if err != nil {
			return ArtifactRecord{}, fmt.Errorf("%s/SKILL.md: %w", id, err)
		}
		rec.Skill = s
		rec.SkillBytes = skillBytes
	}

	if err := captureResources(&rec, layer.Path, dir); err != nil {
		return ArtifactRecord{}, err
	}
	rec.ResolveSkillVersion()
	assignSourceSkillFields(&rec)
	return rec, nil
}

// loadPlainSkillRecord loads a package that holds SKILL.md and no
// ARTIFACT.md as a type: skill artifact. ArtifactBytes and AuthoredBytes stay
// nil, so the §4.7.6 digest frames a zero-length first value. A SKILL.md that
// does not parse fails the walk, as an unparseable ARTIFACT.md does.
//
// Spec: §4.2, §4.3.4
func loadPlainSkillRecord(layer Layer, dir string) (ArtifactRecord, error) {
	id, err := canonicalID(layer.Path, dir)
	if err != nil {
		return ArtifactRecord{}, err
	}
	skillBytes, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return ArtifactRecord{}, err
	}
	s, err := manifest.ParseSkill(skillBytes)
	if err != nil {
		return ArtifactRecord{}, fmt.Errorf("%s/SKILL.md: %w", id, err)
	}
	rec := ArtifactRecord{
		ID:         id,
		Layer:      layer,
		Artifact:   &manifest.Artifact{Type: manifest.TypeSkill},
		Skill:      s,
		SkillBytes: skillBytes,
		Resources:  map[string][]byte{},
	}
	if err := captureResources(&rec, layer.Path, dir); err != nil {
		return ArtifactRecord{}, err
	}
	rec.ResolveSkillVersion()
	assignSourceSkillFields(&rec)
	return rec, nil
}

// captureResources records every file under dir, the package directory
// below layerRoot, as a bundled resource of rec, except the package root's
// manifests and the files of a nested package.
func captureResources(rec *ArtifactRecord, layerRoot, dir string) error {
	fsys := os.DirFS(layerRoot)
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			return skipNestedPackage(fsys, layerRoot, path)
		}
		if path == filepath.Join(dir, "ARTIFACT.md") {
			return nil
		}
		if rec.Artifact.Type == manifest.TypeSkill && path == filepath.Join(dir, "SKILL.md") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rec.Resources[filepath.ToSlash(rel)] = data
		return nil
	})
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
// package carries its own ARTIFACT.md. Directories are domain paths and the
// leaves are artifact packages, so a nested package's files belong to that
// artifact rather than to the enclosing one's bundled resources. A
// subdirectory holding only SKILL.md stays inside the enclosing package, and
// its SKILL.md is a bundled resource.
//
// Spec: §4.2, §4.4
func skipNestedPackage(fsys fs.FS, layerRoot, dir string) error {
	rel, err := filepath.Rel(layerRoot, dir)
	if err != nil {
		return err
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
// segment makes a reference split ambiguously. Both the filesystem-source
// walk and the server ingest walk call this so they share one invariant.
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
	}
	return nil
}
