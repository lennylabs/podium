package ingest_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// fakeProvider is an in-memory source.Provider backed by an fstest
// MapFS. It lets tests parameterize Reference and HistoryRewritten
// without spinning up a real git repo.
type fakeProvider struct {
	files            fs.FS
	reference        string
	historyRewritten bool
	calledWithPrior  string
}

func (p *fakeProvider) ID() string                   { return "fake" }
func (p *fakeProvider) Trigger() source.TriggerModel { return source.TriggerManual }
func (p *fakeProvider) Snapshot(_ context.Context, cfg source.LayerConfig) (*source.Snapshot, error) {
	p.calledWithPrior = cfg.PriorRef
	return &source.Snapshot{
		Reference:        p.reference,
		Files:            p.files,
		CreatedAt:        time.Now().UTC(),
		HistoryRewritten: p.historyRewritten,
	}, nil
}

func contextManifestBody(desc string) string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: " + desc +
		"\nsensitivity: low\n---\n\nbody\n"
}

// Spec: §7.3.1 — SourceIngest threads the prior ref into the
// provider, ingests the snapshot, and updates LastIngestedRef on
// success. The layer config persists with the new commit SHA.
func TestSourceIngest_TracksLastIngestedRef(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	cfg := store.LayerConfig{
		TenantID:        "t",
		ID:              "team-shared",
		SourceType:      "fake",
		LastIngestedRef: "0123456789abcdef",
	}
	if err := st.PutLayerConfig(context.Background(), cfg); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	provider := &fakeProvider{
		files: fstest.MapFS{
			"company-glossary/ARTIFACT.md": &fstest.MapFile{
				Data: []byte(contextManifestBody("glossary")),
			},
		},
		reference: "fedcba9876543210",
	}
	res, err := ingest.SourceIngest(context.Background(), st, provider, cfg, nil, nil)
	if err != nil {
		t.Fatalf("SourceIngest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1", res.Accepted)
	}
	if provider.calledWithPrior != "0123456789abcdef" {
		t.Errorf("provider received PriorRef %q, want %q",
			provider.calledWithPrior, "0123456789abcdef")
	}
	updated, err := st.GetLayerConfig(context.Background(), "t", "team-shared")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if updated.LastIngestedRef != "fedcba9876543210" {
		t.Errorf("LastIngestedRef = %q, want %q",
			updated.LastIngestedRef, "fedcba9876543210")
	}
}

// Spec: §7.3.1 — tolerant policy accepts a rewritten history,
// emitting a layer.history_rewritten audit event. The ingest result
// reports normal acceptance.
func TestSourceIngest_TolerantEmitsRewriteEvent(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	cfg := store.LayerConfig{
		TenantID:        "t",
		ID:              "L",
		SourceType:      "fake",
		LastIngestedRef: "old-sha",
		ForcePushPolicy: "tolerant",
	}
	_ = st.PutLayerConfig(context.Background(), cfg)
	provider := &fakeProvider{
		files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifestBody("x"))},
		},
		reference:        "new-sha",
		historyRewritten: true,
	}
	emitted := false
	emit := func(_ context.Context, tenant, layer, prior, newRef string) {
		emitted = true
		if tenant != "t" || layer != "L" {
			t.Errorf("emit got tenant=%q layer=%q, want t/L", tenant, layer)
		}
		if prior != "old-sha" || newRef != "new-sha" {
			t.Errorf("emit got prior=%q new=%q", prior, newRef)
		}
	}
	if _, err := ingest.SourceIngest(context.Background(), st, provider, cfg, nil, emit); err != nil {
		t.Fatalf("SourceIngest: %v", err)
	}
	if !emitted {
		t.Error("history_rewritten event should have fired")
	}
}

// Spec: §6.10 — strict force-push policy rejects with
// ingest.history_rewritten and skips the ingest.
// Matrix: §6.10 (ingest.history_rewritten)
func TestSourceIngest_StrictRejectsRewrite(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	cfg := store.LayerConfig{
		TenantID:        "t",
		ID:              "L",
		SourceType:      "fake",
		LastIngestedRef: "old-sha",
		ForcePushPolicy: "strict",
	}
	_ = st.PutLayerConfig(context.Background(), cfg)
	provider := &fakeProvider{
		files:            fstest.MapFS{},
		reference:        "new-sha",
		historyRewritten: true,
	}
	_, err := ingest.SourceIngest(context.Background(), st, provider, cfg, nil, nil)
	if !errors.Is(err, ingest.ErrHistoryRewritten) {
		t.Fatalf("got %v, want ErrHistoryRewritten", err)
	}
	// Strict-rejection must not advance LastIngestedRef.
	updated, _ := st.GetLayerConfig(context.Background(), "t", "L")
	if updated.LastIngestedRef != "old-sha" {
		t.Errorf("LastIngestedRef advanced to %q under strict rejection",
			updated.LastIngestedRef)
	}
	if !strings.Contains(err.Error(), "old-sha") {
		t.Errorf("error %q should reference the prior ref", err.Error())
	}
}
