package core

import (
	"context"
	"fmt"

	"github.com/lennylabs/podium/pkg/embedding"
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
// onlyIfMissing=true skips artifacts that already have a vector in
// the store; useful for partial backfills after transient outages.
// onlyIfMissing=false re-embeds everything, useful after a provider
// switch where the old vectors are stale-dimension.
func (r *Registry) Reembed(ctx context.Context, onlyIfMissing bool) (*ReembedResult, error) {
	if r.embedder == nil || r.vector == nil {
		return nil, fmt.Errorf("reembed: vector search not configured")
	}
	manifests, err := r.store.ListManifests(ctx, r.tenantID)
	if err != nil {
		return nil, fmt.Errorf("reembed: list manifests: %w", err)
	}
	res := &ReembedResult{Total: len(manifests)}
	for _, m := range manifests {
		if onlyIfMissing {
			// Best-effort presence check: query with a zero vector
			// scoped to the artifact to see if we already have one.
			// We cheat a bit: a vector of a single non-zero element
			// is enough — we only need the backend to either return
			// the artifact or not.
			probe := make([]float32, r.embedder.Dimensions())
			probe[0] = 1
			matches, _ := r.vector.Query(ctx, r.tenantID, probe, 100)
			if hasMatch(matches, m.ArtifactID, m.Version) {
				continue
			}
		}
		if err := embedAndUpsert(ctx, r.embedder, r.vector, m); err != nil {
			res.Failed = append(res.Failed, ReembedFailure{
				ArtifactID: m.ArtifactID,
				Version:    m.Version,
				Reason:     err.Error(),
			})
			continue
		}
		res.Succeeded++
	}
	return res, nil
}

// ReembedOne re-embeds a single (artifact_id, version). Used by the
// `podium admin reembed --artifact <id>` path.
func (r *Registry) ReembedOne(ctx context.Context, artifactID, version string) error {
	if r.embedder == nil || r.vector == nil {
		return fmt.Errorf("reembed: vector search not configured")
	}
	m, err := r.store.GetManifest(ctx, r.tenantID, artifactID, version)
	if err != nil {
		return fmt.Errorf("reembed: get manifest: %w", err)
	}
	return embedAndUpsert(ctx, r.embedder, r.vector, m)
}

// embedAndUpsert composes the embedding text and upserts the vector.
// Identical to ingest's embedAndStore but operates on a manifest
// fetched from the store rather than one mid-ingest.
func embedAndUpsert(ctx context.Context, e embedding.Provider, v vector.Provider, mr store.ManifestRecord) error {
	text := composeEmbeddingText(mr)
	if text == "" {
		return nil
	}
	vecs, err := e.Embed(ctx, []string{text})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != 1 {
		return fmt.Errorf("embed: expected 1 vector, got %d", len(vecs))
	}
	if err := v.Put(ctx, mr.TenantID, mr.ArtifactID, mr.Version, vecs[0]); err != nil {
		return fmt.Errorf("vector put: %w", err)
	}
	return nil
}

// composeEmbeddingText mirrors the ingest-side projection so the
// embedding-input shape stays consistent across ingest and reembed.
func composeEmbeddingText(mr store.ManifestRecord) string {
	const bodyPrefixMax = 1024
	body := string(mr.Body)
	if len(body) > bodyPrefixMax {
		body = body[:bodyPrefixMax]
	}
	parts := []string{mr.ArtifactID, mr.Description}
	if len(mr.Tags) > 0 {
		parts = append(parts, joinNonEmpty(mr.Tags, " "))
	}
	if body != "" {
		parts = append(parts, body)
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

func hasMatch(matches []vector.Match, id, version string) bool {
	for _, m := range matches {
		if m.ArtifactID == id && m.Version == version {
			return true
		}
	}
	return false
}
