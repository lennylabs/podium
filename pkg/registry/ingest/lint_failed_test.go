package ingest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
)

// Spec: §7.3.1 (Ingest outcome), §6.10 — the ingest.lint_failed message
// states the number of diagnostics and names the failing artifacts in
// identifier order, each with its distinct lint codes, and a diagnostic that
// names no artifact only counts.
// Matrix: §6.10 (ingest.lint_failed)
func TestLintFailedError_NamesTheArtifacts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		diags []lint.Diagnostic
		want  string
	}{
		{"one diagnostic", []lint.Diagnostic{{ArtifactID: "team/a", Code: "lint.invalid_name"}},
			"ingest.lint_failed: 1 lint diagnostic in team/a (lint.invalid_name)"},
		{"sorted with distinct codes", []lint.Diagnostic{
			{ArtifactID: "team/b", Code: "lint.z"},
			{ArtifactID: "team/a", Code: "lint.y"},
			{ArtifactID: "team/b", Code: "lint.x"},
			{ArtifactID: "team/b", Code: "lint.z"},
			{Code: "lint.registry"},
		}, "ingest.lint_failed: 5 lint diagnostics in team/a (lint.y), team/b (lint.x, lint.z)"},
		{"no artifact named", []lint.Diagnostic{{Code: "lint.registry"}, {Code: "lint.registry"}},
			"ingest.lint_failed: 2 lint diagnostics"},
	}
	for _, tc := range cases {
		err := lintFailedError(tc.diags)
		if !errors.Is(err, ErrLintFailed) || err.Error() != tc.want {
			t.Errorf("%s: lintFailedError = %v, want %q wrapping ErrLintFailed", tc.name, err, tc.want)
		}
	}
}

// Spec: §7.3.1 (Ingest outcome) — the message names at most ten artifacts and
// states how many more failed.
func TestLintFailedError_BoundsTheList(t *testing.T) {
	t.Parallel()
	var diags []lint.Diagnostic
	for i := range 13 {
		diags = append(diags, lint.Diagnostic{ArtifactID: fmt.Sprintf("team/a%02d", i), Code: "lint.c"})
	}
	want := "ingest.lint_failed: 13 lint diagnostics in team/a00 (lint.c), team/a01 (lint.c), team/a02 (lint.c), " +
		"team/a03 (lint.c), team/a04 (lint.c), team/a05 (lint.c), team/a06 (lint.c), team/a07 (lint.c), " +
		"team/a08 (lint.c), team/a09 (lint.c), and 3 more artifacts"
	if got := lintFailedError(diags).Error(); got != want {
		t.Errorf("lintFailedError =\n%q\nwant\n%q", got, want)
	}
	want = "ingest.lint_failed: 11 lint diagnostics in team/a00 (lint.c), team/a01 (lint.c), team/a02 (lint.c), " +
		"team/a03 (lint.c), team/a04 (lint.c), team/a05 (lint.c), team/a06 (lint.c), team/a07 (lint.c), " +
		"team/a08 (lint.c), team/a09 (lint.c), and 1 more artifact"
	if got := lintFailedError(diags[:11]).Error(); got != want {
		t.Errorf("lintFailedError of 11 artifacts =\n%q\nwant\n%q", got, want)
	}
}
