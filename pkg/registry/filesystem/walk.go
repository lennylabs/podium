package filesystem

import (
	"errors"
	"fmt"
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
	// AuthoredBytes is ARTIFACT.md as read from disk. It equals ArtifactBytes
	// until the extends: resolver overwrites ArtifactBytes with the merged,
	// parent-hidden re-serialization (§4.6), after which the two differ for a
	// child. Materialization reads ArtifactBytes; the §4.7.6 digest reads
	// AuthoredBytes, because the digest is defined over the bytes as ingested
	// and a merged serialization does not reproduce it. The store's
	// ManifestRecord.Frontmatter carries the same bytes on the registry side.
	AuthoredBytes []byte
}

// CanonicalID is the path under the layer root, separated by "/".
// Equivalent to ArtifactRecord.ID; provided as a method for callers
// that want the explicit name.
func (r *ArtifactRecord) CanonicalID() string { return r.ID }

// Walk walks every layer in r and returns the discovered artifacts in a
// stable order: layer order first, alphabetical canonical-ID within each
// layer.
//
// Per §4.6, two layers that contribute the same canonical ID collide
// unless the higher-precedence artifact declares extends: on the ID.
// WalkOptions.CollisionPolicy selects the outcome of an unsanctioned
// collision: an ingest.collision error (the default), a drop reported
// through OnCollision (filesystem-source sync, §13.11.3), or
// highest-precedence-wins (the workspace overlay's walk of its own
// directory, §6.4).
func (r *Registry) Walk(opts WalkOptions) ([]ArtifactRecord, error) {
	if opts.CollisionPolicy == CollisionPolicyDrop && opts.OnCollision == nil {
		return nil, errors.New("filesystem: CollisionPolicyDrop requires OnCollision")
	}
	all := []ArtifactRecord{}
	for _, layer := range r.Layers {
		records, err := walkLayer(layer)
		if err != nil {
			return nil, err
		}
		all = append(all, records...)
	}

	deduped, kept, err := dedupe(all, opts)
	if err != nil {
		return nil, err
	}

	// spec: §13.11.3 — filesystem source resolves extends: through the same
	// merge the registry applies at load time, so materialization produces
	// equivalent output for the same artifact directory. Callers that want
	// raw layer records (lint, conformance) leave ResolveExtends false. The
	// resolver reads kept rather than all, so a dropped record never serves
	// as a same-ID or different-ID parent.
	if opts.ResolveExtends {
		if err := resolveExtends(deduped, kept); err != nil {
			return nil, err
		}
	}
	return deduped, nil
}

// dedupe applies the §4.6 collision rule to all, which is in layer order.
// It returns one record per canonical ID (deduped) and every record that
// was not dropped, in layer order (kept). Under CollisionPolicyError and
// CollisionPolicyHighestWins, kept equals all.
//
// Spec: §4.6, §13.11.3
func dedupe(all []ArtifactRecord, opts WalkOptions) (deduped, kept []ArtifactRecord, err error) {
	collisionError := opts.CollisionPolicy == CollisionPolicyDefault ||
		opts.CollisionPolicy == CollisionPolicyError
	byID := map[string]int{}
	deduped = make([]ArtifactRecord, 0, len(all))
	kept = make([]ArtifactRecord, 0, len(all))
	for _, rec := range all {
		idx, seen := byID[rec.ID]
		if !seen {
			byID[rec.ID] = len(deduped)
			deduped = append(deduped, rec)
			kept = append(kept, rec)
			continue
		}
		// rec is the higher-precedence record (later layers override
		// earlier), so the collision is sanctioned when its extends:
		// resolves to the colliding canonical ID; the extends merge is
		// applied later. A collision without that declaration is a
		// forbidden silent shadow.
		if !layer.ExtendsOverlays(extendsOf(rec), rec.ID) {
			switch {
			case collisionError:
				return nil, nil, fmt.Errorf("%s: artifact %q present in layers %q and %q",
					layer.CollisionCode, rec.ID, deduped[idx].Layer.ID, rec.Layer.ID)
			case opts.CollisionPolicy == CollisionPolicyDrop:
				// The comparison is against the kept record, so a third
				// layer's drop names the first contributor as ExistingLayer.
				opts.OnCollision(layer.Collision{ArtifactID: rec.ID, Layer: rec.Layer.ID, ExistingLayer: deduped[idx].Layer.ID})
				continue
			}
		}
		deduped[idx] = rec
		kept = append(kept, rec)
	}
	return deduped, kept, nil
}

