package ingest

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

func outboxTestRecord() store.ManifestRecord {
	return store.ManifestRecord{
		TenantID: "default", ArtifactID: "a/x", Version: "1.0.0",
		ContentHash: "sha256:1", Type: "context",
		Description: "variance analysis reference for vendor payments",
	}
}

// With UseVectorOutbox set and an outbox-capable store, commitManifest commits
// the manifest and enqueues a vector_pending row atomically.
func TestCommitManifest_OutboxEnqueue(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	mr := outboxTestRecord()
	if got := composeEmbeddingText(mr); got == "" {
		t.Fatal("test record has no embedding text; choose a record with description")
	}
	if err := commitManifest(ctx, st, Request{UseVectorOutbox: true}, mr); err != nil {
		t.Fatalf("commitManifest: %v", err)
	}
	depth, _, _ := st.VectorOutboxStats(ctx)
	if depth != 1 {
		t.Errorf("outbox depth = %d, want 1 (row not enqueued)", depth)
	}
	if _, err := st.GetManifest(ctx, "default", "a/x", "1.0.0"); err != nil {
		t.Errorf("manifest not committed: %v", err)
	}
}

// Without the flag, commitManifest uses the plain path and enqueues nothing.
func TestCommitManifest_NoOutboxWhenDisabled(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := commitManifest(ctx, st, Request{UseVectorOutbox: false}, outboxTestRecord()); err != nil {
		t.Fatalf("commitManifest: %v", err)
	}
	depth, _, _ := st.VectorOutboxStats(ctx)
	if depth != 0 {
		t.Errorf("outbox depth = %d, want 0 (outbox disabled)", depth)
	}
}
