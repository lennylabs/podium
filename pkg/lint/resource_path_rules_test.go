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

// Spec: §4.4, §7.5.3 — a bundled resource whose path has a segment that names
// .git, literally or under the aliasing rule, is a lint error at any depth,
// and lint_suppress does not silence it. A name such as .gitignore or
// .github is an ordinary resource.
func TestRuleResourcePathReserved(t *testing.T) {
	t.Parallel()
	refused := []string{
		".git/config",
		"scripts/.git/hooks/pre-commit",
		".GIT/config",
		"\u200c.git/config",
		"GIT~1/config",
		".git./config",
		".git::$INDEX_ALLOCATION/config",
		".git",
	}
	accepted := []string{".gitignore", ".github/workflows/ci.yml", "scripts/.gitkeep", "GIT~2/config", "git/config"}
	res := map[string][]byte{}
	for _, p := range append(append([]string{}, refused...), accepted...) {
		res[p] = []byte("x\n")
	}
	rec := filesystem.ArtifactRecord{
		ID:        "team/hooks",
		Artifact:  &manifest.Artifact{Type: manifest.TypeAgent, LintSuppress: []string{"lint.resource_path_reserved"}},
		Resources: res,
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	got := map[string]bool{}
	for _, d := range diags {
		if d.Code != "lint.resource_path_reserved" {
			continue
		}
		if d.Severity != lint.SeverityError || d.ArtifactID != "team/hooks" {
			t.Errorf("diagnostic %+v, want an error on team/hooks", d)
		}
		for _, p := range append(append([]string{}, refused...), accepted...) {
			if strings.Contains(d.Message, strconv.Quote(p)) {
				got[p] = true
			}
		}
	}
	for _, p := range refused {
		if !got[p] {
			t.Errorf("no lint.resource_path_reserved error for %q; diagnostics %+v", p, diags)
		}
	}
	for _, p := range accepted {
		if got[p] {
			t.Errorf("lint.resource_path_reserved reported the ordinary resource %q", p)
		}
	}
}
