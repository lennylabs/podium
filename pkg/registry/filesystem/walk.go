package filesystem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
}

// CanonicalID is the path under the layer root, separated by "/".
// Equivalent to ArtifactRecord.ID; provided as a method for callers
// that want the explicit name.
func (r *ArtifactRecord) CanonicalID() string { return r.ID }

// Walk walks every layer in r and returns the discovered artifacts in a
// stable order: layer order first, alphabetical canonical-ID within each
// layer.
//
// Per §4.6, when two layers contribute the same canonical ID
// without extends:, ingest is rejected. WalkOptions let callers
// either return an error on collision (the spec default) or pick
// the highest-precedence-wins behavior (used by sync's
// effective-view composition).
func (r *Registry) Walk(opts WalkOptions) ([]ArtifactRecord, error) {
	collisionError := opts.CollisionPolicy == CollisionPolicyDefault ||
		opts.CollisionPolicy == CollisionPolicyError

	all := []ArtifactRecord{}
	for _, layer := range r.Layers {
		records, err := walkLayer(layer)
		if err != nil {
			return nil, err
		}
		all = append(all, records...)
	}

	// Detect collisions while preserving the layer order.
	byID := map[string]int{}
	deduped := make([]ArtifactRecord, 0, len(all))
	for _, rec := range all {
		idx, seen := byID[rec.ID]
		if !seen {
			byID[rec.ID] = len(deduped)
			deduped = append(deduped, rec)
			continue
		}
		// spec: §4.6 — "A collision is rejected at ingest unless the
		// higher-precedence artifact declares extends: <lower-precedence-id>."
		// rec is the higher-precedence record (later layers override
		// earlier), so the collision is permitted when its extends:
		// resolves to the colliding canonical ID; the extends merge is
		// applied later at read time. A collision without that declaration
		// is a forbidden silent shadow.
		if collisionError && !declaresExtendsTo(rec, rec.ID) {
			return nil, fmt.Errorf("ingest.collision: artifact %q present in layers %q and %q",
				rec.ID, deduped[idx].Layer.ID, rec.Layer.ID)
		}
		// Highest-precedence wins; later layers override earlier.
		deduped[idx] = rec
	}
	return deduped, nil
}

// declaresExtendsTo reports whether rec's frontmatter declares
// extends: <id>, comparing against the pin-stripped reference. Used to
// honor the §4.6 same-ID extends exception during collision detection.
func declaresExtendsTo(rec ArtifactRecord, id string) bool {
	if rec.Artifact == nil || rec.Artifact.Extends == "" {
		return false
	}
	ref := rec.Artifact.Extends
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	return ref == id
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
	// record and drops earlier ones. Used by sync, which materializes the
	// caller's effective view rather than ingesting raw layers.
	CollisionPolicyHighestWins
)

// WalkOptions configures Walk behavior.
type WalkOptions struct {
	CollisionPolicy CollisionPolicy
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
