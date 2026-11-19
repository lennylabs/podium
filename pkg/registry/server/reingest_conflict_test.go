package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 — "Same version, different content_hash | Rejected as
// ingest.immutable_violation", and ingest.immutable_violation is one of the
// §7.3.1 error codes. A snapshot whose only outcome is a same-version content
// conflict must surface the named code (not an opaque count) so the author can
// see which artifact collided and bump its version.
func TestReingest_PureConflictReturnsImmutableViolation(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return &ingest.Result{
			Conflicts: []ingest.ConflictReport{
				{ArtifactID: "finance/ap/pay-invoice", Version: "1.0.0", OldHash: "sha256:aaa", NewHash: "sha256:bbb"},
			},
		}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()

	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var env struct {
		Code            string         `json:"code"`
		Message         string         `json:"message"`
		Details         map[string]any `json:"details"`
		SuggestedAction string         `json:"suggested_action"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "ingest.immutable_violation" {
		t.Errorf("code = %q, want ingest.immutable_violation", env.Code)
	}
	if env.SuggestedAction == "" {
		t.Errorf("suggested_action empty; want the §6.10 remediation hint")
	}
	conflicts, _ := env.Details["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("details.conflicts = %v, want one entry", env.Details["conflicts"])
	}
	c := conflicts[0].(map[string]any)
	if c["artifact_id"] != "finance/ap/pay-invoice" || c["version"] != "1.0.0" {
		t.Errorf("conflict identity = %v, want finance/ap/pay-invoice@1.0.0", c)
	}
	if c["code"] != "ingest.immutable_violation" {
		t.Errorf("per-conflict code = %v, want ingest.immutable_violation", c["code"])
	}
	if c["old_hash"] != "sha256:aaa" || c["new_hash"] != "sha256:bbb" {
		t.Errorf("conflict hashes = %v, want old sha256:aaa / new sha256:bbb", c)
	}
}

// Spec: §7.3.1 (Removed artifacts) — a conflict on a removed version states
// the removal rather than a content difference, and a conflict whose holder's
// hash is unknown carries no old_hash member.
func TestReingest_RemovedVersionConflictMessage(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return &ingest.Result{Conflicts: []ingest.ConflictReport{
			{ArtifactID: "ops/moved", Version: "1.0.0", OldHash: "sha256:same", NewHash: "sha256:same", Removed: true},
			{ArtifactID: "ops/raced", Version: "2.0.0", NewHash: "sha256:new"},
		}}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()
	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		Message string `json:"message"`
		Details struct {
			Conflicts []map[string]any `json:"conflicts"`
		} `json:"details"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	const want = "same-version conflict: ops/moved@1.0.0 is a removed version, which only its own layer restores, with the same content, until the registry purges it; bump the version (and 1 more)"
	if resp.StatusCode != http.StatusConflict || env.Message != want || len(env.Details.Conflicts) != 2 {
		t.Fatalf("status=%d message=%q conflicts=%v, want 409, %q, and two entries", resp.StatusCode, env.Message, env.Details.Conflicts, want)
	}
	_, unknown := env.Details.Conflicts[1]["old_hash"]
	if env.Details.Conflicts[0]["old_hash"] != "sha256:same" || unknown {
		t.Errorf("conflicts = %v, want old_hash on the removed version and none on the unknown holder", env.Details.Conflicts)
	}
}

// Spec: §7.3.1 — a mixed snapshot (some artifacts accepted alongside
// a conflict) stays a 200 partial success, mirroring the pipeline's lint
// hard-error rule, but still reports each conflict per-artifact with the named
// code so the caller can act on the rejection.
func TestReingest_MixedConflictReports200WithCode(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return &ingest.Result{
			Accepted: 2,
			Ingested: []ingest.IngestedArtifact{
				{ArtifactID: "a", Version: "2.0.0"},
				{ArtifactID: "b", Version: "1.1.0"},
			},
			Conflicts: []ingest.ConflictReport{
				{ArtifactID: "c", Version: "1.0.0", OldHash: "sha256:old", NewHash: "sha256:new"},
			},
		}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()

	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (partial success keeps the accepted artifacts)", resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["accepted"] != float64(2) {
		t.Errorf("accepted = %v, want 2", m["accepted"])
	}
	conflicts, _ := m["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, want one structured entry (not an opaque count)", m["conflicts"])
	}
	c := conflicts[0].(map[string]any)
	if c["artifact_id"] != "c" || c["code"] != "ingest.immutable_violation" {
		t.Errorf("conflict entry = %v, want artifact c with ingest.immutable_violation", c)
	}
}

// Spec: §7.3.1 — a clean snapshot reports an empty conflicts list, never the
// scalar count the field previously carried.
func TestReingest_NoConflictsReportsEmptyList(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return &ingest.Result{Accepted: 1, Ingested: []ingest.IngestedArtifact{{ArtifactID: "a", Version: "1.0.0"}}}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()

	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	conflicts, ok := m["conflicts"].([]any)
	if !ok {
		t.Fatalf("conflicts = %v (%T), want a JSON array", m["conflicts"], m["conflicts"])
	}
	if len(conflicts) != 0 {
		t.Errorf("conflicts = %v, want empty", conflicts)
	}
}

// Spec: §7.3.1 (Removed artifacts) — a cycle refused because its only
// outcome is a same-version content conflict still names, in
// details.removed, the artifacts it soft-deleted, so the author learns of
// the removal from the refusal.
func TestReingest_PureConflictNamesRemovals(t *testing.T) {
	t.Parallel()
	runner := func(_ context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return &ingest.Result{
			Conflicts: []ingest.ConflictReport{{ArtifactID: "a", Version: "1.0.0", OldHash: "sha256:aaa", NewHash: "sha256:bbb"}},
			Removed:   []string{"ops/gone"},
		}, nil
	}
	base, cleanup := newRunnerHarness(t, runner)
	defer cleanup()
	resp, err := http.Post(base+"/v1/layers/reingest?id=team-shared", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		Code    string `json:"code"`
		Details struct {
			Removed []string `json:"removed"`
		} `json:"details"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StatusCode != http.StatusConflict || env.Code != "ingest.immutable_violation" || len(env.Details.Removed) != 1 || env.Details.Removed[0] != "ops/gone" {
		t.Errorf("status %d, code %q, details.removed %v; want 409, ingest.immutable_violation, [ops/gone]", resp.StatusCode, env.Code, env.Details.Removed)
	}
}
