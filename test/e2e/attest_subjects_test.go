package e2e

// `podium attest`, end to end.
//
// Spec §7.3.5 defines the command that writes the subjects an author
// attestation of a layer signs, and §4.7.6 defines the digests it writes: for
// a git layer, the digests of the commit's blob bytes and tree paths. These
// tests run the real binary on a fixture Git repository whose commit holds a
// Git LFS pointer, files under a line-ending rule, and a symbolic link, and
// compare its subjects with the digests registry ingest computes from a git
// snapshot of the same commit. They also cover the refusals, the
// lint.non_portable_name warning of §4.2, and the same warning at registry
// ingest through `podium layer reingest`.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

const (
	attestRuleArtifact = "---\ntype: rule\nversion: 1.0.0\ndescription: Apply the review rule in every session.\nrule_mode: always\n---\n\nBody.\n"
	attestSkillMD      = "---\nname: other\ndescription: Summarize the weekly status notes for the team.\n---\n\nBody.\n"
	attestLFSPointer   = "version https://git-lfs.github.com/spec/v1\noid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 12345\n"
)

// writeTree writes files, keyed by slash path, under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mustGit runs git in dir and fails the test on a non-zero exit.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if r := gitCmd(t, dir, args...); r.Exit != 0 {
		t.Fatalf("git %s: exit=%d stderr=%s", strings.Join(args, " "), r.Exit, r.Stderr)
	}
}

