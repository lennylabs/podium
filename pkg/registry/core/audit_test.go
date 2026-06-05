package core_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

type recorder struct {
	mu     sync.Mutex
	events []core.AuditEvent
}

func (r *recorder) emit(_ context.Context, e core.AuditEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) snapshot() []core.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.AuditEvent, len(r.events))
	copy(out, r.events)
	return out
}

func setupRegistryWithAudit(t *testing.T) (*core.Registry, *recorder) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fstest.MapFS{
			"finance/x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("variance"))},
		},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	rec := &recorder{}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithAudit(rec.emit)
	return reg, rec
}

// Spec: §8.1 — load_domain emits a domain.loaded audit event per call.
func TestAudit_LoadDomain(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Type != "domain.loaded" {
		t.Errorf("got %+v, want one domain.loaded event", events)
	}
	if events[0].Caller != "system:public" {
		t.Errorf("Caller = %q, want system:public for anonymous", events[0].Caller)
	}
}

// Spec: §8.1 — search_artifacts emits an artifacts.searched event;
// query / scope / type appear in context.
func TestAudit_SearchArtifacts(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "variance",
		Type:  "context",
		Scope: "finance",
	}); err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Type != "artifacts.searched" {
		t.Fatalf("got %+v, want artifacts.searched", events)
	}
	if events[0].Context["query"] != "variance" ||
		events[0].Context["scope"] != "finance" ||
		events[0].Context["type"] != "context" {
		t.Errorf("Context = %v", events[0].Context)
	}
}

// Spec: §8.1 — load_artifact emits artifact.loaded with the artifact
// ID as the target.
func TestAudit_LoadArtifact(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.LoadArtifact(context.Background(), publicID, "finance/x", core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Type != "artifact.loaded" {
		t.Fatalf("got %+v, want artifact.loaded", events)
	}
	if events[0].Target != "finance/x" {
		t.Errorf("Target = %q, want finance/x", events[0].Target)
	}
}

// Spec: §4.7.5 — every read event carries the resolved layer
// composition and the result size.
func TestAudit_RecordsResolvedLayersAndResultSize(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)

	if _, err := reg.LoadArtifact(context.Background(), publicID, "finance/x", core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if _, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{Query: "variance"}); err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if _, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}

	byType := map[string]core.AuditEvent{}
	for _, e := range rec.snapshot() {
		byType[e.Type] = e
	}

	load := byType["artifact.loaded"]
	if load.ResultSize != 1 {
		t.Errorf("artifact.loaded ResultSize = %d, want 1", load.ResultSize)
	}
	if len(load.ResolvedLayers) != 1 || load.ResolvedLayers[0] != "L" {
		t.Errorf("artifact.loaded ResolvedLayers = %v, want [L]", load.ResolvedLayers)
	}

	search := byType["artifacts.searched"]
	if search.ResultSize != 1 {
		t.Errorf("artifacts.searched ResultSize = %d, want 1 match", search.ResultSize)
	}
	if len(search.ResolvedLayers) != 1 || search.ResolvedLayers[0] != "L" {
		t.Errorf("artifacts.searched ResolvedLayers = %v, want [L]", search.ResolvedLayers)
	}

	// load_domain over the root renders at least the finance subdomain.
	dom := byType["domain.loaded"]
	if dom.ResultSize < 1 {
		t.Errorf("domain.loaded ResultSize = %d, want >= 1 (subdomains+notable)", dom.ResultSize)
	}
}

