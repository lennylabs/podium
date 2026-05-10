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
		if collisionError {
			return nil, fmt.Errorf("ingest.collision: artifact %q present in layers %q and %q",
				rec.ID, deduped[idx].Layer.ID, rec.Layer.ID)
		}
		// Highest-precedence wins; later layers override earlier.
		deduped[idx] = rec
	}
	return deduped, nil
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
// a canonical artifact ID using forward slashes.
func canonicalID(layerRoot, dir string) (string, error) {
	rel, err := filepath.Rel(layerRoot, dir)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", fmt.Errorf("artifact must live in a subdirectory of the layer (root: %q)", layerRoot)
	}
	return filepath.ToSlash(rel), nil
}
