package core

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/projection"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// overlapSettings carries the §7.3.1 overlap configuration: the description
// thresholds the catalog overlap report applies, the semantic overlap check's
// switch, read threshold, neighbour count, and sweep interval, and the clock
// that stamps computed records.
type overlapSettings struct {
	thresholds lint.Thresholds
	semantic   lint.SemanticConfig
	clock      clock.Clock
}

// WithOverlapChecks sets the §7.3.1 overlap configuration: th for the
// description checks of the catalog overlap report, and sem for the semantic
// overlap check. A zero th takes the default thresholds, and an out-of-range
// sem field takes its default. It is written once, before the registry serves
// a request. Returns the registry for chaining.
//
// Spec: §7.3.1, §13.12
func (r *Registry) WithOverlapChecks(th lint.Thresholds, sem lint.SemanticConfig) *Registry {
	r.overlap = overlapSettings{thresholds: th, semantic: sem.Resolved(), clock: clock.Real{}}
	return r
}

// SemanticOverlapEnabled reports whether PODIUM_LINT_SEMANTIC_OVERLAP is on.
func (r *Registry) SemanticOverlapEnabled() bool { return r.overlap.semantic.Enabled }

// errOverlapUnavailable reports a computation the registry cannot run: the
// check is off, or no search index is wired.
var errOverlapUnavailable = errors.New("semantic overlap cannot be computed")

// errNoStoredVector reports an artifact whose vector the index does not hold
// yet, or holds under another embedding model. The computation fails and keeps
// the artifact's records; the outbox write or a re-embed stores the vector and
// the next computation succeeds.
var errNoStoredVector = errors.New("no current vector is stored for the artifact")

// overlapModel returns the configured embedding model identifier the records
// are stamped with and filtered by: the local embedder's model, or the hosted
// model of a self-embedding backend.
//
// Spec: §7.3.1
func (r *Registry) overlapModel() string {
	if r.embedder != nil {
		return r.embedder.Model()
	}
	if hm, ok := r.vector.(vector.HostedModeler); ok {
		return hm.HostedModel()
	}
	return ""
}

// overlapReady reports whether the registry can compute overlap records: the
// check is on and the §4.7 vector path is wired.
func (r *Registry) overlapReady() error {
	if !r.overlap.semantic.Enabled {
		return fmt.Errorf("%w: PODIUM_LINT_SEMANTIC_OVERLAP is off", errOverlapUnavailable)
	}
	if !r.vectorSearchActive() {
		return fmt.Errorf("%w: no search index is configured", errOverlapUnavailable)
	}
	return nil
}

// overlapNeighbor is one index row near an artifact's projection with the
// cosine similarity of the two projection vectors.
type overlapNeighbor struct {
	artifactID string
	version    string
	similarity float64
}

// semanticNeighbors queries the §4.7 index for the k rows nearest rec's
// projection and reports each row's cosine similarity to it. A self-embedding
// backend is queried with the projection text and a storage-only backend with
// rec's stored vector, restricted to the configured model on a model-versioned
// backend, as search restricts its queries. A collocated backend scores each
// row from the stored vectors, because sqlite-vec's index orders by L2
// distance; a managed backend's similarity is one minus the distance it
// reports.
//
// Spec: §7.3.1, §4.7
func (r *Registry) semanticNeighbors(ctx context.Context, rec store.ManifestRecord, k int) ([]overlapNeighbor, error) {
	tenant := r.TenantFor(ctx)
	if r.embedder == nil {
		matches, err := r.vector.(vector.TextVectorizer).QueryText(ctx, tenant, projection.Artifact(rec), k)
		if err != nil {
			return nil, err
		}
		return neighborsFromDistance(matches), nil
	}
	vec, err := r.currentStoredVector(ctx, tenant, rec)
	if err != nil {
		return nil, err
	}
	var matches []vector.Match
	if mv, ok := vector.ModelVersionedOf(r.vector); ok {
		matches, err = mv.QueryModel(ctx, tenant, vec, k, r.embedder.Model())
	} else {
		matches, err = r.vector.Query(ctx, tenant, vec, k)
	}
	if err != nil {
		return nil, err
	}
	scorer, ok := r.vector.(vector.CosineScorer)
	if !ok {
		return neighborsFromDistance(matches), nil
	}
	return cosineNeighbors(ctx, scorer, tenant, vec, matches)
}

