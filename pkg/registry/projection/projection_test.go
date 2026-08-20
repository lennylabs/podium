package projection_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/projection"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// Spec: §4.7 "Artifact embeddings" — the projection is name, description,
// when_to_use (joined with newlines), and tags (joined with spaces), built
// from the record's frontmatter columns. Empty parts and empty list
// entries are dropped so the input carries no stray separator, and the
// prose body is excluded.
func TestArtifact_Projection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rec  store.ManifestRecord
		want string
	}{
		{
			name: "name and description only",
			rec:  store.ManifestRecord{Name: "Pay Invoice", Description: "settles an AP invoice", Body: []byte("ignored prose")},
			want: "Pay Invoice\nsettles an AP invoice",
		},
		{
			name: "when_to_use joined with newlines",
			rec:  store.ManifestRecord{Name: "n", Description: "d", WhenToUse: []string{"first", "second"}},
			want: "n\nd\nfirst\nsecond",
		},
		{
			name: "tags joined with spaces",
			rec:  store.ManifestRecord{Name: "n", Description: "d", Tags: []string{"finance", "ap"}},
			want: "n\nd\nfinance ap",
		},
		{
			name: "all components present",
			rec: store.ManifestRecord{
				Name:        "run-variance-analysis",
				Description: "flag unusual variance",
				WhenToUse:   []string{"after month-end close", "before board review"},
				Tags:        []string{"finance", "variance"},
				Body:        []byte("SECRET BODY PROSE that must not be embedded"),
			},
			want: "run-variance-analysis\nflag unusual variance\nafter month-end close\nbefore board review\nfinance variance",
		},
		{
			name: "empty name skipped, no leading separator",
			rec:  store.ManifestRecord{Description: "only desc"},
			want: "only desc",
		},
		{
			name: "empty description leaves no blank line",
			rec:  store.ManifestRecord{ArtifactID: "finance/x", Name: "x", Tags: []string{"t"}},
			want: "x\nt",
		},
		{
			name: "interior empty when_to_use entries dropped",
			rec:  store.ManifestRecord{Name: "n", Description: "d", WhenToUse: []string{"", "kept", ""}},
			want: "n\nd\nkept",
		},
		{
			name: "interior empty tag entries dropped",
			rec:  store.ManifestRecord{Name: "n", Description: "d", Tags: []string{"", "finance", ""}},
			want: "n\nd\nfinance",
		},
		{
			name: "fully empty record yields empty string",
			rec:  store.ManifestRecord{Body: []byte("body is never embedded")},
			want: "",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := projection.Artifact(c.rec)
			if got != c.want {
				t.Fatalf("Artifact() = %q, want %q", got, c.want)
			}
			if strings.Contains(got, "SECRET BODY") {
				t.Errorf("projection must not embed the prose body; got %q", got)
			}
			if c.rec.ArtifactID != "" && strings.Contains(got, c.rec.ArtifactID) {
				t.Errorf("artifact id must not appear in the projection; got %q", got)
			}
		})
	}
}

// artifactMD is the authored ARTIFACT.md the agreement test ingests. It
// carries a description, when_to_use, and tags so every part of the
// projection is exercised, plus a prose body that must never be embedded.
const artifactMD = "---\n" +
	"type: agent\n" +
	"version: 1.0.0\n" +
	"description: matches vendor payments against purchase orders\n" +
	"when_to_use:\n" +
	"  - at month-end close\n" +
	"  - before an audit\n" +
	"tags:\n" +
	"  - finance\n" +
	"  - ap\n" +
	"sensitivity: low\n" +
	"---\n\n" +
	"prose that is never embedded\n"

// recordingEmbedder is an embedding.Provider that keeps every text it is
// asked to embed, so a test can assert on the projection a call site hands
// the provider.
type recordingEmbedder struct {
	texts []string
}