// extendsOf returns rec's declared extends: reference, or "" when the record
// carries no parsed ARTIFACT.md, so the §4.6 collision check treats a record
// without frontmatter as declaring no overlay.
func extendsOf(rec ArtifactRecord) string {
	if rec.Artifact == nil {
		return ""
	}
	return rec.Artifact.Extends
}

// CollisionPolicy controls how Walk handles two layers contributing the
// same canonical artifact ID.
type CollisionPolicy int

// CollisionPolicy values.
const (
	// CollisionPolicyDefault is alias for CollisionPolicyError.
	CollisionPolicyDefault CollisionPolicy = iota
	// CollisionPolicyError makes Walk return ingest.collision on duplicate
	// IDs across layers (per §4.6 default behavior, no extends:).
	CollisionPolicyError
	// CollisionPolicyHighestWins keeps the highest-precedence layer's
	// record and drops earlier ones. Used by the workspace overlay's walk
	// of its own directory (pkg/overlay, §6.4), which the §4.6 collision
	// rule exempts.
	CollisionPolicyHighestWins
	// CollisionPolicyDrop keeps the lower-precedence record, drops a
	// higher-precedence one that declares no extends: on the ID, and
	// reports it through WalkOptions.OnCollision (§4.6, §13.11.3). Used by
	// filesystem-source sync.
	CollisionPolicyDrop
)

// WalkOptions configures Walk behavior.
type WalkOptions struct {
	CollisionPolicy CollisionPolicy
	// ResolveExtends folds each record's extends: chain into the record via
	// the shared manifest.MergeExtends before Walk returns, replacing the
	// record's ArtifactBytes/Artifact with the merged, extends-stripped
	// manifest (§4.6, §13.11.3). When false, records keep their authored
	// frontmatter unchanged.
	ResolveExtends bool
	// OnCollision receives each record CollisionPolicyDrop drops. Walk
	// refuses CollisionPolicyDrop when it is nil, so no drop goes
	// unreported.
	OnCollision func(layer.Collision)
}

// walkLayer enumerates every artifact directory in a single layer.
// An artifact directory contains an ARTIFACT.md (and SKILL.md when the
// type is skill). DOMAIN.md files are ignored at this stage; phase 8
// adds domain composition.
func walkLayer(layer Layer) ([]ArtifactRecord, error) {
	var records []ArtifactRecord
	walkErr := filepath.WalkDir(layer.Path, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != layer.Path {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "ARTIFACT.md" {
			return nil
		}
		rec, err := loadArtifactRecord(layer, path)
		if err != nil {
			return err
		}
		records = append(records, rec)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
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

	if err := captureResources(&rec, dir); err != nil {
		return ArtifactRecord{}, err
	}
	return rec, nil
}

func captureResources(rec *ArtifactRecord, dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// spec: §4.2 — directories are domain paths and the leaves are
			// artifact packages. A subdirectory that carries its own
			// ARTIFACT.md is a separate package; its files belong to that
			// artifact, so stop descending here instead of capturing them as
			// this artifact's bundled resources (§4.4).
			if path != dir && hasArtifactManifest(path) {
				return filepath.SkipDir
			}
			return nil
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
		// A root-level ARTIFACT.md has no directory path under the root.
		id = ""
	}
	if err := ValidateCanonicalID(id); err != nil {
		return "", fmt.Errorf("%w (layer root: %q)", err, layerRoot)
	}
	return id, nil
}

// hasArtifactManifest reports whether dir directly contains an ARTIFACT.md
// file, marking it as a nested artifact-package boundary (§4.2).
func hasArtifactManifest(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "ARTIFACT.md"))
	return err == nil && !info.IsDir()
}

// ValidateCanonicalID enforces the §4.2 canonical-ID invariants that every
// artifact-walk path shares: the ID is the non-empty directory path under
// the registry root, and no path segment contains "@". A root-level
// ARTIFACT.md yields an empty ID and is rejected, so every artifact has an
// addressable canonical home. "@" is reserved as the reference delimiter in
// the "<id>@<semver>" and "<id>@sha256:<hash>" grammar; allowing it inside a
// segment makes a reference split ambiguously. Both the filesystem-source
// walk and the server ingest walk call this so they share one invariant.
func ValidateCanonicalID(id string) error {
	if id == "" {
		return errors.New("artifact must live in a subdirectory of the layer (a root-level ARTIFACT.md has no canonical ID)")
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