// attestFixtureRepo commits a layer under skills/ whose commit bytes differ
// from a checkout's: .md files under an eol=crlf rule, an LFS pointer under an
// lfs filter that no configuration defines, and a symbolic link inside a
// package. It returns the repository and a clone of it, whose checkout holds
// CRLF line endings and the link as a link.
func attestFixtureRepo(t *testing.T) (repo, clone string) {
	t.Helper()
	requireGit(t)
	repo = filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-q")
	mustGit(t, repo, "checkout", "-q", "-B", "main")
	writeTree(t, repo, map[string]string{
		".gitattributes":              "*.md text eol=crlf\n*.bin filter=lfs diff=lfs merge=lfs -text\n",
		"skills/shared/common.md":     "Shared text.\n",
		"skills/team/pkg/ARTIFACT.md": attestRuleArtifact,
		"skills/team/pkg/model.bin":   attestLFSPointer,
		"skills/team/other/SKILL.md":  attestSkillMD,
		"skills/team/DOMAIN.md":       "---\ndescription: The team's artifacts.\n---\n",
	})
	if err := os.MkdirAll(filepath.Join(repo, "skills/team/pkg/refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../shared/common.md", filepath.Join(repo, "skills/team/pkg/refs/common.md")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-q", "-m", "layer")
	clone = filepath.Join(t.TempDir(), "clone")
	mustGit(t, filepath.Dir(clone), "clone", "-q", repo, clone)
	return repo, clone
}

// registryChecksums returns the checksums file of the subjects registry
// ingest computes for root at ref: the git layer source's snapshot of the
// repository, walked by ingest, in the format `podium attest` writes.
func registryChecksums(t *testing.T, repo, ref, root string) string {
	t.Helper()
	snap, err := (source.Git{}).Snapshot(context.Background(), source.LayerConfig{Repo: "file://" + repo, Ref: ref, Root: root})
	if err != nil {
		t.Fatalf("git snapshot: %v", err)
	}
	content, err := ingest.LayerContent(snap.Files)
	if err != nil {
		t.Fatalf("ingest.LayerContent: %v", err)
	}
	name, err := attest.LayerSubjectName(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := attest.SubjectFiles(attest.Subject{Name: name, Digest: attest.LayerDigest(content)}, content.Subjects, attest.DefaultMaxSubjects)
	if err != nil || len(files) != 1 {
		t.Fatalf("SubjectFiles = %d files, %v", len(files), err)
	}
	var b bytes.Buffer
	if err := attest.WriteChecksums(&b, files[0]); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// checksumLines maps each subject name of a checksums file to its digest.
func checksumLines(t *testing.T, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		hexDigest, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("checksums line %q has no two-space separator", line)
		}
		out[name] = hexDigest
	}
	return out
}

func readSubjects(t *testing.T, out string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(out, "podium-subjects-1.sha256"))
	if err != nil {
		t.Fatalf("read subjects: %v", err)
	}
	return string(b)
}

// Spec: §4.7.6, §7.3.5, §11 (Package and layer digest tests) — `podium
// attest --commit HEAD` in a checkout writes exactly the subjects registry
// ingest computes from a git snapshot of the same commit, although the
// checkout holds CRLF line endings, an unsmudged LFS pointer, and a link.
// The same command without --commit digests the checkout, gives other
// digests for the packages whose bytes the checkout changed, and warns.
func TestAttest_CommitMatchesRegistryIngest(t *testing.T) {
	t.Parallel()
	repo, clone := attestFixtureRepo(t)
	want := registryChecksums(t, repo, "main", "skills")

	committed := t.TempDir()
	res := runPodium(t, clone, gitEnv(), "attest", "--root", "skills", "--commit", "HEAD", "--out", committed)
	cliWantExit(t, res, 0, "attest --commit HEAD")
	if res.Stderr != "" {
		t.Errorf("attest --commit wrote to stderr: %s", res.Stderr)
	}
	got := readSubjects(t, committed)
	if got != want {
		t.Fatalf("attest --commit subjects =\n%s\nregistry ingest of the commit =\n%s", got, want)
	}
	lines := checksumLines(t, got)
	for _, name := range []string{".podium/layer/skills", "team/pkg@1.0.0", "team/other"} {
		if _, ok := lines[name]; !ok {
			t.Errorf("subjects lack %s:\n%s", name, got)
		}
	}
	if !strings.HasPrefix(got, lines[".podium/layer/skills"]+"  .podium/layer/skills\n") {
		t.Errorf("the layer subject is not the first line:\n%s", got)
	}

	checkout := t.TempDir()
	res = runPodium(t, clone, gitEnv(), "attest", "--root", "skills", "--out", checkout)
	cliWantExit(t, res, 0, "attest of the checkout")
	cliContains(t, res.Stderr, "lies in a Git working tree", "checkout warning")
	cliContains(t, res.Stderr, "--commit HEAD", "checkout warning names --commit")
	worktree := checksumLines(t, readSubjects(t, checkout))
	for _, name := range []string{".podium/layer/skills", "team/pkg@1.0.0", "team/other"} {
		if worktree[name] == lines[name] {
			t.Errorf("%s: the checkout's digest equals the commit's, want the filters and the link to change it", name)
		}
	}
}

// Spec: §4.7.6, §7.3.5 — a package whose directory name holds a line feed
// has no subject name: the command exits 1, names the path with the line feed
// escaped, and writes no file. A --root with a ".." segment is a usage error.
func TestAttest_Refusals(t *testing.T) {
	t.Parallel()
	layer := t.TempDir()
	writeTree(t, layer, map[string]string{
		"team/ok/ARTIFACT.md":   attestRuleArtifact,
		"team/a\nb/ARTIFACT.md": attestRuleArtifact,
	})
	out := filepath.Join(t.TempDir(), "out")
	res := runPodium(t, "", nil, "attest", layer, "--out", out)
	cliWantExit(t, res, 1, "attest of a layer with a line feed in a package name")
	cliContains(t, res.Stderr, `"team/a\nb"`, "refusal names the escaped path")
	if strings.Count(res.Stderr, "\n") != 1 {
		t.Errorf("stderr holds more than one line: %q", res.Stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a refused layer created --out: %v", err)
	}

	res = runPodium(t, "", nil, "attest", layer, "--root", "team/../x", "--out", out)
	cliWantExit(t, res, 2, "attest with a .. segment in --root")
}

// Spec: §4.2, §7.3.5 — the command warns with lint.non_portable_name on an
// ID segment that is a device name and on a resource segment in the 8.3
// short-name form, and exits 0. A package named "CON .txt" draws the warning
// and also has no subject name, because its name holds a space, so that run
// warns and then exits 1.
func TestAttest_PortableNameWarnings(t *testing.T) {
	t.Parallel()
	layer := t.TempDir()
	writeTree(t, layer, map[string]string{
		"team/aux/ARTIFACT.md":      attestRuleArtifact,
		"team/notes/ARTIFACT.md":    attestRuleArtifact,
		"team/notes/refs/con.md":    "Reference.\n",
		"team/hooks/ARTIFACT.md":    attestRuleArtifact,
		"team/hooks/GIT~1/config":   "x\n",
		"team/release/ARTIFACT.md":  attestRuleArtifact,
		"team/release/release~2.md": "Notes.\n",
	})
	res := runPodium(t, "", nil, "attest", layer, "--out", t.TempDir())
	cliWantExit(t, res, 0, "attest with names that are not portable")
	for _, want := range []string{
		`warning: team/aux: the canonical ID has the segment "aux"`,
		`warning: team/notes: bundled resource "refs/con.md" has the segment "con.md"`,
		`warning: team/hooks: bundled resource "GIT~1/config" has the segment "GIT~1"`,
	} {
		cliContains(t, res.Stderr, want, "portable-name warning")
	}
	if strings.Count(res.Stderr, "(lint.non_portable_name)") != 3 {
		t.Errorf("stderr = %s, want three warnings and none for team/release", res.Stderr)
	}

	writeTree(t, layer, map[string]string{"team/CON .txt/ARTIFACT.md": attestRuleArtifact})
	res = runPodium(t, "", nil, "attest", layer, "--out", t.TempDir())
	cliWantExit(t, res, 1, "attest with a package named CON .txt")
	cliContains(t, res.Stderr, `warning: team/CON .txt: the canonical ID has the segment "CON .txt"`, "warning before the refusal")
	cliContains(t, res.Stderr, `error: the package at "team/CON .txt" has no subject name`, "refusal")
}

// Spec: §4.2, §7.3.1 — registry ingest stores the artifacts whose names are
// not portable, and `podium layer reingest` lists each with its ID and the
// segment as a lint.non_portable_name advisory, exiting 0.
func TestAttest_PortableNameAtIngest(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	layer := newRepublishLayer(t, srv, "portable-names")
	ri := reingestArtifacts(t, layer, map[string]string{
		"team/aux/ARTIFACT.md":   agentArtifact("Reconcile vendor invoices at month-end close."),
		"team/notes/ARTIFACT.md": agentArtifact("Summarize the weekly status notes for the team."),
		"team/notes/refs/con.md": "Reference.\n",
	})
	cliContains(t, ri.Stdout, `advisory: team/aux [warning] the canonical ID has the segment "aux"`, "ID advisory")
	cliContains(t, ri.Stdout, `advisory: team/notes [warning] bundled resource "refs/con.md" has the segment "con.md"`, "resource advisory")
	if n := strings.Count(ri.Stdout, "(lint.non_portable_name)"); n != 2 {
		t.Errorf("reingest stdout carries %d portable-name advisories, want 2:\n%s", n, ri.Stdout)
	}
	for _, id := range []string{"team/aux", "team/notes"} {
		var resp map[string]any
		getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &resp)
		if resp["id"] != id {
			t.Errorf("load %s: id=%v, want the stored artifact", id, resp["id"])
		}
	}
}

// Spec: §7.3.5 — the statement format carries the Podium predicate type with
// an empty predicate by default, and --predicate-type sets another type for
// the statement format alone. Runs for two layer roots that share --out keep
// both roots' files, each run prints the paths it wrote, and a rerun for one
// root replaces that root's file alone.
func TestAttest_PredicateTypeAndSharedOut(t *testing.T) {
	t.Parallel()
	layer := t.TempDir()
	writeTree(t, layer, map[string]string{
		"a/team/x/ARTIFACT.md": attestRuleArtifact,
		"b/team/y/ARTIFACT.md": attestRuleArtifact,
	})
	out := t.TempDir()

	res := runPodium(t, "", nil, "attest", layer, "--root", "a", "--out", out, "--format", "statement")
	cliWantExit(t, res, 0, "attest --format statement")
	first := strings.TrimSpace(res.Stdout)
	b, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read the printed path %q: %v", first, err)
	}
	cliContains(t, string(b), `"predicateType":"https://lennylabs.github.io/podium/attestation/subjects/v1","predicate":{}}`, "default predicate type")
	cliContains(t, string(b), `"subject":[{"name":".podium/layer/a",`, "layer subject first")

	res = runPodium(t, "", nil, "attest", layer, "--root", "b", "--out", out, "--format", "statement",
		"--predicate-type", "https://slsa.dev/provenance/v1")
	cliWantExit(t, res, 0, "attest --predicate-type")
	second := strings.TrimSpace(res.Stdout)
	if second == first {
		t.Fatalf("the second root's run printed the first root's path %q", first)
	}
	b, err = os.ReadFile(second)
	if err != nil {
		t.Fatalf("read %q: %v", second, err)
	}
	cliContains(t, string(b), `"predicateType":"https://slsa.dev/provenance/v1","predicate":{}}`, "explicit predicate type")
	cliContains(t, string(b), `"subject":[{"name":".podium/layer/b",`, "second root's layer subject")
	if _, err := os.Stat(first); err != nil {
		t.Errorf("the second root's run removed the first root's file: %v", err)
	}

	res = runPodium(t, "", nil, "attest", layer, "--root", "a", "--out", out, "--format", "statement")
	cliWantExit(t, res, 0, "rerun for the first root")
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("out holds %d files after a rerun, want one per root", len(entries))
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("the rerun for root a removed root b's file: %v", err)
	}

	res = runPodium(t, "", nil, "attest", layer, "--root", "a", "--out", out, "--predicate-type", "https://slsa.dev/provenance/v1")
	cliWantExit(t, res, 2, "--predicate-type with the checksums format")
	res = runPodium(t, "", nil, "attest", layer, "--root", "a", "--out", out, "--format", "statement", "--predicate-type", "subjects/v1")
	cliWantExit(t, res, 2, "a relative --predicate-type")
}
