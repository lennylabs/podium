package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

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

// ErrOverlapUnavailable reports a computation the registry cannot run: the
// check is off, or no search index is wired. The condition lasts until the
// operator changes the configuration, so the rebuild endpoint answers it with
// registry.overlap_rebuild_unavailable rather than a retryable code, and the
// startup pass and the sweep do not run under it.
//
// Spec: §7.3.1, §6.10
var ErrOverlapUnavailable = errors.New("semantic overlap cannot be computed")

// errNoStoredVector reports an artifact whose vector the index does not hold
// yet, or holds under another embedding model. The computation fails and keeps
// the artifact's records; the outbox write or a re-embed stores the vector and
// the next computation succeeds.
var errNoStoredVector = errors.New("no current vector is stored for the artifact")

// errStoredVectorAbsent reports a stored-vector read the backend answered
// with no row. It is the errNoStoredVector case an eventually consistent
// backend such as Pinecone produces right after a write, so the §7.3.1
// outbox-write computation retries it on the outbox's next passes.
var errStoredVectorAbsent = fmt.Errorf("%w: the backend returned no row", errNoStoredVector)

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

// semanticThreshold returns the read threshold applied when a request names
// none: PODIUM_LINT_SEMANTIC_SIMILARITY when it is set, and otherwise the
// default of the configured model identifier the records are stamped with
// (overlapModel), so a self-embedding backend takes its hosted model's
// default.
//
// Spec: §7.3.1, §13.12
func (r *Registry) semanticThreshold() float64 {
	return r.overlap.semantic.Threshold(r.overlapModel())
}

