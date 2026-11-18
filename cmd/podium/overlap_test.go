package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
)

const overlapBody = `{"semantic_overlap":{"state":"on","threshold":0.85,"model":"m1"},` +
	`"diagnostics":[{"artifact_id":"fin/a","code":"lint.semantic_overlap","severity":"warning",` +
	`"message":"name, description, when_to_use, and tags are semantically similar to fin/b (90%)","peers":[{"artifact_id":"fin/b","similarity":0.9}]}],` +
	`"counts":{"error":0,"warning":1,"info":0}}`

// Spec: §7.3.1 — podium overlap prints one line per diagnostic in the podium
// lint text format, writes the response object alone under --json, exits as
// --fail-on directs, and prints an error envelope on standard error with exit
// status 1.
func TestRenderOverlap(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := renderOverlap(&out, &errOut, []byte(overlapBody), 200, false, lint.SeverityError); code != 0 {
		t.Errorf("--fail-on error with a warning: exit %d, want 0", code)
	}
	if want := "[warning] fin/a: name, description, when_to_use, and tags are semantically similar to fin/b (90%) (lint.semantic_overlap)\n"; out.String() != want {
		t.Errorf("text output = %q, want %q", out.String(), want)
	}
	out.Reset()
	if code := renderOverlap(&out, &errOut, []byte(overlapBody), 200, true, lint.SeverityWarning); code != 1 {
		t.Errorf("--fail-on warning with a warning: exit %d, want 1", code)
	}
	var obj map[string]any
	if err := json.Unmarshal(out.Bytes(), &obj); err != nil || obj["semantic_overlap"] == nil {
		t.Errorf("--json output is not the response object: %q (%v)", out.String(), err)
	}
	out.Reset()
	empty := `{"semantic_overlap":{"state":"off","threshold":null,"model":null},"diagnostics":[],"counts":{"error":0,"warning":0,"info":0}}`
	if code := renderOverlap(&out, &errOut, []byte(empty), 200, false, lint.SeverityWarning); code != 0 || out.String() != "overlap: no issues.\n" {
		t.Errorf("empty report: exit %d output %q", code, out.String())
	}
	envelope := `{"code":"registry.invalid_argument","message":"threshold must be a number from 0.5 to 1.0"}`
	if code := renderOverlap(&out, &errOut, []byte(envelope), 400, false, lint.SeverityError); code != 1 || !strings.Contains(errOut.String(), "registry.invalid_argument") {
		t.Errorf("error envelope: exit %d stderr %q", code, errOut.String())
	}
	errOut.Reset()
	if code := renderOverlap(&out, &errOut, []byte("not json"), 200, false, lint.SeverityError); code != 1 || errOut.Len() == 0 {
		t.Errorf("unreadable body: exit %d stderr %q", code, errOut.String())
	}
}

// Spec: §7.3.1 — when computed is below total, podium overlap prints a
// warning on standard error naming both numbers, with and without --json,
// and counts it as a warning: --fail-on warning exits 1 and the default
// --fail-on error keeps exit 0. computed equal to total, and the check off,
// print no warning.
func TestRenderOverlap_IncompleteComputation(t *testing.T) {
	partial := `{"semantic_overlap":{"state":"on","threshold":0.85,"model":"m1","computed":3,"total":5},"diagnostics":[],"counts":{"error":0,"warning":0,"info":0}}`
	for _, asJSON := range []bool{false, true} {
		var out, errOut bytes.Buffer
		if code := renderOverlap(&out, &errOut, []byte(partial), 200, asJSON, lint.SeverityError); code != 0 {
			t.Errorf("json=%v --fail-on error: exit %d, want 0", asJSON, code)
		}
		if !strings.Contains(errOut.String(), "warning: semantic overlap is computed for 3 of 5 artifacts") {
			t.Errorf("json=%v stderr = %q, want the warning naming 3 and 5", asJSON, errOut.String())
		}
		if asJSON {
			var obj map[string]any
			if err := json.Unmarshal(out.Bytes(), &obj); err != nil {
				t.Errorf("--json stdout carries more than the response object: %q (%v)", out.String(), err)
			}
		}
		errOut.Reset()
		if code := renderOverlap(&out, &errOut, []byte(partial), 200, asJSON, lint.SeverityWarning); code != 1 {
			t.Errorf("json=%v --fail-on warning: exit %d, want 1", asJSON, code)
		}
	}
	for _, body := range []string{
		`{"semantic_overlap":{"state":"on","threshold":0.85,"model":"m1","computed":5,"total":5},"diagnostics":[],"counts":{"error":0,"warning":0,"info":0}}`,
		`{"semantic_overlap":{"state":"off","threshold":null,"model":null,"computed":null,"total":null},"diagnostics":[],"counts":{"error":0,"warning":0,"info":0}}`,
	} {
		var out, errOut bytes.Buffer
		if code := renderOverlap(&out, &errOut, []byte(body), 200, false, lint.SeverityWarning); code != 0 || errOut.Len() != 0 {
			t.Errorf("complete or off report: exit %d stderr %q, want 0 and no warning", code, errOut.String())
		}
	}
}

// Spec: §7.3.1 — podium admin rebuild-overlap prints the counts and exits 1
// when the response is an error envelope or failed is not zero.
func TestRenderRebuildOverlap(t *testing.T) {
	cases := []struct {
		body   string
		status int
		exit   int
		out    string
	}{
		{`{"computed":3,"failed":0}`, 200, 0, "computed: 3\nfailed: 0\n"},
		{`{"computed":2,"failed":1}`, 200, 1, "computed: 2\nfailed: 1\n"},
		{`{"code":"auth.forbidden","message":"admin role required"}`, 403, 1, ""},
		{`not json`, 200, 1, ""},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		if code := renderRebuildOverlap(&out, &errOut, []byte(c.body), c.status); code != c.exit || out.String() != c.out {
			t.Errorf("%s: exit %d output %q, want %d and %q", c.body, code, out.String(), c.exit, c.out)
		}
	}
}

// Spec: §7.3.1 — a --threshold outside 0.5 to 1.0, a --fail-on value other
// than error or warning, and a missing --registry are usage errors with exit
// status 2, reported before any request.
func TestOverlapCmd_UsageErrors(t *testing.T) {
	cases := [][]string{
		{"--registry", "http://127.0.0.1:1", "--threshold", "0.3"},
		{"--registry", "http://127.0.0.1:1", "--fail-on", "info"},
		{"--threshold", "0.9"},
	}
	t.Setenv("PODIUM_REGISTRY", "")
	for _, args := range cases {
		if code := overlapCmd(args); code != 2 {
			t.Errorf("overlap %v: exit %d, want 2", args, code)
		}
	}
	if code := adminRebuildOverlapCmd(nil); code != 2 {
		t.Errorf("admin rebuild-overlap with no --registry: exit %d, want 2", code)
	}
}
