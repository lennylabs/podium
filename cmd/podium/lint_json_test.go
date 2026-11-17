package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
)

// Spec: §7.3.1, §4.1, §4.6 — the --json envelope carries registry,
// diagnostics, rejected, and counts; empty members render as [], rejected is
// sorted by artifact ID with details only where the refusing rule defines
// them, so every removed-feature drop carries its constraint and a collision
// drop carries none, and counts carries every severity.
func TestWriteLintEnvelope(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeLintEnvelope(&buf, "/reg", nil, nil); err != nil {
			t.Fatalf("writeLintEnvelope: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", buf.String(), err)
		}
		want := map[string]any{
			"registry":    "/reg",
			"diagnostics": []any{},
			"rejected":    []any{},
			"counts":      map[string]any{"error": float64(0), "warning": float64(0), "info": float64(0)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("envelope = %v, want %v", got, want)
		}
	})

	t.Run("populated", func(t *testing.T) {
		diags := []lint.Diagnostic{
			{ArtifactID: "a/one", Code: "lint.thin_description", Severity: lint.SeverityWarning, Message: "thin"},
			{ArtifactID: "b/two", Code: "x.info", Severity: lint.SeverityInfo, Message: "note"},
		}
		drops := []layer.Drop{
			layer.RemovedFeatureDrop("z/ext", layer.ExtendsRemovedConstraint, layer.ExtendsRemovedReason),
			layer.RemovedFeatureDrop("m/cmd", layer.TypeRemovedConstraint, layer.TypeRemovedReason("command")),
			layer.Collision{ArtifactID: "c/dup", Layer: "l2", ExistingLayer: "l1"}.Drop(),
		}
		var buf bytes.Buffer
		if err := writeLintEnvelope(&buf, "reg", diags, drops); err != nil {
			t.Fatalf("writeLintEnvelope: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", buf.String(), err)
		}
		wantDiags := []any{
			map[string]any{"artifact_id": "a/one", "code": "lint.thin_description", "severity": "warning", "message": "thin"},
			map[string]any{"artifact_id": "b/two", "code": "x.info", "severity": "info", "message": "note"},
		}
		if !reflect.DeepEqual(got["diagnostics"], wantDiags) {
			t.Errorf("diagnostics = %v, want %v", got["diagnostics"], wantDiags)
		}
		rejected, _ := got["rejected"].([]any)
		if len(rejected) != 3 {
			t.Fatalf("rejected = %v, want three entries", got["rejected"])
		}
		first, _ := rejected[0].(map[string]any)
		removed, _ := rejected[1].(map[string]any)
		second, _ := rejected[2].(map[string]any)
		if first["artifact_id"] != "c/dup" || removed["artifact_id"] != "m/cmd" || second["artifact_id"] != "z/ext" {
			t.Errorf("rejected order = %v, %v, %v; want c/dup, m/cmd, z/ext",
				first["artifact_id"], removed["artifact_id"], second["artifact_id"])
		}
		if _, ok := first["details"]; ok {
			t.Errorf("collision entry carries details: %v", first)
		}
		if first["code"] != "ingest.collision" || first["reason"] == "" {
			t.Errorf("collision entry = %v, want ingest.collision with a reason", first)
		}
		wantDetails := map[string]any{"constraint": "extends_removed"}
		if !reflect.DeepEqual(second["details"], wantDetails) || second["code"] != "ingest.invalid_artifact" {
			t.Errorf("extends entry = %v, want ingest.invalid_artifact with details %v", second, wantDetails)
		}
		wantRemoved := map[string]any{"constraint": "type_removed"}
		if !reflect.DeepEqual(removed["details"], wantRemoved) || removed["code"] != "ingest.invalid_artifact" ||
			removed["reason"] != layer.TypeRemovedReason("command") {
			t.Errorf("removed-type entry = %v, want ingest.invalid_artifact with details %v", removed, wantRemoved)
		}
		wantCounts := map[string]any{"error": float64(0), "warning": float64(1), "info": float64(1)}
		if !reflect.DeepEqual(got["counts"], wantCounts) {
			t.Errorf("counts = %v, want %v", got["counts"], wantCounts)
		}
		if got["registry"] != "reg" {
			t.Errorf("registry = %v, want reg", got["registry"])
		}
	})

	t.Run("error counted", func(t *testing.T) {
		diags := []lint.Diagnostic{
			{ArtifactID: "a/one", Code: "lint.manifest_size", Severity: lint.SeverityError, Message: "too large"},
			{ArtifactID: "a/one", Code: "lint.thin_description", Severity: lint.SeverityWarning, Message: "thin"},
		}
		var buf bytes.Buffer
		if err := writeLintEnvelope(&buf, "reg", diags, nil); err != nil {
			t.Fatalf("writeLintEnvelope: %v", err)
		}
		var got struct {
			Counts map[string]int `json:"counts"`
		}
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", buf.String(), err)
		}
		want := map[string]int{"error": 1, "warning": 1, "info": 0}
		if !reflect.DeepEqual(got.Counts, want) {
			t.Errorf("counts = %v, want %v", got.Counts, want)
		}
	})

	t.Run("write failure", func(t *testing.T) {
		if err := writeLintEnvelope(failingWriter{}, "reg", nil, nil); err == nil {
			t.Error("writeLintEnvelope to a failing writer returned nil")
		}
	})
}