// OverlapReady returns nil when the registry can compute overlap records,
// which needs the check on and the §4.7 vector path wired, and otherwise an
// error wrapping ErrOverlapUnavailable that names the missing piece.
//
// Spec: §7.3.1
func (r *Registry) OverlapReady() error {
	if !r.overlap.semantic.Enabled {
		return fmt.Errorf("%w: PODIUM_LINT_SEMANTIC_OVERLAP is off", ErrOverlapUnavailable)
	}
	if !r.vectorSearchActive() {
		return fmt.Errorf("%w: no search index is configured", ErrOverlapUnavailable)
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

// neighborSourceFor prepares rec's index queries. A non-nil given is rec's
// vector as the §4.7 outbox wrote it, and the queries use it with no
// stored-vector read, so a backend that has not made the write readable yet
// does not fail the computation. Otherwise, every backend that can
// return rec's stored vector (vector.VectorReader) is queried with it, a
// self-embedding backend included, restricted to the configured model on a
// model-versioned backend as search restricts its queries. A self-embedding
// backend that cannot return a stored vector is queried with the projection
// text instead. A text query reports a query-to-passage score, which is not
// symmetric and does not reach 1 for identical texts, so it is the fallback
// rather than the default. A collocated backend scores each row from the
// stored vectors, because sqlite-vec's index orders by L2 distance; a managed
// backend's similarity is one minus the distance it reports.
//
// Spec: §7.3.1, §4.7
func (r *Registry) neighborSourceFor(ctx context.Context, rec store.ManifestRecord, given []float32) (neighborSource, error) {
	tenant := r.TenantFor(ctx)
	if given != nil {
		return r.vectorNeighborSource(tenant, given), nil
	}
	if _, canRead := r.vector.(vector.VectorReader); !canRead && r.embedder == nil {
		// vectorSearchActive admits a backend with no local embedder only
		// when it self-embeds, so every such backend is expected to query
		// by text. A backend that does neither fails this computation
		// rather than the process.
		tv, ok := r.vector.(vector.TextVectorizer)
		if !ok {
			return neighborSource{}, fmt.Errorf("vector backend %s cannot return stored vectors and cannot query with text", r.vector.ID())
		}
		text := projection.Artifact(r.withProjectionFields(rec))
		return neighborSource{
			query: func(ctx context.Context, k int) ([]vector.Match, error) { return tv.QueryText(ctx, tenant, text, k) },
			score: scoreByDistance,
		}, nil
	}
	vec, err := r.currentStoredVector(ctx, tenant, rec)
	if err != nil {
		return neighborSource{}, err
	}
	return r.vectorNeighborSource(tenant, vec), nil
}

// vectorNeighborSource returns the index queries of a computation that
// queries with vec: restricted to the configured model on a model-versioned
// backend, and scored from the stored vectors on a collocated backend.
func (r *Registry) vectorNeighborSource(tenant string, vec []float32) neighborSource {
	src := neighborSource{score: scoreByDistance}
	if mv, ok := vector.ModelVersionedOf(r.vector); ok && r.embedder != nil {
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
	return src
}

// currentStoredVector returns rec's stored vector when the backend holds one
// under the configured model (overlapModel). A backend that cannot return
// stored vectors, a missing row, and a row of another model all fail the
// computation.
func (r *Registry) currentStoredVector(ctx context.Context, tenant string, rec store.ManifestRecord) ([]float32, error) {
	reader, ok := r.vector.(vector.VectorReader)
	if !ok {
		return nil, fmt.Errorf("vector backend %s cannot return stored vectors", r.vector.ID())
	}
	vec, model, err := reader.StoredVector(ctx, tenant, rec.ArtifactID, rec.Version)
	if errors.Is(err, vector.ErrNotFound) {
		return nil, errStoredVectorAbsent
	}
	if err != nil {
		return nil, err
	}
	if current := r.overlapModel(); model != "" && model != current {
		return nil, fmt.Errorf("%w: the row carries model %s, the configured model is %s", errNoStoredVector, model, current)
	}
	return vec, nil
}

// clampSimilarity limits a similarity to the range 0 to 1. Float32 vectors
// and a managed backend's float32 score put identical texts slightly above 1
// (1.0000006 on sqlite-vec and 1.0000222 on Pinecone were observed), and a
// cosine of opposed vectors is negative; neither is a similarity the report
// can show. A NaN, which a zero-magnitude vector can produce, becomes 0.
//
// Spec: §7.3.1
func clampSimilarity(s float64) float64 {
	if !(s > 0) {
		return 0
	}
	return min(s, 1)
}

// scoreByDistance takes one minus each reported distance as the similarity,
// which is the cosine similarity when the managed index was created with the
// cosine metric (§7.3.1).
func scoreByDistance(_ context.Context, matches []vector.Match) ([]overlapNeighbor, error) {
	out := make([]overlapNeighbor, 0, len(matches))
	for _, m := range matches {
		out = append(out, overlapNeighbor{artifactID: m.ArtifactID, version: m.Version, similarity: clampSimilarity(1 - float64(m.Distance))})
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
			out = append(out, overlapNeighbor{artifactID: k.ArtifactID, version: k.Version, similarity: clampSimilarity(s)})
		}
	}
	return out, nil
}

// nearestServedPeers finds rec's nearest peers: the configured number of
// artifacts other than rec's whose current served versions (served maps an
// identifier to it) have the rows nearest rec's projection, by descending
// similarity and then by identifier. A peer's nearest row is kept. A non-nil
// given is rec's vector as the outbox wrote it (neighborSourceFor).
//
// Spec: §7.3.1
func (r *Registry) nearestServedPeers(ctx context.Context, rec store.ManifestRecord, served map[string]string, given []float32) ([]overlapNeighbor, error) {
	src, err := r.neighborSourceFor(ctx, rec, given)
	if err != nil {
		return nil, &overlapStepError{step: stepStoredVector, err: err}
	}
	rows, err := r.widenedPeerRows(ctx, src, rec.ArtifactID, served)
	if err != nil {
		return nil, &overlapStepError{step: stepNeighborQuery, err: err}
	}
	peers, err := src.score(ctx, rows)
	if err != nil {
		return nil, &overlapStepError{step: stepScoring, err: err}
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].similarity != peers[j].similarity {
			return peers[i].similarity > peers[j].similarity
		}
		return peers[i].artifactID < peers[j].artifactID
	})
	return peers[:min(len(peers), r.overlap.semantic.Neighbors)], nil
}

// The steps of one computation's index work, which the failure log names so
// an operator can tell a stored-vector read that failed from a neighbour
// query or a scoring call that failed.
const (
	stepStoredVector  = "stored-vector read"
	stepNeighborQuery = "neighbour query"
	stepScoring       = "scoring"
)

// overlapStepError is a failed computation step: step names the call, and
// err is its error.
type overlapStepError struct {
	step string
	err  error
}

