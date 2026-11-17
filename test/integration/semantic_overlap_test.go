package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// overlapWords positions the words the overlap fixtures use: invoices and
// payables are close (cosine about 0.99), and onboarding is far from both.
var overlapWords = map[string][]float32{
	"invoices":   {1, 0, 0, 0},
	"payables":   {0.99, 0.12, 0, 0},
	"onboarding": {0, 0, 1, 0},
	"secretive":  {0.98, 0, 0.2, 0},
}

// overlapEmbedder maps a projection to the vector of the word it contains.
type overlapEmbedder struct{ model string }

func (overlapEmbedder) ID() string      { return "overlap" }
func (e overlapEmbedder) Model() string { return e.model }
func (overlapEmbedder) Dimensions() int { return 4 }
func (overlapEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{0, 0, 0, 1}
		for w, v := range overlapWords {
			if strings.Contains(text, w) {
				out[i] = v
			}
		}
	}
	return out, nil
}

// countingIndex is the memory backend that counts index queries and fails
// them on demand, so a test can assert a read issues none and a failed query
// keeps the stored records.
type countingIndex struct {
	*vector.Memory
	queries atomic.Int64
	fail    atomic.Bool
}

func (c *countingIndex) QueryModel(ctx context.Context, tenant string, vec []float32, k int, model string) ([]vector.Match, error) {
	c.queries.Add(1)
	if c.fail.Load() {
		return nil, errors.New("index unreachable")
	}
	return c.Memory.QueryModel(ctx, tenant, vec, k, model)
}

// overlapHarness wires a SQLite store, a collocated memory index, the reingest
// endpoint, and the registry server the way serverboot does for a collocated
// backend with PODIUM_LINT_SEMANTIC_OVERLAP on.
type overlapHarness struct {
	t      *testing.T
	tenant string
	st     store.Store
	idx    *countingIndex
	reg    *core.Registry
	layers *httptest.Server
	api    *httptest.Server
	mode   *server.ModeTracker
	dirs   map[string]string
}

func newOverlapHarness(t *testing.T, opts ...server.Option) *overlapHarness {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return newOverlapHarnessOn(t, st, "t", opts...)
}

// newOverlapHarnessOn builds the harness over st in tenant.
func newOverlapHarnessOn(t *testing.T, st store.Store, tenant string, opts ...server.Option) *overlapHarness {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	h := &overlapHarness{t: t, tenant: tenant, st: st, idx: &countingIndex{Memory: vector.NewMemory(4)}, mode: server.NewModeTracker(), dirs: map[string]string{}}
	emb := overlapEmbedder{model: "m1"}
	h.reg = core.New(st, tenant, nil).WithVectorSearch(h.idx, emb).
		WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	for _, l := range []store.LayerConfig{
		{TenantID: tenant, ID: "team", SourceType: "local", Public: true},
		{TenantID: tenant, ID: "secret", SourceType: "local", Groups: []string{"eng"}, Order: 1},
	} {
		l.LocalPath = t.TempDir()
		h.dirs[l.ID] = l.LocalPath
		if err := st.PutLayerConfig(ctx, l); err != nil {
			t.Fatalf("PutLayerConfig: %v", err)
		}
	}
	runner := func(ctx context.Context, lc store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return ingest.SourceIngestWithOptions(ctx, st, source.Local{}, lc, ingest.SourceIngestOptions{
			Embedder: func(ctx context.Context, text string) ([]float32, error) {
				v, err := emb.Embed(ctx, []string{text})
				return v[0], err
			},
			VectorPut: func(ctx context.Context, tenant, id, ver string, v []float32) error {
				return h.idx.PutModel(ctx, tenant, id, ver, v, emb.Model())
			},
			SemanticOverlap: func(ctx context.Context, tenant, layerID string, stored []ingest.IngestedArtifact) []lint.Diagnostic {
				keys := make([]vector.RowKey, len(stored))
				for i, a := range stored {
					keys[i] = vector.RowKey{ArtifactID: a.ArtifactID, Version: a.Version}
				}
				return h.reg.IngestSemanticOverlap(core.ContextWithTenant(ctx, tenant), layerID, keys)
			},
		})
	}
	h.layers = httptest.NewServer(server.NewLayerEndpoint(st, tenant, h.mode).WithReingestRunner(runner).Handler())
	t.Cleanup(h.layers.Close)
	all := append([]server.Option{
		server.WithMode(h.mode),
		server.WithIdentityResolver(func(r *http.Request) layer.Identity {
			id := layer.Identity{Sub: r.Header.Get("X-Test-User"), IsAuthenticated: true}
			if g := r.Header.Get("X-Test-Group"); g != "" {
				id.Groups = []string{g}
			}
			return id
		}),
	}, opts...)
	h.api = httptest.NewServer(server.New(h.reg, all...).Handler())
	t.Cleanup(h.api.Close)
	return h
}