// failingWriter rejects every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// Spec: §7.3.1 — --fail-on sets the lowest severity that fails the run, an
// info diagnostic never fails it, and a refused artifact always does.
func TestLintExitCode(t *testing.T) {
	diag := func(s lint.Severity) []lint.Diagnostic {
		return []lint.Diagnostic{{ArtifactID: "a/b", Code: "c", Severity: s, Message: "m"}}
	}
	cases := []struct {
		name    string
		diags   []lint.Diagnostic
		dropped bool
		failOn  lint.Severity
		want    int
	}{
		{"error under error", diag(lint.SeverityError), false, lint.SeverityError, 1},
		{"error under warning", diag(lint.SeverityError), false, lint.SeverityWarning, 1},
		{"warning under error", diag(lint.SeverityWarning), false, lint.SeverityError, 0},
		{"warning under warning", diag(lint.SeverityWarning), false, lint.SeverityWarning, 1},
		{"info under error", diag(lint.SeverityInfo), false, lint.SeverityError, 0},
		{"info under warning", diag(lint.SeverityInfo), false, lint.SeverityWarning, 0},
		{"no diagnostic", nil, false, lint.SeverityWarning, 0},
		{"drop with no diagnostic", nil, true, lint.SeverityError, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lintExitCode(tc.diags, tc.dropped, tc.failOn); got != tc.want {
				t.Errorf("lintExitCode = %d, want %d", got, tc.want)
			}
		})
	}
}

// nearDuplicateBody is a prose body of more than 50 distinct words, so two
// skills that carry it form a lint.near_duplicate_content pair.
const nearDuplicateBody = "Reconcile the ledger against the bank statement before the monthly close begins. " +
	"Collect every outstanding receipt from the shared drive and match each one to a posted entry. " +
	"Flag any unmatched amount above the materiality threshold for review by the controller. " +
	"Record adjustments in the journal with a short note that explains the source document and the approver. " +
	"Archive the signed reconciliation in the audit folder when the review completes.\n"

// writeLintRegistry writes a filesystem registry whose skills carry the
// given name, description, and body, and returns its root.
func writeLintRegistry(t *testing.T, skills map[string][2]string) string {
	t.Helper()
	root := t.TempDir()
	for name, v := range skills {
		dir := filepath.Join(root, "team", "finance", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		skill := "---\nname: " + name + "\ndescription: " + v[0] + "\n---\n\n" + v[1]
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
		art := "---\ntype: skill\nversion: 1.0.0\n---\n"
		if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(art), 0o644); err != nil {
			t.Fatalf("write ARTIFACT.md: %v", err)
		}
	}
	return root
}

// nearDuplicateRegistry holds two skills with distinct descriptions whose
// bodies are identical, so only lint.near_duplicate_content fires.
func nearDuplicateRegistry(t *testing.T) string {
	return writeLintRegistry(t, map[string][2]string{
		"close-books":   {"Reconcile the general ledger at month end", nearDuplicateBody},
		"audit-receipt": {"Prepare receipts for the external auditors", nearDuplicateBody},
	})
}

// Spec: §7.3.1 — an interrupted run writes no JSON object and exits 1, so
// a cancelled near-duplicate pass never reads as a clean catalog.
func TestRunLint_InterruptedWritesNoEnvelope(t *testing.T) {
	root := nearDuplicateRegistry(t)
	for _, asJSON := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stdout, stderr bytes.Buffer
		rc := runLint(ctx, &stdout, &stderr, root, true, asJSON, lint.SeverityError)
		if rc != 1 {
			t.Errorf("asJSON=%v: runLint = %d, want 1", asJSON, rc)
		}
		if stdout.Len() != 0 {
			t.Errorf("asJSON=%v: stdout = %q, want empty", asJSON, stdout.String())
		}
		if !strings.Contains(stderr.String(), "lint interrupted") {
			t.Errorf("asJSON=%v: stderr = %q, want lint interrupted", asJSON, stderr.String())
		}
	}
}

// Spec: §7.3.1 — podium lint runs the catalog advisories over every walked
// artifact; a warning fails the run only under --fail-on warning.
func TestRunLint_AdvisoriesAndFailOn(t *testing.T) {
	root := nearDuplicateRegistry(t)
	var stdout, stderr bytes.Buffer
	if rc := runLint(context.Background(), &stdout, &stderr, root, true, false, lint.SeverityError); rc != 0 {
		t.Errorf("runLint(fail-on error) = %d, want 0; stderr=%q", rc, stderr.String())
	}
	if got := strings.Count(stdout.String(), "lint.near_duplicate_content"); got != 2 {
		t.Errorf("stdout = %q, want two lint.near_duplicate_content lines", stdout.String())
	}
	stdout.Reset()
	if rc := runLint(context.Background(), &stdout, &stderr, root, true, true, lint.SeverityWarning); rc != 1 {
		t.Errorf("runLint(json, fail-on warning) = %d, want 1", rc)
	}
	var env lintEnvelopeJSON
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	if env.Counts.Warning != 2 || env.Registry != root || len(env.Rejected) != 0 {
		t.Errorf("envelope = %+v, want two warnings, registry %q, no rejected", env, root)
	}
}