func (e *overlapStepError) Error() string { return e.step + ": " + e.err.Error() }
func (e *overlapStepError) Unwrap() error { return e.err }

// logOverlapFailure logs a failed computation of rec with the step that
// failed and the artifact identifier.
//
// Spec: §7.3.1
func logOverlapFailure(rec store.ManifestRecord, err error) {
	step, cause := "computation", err
	var se *overlapStepError
	if errors.As(err, &se) {
		step, cause = se.step, se.err
	}
	log.Printf("%s: %s for %s@%s failed: %v", lint.SemanticOverlapCode, step, rec.ArtifactID, rec.Version, cause)
}

// overlapRowLimit returns the most rows one query of a computation asks:
// overlapQueryRowFactor times one more than the configured neighbour count,
// and no more than the backend accepts in one query.
//
// Spec: §7.3.1
func (r *Registry) overlapRowLimit() int {
	limit := overlapQueryRowFactor * (r.overlap.semantic.Neighbors + 1)
	if backend := vector.MaxQueryRowsOf(r.vector); backend > 0 {
		limit = min(limit, backend)
	}
	return limit
}

// widenedPeerRows returns the served peer rows of self's nearest index rows.
// The index keeps a row for every ingested version, and rows of earlier
// versions, self's own included, often lie nearest, so a query of exactly the
// configured count can come back with no peer at all. The computation
// therefore starts at one more row than the configured count and doubles the
// query until it has found that many distinct artifacts, the index returns
// fewer rows than asked, or the query reaches overlapRowLimit. Only the first
// query's failure fails the computation: a later one ends the widening, and
// the rows the previous query found are kept, because they are the nearest
// rows of a smaller query that succeeded and the larger query would only add
// rows after them.
//
// Spec: §7.3.1
func (r *Registry) widenedPeerRows(ctx context.Context, src neighborSource, self string, served map[string]string) ([]vector.Match, error) {
	n := r.overlap.semantic.Neighbors
	limit := r.overlapRowLimit()
	var rows []vector.Match
	for k, first := min(n+1, limit), true; ; k, first = min(2*k, limit), false {
		matches, err := src.query(ctx, k)
		if err != nil {
			if first {
				return nil, err
			}
			log.Printf("%s: %s: the widened query of %d rows failed, keeping the %d peers found: %v",
				lint.SemanticOverlapCode, self, k, len(rows), err)
			return rows, nil
		}
		rows = servedPeerRows(matches, self, served)
		if len(rows) >= n || len(matches) < k || k >= limit {
			return rows, nil
		}
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

// errOverlapSuperseded reports a computation that stored nothing because a
// computation of a newer version of its artifact stored first. It is not a
// failure.
var errOverlapSuperseded = errors.New("a computation of a newer version is stored")

// pendingOverlap is one computation whose index queries are done and whose
// records are not yet stored: rec, the version computed, and its peers.
type pendingOverlap struct {
	rec   store.ManifestRecord
	peers []overlapNeighbor
}

// storeOverlap stores the overlap records of p, one for each peer, whatever
// its similarity, owned by p's artifact and stamped now; the store replaces
// only the records that artifact owns and records the computation's version
// and model. It returns errOverlapSuperseded when the store refuses the
// computation because a computation of another version stored after now.
//
// Spec: §7.3.1
func (r *Registry) storeOverlap(ctx context.Context, p pendingOverlap, now time.Time) error {
	model := r.overlapModel()
	recs := make([]store.OverlapRecord, 0, len(p.peers))
	for _, peer := range p.peers {
		recs = append(recs, store.OverlapRecord{
			Owner: p.rec.ArtifactID, OwnerVersion: p.rec.Version,
			Peer: peer.artifactID, PeerVersion: peer.version,
			Similarity: clampSimilarity(peer.similarity), ModelID: model, ComputedAt: now,
		})
	}
	comp := store.OverlapComputation{ArtifactID: p.rec.ArtifactID, Version: p.rec.Version, ModelID: model, ComputedAt: now}
	err := r.store.ReplaceOverlapRecords(ctx, r.TenantFor(ctx), comp, recs)
	if errors.Is(err, store.ErrOverlapSuperseded) {
		return fmt.Errorf("%w: %s@%s", errOverlapSuperseded, p.rec.ArtifactID, p.rec.Version)
	}
	if err != nil {
		return fmt.Errorf("store overlap records for %s: %w", p.rec.ArtifactID, err)
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

// overlapConfirmChunk is the number of computations of a batch whose index
// queries run before the registry re-reads the served set to confirm their
// versions and stores their records. It bounds the time between a
// computation's confirmation and its store to a chunk's store writes, and
// keeps the served-set listings of a batch to one per chunk rather than one
// per computation. It sets no observable result, so it is not operator-tunable.
const overlapConfirmChunk = 32

// overlapBatchOptions adjusts a batch for the §4.7 outbox-write computation.
// given maps a subject's key to the vector the outbox wrote for it, which the
// computation queries with in place of the stored vector. deferAbsent counts
// a stored-vector read the backend answered with no row as unreadable rather
// than failed, and does not log it, because the outbox retries it. The zero
// value is the plain batch the sweep, the rebuild, a re-embed, and a
// collocated ingest run.
type overlapBatchOptions struct {
	given       map[vector.RowKey][]float32
	deferAbsent bool
}

// overlapBatchOutcome is what a batch did: the identifiers it computed, the
// count of failures, and, under overlapBatchOptions.deferAbsent, the keys
// whose stored vector the backend did not return.
type overlapBatchOutcome struct {
	computed   []string
	failed     int
	unreadable []vector.RowKey
}

// computeOverlapBatch computes each record in subjects, in order. The batch
// runs in chunks of overlapConfirmChunk: it queries the index for each
// computation of a chunk against served, then stores the chunk's records. A
// failure is logged with the artifact identifier and does not stop the
// batch; a cancelled context stops it.
//
// Spec: §7.3.1
func (r *Registry) computeOverlapBatch(ctx context.Context, subjects []store.ManifestRecord, served map[string]store.ManifestRecord, opts overlapBatchOptions) overlapBatchOutcome {
	versions := servedVersions(served)
	var out overlapBatchOutcome
	for start := 0; start < len(subjects) && ctx.Err() == nil; start += overlapConfirmChunk {
		chunk := subjects[start:min(start+overlapConfirmChunk, len(subjects))]
		pending := r.findOverlapPeers(ctx, chunk, versions, opts, &out)
		ids, storeFailed := r.storeOverlapChunk(ctx, pending)
		out.computed = append(out.computed, ids...)
		out.failed += storeFailed
	}
	return out
}

// findOverlapPeers runs the index queries of each record in chunk and returns
// the computations that found their peers, adding each failure to out. A
// failed stored-vector read, first neighbour query, or scoring call is logged
// with the step that failed and leaves that artifact's stored records and
// computation entry in place. Under opts.deferAbsent, a stored-vector read
// the backend answered with no row goes to out.unreadable instead, unlogged.
func (r *Registry) findOverlapPeers(ctx context.Context, chunk []store.ManifestRecord, versions map[string]string, opts overlapBatchOptions, out *overlapBatchOutcome) []pendingOverlap {
	found := make([]pendingOverlap, 0, len(chunk))
	for _, m := range chunk {
		if ctx.Err() != nil {
			break
		}
		key := vector.RowKey{ArtifactID: m.ArtifactID, Version: m.Version}
		peers, err := r.nearestServedPeers(ctx, m, versions, opts.given[key])
		if opts.deferAbsent && errors.Is(err, errStoredVectorAbsent) {
			out.unreadable = append(out.unreadable, key)
			continue
		}
		if err != nil {
			logOverlapFailure(m, err)
			out.failed++
			continue
		}
		found = append(found, pendingOverlap{rec: m, peers: peers})
	}
	return found
}

// storeOverlapChunk stores the records of each computation in pending whose
// version is still its artifact's current served version, and returns the
// identifiers stored and the count that failed.
//
// The served set a batch reads at its start can be stale by the time its
// computations store, and §7.3.1 requires a computation to store nothing when
// its version is no longer served by then, so the chunk re-reads the served
// set first and skips a computation whose version it no longer finds; a later
// computation of the newer version is due. The chunk's time is stamped before
// that read. A computation of a newer version that stores after the read was
// stamped later, so the store refuses this chunk's computation of the older
// version (store.ErrOverlapSuperseded) rather than overwrite it. A skipped or
// refused computation stored nothing and counts as neither stored nor failed.
//
// Spec: §7.3.1
func (r *Registry) storeOverlapChunk(ctx context.Context, pending []pendingOverlap) ([]string, int) {
	if len(pending) == 0 || ctx.Err() != nil {
		return nil, 0
	}
	now := r.overlap.clock.Now()
	current, err := r.servedArtifacts(ctx)
	if err != nil {
		log.Printf("%s: confirm the served versions of %d computations: %v", lint.SemanticOverlapCode, len(pending), err)
		return nil, len(pending)
	}
	var stored []string
	failed := 0
	for _, p := range pending {
		if cur, ok := current[p.rec.ArtifactID]; !ok || cur.Version != p.rec.Version {
			log.Printf("%s: skipped %s@%s: the version is no longer served", lint.SemanticOverlapCode, p.rec.ArtifactID, p.rec.Version)
			continue
		}
		err := r.storeOverlap(ctx, p, now)
		if errors.Is(err, errOverlapSuperseded) {
			log.Printf("%s: skipped: %v", lint.SemanticOverlapCode, err)
			continue
		}
		if err != nil {
			log.Printf("%s: %v", lint.SemanticOverlapCode, err)
			failed++
			continue
		}
		stored = append(stored, p.rec.ArtifactID)
	}
	return stored, failed
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
	out, _, err := r.computeOverlapKeys(ctx, keys, overlapBatchOptions{})
	return OverlapComputeResult{Computed: len(out.computed), Failed: out.failed}, err
}

// OverlapWrite is one vector the §4.7 outbox wrote to a managed backend:
// the row's key, and the vector the row carried with the model that produced
// it. Vector is nil when the backend embedded the row's text itself.
type OverlapWrite struct {
	Key    vector.RowKey
	Vector []float32
	Model  string
}

// OutboxOverlapResult is the outcome of the outbox-write computation: the
// counts, and the keys whose stored vector the backend did not return yet,
// which count as neither computed nor failed.
type OutboxOverlapResult struct {
	OverlapComputeResult
	Unreadable []vector.RowKey
}

// ComputeOverlapAtWrite is ComputeOverlap for the artifacts one §4.7 outbox
// pass wrote to a managed backend. A write that carries a vector produced by
// the configured embedding model (overlapModel) is queried with that vector
// and reads no stored vector, so it does not depend on the backend returning
// the write yet. A write with no vector, as on a self-embedding backend, or
// with a vector of another model reads the stored vector; when the backend
// answers that read with no row, the key is returned in Unreadable without a
// failure log, and the outbox retries it on its next passes.
//
// Spec: §7.3.1, §4.7
func (r *Registry) ComputeOverlapAtWrite(ctx context.Context, writes []OverlapWrite) (OutboxOverlapResult, error) {
	keys := make([]vector.RowKey, len(writes))
	opts := overlapBatchOptions{given: map[vector.RowKey][]float32{}, deferAbsent: true}
	model := r.overlapModel()
	for i, w := range writes {
		keys[i] = w.Key
		if len(w.Vector) > 0 && w.Model == model {
			opts.given[w.Key] = w.Vector
		}
	}
	out, _, err := r.computeOverlapKeys(ctx, keys, opts)
	return OutboxOverlapResult{
		OverlapComputeResult: OverlapComputeResult{Computed: len(out.computed), Failed: out.failed},
		Unreadable:           out.unreadable,
	}, err
}

// computeOverlapKeys is ComputeOverlap under opts, returning the batch's
// outcome and the served set it computed against.
func (r *Registry) computeOverlapKeys(ctx context.Context, keys []vector.RowKey, opts overlapBatchOptions) (overlapBatchOutcome, map[string]store.ManifestRecord, error) {
	if err := r.OverlapReady(); err != nil {
		return overlapBatchOutcome{}, nil, err
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		return overlapBatchOutcome{}, nil, err
	}
	var subjects []store.ManifestRecord
	seen := map[string]bool{}
	for _, k := range keys {
		if m, ok := served[k.ArtifactID]; ok && m.Version == k.Version && !seen[k.ArtifactID] {
			seen[k.ArtifactID] = true
			subjects = append(subjects, m)
		}
	}
	return r.computeOverlapBatch(ctx, subjects, served, opts), served, nil
}
