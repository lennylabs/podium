package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// textCaptureEmbedder records every text it embeds.
type textCaptureEmbedder struct {
	mu    sync.Mutex
	texts []string
}

func (*textCaptureEmbedder) ID() string      { return "capture" }
func (*textCaptureEmbedder) Model() string   { return "capture-model" }
func (*textCaptureEmbedder) Dimensions() int { return 4 }
func (e *textCaptureEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.texts = append(e.texts, texts...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0, 0}
	}
	return out, nil
}

// sqliteProjectionRegistry stores a plain skill whose when_to_use is
// top-level, and an agent whose ARTIFACT.md carries name and when_to_use, in a
// SQLite store, which persists neither field.
func sqliteProjectionRegistry(t *testing.T) (*core.Registry, *textCaptureEmbedder) {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "podium.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "greet", Version: "1.0.0", ContentHash: "sha256:greet", Type: "skill",
			Name: "greet", Description: "Greet the user.", WhenToUse: []string{"Use when a zorblax report is due."}, Layer: "L",
			SkillRaw: []byte("---\nname: greet\ndescription: Greet the user.\nwhen_to_use: Use when a zorblax report is due.\n---\n\nbody\n")},
		{TenantID: "t", ArtifactID: "pay", Version: "1.0.0", ContentHash: "sha256:pay", Type: "agent",
			Name: "payer", Description: "Pay invoices.", WhenToUse: []string{"After a quuxite approval."}, Layer: "L",
			Frontmatter: []byte("---\ntype: agent\nversion: 1.0.0\nname: payer\ndescription: Pay invoices.\nwhen_to_use:\n  - After a quuxite approval.\n---\n\nbody\n")},
	} {
		if err := st.PutManifest(ctx, m); err != nil {
			t.Fatalf("PutManifest %s: %v", m.ArtifactID, err)
		}
	}
	e := &textCaptureEmbedder{}
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(vector.NewMemory(4), e)
	return reg, e
}

// Spec: §4.7, §4.3.4 — on a SQL store, which returns rows without name and
// when_to_use, search still matches a word only when_to_use holds, whether
// the skill's SKILL.md states it at the top level or an agent's ARTIFACT.md
// states it.
func TestSearchArtifacts_SQLStoreReadsWhenToUse(t *testing.T) {
	t.Parallel()
	reg, _ := sqliteProjectionRegistry(t)
	for query, want := range map[string]string{"zorblax": "greet", "quuxite": "pay"} {
		res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{Query: query, TopK: 5})
		if err != nil {
			t.Fatalf("SearchArtifacts(%q): %v", query, err)
		}
		if res.TotalMatched != 1 || len(res.Results) != 1 || res.Results[0].ID != want {
			t.Errorf("SearchArtifacts(%q) = %+v, want %s alone", query, res.Results, want)
		}
	}
}

// Spec: §4.7 — a re-embed over a SQL store embeds the same projection ingest
// does: name, description, and when_to_use, read from the row bytes.
func TestReembed_SQLStoreProjectsNameAndWhenToUse(t *testing.T) {
	t.Parallel()
	reg, e := sqliteProjectionRegistry(t)
	if _, err := reg.Reembed(context.Background(), core.ReembedOptions{}); err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	want := map[string]bool{
		"greet\nGreet the user.\nUse when a zorblax report is due.": false,
		"payer\nPay invoices.\nAfter a quuxite approval.":           false,
	}
	for _, text := range e.texts {
		if _, ok := want[text]; ok {
			want[text] = true
		}
	}
	for text, seen := range want {
		if !seen {
			t.Errorf("no embed of %q; embedded %q", text, strings.Join(e.texts, " | "))
		}
	}
}
