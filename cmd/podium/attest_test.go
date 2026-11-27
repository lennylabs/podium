package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

const attestRuleMD = "---\ntype: rule\nversion: 1.0.0\ndescription: Apply the rule in every session.\nrule_mode: always\n---\n\nBody.\n"

// writeAttestLayer writes files, keyed by slash path, under dir.
func writeAttestLayer(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runAttestT runs the command and returns its exit code and streams.
func runAttestT(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runAttest(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

// wantLayerSubjects computes the subjects the filesystem walk gives for dir,
// written as checksums lines, so a test compares the command with the
// library rather than with a pinned digest.
func wantLayerSubjects(t *testing.T, dir, root string) string {
	t.Helper()
	content, err := filesystem.LayerContent(filesystem.Layer{ID: "l", Path: filepath.Join(dir, root)})
	if err != nil {
		t.Fatalf("LayerContent: %v", err)
	}
	name, err := attest.LayerSubjectName(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := attest.SubjectFiles(attest.Subject{Name: name, Digest: attest.LayerDigest(content)}, content.Subjects, attest.DefaultMaxSubjects)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := attest.WriteChecksums(&b, files[0]); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// Spec: §7.3.5 — usage errors exit 2 and write nothing: more than one <dir>,
// an unknown format, a bound below 2, a --root with an empty, ".", or ".."
// segment or an excluded character, and an unknown flag. --help exits 0.
func TestAttest_UsageErrors(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "out")
	for _, args := range [][]string{
		{"a", "b"},
		{"--format", "json"},
		{"--max-subjects", "1"},
		{"--max-subjects", "x"},
		{"--root", "skills/../x"},
		{"--root", "a//b"},
		{"--root", "a b"},
		{"--bogus"},
	} {
		code, _, stderr := runAttestT(t, append(args, "--out", out)...)
		if code != 2 || stderr == "" {
			t.Errorf("podium attest %v = %d, stderr %q, want 2 with a reason", args, code, stderr)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a usage error created --out: %v", err)
	}
	if code, _, _ := runAttestT(t, "--help"); code != 0 {
		t.Errorf("--help = %d, want 0", code)
	}
}

// Spec: §7.3.5 — the command writes the checksums file of the layer with the
// layer subject first and the artifacts in ID order, removes the subject
// files of an earlier run and no other file, prints each path it wrote, and
// gives the subjects of the filesystem walk.
func TestAttest_WritesChecksums(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{
		"skills/team/b/ARTIFACT.md": attestRuleMD,
		"skills/team/a/ARTIFACT.md": attestRuleMD,
		"skills/team/DOMAIN.md":     "---\ndescription: Team.\n---\n",
	})
	out := filepath.Join(t.TempDir(), "out")
	writeAttestLayer(t, out, map[string]string{
		"podium-subjects-7.sha256": "stale\n",
		"podium-statement-2.json":  "stale\n",
		"notes.md":                 "keep\n",
	})
	code, stdout, stderr := runAttestT(t, dir, "--root", "/skills/", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	written := filepath.Join(out, "podium-subjects-1.sha256")
	if stdout != written+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, written+"\n")
	}
	got, err := os.ReadFile(written)
	if err != nil {
		t.Fatal(err)
	}
	if want := wantLayerSubjects(t, dir, "skills"); string(got) != want {
		t.Errorf("checksums =\n%s\nwant\n%s", got, want)
	}
	lines := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], "  .podium/layer/skills") ||
		!strings.HasSuffix(lines[1], "  team/a@1.0.0") || !strings.HasSuffix(lines[2], "  team/b@1.0.0") {
		t.Errorf("lines = %q, want the layer subject then team/a and team/b", lines)
	}
	entries, _ := os.ReadDir(out)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "notes.md,podium-subjects-1.sha256" {
		t.Errorf("out holds %v, want notes.md and this run's file", names)
	}
}

// Spec: §7.3.5 — the statement format splits the layer over files of at most
// --max-subjects subjects, each beginning with the layer subject.
func TestAttest_StatementFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{
		"a/ARTIFACT.md": attestRuleMD,
		"b/ARTIFACT.md": attestRuleMD,
		"c/ARTIFACT.md": attestRuleMD,
	})
	out := t.TempDir()
	code, stdout, stderr := runAttestT(t, dir, "--out", out, "--format", "statement", "--max-subjects", "2")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if n := strings.Count(stdout, "\n"); n != 3 {
		t.Fatalf("stdout = %q, want three files", stdout)
	}
	for i, id := range []string{"a", "b", "c"} {
		b, err := os.ReadFile(filepath.Join(out, attest.FormatStatement.FileName(i+1)))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.Contains(s, `"subject":[{"name":".podium/layer",`) || !strings.Contains(s, `{"name":"`+id+`@1.0.0",`) {
			t.Errorf("statement %d = %s, want the layer subject then %s", i+1, s, id)
		}
	}
}

