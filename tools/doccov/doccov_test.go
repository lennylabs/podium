// Unit tests for the doccov runnable-doc-coverage gate.
//
// Spec: §11 Verification — the verification suite includes a documentation
// coverage gate that maps every doc page carrying a runnable command example to
// a covering test or an explicit waiver. These tests pin the classifier, the
// manifest loader, the D-slug resolver, and the check/report exit codes against
// fixture docs in a temp dir.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes content to dir/rel, creating parent directories.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

// ----- classifier ----------------------------------------------------------

func TestContentClassifier_RunnableShellTags(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"bash":          "```bash\npodium sync\n```",
		"sh":            "```sh\nmake test\n```",
		"shell":         "```shell\ncurl http://x\n```",
		"console":       "```console\n$ podium init\n```",
		"zsh":           "```zsh\ngo build ./...\n```",
		"shell-session": "```shell-session\npodium lint --registry .\n```",
	}
	for name, body := range cases {
		if !contentHasRunnableBlock(body) {
			t.Errorf("%s block should be runnable:\n%s", name, body)
		}
	}
}

func TestContentClassifier_UntaggedBlockWithCommand(t *testing.T) {
	t.Parallel()
	// cli.md-style: an untagged block whose lines start with a command.
	body := "Some prose.\n\n```\npodium sync\npodium status\n```\n"
	if !contentHasRunnableBlock(body) {
		t.Errorf("untagged block with a podium command should be runnable")
	}
	// Untagged block with a leading "$ " prompt.
	if !contentHasRunnableBlock("```\n$ make build\n```") {
		t.Errorf("untagged block with a prompted make command should be runnable")
	}
}

func TestContentClassifier_ConfigBlocksNotRunnable(t *testing.T) {
	t.Parallel()
	// A command name inside a yaml/json/python block is not a runnable command.
	cases := map[string]string{
		"yaml-string": "```yaml\nhook: \"podium admin runtime register\"\n```",
		"json-value":  "```json\n{ \"action\": \"run podium sync\" }\n```",
		"python-mod":  "```python\nfrom podium import Client\n```",
		"toml":        "```toml\ncmd = \"make build\"\n```",
		"go":          "```go\nfunc main() { exec.Command(\"podium\") }\n```",
		"markdown":    "```markdown\nRun `podium sync` to start.\n```",
	}
	for name, body := range cases {
		if contentHasRunnableBlock(body) {
			t.Errorf("%s block must not be runnable:\n%s", name, body)
		}
	}
}

func TestContentClassifier_DirectoryTreeNotRunnable(t *testing.T) {
	t.Parallel()
	// domains.md-style: an untagged directory tree. "~/podium-artifacts/" must
	// not be read as a podium command because the line starts with "~".
	body := "```\n~/podium-artifacts/\n  personal/\n    greet/\n```"
	if contentHasRunnableBlock(body) {
		t.Errorf("a directory tree mentioning podium in a path must not be runnable")
	}
}

func TestContentClassifier_InlineCodeNotRunnable(t *testing.T) {
	t.Parallel()
	// why-podium.md-style: an inline `podium sync` reference in prose, with no
	// fenced runnable block, is not a runnable page.
	body := "This page mentions `podium sync` inline but fences nothing.\n"
	if contentHasRunnableBlock(body) {
		t.Errorf("inline code reference must not make a page runnable")
	}
}

func TestContentClassifier_TildeFenceAndInfoString(t *testing.T) {
	t.Parallel()
	if !contentHasRunnableBlock("~~~bash\npodium sync\n~~~") {
		t.Errorf("a tilde-fenced bash block should be runnable")
	}
	// An info string after the language tag still classifies by the tag.
	if !contentHasRunnableBlock("```bash title=\"setup\"\npodium init\n```") {
		t.Errorf("a bash block with an info string should be runnable")
	}
	if contentHasRunnableBlock("```yaml title=\"config\"\nkey: podium\n```") {
		t.Errorf("a yaml block with an info string must not be runnable")
	}
}

// ----- D-slug resolution ----------------------------------------------------