// currentStoredVector returns rec's stored vector when the backend holds one
// under the configured model. A backend that cannot return stored vectors, a
// missing row, and a row of another model all fail the computation.
func (r *Registry) currentStoredVector(ctx context.Context, tenant string, rec store.ManifestRecord) ([]float32, error) {
	reader, ok := r.vector.(vector.VectorReader)
	if !ok {
		return nil, fmt.Errorf("vector backend %s cannot return stored vectors", r.vector.ID())
	}
	vec, model, err := reader.StoredVector(ctx, tenant, rec.ArtifactID, rec.Version)
	if errors.Is(err, vector.ErrNotFound) {
		return nil, errNoStoredVector
	}
	if err != nil {
		return nil, err
	}
	if current := r.embedder.Model(); model != "" && model != current {
		return nil, fmt.Errorf("%w: the row carries model %s, the configured model is %s", errNoStoredVector, model, current)
	}
	return vec, nil
}

// neighborsFromDistance takes one minus each reported distance as the
// similarity, which is the cosine similarity when the managed index was
// created with the cosine metric (§7.3.1).
func neighborsFromDistance(matches []vector.Match) []overlapNeighbor {
	out := make([]overlapNeighbor, 0, len(matches))
	for _, m := range matches {
		out = append(out, overlapNeighbor{artifactID: m.ArtifactID, version: m.Version, similarity: 1 - float64(m.Distance)})
	}
	return out
}

// cosineNeighbors scores each match from the stored vectors through the
// collocated backend. A row deleted between the query and the scoring is
// dropped.
func cosineNeighbors(ctx context.Context, scorer vector.CosineScorer, tenant string, vec []float32, matches []vector.Match) ([]overlapNeighbor, error) {
	keys := make([]vector.RowKey, len(matches))
	for i, m := range matches {
		keys[i] = vector.RowKey{ArtifactID: m.ArtifactID, Version: m.Version}
	}
	sims, err := scorer.CosineSimilarities(ctx, tenant, vec, keys)
	if err != nil {
		return nil, err
	}
	out := make([]overlapNeighbor, 0, len(keys))
	for _, k := range keys {
		if s, ok := sims[k]; ok {
			out = append(out, overlapNeighbor{artifactID: k.ArtifactID, version: k.Version, similarity: s})
		}
	}
	return out, nil
}

// computeOverlap computes the overlap records of rec, the current served
// version of its artifact. It queries the index for the configured number of
// nearest rows and stores one record for each row that is the served version
// (served maps an identifier to it) of another artifact, whatever its
// similarity, replacing every record that names rec's artifact. A failed index
// query returns its error and leaves the stored records in place.
//
// Spec: §7.3.1
func (r *Registry) computeOverlap(ctx context.Context, rec store.ManifestRecord, served map[string]string) error {
	rows, err := r.semanticNeighbors(ctx, rec, r.overlap.semantic.Neighbors)
	if err != nil {
		return fmt.Errorf("index query for %s@%s: %w", rec.ArtifactID, rec.Version, err)
	}
	now := r.overlap.clock.Now()
	model := r.overlapModel()
	byPeer := map[string]store.OverlapRecord{}
	for _, row := range rows {
		if row.artifactID == rec.ArtifactID || served[row.artifactID] != row.version {
			continue
		}
		if prev, seen := byPeer[row.artifactID]; seen && prev.Similarity >= row.similarity {
			continue
		}
		byPeer[row.artifactID] = store.OverlapRecord{
			ArtifactA: rec.ArtifactID, VersionA: rec.Version,
			ArtifactB: row.artifactID, VersionB: row.version,
			Similarity: row.similarity, ModelID: model, ComputedAt: now,
		}
	}
	recs := make([]store.OverlapRecord, 0, len(byPeer))
	for _, o := range byPeer {
		recs = append(recs, o)
	}
	if err := r.store.ReplaceOverlapRecords(ctx, r.TenantFor(ctx), rec.ArtifactID, recs); err != nil {
		return fmt.Errorf("store overlap records for %s: %w", rec.ArtifactID, err)
	}
	return nil
}

