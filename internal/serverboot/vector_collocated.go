package serverboot

import (
	"context"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/vector"
)

// collocatedVectorIngest carries the ingest-time embedding closures for a
// collocated vector backend (pgvector, sqlite-vec). The spec requires these
// backends to compute and store the embedding in the same transaction as the
// manifest commit (§4.7: "The collocated defaults (pgvector, sqlite-vec)
// sidestep the outbox entirely; embeddings and metadata commit in a single
// database transaction"). The bootstrap and reingest paths attach these to the
// ingest request so boot-ingested and reingested artifacts are actually
// embedded into the backend rather than left BM25-only.
//
// The zero value (all nil) is the no-vector or outbox case: the bootstrap paths
// pass it through unchanged and ingest writes no vector inline.
type collocatedVectorIngest struct {
	Embedder        ingest.EmbedderFunc
	VectorPut       ingest.VectorPutFunc
	DomainVectorPut ingest.DomainVectorPutFunc
}

// buildCollocatedVectorIngest returns the ingest-time embedding closures for a
// collocated vector backend, or the zero value when embedding at ingest does
// not apply. It applies only when a vector backend and a local embedder are
// both present and the backend is not routed through the §4.7.2 outbox (the
// managed-backend path, which defers the write to the drain worker). A
// self-embedding backend has no local embedder, so it returns the zero value
// and ingest stores no vector inline; that backend's text projection is written
// through its own put path, not handled here.
//
// The VectorPut closure mirrors core.upsertVector: a model-versioned backend
// (pgvector, sqlite-vec) tags each row with the embedder's model id so a later
// query restricts to the current model and a re-embed can purge stale-model
// rows (§4.7 model versioning). The DomainVectorPut closure embeds DOMAIN.md
// projections the same way so search_domains has a current semantic index.
func buildCollocatedVectorIngest(vec vector.Provider, emb embedding.Provider, useVectorOutbox bool) collocatedVectorIngest {
	if vec == nil || emb == nil || useVectorOutbox {
		return collocatedVectorIngest{}
	}
	mv, modelVersioned := vector.ModelVersionedOf(vec)
	model := emb.Model()
	return collocatedVectorIngest{
		Embedder: func(ctx context.Context, text string) ([]float32, error) {
			vecs, err := emb.Embed(ctx, []string{text})
			if err != nil {
				return nil, err
			}
			return vecs[0], nil
		},
		VectorPut: func(ctx context.Context, tenantID, artifactID, version string, v []float32) error {
			if modelVersioned {
				return mv.PutModel(ctx, tenantID, artifactID, version, v, model)
			}
			return vec.Put(ctx, tenantID, artifactID, version, v)
		},
		DomainVectorPut: func(ctx context.Context, tenantID, path string, v []float32) error {
			if modelVersioned {
				return mv.PutModel(ctx, tenantID, path, core.DomainVectorVersion, v, model)
			}
			return vec.Put(ctx, tenantID, path, core.DomainVectorVersion, v)
		},
	}
}