func (*recordingEmbedder) ID() string      { return "recording" }
func (*recordingEmbedder) Model() string   { return "recording-model" }
func (*recordingEmbedder) Dimensions() int { return 8 }

func (e *recordingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.texts = append(e.texts, texts...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, 8)
	}
	return out, nil
}

// Spec: §4.7 "Artifact embeddings" and §4.7 "Dual-write semantics for
// external vector backends" — the ingest write path (embedAndStore on the
// inline path and the outbox enqueue in commitManifest) and `podium admin
// reembed` (Registry.ReembedOne) must embed identical text for one
// artifact. A managed backend takes the outbox text and a collocated one
// takes the inline text, so a divergence between them indexes the same
// artifact differently per deployment mode (§2.2).
//
// Each of the three strings is captured from the path that produces it
// rather than recomputed here, so reintroducing a local projection copy at
// any call site fails this test.
//
// The paths run against store.Memory rather than a SQL backend on purpose:
// neither SQL store persists Name or WhenToUse, so a record read back from
// SQLite or Postgres projects less text than the one ingest holds. That
// divergence predates this change, and collapsing the two copies does not
// close it.
func TestProjection_IngestAndReembedAgree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	files := fstest.MapFS{
		"finance/reconcile/ARTIFACT.md": &fstest.MapFile{Data: []byte(artifactMD)},
	}

	// The inline ingest path: embedAndStore hands the projection to the
	// configured embedder.
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	var inlineTexts []string
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "acme",
		LayerID:  "L",
		Files:    files,
		Embedder: func(_ context.Context, text string) ([]float32, error) {
			inlineTexts = append(inlineTexts, text)
			return make([]float32, 8), nil
		},
		VectorPut: func(context.Context, string, string, string, []float32) error { return nil },
	})
	if err != nil {
		t.Fatalf("Ingest (inline): %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1 (lint failures: %+v)", res.Accepted, res.LintFailures)
	}
	if len(inlineTexts) != 1 {
		t.Fatalf("embedder called %d times on the inline path, want 1", len(inlineTexts))
	}
	inline := inlineTexts[0]

	// The outbox ingest path: commitManifest enqueues the same projection
	// on the vector_pending row a managed backend drains.
	outboxStore := store.NewMemory()
	if err := outboxStore.CreateTenant(ctx, store.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(ctx, outboxStore, ingest.Request{
		TenantID:        "acme",
		LayerID:         "L",
		Files:           files,
		UseVectorOutbox: true,
	}); err != nil {
		t.Fatalf("Ingest (outbox): %v", err)
	}
	pending, err := outboxStore.ListVectorPending(ctx, 10, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListVectorPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("outbox depth = %d, want 1", len(pending))
	}
	outbox := pending[0].Text

	// The reembed path: Registry.ReembedOne re-projects the stored record.
	stored, err := st.ListManifests(ctx, "acme")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored manifests = %d, want 1", len(stored))
	}
	emb := &recordingEmbedder{}
	reg := core.New(st, "acme", nil).WithVectorSearch(vector.NewMemory(8), emb)
	if err := reg.ReembedOne(ctx, stored[0].ArtifactID, stored[0].Version); err != nil {
		t.Fatalf("ReembedOne: %v", err)
	}
	if len(emb.texts) != 1 {
		t.Fatalf("embedder called %d times on the reembed path, want 1", len(emb.texts))
	}
	reembed := emb.texts[0]

	if inline != outbox {
		t.Errorf("ingest inline text %q != outbox text %q", inline, outbox)
	}
	if inline != reembed {
		t.Errorf("ingest text %q != reembed text %q", inline, reembed)
	}
	const want = "matches vendor payments against purchase orders\nat month-end close\nbefore an audit\nfinance ap"
	if inline != want {
		t.Errorf("ingest embedded %q, want %q", inline, want)
	}
	if strings.Contains(inline, "prose that is never embedded") {
		t.Errorf("the prose body must not be embedded; got %q", inline)
	}
}
