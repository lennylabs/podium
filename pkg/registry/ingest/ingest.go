// Package ingest implements the ingest pipeline described in spec
// §7.3.1: fetch a layer's snapshot, walk the diff, run lint as defense
// in depth, validate immutability, content-hash, store manifest and
// bundled resources, and emit an event.
//
// Ingest is the single entry point both the filesystem-source path
// and the (future) Git webhook path call into. Callers supply a layer
// source's fs.FS; ingest does the rest.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// Errors mapping to §6.10 namespaces.
var (
	// ErrLintFailed maps to ingest.lint_failed (§7.3.1).
	ErrLintFailed = errors.New("ingest.lint_failed")
	// ErrFrozen maps to ingest.frozen — a freeze window blocks ingest.
	ErrFrozen = errors.New("ingest.frozen")
	// ErrPublicModeSensitive maps to
	// ingest.public_mode_rejects_sensitive (§13.10).
	ErrPublicModeSensitive = errors.New("ingest.public_mode_rejects_sensitive")
	// ErrInvalidArtifact wraps parse / structural errors that surface at
	// ingest as ingest.lint_failed.
	ErrInvalidArtifact = errors.New("ingest.invalid_artifact")
)

// Request is a single layer ingest invocation.
type Request struct {
	// TenantID identifies the org owning the layer. Required.
	TenantID string
	// LayerID identifies the layer being ingested.
	LayerID string
	// Sensitivity floor: artifacts with sensitivity below the floor are
	// rejected at ingest. Used by §13.10 public mode to refuse medium
	// and high sensitivity. Empty means no floor.
	RejectAtOrAbove manifest.Sensitivity
	// Files is the source's snapshot exposed as fs.FS. The Local
	// LayerSourceProvider produces this from os.DirFS; the Git
	// provider exposes the checked-out tree the same way.
	Files fs.FS
	// Linter applies §4.3 lint rules. Lint errors are reported in
	// Result.LintFailures and abort the per-artifact ingest.
	Linter *lint.Linter
	// Clock provides ingest timestamps; defaults to clock.Real.
	Clock clock.Clock
}

// Result reports what happened.
type Result struct {
	// Accepted is the number of (artifact_id, version) pairs newly stored.
	Accepted int
	// Idempotent is the number of pairs that matched an existing
	// (id, version, content_hash) triple and were no-ops.
	Idempotent int
	// LintFailures collects diagnostics for artifacts rejected by lint.
	LintFailures []lint.Diagnostic
	// Conflicts reports (id, version) pairs that already exist with a
	// different content hash. Each entry is a distinct §4.7 immutability
	// violation.
	Conflicts []ConflictReport
	// Rejected reports artifacts rejected for other reasons (sensitivity
	// floor, parse failure, missing SKILL.md for type: skill, etc.).
	Rejected []RejectedArtifact
}

// ConflictReport names a (id, version) that already has different bytes.
type ConflictReport struct {
	ArtifactID string
	Version    string
	OldHash    string
	NewHash    string
}

// RejectedArtifact names an artifact rejected at ingest with a reason.
type RejectedArtifact struct {
	ArtifactID string
	Reason     string
	Code       string // §6.10 namespaced code
}