func TestScanDocSlugs_ParsesHeaders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Single-line header.
	writeFile(t, dir, "quickstart_flow_test.go",
		"package e2e\n\n// End-to-end tests for docs/getting-started/quickstart.md (D-quickstart).\nfunc TestX(t *testing.T) {}\n")
	// Header that wraps the slug onto the next comment line.
	writeFile(t, dir, "server_operations_test.go",
		"package e2e\n\n// End-to-end tests for docs/deployment/operator-guide.md\n// (D-operator-guide). The page covers day-two operations.\nfunc TestY(t *testing.T) {}\n")
	// Glob path header.
	writeFile(t, dir, "auth_oidc_test.go",
		"package e2e\n\n// End-to-end tests for docs/deployment/oidc/*.md (D-oidc).\nfunc TestZ(t *testing.T) {}\n")
	// A file with no header declares no slug.
	writeFile(t, dir, "helpers_test.go", "package e2e\n\nfunc helper() {}\n")

	slugs, err := ScanDocSlugs(dir)
	if err != nil {
		t.Fatalf("ScanDocSlugs: %v", err)
	}
	for _, want := range []string{"D-quickstart", "D-operator-guide", "D-oidc"} {
		if _, ok := slugs[want]; !ok {
			t.Errorf("missing slug %s; got %v", want, slugs)
		}
	}
	if !strings.HasSuffix(slugs["D-quickstart"], "quickstart_flow_test.go") {
		t.Errorf("D-quickstart -> %s", slugs["D-quickstart"])
	}
	if len(slugs) != 3 {
		t.Errorf("got %d slugs, want 3: %v", len(slugs), slugs)
	}
}

func TestScanDocSlugs_MissingDirIsEmpty(t *testing.T) {
	t.Parallel()
	slugs, err := ScanDocSlugs(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("ScanDocSlugs on missing dir: %v", err)
	}
	if len(slugs) != 0 {
		t.Errorf("got %d slugs from a missing dir, want 0", len(slugs))
	}
}

// ----- manifest -------------------------------------------------------------

func TestLoadManifest_RejectsAmbiguousEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Entry sets both slug and waiver.
	writeFile(t, dir, "both.yaml", "pages:\n  - path: docs/a.md\n    slug: D-a\n    waiver: nope\n")
	if _, err := LoadManifest(filepath.Join(dir, "both.yaml")); err == nil {
		t.Errorf("manifest with both slug and waiver should fail to load")
	}
	// Entry sets neither.
	writeFile(t, dir, "neither.yaml", "pages:\n  - path: docs/a.md\n")
	if _, err := LoadManifest(filepath.Join(dir, "neither.yaml")); err == nil {
		t.Errorf("manifest with neither slug nor waiver should fail to load")
	}
	// Duplicate paths.
	writeFile(t, dir, "dup.yaml", "pages:\n  - path: docs/a.md\n    slug: D-a\n  - path: docs/a.md\n    waiver: dup\n")
	if _, err := LoadManifest(filepath.Join(dir, "dup.yaml")); err == nil {
		t.Errorf("manifest with duplicate paths should fail to load")
	}
}

// ----- check / report end to end -------------------------------------------

// scenario builds a temp repo with one mapped runnable page, one waived
// runnable page, a non-runnable page, the covering e2e file, and a manifest.
// unmappedRunnable controls whether an extra runnable page is left out of the
// manifest, which is the failure the gate must catch.
func scenario(t *testing.T, unmappedRunnable bool) (root string, pages []string, m *Manifest, slugs map[string]string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, root, "go.mod", "module example\n")
	// A mapped runnable page.
	writeFile(t, root, "docs/getting-started/quickstart.md", "# Quickstart\n\n```bash\npodium init\npodium sync\n```\n")
	// A waived runnable page.
	writeFile(t, root, "docs/index.md", "# Home\n\n```bash\npodium init\n```\n")
	// A non-runnable page: a yaml config block only.
	writeFile(t, root, "docs/reference/config.md", "# Config\n\n```yaml\nkey: podium\n```\n")
	if unmappedRunnable {
		// A second runnable page deliberately left out of the manifest.
		writeFile(t, root, "docs/authoring/your-first-skill.md", "# Skill\n\n```bash\npodium lint --registry .\n```\n")
	}
	// The covering e2e file for D-quickstart.
	writeFile(t, root, "test/e2e/quickstart_flow_test.go",
		"package e2e\n\n// End-to-end tests for docs/getting-started/quickstart.md (D-quickstart).\nfunc TestQuickstart_Init(t *testing.T) {}\n")
	// The manifest: quickstart covered, index waived.
	writeFile(t, root, "manifest.yaml",
		"pages:\n"+
			"  - path: docs/getting-started/quickstart.md\n    slug: D-quickstart\n"+
			"  - path: docs/index.md\n    waiver: duplicates quickstart\n")

	var err error
	pages, err = ScanRunnablePages(root, "docs")
	if err != nil {
		t.Fatalf("ScanRunnablePages: %v", err)
	}
	m, err = LoadManifest(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	slugs, err = ScanDocSlugs(filepath.Join(root, "test", "e2e"))
	if err != nil {
		t.Fatalf("ScanDocSlugs: %v", err)
	}
	return root, pages, m, slugs
}