// Spec: §7.3.1, §4.6 — under --json a refused extends: artifact is listed
// in rejected and not named on standard error, and the run exits 1.
func TestRunLint_JSONRejectedNotOnStderr(t *testing.T) {
	root := writeLintRegistry(t, map[string][2]string{
		"close-books": {"Reconcile the general ledger at month end", "Body.\n"},
	})
	art := "---\ntype: skill\nversion: 1.0.0\nextends: team/finance/other\n---\n"
	if err := os.WriteFile(filepath.Join(root, "team", "finance", "close-books", "ARTIFACT.md"), []byte(art), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if rc := runLint(context.Background(), &stdout, &stderr, root, true, true, lint.SeverityError); rc != 1 {
		t.Errorf("runLint = %d, want 1", rc)
	}
	if strings.Contains(stderr.String(), "rejected") {
		t.Errorf("stderr = %q, want no rejected line under --json", stderr.String())
	}
	var env lintEnvelopeJSON
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	if len(env.Rejected) != 1 || env.Rejected[0].Details["constraint"] != "extends_removed" {
		t.Errorf("rejected = %+v, want one extends_removed entry", env.Rejected)
	}

	stdout.Reset()
	stderr.Reset()
	if rc := runLint(context.Background(), &stdout, &stderr, root, true, false, lint.SeverityError); rc != 1 {
		t.Errorf("runLint(human) = %d, want 1", rc)
	}
	if !strings.Contains(stderr.String(), "ingest.invalid_artifact") {
		t.Errorf("stderr = %q, want the rejected line", stderr.String())
	}
	if strings.Contains(stdout.String(), "lint: no issues.") {
		t.Errorf("stdout = %q, want no clean line when an artifact was refused", stdout.String())
	}
}

// Spec: §7.3.1 — a registry that cannot be opened writes no JSON object,
// and a failing standard output is reported as an error.
func TestRunLint_OpenFailureAndWriteFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing")
	if rc := runLint(context.Background(), &stdout, &stderr, missing, true, true, lint.SeverityError); rc != 1 {
		t.Errorf("runLint(missing) = %d, want 1", rc)
	}
	if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "error: ") {
		t.Errorf("stdout=%q stderr=%q, want no envelope and an error line", stdout.String(), stderr.String())
	}

	// A §4.6 collision fails the walk rather than the open.
	stderr.Reset()
	collide := t.TempDir()
	files := map[string]string{
		".registry-config":               "multi_layer: true\nlayer_order:\n  - org\n  - team\n",
		"org/ops/close/ARTIFACT.md":      "---\ntype: agent\nversion: 1.0.0\ndescription: Close the books for the org layer\n---\n\nOrg.\n",
		"team/ops/close/ARTIFACT.md":     "---\ntype: agent\nversion: 1.0.0\ndescription: Close the books for the team layer\n---\n\nTeam.\n",
		"team/ops/unrelated/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Notes kept beside the colliding copy\n---\n\nNotes.\n",
	}
	for rel, body := range files {
		p := filepath.Join(collide, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if rc := runLint(context.Background(), &stdout, &stderr, collide, true, true, lint.SeverityError); rc != 1 {
		t.Errorf("runLint(collision) = %d, want 1", rc)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "ingest.collision") {
		t.Errorf("stdout=%q stderr=%q, want no envelope and the collision error", stdout.String(), stderr.String())
	}

	stderr.Reset()
	root := nearDuplicateRegistry(t)
	if rc := runLint(context.Background(), failingWriter{}, &stderr, root, true, true, lint.SeverityError); rc != 1 {
		t.Errorf("runLint(failing stdout) = %d, want 1", rc)
	}
	if !strings.Contains(stderr.String(), "write json") {
		t.Errorf("stderr = %q, want the write failure", stderr.String())
	}
}

// Spec: §7.3.1 — a --fail-on value other than error or warning is a usage
// error that exits 2 before the registry is opened.
func TestLintCmd_FailOnUsage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	out := captureStderr(t, func() {
		if rc := lintCmd([]string{"--registry", missing, "--fail-on", "info"}); rc != 2 {
			t.Errorf("lintCmd(--fail-on info) = %d, want 2", rc)
		}
	})
	if !strings.Contains(out, "error: --fail-on must be error or warning") {
		t.Errorf("stderr = %q, want the --fail-on usage error", out)
	}
	if strings.Contains(out, "missing") {
		t.Errorf("stderr = %q, want the usage error before the registry is opened", out)
	}
}
