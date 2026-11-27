package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.11 (Reported strings), §7.3.1 — the layer commands write each
// control, format, line, and paragraph separator character of a layer
// object as a \u escape, a surrogate pair above U+FFFF, so the output stays
// valid JSON; an invalid byte reads as \ufffd.
func TestEscapeControls(t *testing.T) {
	in := []byte("{\n  \"a\": \"x\u202ey\u2028\U000e0001z\x01\xff\"\n}")
	out := escapeControls(in)
	want := "{\n  \"a\": \"x\\u202ey\\u2028\\udb40\\udc01z\\u0001\\ufffd\"\n}"
	if string(out) != want {
		t.Fatalf("got %s, want %s", out, want)
	}
	var v map[string]string
	if err := json.Unmarshal(out, &v); err != nil || v["a"] != "x\u202ey\u2028\U000e0001z\x01\ufffd" {
		t.Fatalf("decode %v %q", err, v["a"])
	}
}

// Spec: §4.7.11 (Attestation record), §7.3.1 — `podium layer show` writes,
// beside the layer object, the next step under a status other than ok, the
// count of the artifacts the last cycle dropped, and the note for a layer
// in the mode none; a status with no constraint of its own takes the step
// of its code, and ok writes no step.
func TestWriteAttestationNotes(t *testing.T) {
	cases := map[string][]string{
		`{"id":"L","attestation_status":"ingest.attestation_stale","attestation_details":{"constraint":"mark"}}`: {
			`attestation status "ingest.attestation_stale" (constraint "mark")`, "--reset-attestation-mark`.",
		},
		`{"id":"L","attestation_status":"ingest.attestation_policy_invalid","attestation_details":{"constraint":"no_requirements"}}`: {
			"Update the policy with `podium layer update`.",
		},
		`{"id":"L","attestation_status":"ok","attestation_dropped":2}`:                        {"the last cycle dropped 2 artifacts; `podium layer reingest L`"},
		`{"id":"L","trust_mode":"none","attestation_status":"ingest.attestation_root_stale"}`: {"no cycle updates its attestation record", "PODIUM_ATTESTATION_TRUSTED_ROOT_FILE"},
		`{"id":"L","attestation_status":"ingest.source_unreachable","attestation_details":{"constraint":"fetch_deadline"}}`: {
			"Raise PODIUM_GIT_FETCH_TIMEOUT",
		},
	}
	for raw, wants := range cases {
		var out bytes.Buffer
		writeAttestationNotes(&out, json.RawMessage(raw))
		for _, w := range wants {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: output %q, want %q", raw, out.String(), w)
			}
		}
	}
	var out bytes.Buffer
	writeAttestationNotes(&out, json.RawMessage(`{"id":"L","attestation_status":"ok"}`))
	writeAttestationNotes(&out, json.RawMessage(`not json`))
	writeAttestationNotes(&out, json.RawMessage(`{"id":"L","attestation_status":"ingest.unknown"}`))
	if out.Len() != 0 {
		t.Errorf("output %q, want none", out.String())
	}
}

// Spec: §4.7.11 (Mark reset), §7.3.1 — `podium layer reingest
// --reset-attestation-mark --to-commit <sha>` sends both members, and
// --to-commit without the reset is a usage error that contacts no registry.
// `podium layer show` prints the record's next step on standard error.
func TestLayerReingestCmd_ResetFlags(t *testing.T) {
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{TenantID: "default", ID: "team", SourceType: "git",
		Repo: "https://github.com/acme/layer", Ref: "main", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	var got *ingest.MarkReset
	runner := func(ctx context.Context, _ store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		got = server.MarkResetFromContext(ctx)
		return &ingest.Result{Removed: []string{}}, nil
	}
	ts := httptest.NewServer(server.NewLayerEndpoint(st, "default", server.NewModeTracker()).WithReingestRunner(runner).Handler())
	t.Cleanup(ts.Close)
	commit := strings.Repeat("e", 40)
	if rc := layerReingest([]string{"--registry", ts.URL, "--reset-attestation-mark", "--to-commit", commit, "team"}); rc != 0 || got == nil || got.ToCommit != commit {
		t.Fatalf("rc %d, reset %+v", rc, got)
	}
	if rc := layerReingest([]string{"--registry", ts.URL, "--to-commit", commit, "team"}); rc != 2 {
		t.Fatalf("--to-commit alone rc = %d, want 2", rc)
	}
	_, _, _, err := st.ClaimLayerIngestWithReset(context.Background(), "default", "team", "c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteLayerAttestation(context.Background(), "default", "team", "c", store.AttestationWrite{
		Status: "ingest.attestation_missing", Details: map[string]any{"constraint": "no_bundle"}, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		if rc := layerShow([]string{"--registry", ts.URL, "team"}); rc != 0 {
			t.Errorf("show rc = %d", rc)
		}
	})
	if !strings.Contains(stderr, "next step: Check that the tracked ref is the signed ref") {
		t.Errorf("stderr %q", stderr)
	}
}
