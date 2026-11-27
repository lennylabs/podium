package lint_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// portableDiags runs the advisory rules, which carry the portable-name
// warning, over the records and returns its diagnostics.
func portableDiags(records ...filesystem.ArtifactRecord) []lint.Diagnostic {
	var out []lint.Diagnostic
	for _, d := range (&lint.Linter{Rules: (&lint.Linter{}).AdvisoryRules()}).Lint(context.Background(), nil, records) {
		if d.Code == lint.NonPortableNameCode {
			out = append(out, d)
		}
	}
	return out
}

// portableRecord is an agent record whose lint_suppress names the
// portable-name code, with one bundled resource per path.
func portableRecord(id string, paths ...string) filesystem.ArtifactRecord {
	res := map[string][]byte{}
	for _, p := range paths {
		res[p] = []byte("x\n")
	}
	return filesystem.ArtifactRecord{
		ID:        id,
		Artifact:  &manifest.Artifact{Type: manifest.TypeAgent, LintSuppress: []string{lint.NonPortableNameCode}},
		Resources: res,
	}
}

// Spec: §4.2 — lint warns once per artifact whose canonical ID or bundled
// resource path holds a segment that is not a portable name. The warning
// names the first such segment, the ID before the resource paths and the
// paths in byte order, and lint_suppress does not silence it.
func TestRuleNonPortableName(t *testing.T) {
	t.Parallel()
	diags := portableDiags(
		portableRecord("team/CON .txt", "zz/aux.md"),
		portableRecord("team/ok", "b/nul", "a/GIT~1/config", "ok.md"),
		portableRecord("team/clean", "refs/notes.md", "release~2/x.md"),
	)
	if len(diags) != 2 {
		t.Fatalf("diagnostics = %+v, want two", diags)
	}
	byID := map[string]lint.Diagnostic{}
	for _, d := range diags {
		if d.Severity != lint.SeverityWarning {
			t.Errorf("%s: severity %s, want warning", d.ArtifactID, d.Severity)
		}
		byID[d.ArtifactID] = d
	}
	if d := byID["team/CON .txt"]; !strings.Contains(d.Message, "canonical ID") || !strings.Contains(d.Message, strconv.Quote("CON .txt")) {
		t.Errorf("ID warning = %q, want it to name the ID segment", d.Message)
	}
	if d := byID["team/ok"]; !strings.Contains(d.Message, strconv.Quote("a/GIT~1/config")) || !strings.Contains(d.Message, strconv.Quote("GIT~1")) {
		t.Errorf("resource warning = %q, want it to name a/GIT~1/config and GIT~1", d.Message)
	}
}

// Spec: §4.2, §7.3.5 — the rule reads the ID alone of a record that carries
// no parsed fields, which is how `podium attest` runs it over an unresolved
// package.
func TestRuleNonPortableName_UnparsedRecord(t *testing.T) {
	t.Parallel()
	recs := []filesystem.ArtifactRecord{{ID: "team/nul", Unparseable: "manifest: bad"}, {ID: "team/fine", Unparseable: "manifest: bad"}}
	diags := (&lint.Linter{Rules: []lint.Rule{lint.NonPortableNameRule()}}).Lint(context.Background(), nil, recs)
	if len(diags) != 1 || diags[0].ArtifactID != "team/nul" || diags[0].Code != lint.NonPortableNameCode {
		t.Errorf("diagnostics = %+v, want one for team/nul", diags)
	}
}