// Spec: §4.7.5 / §4.6 — the resolved layer composition records only the
// layers visible to the caller, in precedence order (lowest first).
func TestAudit_ResolvedLayersReflectEffectiveView(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rec := &recorder{}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "pub", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "alice-personal", Visibility: layer.Visibility{Users: []string{"alice@acme.com"}}, Precedence: 2},
	}).WithAudit(rec.emit)

	// bob is authenticated but not a member of alice-personal: he sees
	// only the public layer.
	bob := layer.Identity{Sub: "bob@acme.com", Email: "bob@acme.com", IsAuthenticated: true}
	if _, err := reg.LoadDomain(context.Background(), bob, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain bob: %v", err)
	}
	// alice sees both layers, ordered by precedence (pub < alice-personal).
	alice := layer.Identity{Sub: "alice@acme.com", Email: "alice@acme.com", IsAuthenticated: true}
	if _, err := reg.LoadDomain(context.Background(), alice, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain alice: %v", err)
	}

	evs := rec.snapshot()
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if got := evs[0].ResolvedLayers; len(got) != 1 || got[0] != "pub" {
		t.Errorf("bob ResolvedLayers = %v, want [pub]", got)
	}
	if got := evs[1].ResolvedLayers; len(got) != 2 || got[0] != "pub" || got[1] != "alice-personal" {
		t.Errorf("alice ResolvedLayers = %v, want [pub alice-personal]", got)
	}
}

// Spec: §4.5.5 line 540 — the domain.loaded event records the resolved
// render depth and the fold decisions per call. A plain root
// load reports the default depth, no cap, no folds, and no pass-through
// collapse.
func TestAudit_LoadDomainRecordsDepthAndFoldDecisions(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	ev := rec.snapshot()[0]
	want := map[string]string{
		"depth":                 strconv.Itoa(core.DefaultMaxDepth),
		"depth_capped":          "false",
		"folded_subdomains":     "0",
		"passthrough_collapsed": "false",
	}
	for k, v := range want {
		if ev.Context[k] != v {
			t.Errorf("Context[%q] = %q, want %q (full: %v)", k, ev.Context[k], v, ev.Context)
		}
	}
}

// Spec: §4.5.5 / §8 — a caller depth above the resolved ceiling is recorded
// as the capped depth with depth_capped=true.
func TestAudit_LoadDomainRecordsDepthCap(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{Depth: 99}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	ev := rec.snapshot()[0]
	if ev.Context["depth"] != strconv.Itoa(core.DefaultMaxDepth) {
		t.Errorf("depth = %q, want capped to %d", ev.Context["depth"], core.DefaultMaxDepth)
	}
	if ev.Context["depth_capped"] != "true" {
		t.Errorf("depth_capped = %q, want true", ev.Context["depth_capped"])
	}
}

// Spec: §4.5.5 / §8 — fold_below_artifacts collapses sparse subdomains and
// the count is recorded in the fold summary. The nested registry
// has two sparse subdomains (risk, close-reporting) below the threshold.
func TestAudit_LoadDomainRecordsFoldedSubdomainCount(t *testing.T) {
	t.Parallel()
	reg := nestedRegistry(t)
	rec := &recorder{}
	reg.WithAudit(rec.emit)
	if _, err := reg.LoadDomain(context.Background(), publicID, "finance", core.LoadDomainOptions{FoldBelowArtifacts: 2}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	ev := rec.snapshot()[0]
	if ev.Context["folded_subdomains"] != "2" {
		t.Errorf("folded_subdomains = %q, want 2 (full: %v)", ev.Context["folded_subdomains"], ev.Context)
	}
}

// Spec: §4.5.5 / §8 — a collapsed single-child pass-through chain sets
// passthrough_collapsed=true in the fold summary.
func TestAudit_LoadDomainRecordsPassthroughCollapse(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, leaf := range []string{"x", "y"} {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: "a/b/c/d/" + leaf,
			Version: "1.0.0", ContentHash: "sha256:" + leaf, Type: "skill", Layer: "L",
		}); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	rec := &recorder{}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}},
	}).WithAudit(rec.emit)
	if _, err := reg.LoadDomain(context.Background(), publicID, "a", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	ev := rec.snapshot()[0]
	if ev.Context["passthrough_collapsed"] != "true" {
		t.Errorf("passthrough_collapsed = %q, want true (full: %v)", ev.Context["passthrough_collapsed"], ev.Context)
	}
}

// Spec: §8.1 — authenticated callers record their sub claim, not
// system:public.
func TestAudit_AuthenticatedCallerRecordsSub(t *testing.T) {
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	id := layer.Identity{Sub: "joan", IsAuthenticated: true}
	if _, err := reg.LoadDomain(context.Background(), id, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Caller != "joan" {
		t.Errorf("Caller = %q, want joan", events[0].Caller)
	}
}
