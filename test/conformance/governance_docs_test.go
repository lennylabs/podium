// Package conformance holds the cross-cutting suites that any built-in
// or community implementation runs against. This suite verifies that the
// governance documentation matches the project-model claim in spec §1.6:
// the proposal process for spec changes and the proposals/ directory that
// GOVERNANCE.md points readers to.
package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the absolute path to the repository root, derived from
// this test file's location so the test runs from any working directory.
func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func readRepoFile(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// governancePages are the pages §1.6 points readers to: GOVERNANCE.md and
// its published counterpart on the documentation site.
var governancePages = []string{
	"GOVERNANCE.md",
	filepath.Join("docs", "about", "governance.md"),
}

// proposalFileName matches a numbered proposal file, NNNN-<slug>.md.
var proposalFileName = regexp.MustCompile(`^\d{4}-[a-z0-9-]+\.md$`)

// Spec: §1.6 Project Model — "Governance. Maintainer model and a proposal
// process for spec changes; see GOVERNANCE.md." The pages the spec points to
// must describe the proposal process, including the approval state and the
// state in which the spec edits have landed. The retired RFC process and its
// docs/rfc/ directory must not reappear on them.
func TestGovernanceDocs_DescribeProposalProcess(t *testing.T) {
	t.Parallel()

	spec := readRepoFile(t, filepath.Join("spec", "01-overview.md"))
	if !strings.Contains(spec, "proposal process for spec changes; see `GOVERNANCE.md`") {
		t.Fatalf("spec §1.6 governance claim changed; update this conformance test to match")
	}

	for _, rel := range governancePages {
		body := readRepoFile(t, rel)
		for _, want := range []string{"proposal process", "proposals/", "`Approved`", "Applied to spec"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not contain %q; spec §1.6 requires it to describe the proposal process", rel, want)
			}
		}
		for _, retired := range []string{"RFC process", "docs/rfc"} {
			if strings.Contains(body, retired) {
				t.Errorf("%s contains the retired phrase %q; spec §1.6 names the proposal process", rel, retired)
			}
		}
	}
}

// Spec: §1.6 Project Model — GOVERNANCE.md directs contributors to the
// proposals/ directory. The link must resolve to a directory of numbered
// proposal files. Only .md entries are checked, so an untracked file such as
// a Finder-created .DS_Store does not fail the test.
func TestGovernanceDocs_ProposalDirectoryExists(t *testing.T) {
	t.Parallel()

	governance := readRepoFile(t, "GOVERNANCE.md")
	if !strings.Contains(governance, "](proposals/)") {
		t.Fatalf("GOVERNANCE.md does not link proposals/; spec §1.6 points readers there for the proposal process")
	}

	entries, err := os.ReadDir(filepath.Join(repoRoot(t), "proposals"))
	if err != nil {
		t.Fatalf("read proposals/: %v", err)
	}
	numbered := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		if !proposalFileName.MatchString(e.Name()) {
			t.Errorf("proposals/%s does not follow the NNNN-<slug>.md numbering GOVERNANCE.md describes", e.Name())
			continue
		}
		numbered++
	}
	if numbered == 0 {
		t.Fatalf("proposals/ holds no NNNN-<slug>.md file")
	}
}
