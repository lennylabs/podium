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

// reservedDiags returns the lint.resource_path_reserved diagnostics for one
// agent record whose resources are paths, with lint_suppress naming the code.
func reservedDiags(t *testing.T, id string, paths ...string) []lint.Diagnostic {
	t.Helper()
	res := map[string][]byte{}
	for _, p := range paths {
		res[p] = []byte("x\n")
	}
	rec := filesystem.ArtifactRecord{
		ID:        id,
		Artifact:  &manifest.Artifact{Type: manifest.TypeAgent, LintSuppress: []string{"lint.resource_path_reserved"}},
		Resources: res,
	}
	var out []lint.Diagnostic
	for _, d := range (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec}) {
		if d.Code == "lint.resource_path_reserved" {
			out = append(out, d)
		}
	}
	return out
}

// Spec: §4.4, §7.5.3 — a bundled resource whose path has a segment that names
// .git under the aliasing rule is a lint error at any depth that
// lint_suppress does not silence, and a record that holds the literal name
// is refused too. A name such as .gitignore or .github is an ordinary
// resource.
func TestRuleResourcePathReserved(t *testing.T) {
	t.Parallel()
	accepted := []string{".gitignore", ".github/workflows/ci.yml", "scripts/.gitkeep", "GIT~2/config", "git/config"}
	for _, p := range []string{
		"scripts/.GIT/hooks/pre-commit",
		".GIT/config",
		"\u200c.git/config",
		"GIT~1/config",
		".git./config",
		".git::$INDEX_ALLOCATION/config",
		".git/config",
	} {
		diags := reservedDiags(t, "team/hooks", append([]string{p}, accepted...)...)
		if len(diags) != 1 || diags[0].Severity != lint.SeverityError || diags[0].ArtifactID != "team/hooks" ||
			!strings.Contains(diags[0].Message, strconv.Quote(p)) {
			t.Errorf("resource %q: diagnostics %+v, want one error naming it", p, diags)
		}
	}
	if diags := reservedDiags(t, "team/ok", accepted...); len(diags) != 0 {
		t.Errorf("ordinary resources: diagnostics %+v, want none", diags)
	}
}

// Spec: §4.4 — lint reports one error per artifact, naming the first
// offending path in byte order, however many files sit below the name.
func TestRuleResourcePathReserved_OnePerArtifact(t *testing.T) {
	t.Parallel()
	diags := reservedDiags(t, "team/clone", "GIT~1/objects/ab/cdef", "GIT~1/HEAD", "GIT~1/config", ".GIT/index")
	if len(diags) != 1 || !strings.Contains(diags[0].Message, strconv.Quote(".GIT/index")) {
		t.Errorf("diagnostics %+v, want one error naming .GIT/index", diags)
	}
}
