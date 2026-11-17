package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"

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

// overlapQueryRowFactor bounds how far a computation widens its index query:
// at most this many times one more than PODIUM_LINT_SEMANTIC_NEIGHBORS rows.
// The factor is fixed by §7.3.1 rather than operator-tunable.
const overlapQueryRowFactor = 50

// neighborSource answers the index queries of one artifact's computation:
// query returns the k rows nearest the artifact's projection, and score
// reports the cosine similarity of each given row to it.
type neighborSource struct {
	query func(ctx context.Context, k int) ([]vector.Match, error)
	score func(ctx context.Context, matches []vector.Match) ([]overlapNeighbor, error)
}

// neighborSourceFor prepares rec's index queries. A self-embedding backend is
// queried with the projection text and a storage-only backend with rec's
// stored vector, restricted to the configured model on a model-versioned
// backend, as search restricts its queries. A collocated backend scores each
// row from the stored vectors, because sqlite-vec's index orders by L2
// distance; a managed backend's similarity is one minus the distance it
// reports.
//
// Spec: §7.3.1, §4.7
func (r *Registry) neighborSourceFor(ctx context.Context, rec store.ManifestRecord) (neighborSource, error) {
	tenant := r.TenantFor(ctx)
	if r.embedder == nil {
		tv := r.vector.(vector.TextVectorizer)
		text := projection.Artifact(rec)
		return neighborSource{
			query: func(ctx context.Context, k int) ([]vector.Match, error) { return tv.QueryText(ctx, tenant, text, k) },
			score: scoreByDistance,
		}, nil
	}
	vec, err := r.currentStoredVector(ctx, tenant, rec)
	if err != nil {
		return neighborSource{}, err
	}
	src := neighborSource{score: scoreByDistance}
	if mv, ok := vector.ModelVersionedOf(r.vector); ok {
		model := r.embedder.Model()
		src.query = func(ctx context.Context, k int) ([]vector.Match, error) {
			return mv.QueryModel(ctx, tenant, vec, k, model)
		}
	} else {
		src.query = func(ctx context.Context, k int) ([]vector.Match, error) { return r.vector.Query(ctx, tenant, vec, k) }
	}
	if scorer, ok := r.vector.(vector.CosineScorer); ok {
		src.score = func(ctx context.Context, matches []vector.Match) ([]overlapNeighbor, error) {
			return cosineNeighbors(ctx, scorer, tenant, vec, matches)
		}
	}
	return src, nil
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

// scoreByDistance takes one minus each reported distance as the similarity,
// which is the cosine similarity when the managed index was created with the
// cosine metric (§7.3.1).
func scoreByDistance(_ context.Context, matches []vector.Match) ([]overlapNeighbor, error) {
	out := make([]overlapNeighbor, 0, len(matches))
	for _, m := range matches {
		out = append(out, overlapNeighbor{artifactID: m.ArtifactID, version: m.Version, similarity: 1 - float64(m.Distance)})
	}
	return out, nil
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

// nearestServedPeers finds rec's nearest peers: the configured number of
// artifacts other than rec's whose current served versions (served maps an
// identifier to it) have the rows nearest rec's projection. The index keeps a
// row for every ingested version, and rows of earlier versions, rec's own
// included, often lie nearest, so a query of exactly that many rows can come
// back with no peer at all. The computation therefore starts at one more row
// than the configured count and doubles the query until it has found that
// many distinct artifacts, the index returns fewer rows than asked, or the
// query reaches overlapQueryRowFactor times its first size. A peer's nearest
// row is kept, and the peers come back by descending similarity and then by
// identifier.
//
// Spec: §7.3.1
func (r *Registry) nearestServedPeers(ctx context.Context, rec store.ManifestRecord, served map[string]string) ([]overlapNeighbor, error) {
	src, err := r.neighborSourceFor(ctx, rec)
	if err != nil {
		return nil, err
	}
	n := r.overlap.semantic.Neighbors
	limit := overlapQueryRowFactor * (n + 1)
	for k := n + 1; ; k = min(2*k, limit) {
		matches, err := src.query(ctx, k)
		if err != nil {
			return nil, err
		}
		rows := servedPeerRows(matches, rec.ArtifactID, served)
		if len(rows) < n && len(matches) >= k && k < limit {
			continue
		}
		peers, err := src.score(ctx, rows)
		if err != nil {
			return nil, err
		}
		sort.Slice(peers, func(i, j int) bool {
			if peers[i].similarity != peers[j].similarity {
				return peers[i].similarity > peers[j].similarity
			}
			return peers[i].artifactID < peers[j].artifactID
		})
		return peers[:min(len(peers), n)], nil
	}
}

// servedPeerRows keeps the rows that are the served version of an artifact
// other than self, one per artifact: the first, which the index returned as
// the nearest.
func servedPeerRows(matches []vector.Match, self string, served map[string]string) []vector.Match {
	seen := map[string]bool{}
	out := make([]vector.Match, 0, len(matches))
	for _, m := range matches {
		if m.ArtifactID == self || seen[m.ArtifactID] || served[m.ArtifactID] != m.Version {
			continue
		}
		seen[m.ArtifactID] = true
		out = append(out, m)
	}
	return out
}

// computeOverlap computes the overlap records of rec, the current served
// version of its artifact. It finds rec's nearest served peers and stores one
// record for each, whatever its similarity, owned by rec's artifact; the
// store replaces only the records rec's artifact owns and records the
// computation's version and model. A failed index query returns its error and
// leaves the stored records and the computation entry in place.
//
// Spec: §7.3.1
func (r *Registry) computeOverlap(ctx context.Context, rec store.ManifestRecord, served map[string]string) error {
	peers, err := r.nearestServedPeers(ctx, rec, served)
	if err != nil {
		return fmt.Errorf("index query for %s@%s: %w", rec.ArtifactID, rec.Version, err)
	}
	now := r.overlap.clock.Now()
	model := r.overlapModel()
	recs := make([]store.OverlapRecord, 0, len(peers))
	for _, p := range peers {
		recs = append(recs, store.OverlapRecord{
			Owner: rec.ArtifactID, OwnerVersion: rec.Version,
			Peer: p.artifactID, PeerVersion: p.version,
			Similarity: p.similarity, ModelID: model, ComputedAt: now,
		})
	}
	comp := store.OverlapComputation{ArtifactID: rec.ArtifactID, Version: rec.Version, ModelID: model, ComputedAt: now}
	if err := r.store.ReplaceOverlapRecords(ctx, r.TenantFor(ctx), comp, recs); err != nil {
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
// at any other version is skipped. It lists the tenant's served set once for
// all of keys, so the §4.7 outbox drain passes every key one pass wrote to a
// managed backend in one call, and the ingest cycle calls it through
// IngestSemanticOverlap on a collocated one. A failure is logged and counted
// and leaves that artifact's records in place.
//
// Spec: §7.3.1
func (r *Registry) ComputeOverlap(ctx context.Context, keys []vector.RowKey) (OverlapComputeResult, error) {
	computed, failed, _, err := r.computeOverlapKeys(ctx, keys)
	return OverlapComputeResult{Computed: len(computed), Failed: failed}, err
}

// computeOverlapKeys is ComputeOverlap returning the computed identifiers and
// the served set it computed against.
func (r *Registry) computeOverlapKeys(ctx context.Context, keys []vector.RowKey) ([]string, int, map[string]store.ManifestRecord, error) {
	if err := r.overlapReady(); err != nil {
		return nil, 0, nil, err
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		return nil, 0, nil, err
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
	return computed, failed, served, nil
}