// servedArtifacts returns the current served version (§4.7.6) of every
// servable artifact of the request's tenant, keyed by identifier.
func (r *Registry) servedArtifacts(ctx context.Context) (map[string]store.ManifestRecord, error) {
	all, err := r.store.ListManifests(ctx, r.TenantFor(ctx))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	out := map[string]store.ManifestRecord{}
	for _, m := range dedupeLatest(r.servableRows(ctx, all)) {
		out[m.ArtifactID] = m
	}
	return out, nil
}

// servedVersions projects a served set onto identifier to version.
func servedVersions(served map[string]store.ManifestRecord) map[string]string {
	out := make(map[string]string, len(served))
	for id, m := range served {
		out[id] = m.Version
	}
	return out
}

// OverlapComputeResult is the outcome of a batch of overlap computations: the
// artifacts computed and the computations that failed.
type OverlapComputeResult struct {
	Computed int `json:"computed"`
	Failed   int `json:"failed"`
}

// computeOverlapBatch computes each record in subjects, in order, and returns
// the identifiers it computed and the count of failures. A failure is logged
// with the artifact identifier and does not stop the batch; a cancelled
// context stops it.
func (r *Registry) computeOverlapBatch(ctx context.Context, subjects []store.ManifestRecord, served map[string]store.ManifestRecord) ([]string, int) {
	versions := servedVersions(served)
	var computed []string
	failed := 0
	for _, m := range subjects {
		if ctx.Err() != nil {
			break
		}
		if err := r.computeOverlap(ctx, m, versions); err != nil {
			log.Printf("%s: %v", lint.SemanticOverlapCode, err)
			failed++
			continue
		}
		computed = append(computed, m.ArtifactID)
	}
	return computed, failed
}

// ComputeOverlap computes the §7.3.1 overlap records of each (artifact,
// version) in keys that is the current served version of its artifact; a key
// at any other version is skipped. The registry calls it when the §4.7 outbox
// writes an artifact's vector to a managed backend, and the ingest cycle calls
// it through IngestSemanticOverlap on a collocated one. A failure is logged
// and counted and leaves that artifact's records in place.
//
// Spec: §7.3.1
func (r *Registry) ComputeOverlap(ctx context.Context, keys []vector.RowKey) (OverlapComputeResult, error) {
	computed, failed, err := r.computeOverlapKeys(ctx, keys)
	return OverlapComputeResult{Computed: len(computed), Failed: failed}, err
}

// computeOverlapKeys is ComputeOverlap returning the computed identifiers.
func (r *Registry) computeOverlapKeys(ctx context.Context, keys []vector.RowKey) ([]string, int, error) {
	if err := r.overlapReady(); err != nil {
		return nil, 0, err
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		return nil, 0, err
	}
	var subjects []store.ManifestRecord
	seen := map[string]bool{}
	for _, k := range keys {
		if m, ok := served[k.ArtifactID]; ok && m.Version == k.Version && !seen[k.ArtifactID] {
			seen[k.ArtifactID] = true
			subjects = append(subjects, m)
		}
	}
	computed, failed := r.computeOverlapBatch(ctx, subjects, served)
	return computed, failed, nil
}