// write stores an agent artifact in a layer's source directory.
func (h *overlapHarness) write(layerID, id, version, desc string) {
	h.t.Helper()
	dir := filepath.Join(h.dirs[layerID], id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatalf("mkdir: %v", err)
	}
	body := fmt.Sprintf("---\ntype: agent\nversion: %s\ndescription: %s\nsensitivity: low\n---\n\nbody\n", version, desc)
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		h.t.Fatalf("write: %v", err)
	}
}

type reingestBody struct {
	Accepted   int `json:"accepted"`
	Advisories []struct {
		ArtifactID string `json:"artifact_id"`
		Code       string `json:"code"`
		Message    string `json:"message"`
	} `json:"advisories"`
}

func (h *overlapHarness) reingest(layerID string) reingestBody {
	h.t.Helper()
	resp, err := http.Post(h.layers.URL+"/v1/layers/reingest?id="+layerID, "application/json", nil)
	if err != nil {
		h.t.Fatalf("reingest: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body reingestBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("reingest %s: status %d %+v", layerID, resp.StatusCode, body)
	}
	return body
}

func (b reingestBody) semantic(id string) string {
	for _, a := range b.Advisories {
		if a.ArtifactID == id && a.Code == lint.SemanticOverlapCode {
			return a.Message
		}
	}
	return ""
}

type overlapResponse struct {
	SemanticOverlap struct {
		State     string   `json:"state"`
		Threshold *float64 `json:"threshold"`
		Model     *string  `json:"model"`
	} `json:"semantic_overlap"`
	Diagnostics []struct {
		ArtifactID string `json:"artifact_id"`
		Code       string `json:"code"`
		Message    string `json:"message"`
		Peers      []struct {
			ArtifactID string  `json:"artifact_id"`
			Similarity float64 `json:"similarity"`
			ComputedAt *string `json:"computed_at"`
		} `json:"peers"`
	} `json:"diagnostics"`
	Counts struct {
		Warning int `json:"warning"`
	} `json:"counts"`
	Code string `json:"code"`
}

func (h *overlapHarness) overlap(query, user, group string) (int, overlapResponse) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.api.URL+"/v1/overlap"+query, nil)
	req.Header.Set("X-Test-User", user)
	req.Header.Set("X-Test-Group", group)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("GET /v1/overlap: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body overlapResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func (r overlapResponse) semanticPeers(id string) string {
	for _, d := range r.Diagnostics {
		if d.ArtifactID == id && d.Code == lint.SemanticOverlapCode {
			ids := make([]string, 0, len(d.Peers))
			for _, p := range d.Peers {
				ids = append(ids, p.ArtifactID)
			}
			return strings.Join(ids, ",")
		}
	}
	return ""
}

// Spec: §7.3.1 — an ingest that stores new versions stores a record for each
// neighbour whatever its similarity, reports lint.semantic_overlap for a peer
// at or above the threshold among its advisories and not for one below it,
// and never names a peer of a layer restricted to groups or users.
func TestSemanticOverlap_IngestStoresRecordsAndReportsAdvisory(t *testing.T) {
	t.Parallel()
	checkIngestStoresRecords(t, newOverlapHarness(t))
}

// Spec: §7.3.1 — the metadata store keeps the overlap records on Postgres as
// on SQLite, so the same ingest stores and reports the same overlap.
func TestSemanticOverlap_IngestOnPostgres(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping the Postgres-backed overlap ingest")
	}
	pg, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres: %v (database unreachable)", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	tenant := fmt.Sprintf("ovl%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = pg.DropOrg(context.Background(), tenant) })
	checkIngestStoresRecords(t, newOverlapHarnessOn(t, pg, tenant))
}