func TestCheck_PassesWhenEveryRunnablePageIsMapped(t *testing.T) {
	t.Parallel()
	root, pages, m, slugs := scenario(t, false)
	// The config page must not be classified as runnable.
	for _, p := range pages {
		if strings.HasSuffix(p, "config.md") {
			t.Fatalf("yaml-only page was classified as runnable: %v", pages)
		}
	}
	var buf bytes.Buffer
	rc := check(&buf, root, pages, m, slugs)
	if rc != 0 {
		t.Fatalf("check rc = %d, want 0\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "all mapped") {
		t.Errorf("expected success message, got:\n%s", buf.String())
	}
}

func TestCheck_FlagsUnmappedRunnablePage(t *testing.T) {
	t.Parallel()
	root, pages, m, slugs := scenario(t, true)
	var buf bytes.Buffer
	rc := check(&buf, root, pages, m, slugs)
	if rc != 1 {
		t.Fatalf("check rc = %d, want 1 (unmapped page must fail)\n%s", rc, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "your-first-skill.md") || !strings.Contains(out, "not in manifest") {
		t.Errorf("expected unmapped your-first-skill.md problem, got:\n%s", out)
	}
}

func TestCheck_FlagsSlugWithNoTestFile(t *testing.T) {
	t.Parallel()
	root, pages, m, _ := scenario(t, false)
	// Resolve against an empty slug set: the mapped slug now resolves to nothing.
	var buf bytes.Buffer
	rc := check(&buf, root, pages, m, map[string]string{})
	if rc != 1 {
		t.Fatalf("check rc = %d, want 1 (unresolved slug must fail)\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "D-quickstart") {
		t.Errorf("expected unresolved-slug problem for D-quickstart, got:\n%s", buf.String())
	}
}

func TestCheck_FlagsMappedTestFileMissingOnDisk(t *testing.T) {
	t.Parallel()
	root, pages, m, slugs := scenario(t, false)
	// Point the slug at a file that does not exist on disk.
	slugs["D-quickstart"] = filepath.Join(root, "test", "e2e", "deleted_test.go")
	var buf bytes.Buffer
	rc := check(&buf, root, pages, m, slugs)
	if rc != 1 {
		t.Fatalf("check rc = %d, want 1 (missing test file must fail)\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "does not exist") {
		t.Errorf("expected missing-file problem, got:\n%s", buf.String())
	}
}

func TestCheck_FlagsManifestEntryForDeletedPage(t *testing.T) {
	t.Parallel()
	root, pages, _, slugs := scenario(t, false)
	// A manifest that references a page absent from the tree.
	stale := &Manifest{Pages: []Entry{
		{Path: "docs/getting-started/quickstart.md", Slug: "D-quickstart"},
		{Path: "docs/index.md", Waiver: "duplicates quickstart"},
		{Path: "docs/deleted.md", Waiver: "removed page"},
	}}
	var buf bytes.Buffer
	rc := check(&buf, root, pages, stale, slugs)
	if rc != 1 {
		t.Fatalf("check rc = %d, want 1 (stale manifest entry must fail)\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "docs/deleted.md") {
		t.Errorf("expected missing-page problem, got:\n%s", buf.String())
	}
}

func TestCheck_FlagsCoveredPageThatLostItsRunnableBlock(t *testing.T) {
	t.Parallel()
	root, pages, _, slugs := scenario(t, false)
	// config.md exists but is not runnable; covering it (rather than scanning it
	// as runnable) means the manifest claims coverage the page no longer needs.
	m := &Manifest{Pages: []Entry{
		{Path: "docs/getting-started/quickstart.md", Slug: "D-quickstart"},
		{Path: "docs/index.md", Waiver: "duplicates quickstart"},
		{Path: "docs/reference/config.md", Slug: "D-quickstart"},
	}}
	var buf bytes.Buffer
	rc := check(&buf, root, pages, m, slugs)
	if rc != 1 {
		t.Fatalf("check rc = %d, want 1 (stale coverage must fail)\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "config.md") || !strings.Contains(buf.String(), "no longer") {
		t.Errorf("expected stale-coverage problem for config.md, got:\n%s", buf.String())
	}
}

func TestReport_CountsDispositions(t *testing.T) {
	t.Parallel()
	_, pages, m, slugs := scenario(t, false)
	var buf bytes.Buffer
	rc := report(&buf, pages, m, slugs)
	if rc != 0 {
		t.Errorf("report rc = %d, want 0", rc)
	}
	out := buf.String()
	for _, want := range []string{"covered", "waived", "quickstart.md", "D-quickstart", "index.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestScanRunnablePages_IncludesReadme(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example\n")
	writeFile(t, root, "README.md", "# Project\n\n```bash\ngo build ./...\n```\n")
	writeFile(t, root, "docs/x.md", "# X\n\nno code here.\n")
	pages, err := ScanRunnablePages(root, "docs")
	if err != nil {
		t.Fatalf("ScanRunnablePages: %v", err)
	}
	found := false
	for _, p := range pages {
		if p == "README.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("README.md with a runnable block should be scanned; got %v", pages)
	}
}