// Ingest runs the pipeline against st and returns a Result.
//
// Ingest is idempotent: running it twice with the same inputs has the
// same effect as running it once.
func Ingest(ctx context.Context, st store.Store, req Request) (*Result, error) {
	if req.TenantID == "" {
		return nil, errors.New("ingest: TenantID is required")
	}
	if req.Files == nil {
		return nil, fmt.Errorf("ingest: Files is required (no snapshot)")
	}
	if req.Clock == nil {
		req.Clock = clock.Real{}
	}
	if req.Linter == nil {
		req.Linter = &lint.Linter{}
	}

	now := req.Clock.Now().UTC()

	// Walk the layer's filesystem to find every ARTIFACT.md.
	records, err := walkLayer(req.Files, req.LayerID)
	if err != nil {
		return nil, err
	}

	// Run lint over the collected set; diagnostics with severity Error
	// abort their artifact's ingest.
	diags := req.Linter.Lint(nil, records)

	res := &Result{}
	errsByID := groupLintErrors(diags)
	for _, rec := range records {
		// Lint blocks ingest at error severity.
		if errs, ok := errsByID[rec.ID]; ok {
			res.LintFailures = append(res.LintFailures, errs...)
			continue
		}

		// Sensitivity floor (public mode) per §13.10.
		if rejectsSensitivity(req.RejectAtOrAbove, rec.Artifact.Sensitivity) {
			res.Rejected = append(res.Rejected, RejectedArtifact{
				ArtifactID: rec.ID,
				Reason: fmt.Sprintf("sensitivity %q rejected at floor %q",
					rec.Artifact.Sensitivity, req.RejectAtOrAbove),
				Code: "ingest.public_mode_rejects_sensitive",
			})
			continue
		}

		mr, err := manifestRecordFor(rec, req.TenantID, req.LayerID, now)
		if err != nil {
			res.Rejected = append(res.Rejected, RejectedArtifact{
				ArtifactID: rec.ID,
				Reason:     err.Error(),
				Code:       "ingest.invalid_artifact",
			})
			continue
		}

		// Check current state to distinguish accepted vs idempotent vs
		// immutable-violation.
		existing, err := st.GetManifest(ctx, req.TenantID, mr.ArtifactID, mr.Version)
		switch {
		case err == nil:
			if existing.ContentHash == mr.ContentHash {
				res.Idempotent++
				continue
			}
			res.Conflicts = append(res.Conflicts, ConflictReport{
				ArtifactID: mr.ArtifactID,
				Version:    mr.Version,
				OldHash:    existing.ContentHash,
				NewHash:    mr.ContentHash,
			})
			continue
		case errors.Is(err, store.ErrNotFound):
			// Insert below.
		default:
			return nil, err
		}

		if err := st.PutManifest(ctx, mr); err != nil {
			if errors.Is(err, store.ErrImmutableViolation) {
				res.Conflicts = append(res.Conflicts, ConflictReport{
					ArtifactID: mr.ArtifactID,
					Version:    mr.Version,
					NewHash:    mr.ContentHash,
				})
				continue
			}
			return nil, err
		}
		res.Accepted++

		// Populate cross-type dependency edges for §4.7.3.
		for _, edge := range edgesFor(rec.Artifact, rec.ID) {
			if err := st.PutDependency(ctx, req.TenantID, edge); err != nil {
				return nil, err
			}
		}
	}

	if len(res.LintFailures) > 0 && res.Accepted == 0 && res.Idempotent == 0 && len(res.Conflicts) == 0 {
		return res, fmt.Errorf("%w: %d diagnostics", ErrLintFailed, len(res.LintFailures))
	}
	return res, nil
}

func walkLayer(fsys fs.FS, layerID string) ([]filesystem.ArtifactRecord, error) {
	var out []filesystem.ArtifactRecord
	walkErr := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != "." {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "ARTIFACT.md" {
			return nil
		}
		rec, err := loadOne(fsys, p, layerID)
		if err != nil {
			return err
		}
		out = append(out, rec)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func loadOne(fsys fs.FS, artifactPath, layerID string) (filesystem.ArtifactRecord, error) {
	dir := dirOf(artifactPath)
	id := dirToCanonical(dir)

	bytes, err := fs.ReadFile(fsys, artifactPath)
	if err != nil {
		return filesystem.ArtifactRecord{}, err
	}
	a, err := manifest.ParseArtifact(bytes)
	if err != nil {
		return filesystem.ArtifactRecord{}, fmt.Errorf("%s: %w", id, err)
	}
	rec := filesystem.ArtifactRecord{
		ID:            id,
		Layer:         filesystem.Layer{ID: layerID},
		Artifact:      a,
		ArtifactBytes: bytes,
		Resources:     map[string][]byte{},
	}
	if a.Type == manifest.TypeSkill {
		skillPath := joinPath(dir, "SKILL.md")
		skillBytes, err := fs.ReadFile(fsys, skillPath)
		if err != nil {
			return filesystem.ArtifactRecord{}, fmt.Errorf("%s: type: skill missing SKILL.md", id)
		}
		s, err := manifest.ParseSkill(skillBytes)
		if err != nil {
			return filesystem.ArtifactRecord{}, fmt.Errorf("%s/SKILL.md: %w", id, err)
		}
		rec.Skill = s
		rec.SkillBytes = skillBytes
	}
	// Walk the artifact's directory for bundled resources.
	walkErr := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := d.Name()
		if base == "ARTIFACT.md" || (a.Type == manifest.TypeSkill && base == "SKILL.md") {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, dir+"/")
		rec.Resources[rel] = data
		return nil
	})
	if walkErr != nil {
		return filesystem.ArtifactRecord{}, walkErr
	}
	return rec, nil
}