func checkIngestStoresRecords(t *testing.T, h *overlapHarness) {
	t.Helper()
	h.write("secret", "sec/twin", "1.0.0", "secretive twin of the invoice skill")
	h.reingest("secret")
	h.write("team", "fin/invoices", "1.0.0", "reconciles invoices")
	h.write("team", "fin/payables", "1.0.0", "reconciles payables")
	h.write("team", "hr/onboard", "1.0.0", "runs onboarding")
	res := h.reingest("team")
	if res.Accepted != 3 {
		t.Fatalf("accepted = %d, want 3", res.Accepted)
	}
	msg := res.semantic("fin/invoices")
	if !strings.Contains(msg, "fin/payables (") || strings.Contains(msg, "sec/twin") || strings.Contains(msg, "hr/onboard") {
		t.Errorf("fin/invoices advisory = %q, want fin/payables only", msg)
	}
	if res.semantic("hr/onboard") != "" {
		t.Errorf("hr/onboard drew an advisory below the threshold: %q", res.semantic("hr/onboard"))
	}
	recs, err := h.st.ListOverlapRecords(context.Background(), h.tenant)
	if err != nil {
		t.Fatalf("ListOverlapRecords: %v", err)
	}
	// Every pair among the four artifacts is a neighbour within 10 rows.
	if len(recs) != 6 {
		t.Errorf("stored %d records, want 6 (one per pair, whatever the similarity)", len(recs))
	}
}

// Spec: §7.3.1 — GET /v1/overlap reports stored records whose two artifacts
// the caller can see, so two callers with different visibility get different
// reports and a hidden artifact is never named, counted, or listed as a peer;
// a different threshold changes the report and issues no index query.
func TestSemanticOverlap_ReportVisibilityAndThreshold(t *testing.T) {
	t.Parallel()
	h := newOverlapHarness(t)
	h.write("secret", "sec/twin", "1.0.0", "secretive twin of the invoice skill")
	h.reingest("secret")
	h.write("team", "fin/invoices", "1.0.0", "reconciles invoices")
	h.write("team", "fin/payables", "1.0.0", "reconciles payables")
	h.reingest("team")
	before := h.idx.queries.Load()

	status, alice := h.overlap("", "alice", "")
	_, bob := h.overlap("", "bob", "eng")
	if status != http.StatusOK || alice.SemanticOverlap.State != "on" || *alice.SemanticOverlap.Model != "m1" {
		t.Fatalf("alice report: status %d %+v", status, alice.SemanticOverlap)
	}
	if got := alice.semanticPeers("fin/invoices"); got != "fin/payables" {
		t.Errorf("alice fin/invoices peers = %q, want fin/payables alone", got)
	}
	if got := bob.semanticPeers("fin/invoices"); !strings.Contains(got, "sec/twin") {
		t.Errorf("bob fin/invoices peers = %q, want sec/twin included", got)
	}
	for _, d := range alice.Diagnostics {
		if strings.Contains(d.Message, "sec/twin") || d.ArtifactID == "sec/twin" {
			t.Errorf("alice report names the hidden artifact: %+v", d)
		}
	}
	_, strict := h.overlap("?threshold=1.0", "alice", "")
	if strict.semanticPeers("fin/invoices") != "" || *strict.SemanticOverlap.Threshold != 1.0 {
		t.Errorf("at threshold 1.0 the report still names peers: %+v", strict)
	}
	_, one := h.overlap("?artifact=fin/payables", "bob", "eng")
	for _, d := range one.Diagnostics {
		if d.ArtifactID != "fin/payables" {
			t.Errorf("artifact filter returned %s", d.ArtifactID)
		}
	}
	if one.Counts.Warning != len(one.Diagnostics) || len(one.Diagnostics) == 0 {
		t.Errorf("artifact filter counts %+v for %d diagnostics", one.Counts, len(one.Diagnostics))
	}
	if after := h.idx.queries.Load(); after != before {
		t.Errorf("the report issued %d index queries, want none", after-before)
	}
	if status, bad := h.overlap("?threshold=0.2", "alice", ""); status != http.StatusBadRequest || bad.Code != "registry.invalid_argument" {
		t.Errorf("threshold 0.2: status %d code %q, want 400 registry.invalid_argument", status, bad.Code)
	}
}

