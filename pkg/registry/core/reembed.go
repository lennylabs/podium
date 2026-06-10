package core

import (
	"context"
	"fmt"
	"strconv"
	"time"

	domainpkg "github.com/lennylabs/podium/pkg/domain"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// ReembedResult reports what `Reembed` did across the tenant. Used
// by the admin CLI for human-readable output and by callers that
// re-embed in response to a configured-provider change.
type ReembedResult struct {
	Total     int
	Succeeded int
	Failed    []ReembedFailure
}

// ReembedFailure names an artifact whose embedding call failed.
type ReembedFailure struct {
	ArtifactID string
	Version    string
	Reason     string
}

// ReembedOptions selects which artifacts a Reembed pass covers. The
// zero value re-embeds every visible manifest in the tenant, matching
// the §4.7 "podium admin reembed" default (--all).
type ReembedOptions struct {
	// OnlyIfMissing skips artifacts that already have a stored vector,
	// for partial backfills after a transient embedding outage. spec:
	// §4.7 "podium admin reembed --only-missing".
	OnlyIfMissing bool
	// Since, when non-zero, restricts the pass to artifacts ingested at
	// or after this instant. Operators use it to re-embed only recently
	// ingested artifacts after a model change. spec: §4.7 "podium admin
	// reembed --since <timestamp>".
	Since time.Time
}

// Reembed re-runs the §4.7 embedding generation for every visible
// manifest in the tenant. The vector store's per-row UPSERT keeps
// each artifact's vector atomically replaced; in-flight searches
// see either the prior embedding or the new one, never half-state.
//
// Use cases:
//   - Switching `EmbeddingProvider` (different model / dimension).
//   - Backfilling artifacts whose original ingest failed to embed.
//   - Operator-initiated dimension upgrade on `podium admin reembed`.
//
// opts.OnlyIfMissing skips artifacts that already have a vector in the
// store; useful for partial backfills after transient outages. The zero
// value re-embeds everything, useful after a provider switch where the
// old vectors are stale-dimension. opts.Since restricts the pass to
// artifacts ingested at or after the given instant.
func (r *Registry) Reembed(ctx context.Context, opts ReembedOptions) (*ReembedResult, error) {
	if !r.vectorSearchActive() {
		return nil, fmt.Errorf("reembed: vector search not configured")
	}
	manifests, err := r.store.ListManifests(ctx, r.tenantFor(ctx))
	if err != nil {
		return nil, fmt.Errorf("reembed: list manifests: %w", err)
	}
	// §4.7 "Domain embeddings": domains are re-embedded on a full pass
	// alongside artifacts. Fetched up front so the --only-missing probe
	// can size its top-K to cover stored domain vectors too.
	domainRecs, err := r.store.ListDomains(ctx, r.tenantFor(ctx))
	if err != nil {
		return nil, fmt.Errorf("reembed: list domains: %w", err)
	}
	// spec: §4.7 `--since <timestamp>` — drop artifacts ingested before
	// the cutoff so a post-model-change pass only re-embeds recent work.
	if !opts.Since.IsZero() {
		kept := manifests[:0:0]
		for _, m := range manifests {
			if !m.IngestedAt.Before(opts.Since) {
				kept = append(kept, m)
			}
		}
		manifests = kept
	}

	// spec: §4.7 `--only-missing` — build the set of (id, version)
	// tuples that already have a vector in one exhaustive query rather
	// than a per-artifact top-K probe. The store keys at most one vector
	// per manifest record (one row per (id, version)), so a query with
	// topK == len(manifests) returns every stored vector for the tenant;
	// the prior fixed topK=100 probe falsely reported existing vectors
	// missing once a tenant held more than 100 artifacts. The
	// top-K also covers stored domain vectors so they cannot displace an
	// artifact vector out of the probe result; domain rows are skipped.
	// The probe builds a query vector from the local embedder's dimension. A
	// self-embedding backend has no local embedder, so the optimization is
	// skipped and every artifact is re-embedded (the upsert is idempotent).
	present := map[string]bool{}
	if opts.OnlyIfMissing && len(manifests) > 0 && r.embedder != nil {
		probe := make([]float32, r.embedder.Dimensions())
		probe[0] = 1
		matches, _ := r.vector.Query(ctx, r.tenantFor(ctx), probe, len(manifests)+len(domainRecs))
		for _, m := range matches {
			if m.Version == DomainVectorVersion {
				continue
			}
			present[m.ArtifactID+"@"+m.Version] = true
		}
	}

	// §4.7 model versioning: the model the rows are being (re-)tagged with, used
	// for the stale-row purge and the progress events. Empty for a
	// self-embedding backend (it carries no local model id).
	modelID := ""
	if r.embedder != nil {
		modelID = r.embedder.Model()
	}

	res := &ReembedResult{Total: len(manifests)}
	// §4.7 "emits embedding.reembed_in_progress events for progress monitoring":
	// announce the pass, then emit periodic progress so an operator can watch a
	// long re-embed advance.
	r.emitReembedProgress(ctx, 0, res.Total, modelID)
	for i, m := range manifests {
		if opts.OnlyIfMissing && present[m.ArtifactID+"@"+m.Version] {
			continue
		}
		if err := r.embedAndUpsert(ctx, m); err != nil {
			res.Failed = append(res.Failed, ReembedFailure{
				ArtifactID: m.ArtifactID,
				Version:    m.Version,
				Reason:     err.Error(),
			})
			continue
		}
		res.Succeeded++
		if (i+1)%50 == 0 {
			r.emitReembedProgress(ctx, i+1, res.Total, modelID)
		}
	}

	// §4.7 "Domain embeddings": re-embed every DOMAIN.md projection so
	// search_domains has a current semantic index. Domains carry no ingest
	// timestamp, so a --since pass (which targets recently-ingested
	// artifacts) leaves them untouched; a full pass re-embeds them all (the
	// upsert is idempotent).
	if opts.Since.IsZero() {
		for _, dr := range domainRecs {
			res.Total++
			if err := r.embedAndUpsertDomain(ctx, dr); err != nil {
				res.Failed = append(res.Failed, ReembedFailure{
					ArtifactID: dr.Path,
					Version:    DomainVectorVersion,
					Reason:     err.Error(),
				})
				continue
			}
			res.Succeeded++
		}
	}

	// §4.7 "Once re-embedding completes, stale-dimension rows are purged." A
	// full pass (every visible artifact re-tagged with the current model) drops
	// the previous model's rows on a model-versioned backend. A partial pass
	// (--only-missing or --since) leaves them, since it did not re-tag the whole
	// catalogue.
	if modelID != "" && !opts.OnlyIfMissing && opts.Since.IsZero() {
		if mv, ok := vector.ModelVersionedOf(r.vector); ok {
			if purged, err := mv.PurgeModelExcept(ctx, r.tenantFor(ctx), modelID); err == nil && purged > 0 {
				r.emit(ctx, AuditEvent{
					Type:    "embedding.reembed_purged",
					Caller:  "system",
					Target:  r.tenantFor(ctx),
					Context: map[string]string{"purged": strconv.Itoa(purged), "model": modelID},
				})
			}
		}
	}
	r.emitReembedProgress(ctx, res.Succeeded, res.Total, modelID)
	return res, nil
}

// emitReembedProgress fires an embedding.reembed_in_progress audit event with
// the done/total counts and the target model so operators can monitor a long
// re-embed (§4.7). A nil audit emitter makes it a no-op.
func (r *Registry) emitReembedProgress(ctx context.Context, done, total int, modelID string) {
	r.emit(ctx, AuditEvent{
		Type:   "embedding.reembed_in_progress",
		Caller: "system",
		Target: r.tenantFor(ctx),
		Context: map[string]string{
			"done":  strconv.Itoa(done),
			"total": strconv.Itoa(total),
			"model": modelID,
		},
	})
}

// ReembedOne re-embeds a single (artifact_id, version). Used by the
// `podium admin reembed --artifact <id>` path.
func (r *Registry) ReembedOne(ctx context.Context, artifactID, version string) error {
	if !r.vectorSearchActive() {
		return fmt.Errorf("reembed: vector search not configured")
	}
	m, err := r.store.GetManifest(ctx, r.tenantFor(ctx), artifactID, version)
	if err != nil {
		return fmt.Errorf("reembed: get manifest: %w", err)
	}
	return r.embedAndUpsert(ctx, m)
}

// embedAndUpsert composes the embedding text and upserts the row, routing
// through upsertVector so a self-embedding backend (§13.12)
// receives raw text. Mirrors ingest's embedAndStore but operates on a
// manifest fetched from the store rather than one mid-ingest.
func (r *Registry) embedAndUpsert(ctx context.Context, mr store.ManifestRecord) error {
	return r.upsertVector(ctx, mr.TenantID, mr.ArtifactID, mr.Version, composeEmbeddingText(mr))
}

// embedAndUpsertDomain composes the §4.7 domain projection from a
// DOMAIN.md record and upserts it under the reserved DomainVectorVersion
// so search_domains can rank over it. Malformed frontmatter is skipped
// (the linter reports it at ingest); a domain with no projectable text is
// a no-op.
func (r *Registry) embedAndUpsertDomain(ctx context.Context, dr store.DomainRecord) error {
	d, err := manifest.ParseDomain(dr.Raw)
	if err != nil {
		return nil
	}
	return r.upsertVector(ctx, dr.TenantID, dr.Path, DomainVectorVersion, domainpkg.EmbeddingProjection(d))
}

// composeEmbeddingText mirrors the ingest-side §4.7 projection so the
// embedding input stays consistent across ingest and reembed: name,
// description, when_to_use (joined with newlines), and tags (joined).
// The prose body is not embedded. spec: §4.7 "Artifact embeddings".
func composeEmbeddingText(mr store.ManifestRecord) string {
	parts := []string{mr.Name, mr.Description}
	if len(mr.WhenToUse) > 0 {
		parts = append(parts, joinNonEmpty(mr.WhenToUse, "\n"))
	}
	if len(mr.Tags) > 0 {
		parts = append(parts, joinNonEmpty(mr.Tags, " "))
	}
	return joinNonEmpty(parts, "\n")
}

func joinNonEmpty(parts []string, sep string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += p
	}
	return out
}
