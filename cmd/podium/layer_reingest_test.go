package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 — `podium layer register --force-push-policy strict`
// persists the policy on the layer config.
func TestLayerRegisterCmd_ForcePushPolicy(t *testing.T) {
	const tenantID = "default"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	ts := httptest.NewServer(server.NewLayerEndpoint(st, tenantID, server.NewModeTracker()).Handler())
	t.Cleanup(ts.Close)

	rc := layerRegister([]string{
		"--registry", ts.URL, "--id", "team",
		"--repo", "git@example/team.git", "--ref", "main",
		"--force-push-policy", "strict",
	})
	if rc != 0 {
		t.Fatalf("layerRegister rc = %d, want 0", rc)
	}
	got, err := st.GetLayerConfig(context.Background(), tenantID, "team")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.ForcePushPolicy != "strict" {
		t.Errorf("ForcePushPolicy = %q, want strict", got.ForcePushPolicy)
	}
}

// Spec: §4.7.2 — `--break-glass` without `--justification` is a
// client-side argument error (rc 2) and never contacts the registry.
func TestLayerReingestCmd_BreakGlassRequiresJustification(t *testing.T) {
	rc := layerReingest([]string{
		"--registry", "http://127.0.0.1:0", "--break-glass", "team",
	})
	if rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
}

// Spec: §4.7.2 — the break-glass flags reach the server as a request
// body the reingest runner receives (justification + approvers).
func TestLayerReingestCmd_BreakGlassBodySent(t *testing.T) {
	const tenantID = "default"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: tenantID, ID: "team", SourceType: "local", LocalPath: "/tmp/x",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var gotBG *server.BreakGlass
	runner := func(_ context.Context, _ store.LayerConfig, bg *server.BreakGlass) (*ingest.Result, error) {
		gotBG = bg
		return &ingest.Result{}, nil
	}
	endpoint := server.NewLayerEndpoint(st, tenantID, server.NewModeTracker()).WithReingestRunner(runner)
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	rc := layerReingest([]string{
		"--registry", ts.URL,
		"--break-glass", "--justification", "year-end hotfix",
		"--approver", "alice@acme.com", "--approver", "bob@acme.com",
		"team",
	})
	if rc != 0 {
		t.Fatalf("layerReingest rc = %d, want 0", rc)
	}
	if gotBG == nil || gotBG.Justification != "year-end hotfix" || len(gotBG.Approvers) != 2 {
		t.Errorf("break-glass grant not threaded: %+v", gotBG)
	}
}

// Spec: §7.3.1 — a completed ingest cycle classifies each artifact as
// accepted, unchanged, or dropped, and the report names each conflicted and
// each rejected artifact on standard error with its identifier, its §6.10
// code, and its reason.
func TestWriteIngestReport_Classification(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantDropped int
		wantOK      bool
		wantStdout  []string
		wantStderr  []string
		emptyOut    bool
		emptyErr    bool
	}{
		{
			// The shipped gate on len(artifacts) > 0 skipped this body entirely
			// and printed the raw JSON to stdout with exit 0.
			name: "rejected only",
			body: `{"layer":"finance","accepted":0,"idempotent":0,"artifacts":[],
				"rejected":[{"artifact_id":"finance/ap/pay-invoice","code":"ingest.collision",
				"reason":"artifact id already contributed by layer core"}]}`,
			wantDropped: 1,
			wantOK:      true,
			wantStderr: []string{
				"rejected: finance/ap/pay-invoice (ingest.collision): artifact id already contributed by layer core\n",
			},
			emptyOut: true,
		},
		{
			// A registry with no ingest runner wired answers with a queue-only
			// acknowledgement. It carries `queued` and `queued_at` exactly as a
			// cycle report does, so the absence of `accepted` is what separates
			// the two.
			name:     "runner-less acknowledgement",
			body:     `{"queued":"my-layer","queued_at":"2026-09-08T10:00:00Z"}`,
			wantOK:   false,
			emptyOut: true,
			emptyErr: true,
		},
		{
			name:     "body is not JSON",
			body:     "reingest queued\n",
			wantOK:   false,
			emptyOut: true,
			emptyErr: true,
		},
		{
			// The handler answers 200 rather than 409 here, because the 409 arm
			// requires nothing accepted and nothing unchanged.
			name: "mixed conflict",
			body: `{"layer":"team","accepted":1,"idempotent":0,
				"artifacts":[{"id":"ops/oncall","version":"1.0.0","status":"accepted"}],
				"conflicts":[{"artifact_id":"ops/runbook","version":"2.1.0",
				"old_hash":"aaa","new_hash":"bbb","code":"ingest.immutable_violation"}]}`,
			wantDropped: 1,
			wantOK:      true,
			wantStdout:  []string{"artifact: ops/oncall@1.0.0   layer: team\n"},
			wantStderr:  []string{"conflict: ops/runbook@2.1.0 rejected (ingest.immutable_violation); bump the version\n"},
		},
		{
			// Neither a §3.3 advisory nor a §4.7 embedding failure is a drop:
			// the artifact is stored and served in both cases.
			name: "advisory and embedding failure are not drops",
			body: `{"layer":"team","accepted":1,"idempotent":0,
				"artifacts":[{"id":"ops/oncall","version":"1.0.0","status":"accepted"}],
				"advisories":[{"artifact_id":"ops/oncall","code":"lint.thin_description",
				"severity":"warning","message":"description is thin"}],
				"embedding_failures":[{"artifact_id":"ops/oncall","version":"1.0.0",
				"reason":"embedding provider unreachable"}]}`,
			wantDropped: 0,
			wantOK:      true,
			wantStdout: []string{
				"artifact: ops/oncall@1.0.0   layer: team\n",
				"advisory: ops/oncall [warning] description is thin (lint.thin_description)\n",
			},
			wantStderr: []string{"embedding failure: ops/oncall@1.0.0: embedding provider unreachable\n"},
		},
		{
			// lint_failures counts diagnostics rather than artifacts: res.LintFailures
			// in pkg/registry/ingest/ingest.go collects every diagnostic raised
			// against each lint-rejected artifact, and the handler's lint_failures
			// field in pkg/registry/server/layers.go emits that slice's length. One
			// artifact with two diagnostics reports 2, so the number is a diagnostic
			// count and no reader may turn it into an artifact count.
			name: "mixed lint",
			body: `{"layer":"team","accepted":1,"idempotent":0,"lint_failures":2,
				"artifacts":[{"id":"ops/oncall","version":"1.0.0","status":"accepted"}]}`,
			wantDropped: 2,
			wantOK:      true,
			wantStdout:  []string{"artifact: ops/oncall@1.0.0   layer: team\n"},
			wantStderr:  []string{"lint failures: 2\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			dropped, ok := writeIngestReport(&stdout, &stderr, "requested-layer", []byte(tc.body))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if dropped != tc.wantDropped {
				t.Errorf("dropped = %d, want %d", dropped, tc.wantDropped)
			}
			// The rendered text is asserted rather than the counts alone, so a
			// dropped JSON tag on a nested row (which renders as
			// "rejected:  (): ") fails the test.
			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
				}
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
				}
			}
			if tc.emptyOut && stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if tc.emptyErr && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

