package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// newRunnerHarness serves a layer endpoint backed by a memory store with the
// supplied reingest runner wired. It seeds one local layer so reingest
// resolves a config.
func newRunnerHarness(t *testing.T, runner server.ReingestRunner) (string, func()) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: "t", ID: "team-shared", SourceType: "local", LocalPath: "/tmp/x",
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	e := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).WithReingestRunner(runner)
	ts := httptest.NewServer(e.Handler())
	return ts.URL, ts.Close
}

// spec: §7.3.1 (F-7.3.4) — with a runner wired, reingest runs the pipeline and
// returns the result summary (accepted/idempotent counts).
func TestReingest_RunsPipelineAndReturnsSummary(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, cfg store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return &ingest.Result{Accepted: 3, Idempotent: 1}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()

	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if m["accepted"] != float64(3) {
		t.Errorf("accepted = %v, want 3", m["accepted"])
	}
	if m["queued"] != "team-shared" {
		t.Errorf("queued = %v, want team-shared", m["queued"])
	}
}

// spec: §4.7.2 (F-7.3.9) — a break-glass body is parsed and threaded to the
// runner with its justification and approvers.
func TestReingest_BreakGlassThreadedToRunner(t *testing.T) {
	t.Parallel()
	var gotBG *server.BreakGlass
	runner := func(_ context.Context, _ store.LayerConfig, bg *server.BreakGlass) (*ingest.Result, error) {
		gotBG = bg
		return &ingest.Result{}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()

	body, _ := json.Marshal(map[string]any{
		"break_glass":   true,
		"justification": "incident-7",
		"approvers":     []string{"alice@acme.com", "bob@acme.com"},
	})
	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotBG == nil {
		t.Fatalf("runner received nil break-glass grant")
	}
	if gotBG.Justification != "incident-7" || len(gotBG.Approvers) != 2 {
		t.Errorf("break-glass grant = %+v", gotBG)
	}
}

// spec: §4.7.2 — break-glass without a justification is rejected before the
// pipeline runs.
func TestReingest_BreakGlassMissingJustificationRejected(t *testing.T) {
	t.Parallel()
	called := false
	runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		called = true
		return &ingest.Result{}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()

	body, _ := json.Marshal(map[string]any{"break_glass": true})
	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if called {
		t.Errorf("runner must not run without a justification")
	}
}

// spec: §6.10 — the ingest sentinels map to their structured error codes.
func TestReingest_ErrorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"frozen", ingest.ErrFrozen, http.StatusConflict, "ingest.frozen"},
		{"history", ingest.ErrHistoryRewritten, http.StatusConflict, "ingest.history_rewritten"},
		{"lint", ingest.ErrLintFailed, http.StatusUnprocessableEntity, "ingest.lint_failed"},
		{"unreachable", source.ErrSourceUnreachable, http.StatusBadGateway, "ingest.source_unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
				return nil, tc.err
			}
			base, cleanup := newRunnerHarness(t, runner)
			defer cleanup()
			resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.code {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.code)
			}
			var m map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&m)
			if m["code"] != tc.body {
				t.Errorf("code = %v, want %v", m["code"], tc.body)
			}
		})
	}
}

// spec: §7.3.1 (F-7.3.7) — force_push_policy must be one of "", tolerant, or
// strict; an unknown value is rejected, a valid one persists.
func TestRegister_ForcePushPolicyValidation(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "bad", "source_type": "git", "force_push_policy": "loose",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid policy status = %d: %s", resp.StatusCode, body)
	}

	resp, body = mustPost(t, base, "/v1/layers", map[string]any{
		"id": "good", "source_type": "git", "force_push_policy": "strict",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("valid policy status = %d: %s", resp.StatusCode, body)
	}
	got, err := st.GetLayerConfig(context.Background(), "t", "good")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.ForcePushPolicy != "strict" {
		t.Errorf("ForcePushPolicy = %q, want strict", got.ForcePushPolicy)
	}
}