// Spec: §4.7.6, §7.3.5 — a package whose directory name holds a line feed
// has no subject name. The command exits 1, names the path with the line
// feed escaped, and neither writes nor removes a file.
func TestAttest_RefusesALayerWithNoSubjectName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{
		"team/ok/ARTIFACT.md":   attestRuleMD,
		"team/a\nb/ARTIFACT.md": attestRuleMD,
	})
	out := t.TempDir()
	writeAttestLayer(t, out, map[string]string{"podium-subjects-1.sha256": "earlier\n"})
	code, stdout, stderr := runAttestT(t, dir, "--out", out)
	if code != 1 || stdout != "" {
		t.Fatalf("exit %d, stdout %q, want 1 and nothing written", code, stdout)
	}
	if !strings.Contains(stderr, `"team/a\nb"`) || strings.Count(stderr, "\n") != 1 {
		t.Errorf("stderr = %q, want one line naming team/a\\nb escaped", stderr)
	}
	if b, err := os.ReadFile(filepath.Join(out, "podium-subjects-1.sha256")); err != nil || string(b) != "earlier\n" {
		t.Errorf("the earlier file = %q, %v, want it untouched", b, err)
	}
}

// Spec: §7.3.5 — a layer root that does not exist or is not a directory, and
// an --out that cannot be created, exit 1.
func TestAttest_LayerRootAndOutFailures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{"a/ARTIFACT.md": attestRuleMD, "file": "x"})
	for _, args := range [][]string{
		{filepath.Join(dir, "missing")},
		{dir, "--root", "file"},
		{dir, "--out", filepath.Join(dir, "file")},
		{dir, "--out", filepath.Join(dir, "file", "sub")},
	} {
		if code, _, stderr := runAttestT(t, args...); code != 1 || !strings.HasPrefix(stderr, "error: ") {
			t.Errorf("podium attest %v = %d, stderr %q, want 1", args, code, stderr)
		}
	}
}

// Spec: §4.2, §7.3.5 — the command writes the lint.non_portable_name warning
// for a package whose ID or resource path holds a segment that is not
// portable, and the warning changes no exit status.
func TestAttest_PortableNameWarnings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{
		"team/aux/ARTIFACT.md":    attestRuleMD,
		"team/ok/ARTIFACT.md":     attestRuleMD,
		"team/ok/GIT~1/config":    "x\n",
		"team/clean/ARTIFACT.md":  attestRuleMD,
		"team/clean/release~2.md": "x\n",
	})
	code, _, stderr := runAttestT(t, dir, "--out", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	for _, want := range []string{
		`warning: team/aux: the canonical ID has the segment "aux"`,
		`warning: team/ok: bundled resource "GIT~1/config" has the segment "GIT~1"`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	}
	if strings.Contains(stderr, "team/clean") || strings.Count(stderr, "(lint.non_portable_name)") != 2 {
		t.Errorf("stderr = %q, want exactly the two warnings", stderr)
	}
}