// manifestRecordFor canonicalizes one ArtifactRecord into a ManifestRecord
// suitable for the store, computing the content hash per §4.7.6.
func manifestRecordFor(rec filesystem.ArtifactRecord, tenantID, layerID string, ingestedAt time.Time) (store.ManifestRecord, error) {
	hash := contentHashOf(rec)
	body := rec.Artifact.Body
	if rec.Artifact.Type == manifest.TypeSkill && rec.Skill != nil {
		body = rec.Skill.Body
	}
	return store.ManifestRecord{
		TenantID:    tenantID,
		ArtifactID:  rec.ID,
		Version:     rec.Artifact.Version,
		ContentHash: "sha256:" + hash,
		Type:        string(rec.Artifact.Type),
		Description: rec.Artifact.Description,
		Tags:        rec.Artifact.Tags,
		Sensitivity: string(rec.Artifact.Sensitivity),
		Layer:       layerID,
		Deprecated:  rec.Artifact.Deprecated,
		IngestedAt:  ingestedAt,
		Frontmatter: rec.ArtifactBytes,
		Body:        []byte(body),
	}, nil
}

// contentHashOf computes the canonical content hash for an artifact:
// SHA-256 over the artifact bytes, the optional SKILL.md bytes, and
// every bundled resource in sorted-path order. Spec §4.7.6.
func contentHashOf(rec filesystem.ArtifactRecord) string {
	parts := [][]byte{rec.ArtifactBytes, rec.SkillBytes}
	keys := make([]string, 0, len(rec.Resources))
	for k := range rec.Resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, []byte(k))
		parts = append(parts, rec.Resources[k])
	}
	return version.ContentHash(parts...)
}

// edgesFor extracts cross-type dependency edges from the artifact
// frontmatter (§4.7.3): extends, delegates_to, mcpServers references.
func edgesFor(a *manifest.Artifact, id string) []store.DependencyEdge {
	var out []store.DependencyEdge
	if a.Extends != "" {
		out = append(out, store.DependencyEdge{
			From: id, To: stripPin(a.Extends), Kind: "extends",
		})
	}
	for _, target := range a.DelegatesTo {
		out = append(out, store.DependencyEdge{
			From: id, To: stripPin(target), Kind: "delegates_to",
		})
	}
	for _, srv := range a.MCPServers {
		out = append(out, store.DependencyEdge{
			From: id, To: srv.Name, Kind: "mcpServers",
		})
	}
	return out
}

// stripPin removes the @semver / @sha256 suffix from a reference so the
// dependency graph keys on the canonical artifact ID.
func stripPin(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i]
	}
	return ref
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func joinPath(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

func dirToCanonical(dir string) string {
	if dir == "" || dir == "." {
		return ""
	}
	return dir
}

func rejectsSensitivity(floor, actual manifest.Sensitivity) bool {
	if floor == "" {
		return false
	}
	rank := func(s manifest.Sensitivity) int {
		switch s {
		case manifest.SensitivityHigh:
			return 3
		case manifest.SensitivityMedium:
			return 2
		case manifest.SensitivityLow:
			return 1
		}
		return 0
	}
	return rank(actual) >= rank(floor)
}

// groupLintErrors keeps only error-severity diagnostics, grouped by
// artifact ID. Warning and info diagnostics do not block ingest.
func groupLintErrors(diags []lint.Diagnostic) map[string][]lint.Diagnostic {
	out := map[string][]lint.Diagnostic{}
	for _, d := range diags {
		if d.Severity != lint.SeverityError {
			continue
		}
		out[d.ArtifactID] = append(out[d.ArtifactID], d)
	}
	return out
}
