package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.11 (Ingest verification), §7.3.1 (Errors) — a reingest that an
// attestation step refuses is answered with 422, the refusal's code and
// details, and the code's suggested action. ingest.attestation_root_stale
// alone is retryable, because a refresh of the root clears it.
//
// Matrix: §6.10 (ingest.attestation_missing)
// Matrix: §6.10 (ingest.attestation_mismatch)
// Matrix: §6.10 (ingest.attestation_stale)
// Matrix: §6.10 (ingest.attestation_invalid)
// Matrix: §6.10 (ingest.attestation_policy_invalid)
// Matrix: §6.10 (ingest.attestation_root_stale)
func TestReingest_AttestationRefusals(t *testing.T) {
	t.Parallel()
	for _, code := range []string{
		ingest.CodeAttestationMissing, ingest.CodeAttestationMismatch, ingest.CodeAttestationStale,
		ingest.CodeAttestationInvalid, ingest.CodeAttestationPolicyInvalid, ingest.CodeAttestationRootStale,
	} {
		runner := func(context.Context, store.LayerConfig, *server.BreakGlass) (*ingest.Result, error) {
			return nil, &ingest.AttestationError{Code: code, Refusal: true, Message: code + ": refused",
				Details: map[string]any{"constraint": "c"}}
		}
		base, cleanup := newRunnerHarness(t, runner)
		resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Code            string         `json:"code"`
			Details         map[string]any `json:"details"`
			Retryable       bool           `json:"retryable"`
			SuggestedAction string         `json:"suggested_action"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		_ = resp.Body.Close()
		cleanup()
		if resp.StatusCode != http.StatusUnprocessableEntity || env.Code != code || env.Details["constraint"] != "c" ||
			env.SuggestedAction == "" || env.Retryable != (code == ingest.CodeAttestationRootStale) {
			t.Errorf("%s: status %d, envelope %+v", code, resp.StatusCode, env)
		}
	}
}

// Spec: §4.7.11 (Mark reset), §7.3.1 — a reingest body that asks for the mark
// reset reaches the runner with the reset and its commit in lower case; a
// to_commit without the reset, one on a layer whose source answers no
// lineage query, one longer than 128 bytes, and one that is not a full
// hexadecimal commit name are refused with registry.invalid_argument before
// the runner runs.
func TestReingest_MarkResetBody(t *testing.T) {
	t.Parallel()
	var got []*ingest.MarkReset
	runner := func(ctx context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		got = append(got, server.MarkResetFromContext(ctx))
		return &ingest.Result{Removed: []string{}}, nil
	}
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	for _, lc := range []store.LayerConfig{
		{TenantID: "t", ID: "git-layer", SourceType: "git", Repo: "https://github.com/acme/layer", Ref: "main"},
		{TenantID: "t", ID: "local-layer", SourceType: "local", LocalPath: "/tmp/x"},
	} {
		if err := st.PutLayerConfig(context.Background(), lc); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(server.NewLayerEndpoint(st, "t", server.NewModeTracker()).WithReingestRunner(runner).Handler())
	defer ts.Close()
	srv := ts.URL
	post := func(id, body string) int {
		resp, err := http.Post(srv+"/v1/layers/reingest?id="+id, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	commit := strings.Repeat("A", 40)
	for _, tc := range []struct {
		id, body string
		status   int
	}{
		{"git-layer", `{"reset_attestation_mark":true,"to_commit":"` + commit + `"}`, 200},
		{"local-layer", `{"reset_attestation_mark":true}`, 200},
		{"git-layer", `{}`, 200},
		{"git-layer", `{"to_commit":"` + commit + `"}`, 400},
		{"local-layer", `{"reset_attestation_mark":true,"to_commit":"` + commit + `"}`, 400},
		{"git-layer", `{"reset_attestation_mark":true,"to_commit":"` + strings.Repeat("a", 129) + `"}`, 400},
		{"git-layer", `{"reset_attestation_mark":true,"to_commit":"abc123"}`, 400},
		{"git-layer", `{"reset_attestation_mark":true,"to_commit":"` + strings.Repeat("g", 40) + `"}`, 400},
	} {
		if status := post(tc.id, tc.body); status != tc.status {
			t.Errorf("%s %s: status %d, want %d", tc.id, tc.body, status, tc.status)
		}
	}
	if len(got) != 3 || got[0].ToCommit != strings.ToLower(commit) || got[1] == nil || got[1].ToCommit != "" || got[2] != nil {
		t.Fatalf("resets %+v", got)
	}
}