// Spec: §7.3.1 — a failed index query keeps the artifact's existing records,
// produces no ingest diagnostic for it, and does not fail the cycle.
func TestSemanticOverlap_FailedQueryKeepsRecords(t *testing.T) {
	t.Parallel()
	h := newOverlapHarness(t)
	h.write("team", "fin/invoices", "1.0.0", "reconciles invoices")
	h.write("team", "fin/payables", "1.0.0", "reconciles payables")
	h.reingest("team")
	h.idx.fail.Store(true)
	h.write("team", "fin/invoices", "1.1.0", "reconciles invoices again")
	res := h.reingest("team")
	if res.Accepted != 1 || res.semantic("fin/invoices") != "" {
		t.Errorf("cycle with a failing index: accepted %d, advisory %q", res.Accepted, res.semantic("fin/invoices"))
	}
	recs, _ := h.st.ListOverlapRecords(context.Background(), h.tenant)
	if len(recs) != 1 || recs[0].VersionA != "1.0.0" {
		t.Errorf("records = %+v, want the 1.0.0 record kept", recs)
	}
}

// Spec: §7.3.1, §13.2.1 — a read-only registry serves GET /v1/overlap and
// rejects the rebuild write with registry.read_only.
func TestSemanticOverlap_ReadOnlyRegistry(t *testing.T) {
	t.Parallel()
	h := newOverlapHarness(t, server.WithUnauthenticatedReembed())
	h.write("team", "fin/invoices", "1.0.0", "reconciles invoices")
	h.write("team", "fin/payables", "1.0.0", "reconciles payables")
	h.reingest("team")
	h.mode.Set(server.ModeReadOnly)
	status, body := h.overlap("", "alice", "")
	if status != http.StatusOK || body.semanticPeers("fin/invoices") != "fin/payables" {
		t.Errorf("read-only GET: status %d peers %q", status, body.semanticPeers("fin/invoices"))
	}
	if status, code := postRebuild(t, h.api.URL, "alice"); status != http.StatusServiceUnavailable || code != "registry.read_only" {
		t.Errorf("read-only rebuild: status %d code %q", status, code)
	}
}

func postRebuild(t *testing.T, base, user string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/admin/overlap/rebuild", nil)
	req.Header.Set("X-Test-User", user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST rebuild: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Code     string `json:"code"`
		Computed *int   `json:"computed"`
		Failed   *int   `json:"failed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode == http.StatusOK && (body.Computed == nil || body.Failed == nil) {
		t.Errorf("rebuild answered 200 without {computed, failed}")
	}
	return resp.StatusCode, body.Code
}

// Spec: §7.3.1, §4.7 — the rebuild is authorized as re-embed is: a caller
// without the admin role is rejected with auth.forbidden, an admin computes
// every served artifact, and a deployment that authenticates no caller
// admits the request.
func TestSemanticOverlap_RebuildAuthorization(t *testing.T) {
	t.Parallel()
	gated := newOverlapHarness(t)
	gated.write("team", "fin/invoices", "1.0.0", "reconciles invoices")
	gated.reingest("team")
	if err := gated.st.GrantAdmin(context.Background(), store.AdminGrant{UserID: "carol", OrgID: gated.tenant}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	if status, code := postRebuild(t, gated.api.URL, "alice"); status != http.StatusForbidden || code != "auth.forbidden" {
		t.Errorf("non-admin rebuild: status %d code %q, want 403 auth.forbidden", status, code)
	}
	if status, _ := postRebuild(t, gated.api.URL, "carol"); status != http.StatusOK {
		t.Errorf("admin rebuild: status %d, want 200", status)
	}
	open := newOverlapHarness(t, server.WithUnauthenticatedReembed())
	if status, _ := postRebuild(t, open.api.URL, ""); status != http.StatusOK {
		t.Errorf("unauthenticated-deployment rebuild: status %d, want 200", status)
	}
}

// Spec: §7.3.1 — the report and the rebuild accept only their own methods.
func TestSemanticOverlap_Methods(t *testing.T) {
	t.Parallel()
	h := newOverlapHarness(t, server.WithUnauthenticatedReembed())
	for path, method := range map[string]string{"/v1/overlap": http.MethodPost, "/v1/admin/overlap/rebuild": http.MethodGet} {
		req, _ := http.NewRequest(method, h.api.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status %d, want 405", method, path, resp.StatusCode)
		}
	}
}