// commitAttestRepo commits files into a new repository at dir.
func commitAttestRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	writeAttestLayer(t, dir, files)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Unix(0, 0).UTC()}
	if _, err := wt.Commit("c", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatal(err)
	}
}

// Spec: §7.3.5 — with --commit the command reads the commit's tree, so an
// edit in the working tree after the commit leaves the subjects unchanged,
// and without --commit in a working tree it reads the files and warns that
// it does. A revision that names no commit exits 1.
func TestAttest_CommitAndWorkingTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	commitAttestRepo(t, dir, map[string]string{"skills/team/a/ARTIFACT.md": attestRuleMD})
	committed := wantLayerSubjects(t, dir, "skills")

	readOut := func(out string) string {
		b, err := os.ReadFile(filepath.Join(out, "podium-subjects-1.sha256"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	out := t.TempDir()
	if code, _, stderr := runAttestT(t, dir, "--root", "skills", "--commit", "HEAD", "--out", out); code != 0 || stderr != "" {
		t.Fatalf("--commit HEAD = %d, stderr %q", code, stderr)
	}
	if got := readOut(out); got != committed {
		t.Errorf("--commit subjects =\n%s\nwant the committed layer\n%s", got, committed)
	}

	writeAttestLayer(t, dir, map[string]string{"skills/team/a/notes.md": "uncommitted\n"})
	if code, _, _ := runAttestT(t, filepath.Join(dir, "skills"), "--commit", "HEAD", "--out", out); code != 0 {
		t.Fatalf("--commit from the layer directory = %d", code)
	}
	// The layer digest does not depend on the root; the name does.
	want := strings.Replace(committed, "  .podium/layer/skills\n", "  .podium/layer\n", 1)
	if got := readOut(out); got != want {
		t.Errorf("--commit from the layer directory =\n%s\nwant\n%s", got, want)
	}

	code, _, stderr := runAttestT(t, dir, "--root", "skills", "--out", out)
	if code != 0 || !strings.Contains(stderr, "Git working tree") || !strings.Contains(stderr, "--commit HEAD") {
		t.Errorf("working tree = %d, stderr %q, want 0 and the checkout warning", code, stderr)
	}
	if got := readOut(out); got == committed {
		t.Error("the working tree with an uncommitted file gave the committed subjects")
	}

	if code, _, stderr := runAttestT(t, dir, "--commit", "no-such-branch", "--out", out); code != 1 || !strings.Contains(stderr, "no-such-branch") {
		t.Errorf("unknown revision = %d, stderr %q, want 1 naming it", code, stderr)
	}
	if code, _, _ := runAttestT(t, dir, "--root", "missing", "--commit", "HEAD", "--out", out); code != 1 {
		t.Errorf("a root the commit lacks = %d, want 1", code)
	}
}

// Spec: §7.3.5 — a failed walk of the commit, here a gitlink-free tree whose
// package has a manifest at the layer root, exits 1 and names the commit.
func TestAttest_CommitWalkFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	commitAttestRepo(t, dir, map[string]string{"skills/ARTIFACT.md": attestRuleMD})
	code, _, stderr := runAttestT(t, dir, "--root", "skills", "--commit", "HEAD", "--out", t.TempDir())
	if code != 1 || !strings.Contains(stderr, "walk commit") {
		t.Errorf("exit %d, stderr %q, want 1 naming the walk of the commit", code, stderr)
	}
}

// Spec: §7.3.5 — oneLine escapes each character that would break an output
// line and leaves every other character as it is.
func TestOneLine(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"plain path": "plain path",
		"a\nb":       `a\u000ab`,
		"a\rb\tc":    `a\u000db\u0009c`,
		"a" + string(rune(0x2028)) + "b" + string(rune(0x2029)) + "c": `a\u2028b\u2029c`,
		"a\u0085b":          `a\u0085b`,
		"tab-free café 日本語": "tab-free café 日本語",
	}
	for in, want := range cases {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}