// Spec: §7.3.1 — `podium layer reingest <id>` exits non-zero when the cycle
// dropped at least one artifact, and exits zero on a clean cycle.
func TestLayerReingestCmd_ExitsOneOnDroppedArtifacts(t *testing.T) {
	cases := []struct {
		name   string
		result ingest.Result
		want   int
	}{
		{
			name: "rejected only",
			result: ingest.Result{
				Rejected: []ingest.RejectedArtifact{{
					ArtifactID: "finance/ap/pay-invoice",
					Code:       "ingest.public_mode_rejects_sensitive",
					Reason:     "sensitivity medium exceeds the public-mode floor",
				}},
			},
			want: 1,
		},
		{
			name: "mixed accepted and rejected",
			result: ingest.Result{
				Accepted: 1,
				Ingested: []ingest.IngestedArtifact{{ArtifactID: "ops/oncall", Version: "1.0.0", Accepted: true}},
				Rejected: []ingest.RejectedArtifact{{
					ArtifactID: "finance/ap/pay-invoice",
					Code:       "ingest.public_mode_rejects_sensitive",
					Reason:     "sensitivity medium exceeds the public-mode floor",
				}},
			},
			want: 1,
		},
		{
			// The 409 arm requires nothing accepted and nothing unchanged, so
			// this snapshot is answered 200 and the transport arm never sees it.
			name: "mixed same-version conflict",
			result: ingest.Result{
				Accepted:  1,
				Ingested:  []ingest.IngestedArtifact{{ArtifactID: "ops/oncall", Version: "1.0.0", Accepted: true}},
				Conflicts: []ingest.ConflictReport{{ArtifactID: "ops/runbook", Version: "2.1.0", OldHash: "aaa", NewHash: "bbb"}},
			},
			want: 1,
		},
		{
			name: "clean cycle",
			result: ingest.Result{
				Accepted: 1,
				Ingested: []ingest.IngestedArtifact{{ArtifactID: "ops/oncall", Version: "1.0.0", Accepted: true}},
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const tenantID = "default"
			st := store.NewMemory()
			if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
				t.Fatalf("CreateTenant: %v", err)
			}
			if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
				TenantID: tenantID, ID: "team", SourceType: "local", LocalPath: "/tmp/x",
				CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			res := tc.result
			runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
				return &res, nil
			}
			endpoint := server.NewLayerEndpoint(st, tenantID, server.NewModeTracker()).WithReingestRunner(runner)
			ts := httptest.NewServer(endpoint.Handler())
			t.Cleanup(ts.Close)

			var rc int
			// Both process-wide streams are swapped, because the command writes
			// its report straight to os.Stdout and os.Stderr.
			_ = captureStdout(t, func() {
				_ = captureStderr(t, func() {
					rc = layerReingest([]string{"--registry", ts.URL, "team"})
				})
			})
			if rc != tc.want {
				t.Errorf("layerReingest rc = %d, want %d", rc, tc.want)
			}
		})
	}
}
