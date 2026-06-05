package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// modelEmbedder returns a fixed vector and a configurable model id so a test
// can simulate a model switch between reembed passes.
type modelEmbedder struct {
	dim   int
	model string
}

func (modelEmbedder) ID() string        { return "test" }
func (e modelEmbedder) Model() string   { return e.model }
func (e modelEmbedder) Dimensions() int { return e.dim }
func (e modelEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, e.dim)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

// Reembed tags rows with the configured model, emits embedding.reembed_in_progress
// progress events, restricts subsequent model-scoped queries to that model, and
// purges a stale model's rows on a later full pass.
func TestReembed_ModelVersioningTagsRestrictsAndPurges(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "alpha", Version: "1.0.0",
		ContentHash: "sha256:1", Type: "skill", Description: "alpha skill", Layer: "L",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	dim := 4
	v := vector.NewMemory(dim)
	var events []core.AuditEvent
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(v, modelEmbedder{dim: dim, model: "v1"}).
		WithAudit(func(_ context.Context, e core.AuditEvent) { events = append(events, e) })

	if _, err := reg.Reembed(ctx, core.ReembedOptions{}); err != nil {
		t.Fatalf("reembed v1: %v", err)
	}
	if !hasProgressEvent(events, "v1") {
		t.Error("no embedding.reembed_in_progress event for model v1")
	}

	q := func() []float32 { x := make([]float32, dim); x[0] = 1; return x }()
	if got, _ := v.QueryModel(ctx, "t", q, 5, "v1"); len(got) == 0 {
		t.Error("alpha not found under model v1 after reembed")
	}
	if got, _ := v.QueryModel(ctx, "t", q, 5, "v2"); len(got) != 0 {
		t.Errorf("v1-tagged alpha leaked into a model-v2 query: %v", got)
	}

	// A stale row from the old model, with no live manifest, must be purged on
	// the next full pass under the new model.
	if err := v.PutModel(ctx, "t", "ghost", "1.0.0", q, "v1"); err != nil {
		t.Fatalf("seed ghost: %v", err)
	}
	reg = reg.WithVectorSearch(v, modelEmbedder{dim: dim, model: "v2"})
	if _, err := reg.Reembed(ctx, core.ReembedOptions{}); err != nil {
		t.Fatalf("reembed v2: %v", err)
	}
	if got, _ := v.QueryModel(ctx, "t", q, 5, ""); idsHas(got, "ghost") {
		t.Error("stale model-v1 ghost row survived the v2 reembed purge")
	}
	if got, _ := v.QueryModel(ctx, "t", q, 5, "v2"); !idsHas(got, "alpha") {
		t.Error("alpha not re-tagged to v2 after the second reembed")
	}
}

func hasProgressEvent(events []core.AuditEvent, model string) bool {
	for _, e := range events {
		if e.Type == "embedding.reembed_in_progress" && e.Context["model"] == model {
			return true
		}
	}
	return false
}

func idsHas(ms []vector.Match, id string) bool {
	for _, m := range ms {
		if m.ArtifactID == id {
			return true
		}
	}
	return false
}
