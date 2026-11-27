package ghactions

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The tests in this file run the guard and publish scripts of the rendered
// workflow with bash against fixture repositories, with a local bare
// repository as the origin and a stub podium whose import copies each
// bundle into the attestation directory. They pin the Git plumbing of the
// recipe: which commit is signed, what the signed ref's commit holds, and
// its parents.

// recipe is a fixture: a bare origin, a seeded main branch, and the
// scripts of the rendered workflow.
type recipe struct {
	t       *testing.T
	base    string
	origin  string
	bin     string
	guard   string
	publish string
	env     []string
}

// stubPodium simulates `podium attest import`: it copies each bundle into
// the root's attestation directory under its Git blob hash, and removes the
// bundles of the build signer the directory held, as pruning does when the
// imported set lists every subject they list. When $RACE_MARKER exists, the
// first call removes it and pushes a scanner's commit to the signed ref, so
// the publish loses the race of its first push.
const stubPodium = `#!/usr/bin/env bash
set -euo pipefail
[ "$1" = attest ] && [ "$2" = import ] || { echo "stub: $*" >&2; exit 64; }
shift 2
root=""
while [ $# -gt 0 ]; do
  case "$1" in
    --root) root="$2"; shift 2 ;;
    --commit) shift 2 ;;
    *) break ;;
  esac
done
r="${root#/}"; r="${r%/}"
dir="${r:+$r/}.podium/attestations"
mkdir -p "$dir"
for f in "$dir"/*.sigstore.json; do
  [ -e "$f" ] || continue
  if head -n 1 "$f" | grep -q '^signer=build$'; then rm -f "$f"; fi
done
for b in "$@"; do
  cp "$b" "$dir/$(git hash-object "$b").sigstore.json"
done
if [ -n "${RACE_MARKER:-}" ] && [ -e "$RACE_MARKER" ]; then
  rm -f "$RACE_MARKER"
  "$RACE_SCRIPT"
fi
`

func newRecipe(t *testing.T) *recipe {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the recipe's scripts run under bash on a Linux runner")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	base := t.TempDir()
	r := &recipe{t: t, base: base, origin: filepath.Join(base, "origin.git"), bin: filepath.Join(base, "bin")}
	r.env = []string{
		"HOME=" + base, "GIT_CONFIG_GLOBAL=" + filepath.Join(base, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com", "GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com",
		"PATH=" + r.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"PODIUM_ROOTS=skills", "PODIUM_MAIN_BRANCH=main", "PODIUM_SIGNED_REF=podium/signed", "PODIUM_RECOVER=false",
		"RUNNER_TEMP=" + filepath.Join(base, "runner"),
	}
	if err := os.WriteFile(filepath.Join(base, "gitconfig"), []byte("[init]\n\tdefaultBranch = main\n[protocol \"file\"]\n\tallow = always\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.bin, "podium"), []byte(stubPodium), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := parseWorkflow(t, Workflow{Kind: KindRepository, Version: "1.2.3", Branch: "main", Roots: []string{"skills"}})
	r.guard = stringOf(step(t, doc, "subjects", "Guard")["run"])
	r.publish = stringOf(step(t, doc, "publish", "Publish")["run"])

	r.git("", "init", "-q", "--bare", r.origin)
	seed := filepath.Join(base, "seed")
	r.git("", "clone", "-q", r.origin, seed)
	r.commitFile(seed, "skills/greet/ARTIFACT.md", "v1")
	r.git(seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	return r
}

// git runs git in dir and returns its trimmed standard output.
func (r *recipe) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitFile writes content to rel in the working tree dir and commits it.
func (r *recipe) commitFile(dir, rel, content string) string {
	r.t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git(dir, "add", "-A")
	r.git(dir, "commit", "-q", "-m", "change "+rel)
	return r.git(dir, "rev-parse", "HEAD")
}

// pushMain commits a change on main in a fresh clone and returns the commit.
func (r *recipe) pushMain(content string) string {
	r.t.Helper()
	dir, err := os.MkdirTemp(r.base, "dev-")
	if err != nil {
		r.t.Fatal(err)
	}
	r.git("", "clone", "-q", r.origin, dir)
	c := r.commitFile(dir, "skills/greet/ARTIFACT.md", content)
	r.git(dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	return c
}

// checkout returns a depth-1 clone of main's tip, as actions/checkout
// writes one.
func (r *recipe) checkout() string {
	r.t.Helper()
	dir, err := os.MkdirTemp(r.base, "run-")
	if err != nil {
		r.t.Fatal(err)
	}
	r.git("", "clone", "-q", "--depth", "1", "--branch", "main", "file://"+r.origin, dir)
	return dir
}

// runScript runs script with bash in dir under the fixture's environment
// plus extra, and returns its exit code and output.
func (r *recipe) runScript(dir, script string, extra ...string) (int, string) {
	r.t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, r.env...), extra...)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		r.t.Fatalf("bash: %v", err)
	}
	return 0, string(out)
}

// bundle writes the run's bundle for root index 1 with the given first
// line, which the stub reads as the signer.
func (r *recipe) bundle(signer, body string) {
	r.t.Helper()
	dir := filepath.Join(r.base, "runner", "podium-bundles", "1")
	if err := os.RemoveAll(filepath.Join(r.base, "runner")); err != nil {
		r.t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "podium-subjects-1.sha256.jsonl"), []byte("signer="+signer+"\n"+body+"\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// publishRun runs the publish script for main's tip and returns the
// resulting tip of the signed ref.
func (r *recipe) publishRun(extra ...string) string {
	r.t.Helper()
	dir := r.checkout()
	summary := filepath.Join(r.base, "summary")
	code, out := r.runScript(dir, r.publish, append([]string{"GITHUB_STEP_SUMMARY=" + summary}, extra...)...)
	if code != 0 {
		r.t.Fatalf("publish exited %d:\n%s", code, out)
	}
	return r.git(r.origin, "rev-parse", "refs/heads/podium/signed")
}

// signedFiles lists the bundle files in the attestation directory of the
// signed ref's tip, with the first line of each.
func (r *recipe) signedFiles() []string {
	r.t.Helper()
	names := r.git(r.origin, "ls-tree", "--name-only", "refs/heads/podium/signed:skills/.podium/attestations")
	var out []string
	for _, n := range strings.Fields(names) {
		first, _, _ := strings.Cut(r.git(r.origin, "show", "refs/heads/podium/signed:skills/.podium/attestations/"+n), "\n")
		out = append(out, first)
	}
	return out
}

func (r *recipe) parents(commit string) []string {
	return strings.Fields(r.git(r.origin, "log", "-1", "--format=%P", commit))
}

// Spec: §7.3.5 (Writing the signing workflow) — the guard signs only the
// tip of the default branch on that branch, ends a stopped run with
// success and a notice, and fails closed when it cannot read the tip.
func TestRecipeScript_Guard(t *testing.T) {
	t.Parallel()
	r := newRecipe(t)
	old := r.git(r.origin, "rev-parse", "refs/heads/main")
	tip := r.pushMain("v2")
	work := filepath.Join(r.base, "full")
	r.git("", "clone", "-q", r.origin, work)

	cases := []struct {
		name, ref, sha, want string
	}{
		{"tip", "refs/heads/main", tip, "sign=true"},
		{"re-run of an older commit", "refs/heads/main", old, "sign=false"},
		{"another branch", "refs/heads/feature", tip, "sign=false"},
	}
	for _, c := range cases {
		output, summary := filepath.Join(r.base, "out-"+c.name), filepath.Join(r.base, "summary-"+c.name)
		code, out := r.runScript(work, r.guard, "GITHUB_REF="+c.ref, "GITHUB_SHA="+c.sha, "GITHUB_OUTPUT="+output, "GITHUB_STEP_SUMMARY="+summary)
		got, _ := os.ReadFile(output)
		if code != 0 || strings.TrimSpace(string(got)) != c.want {
			t.Errorf("%s: exit %d, output %q, want %q\n%s", c.name, code, got, c.want, out)
		}
		if c.want == "sign=false" {
			notes, _ := os.ReadFile(summary)
			if !strings.Contains(out, "::notice::podium-attest did not sign") || !strings.Contains(string(notes), "did not sign") {
				t.Errorf("%s: no notice in the log or the summary:\n%s\n%s", c.name, out, notes)
			}
		}
	}

	r.git(work, "remote", "set-url", "origin", filepath.Join(r.base, "gone.git"))
	output := filepath.Join(r.base, "out-unreachable")
	code, out := r.runScript(work, r.guard, "GITHUB_REF=refs/heads/main", "GITHUB_SHA="+tip, "GITHUB_OUTPUT="+output, "GITHUB_STEP_SUMMARY="+filepath.Join(r.base, "s"))
	if code == 0 {
		t.Errorf("the guard passed with an unreachable origin:\n%s", out)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — the first run creates the
// signed ref with main as the only parent; a later run restores the
// directory from the signed ref's tip, so a second signer's bundle stays,
// and commits with the tip and main as parents; a recovery run keeps only
// its own bundles; a rewritten main still publishes as a fast-forward.
func TestRecipeScript_Publish(t *testing.T) {
	t.Parallel()
	r := newRecipe(t)
	main1 := r.git(r.origin, "rev-parse", "refs/heads/main")

	r.bundle("build", "run 1")
	first := r.publishRun()
	if p := r.parents(first); len(p) != 1 || p[0] != main1 {
		t.Fatalf("first commit parents = %v, want [%s]", p, main1)
	}
	if got := r.signedFiles(); len(got) != 1 || got[0] != "signer=build" {
		t.Fatalf("first run files = %v", got)
	}
	if r.git(r.origin, "show", first+":skills/greet/ARTIFACT.md") != "v1" {
		t.Error("the signed commit does not hold main's tree")
	}

	// A scanner appends its bundle to the signed ref's tip.
	scan := filepath.Join(r.base, "scan")
	r.git("", "clone", "-q", "--branch", "podium/signed", r.origin, scan)
	r.commitFile(scan, "skills/.podium/attestations/scan.sigstore.json", "signer=scan\n")
	r.git(scan, "push", "-q", "origin", "HEAD:refs/heads/podium/signed")
	scanned := r.git(r.origin, "rev-parse", "refs/heads/podium/signed")

	main2 := r.pushMain("v2")
	r.bundle("build", "run 2")
	second := r.publishRun()
	if p := r.parents(second); len(p) != 2 || p[0] != scanned || p[1] != main2 {
		t.Fatalf("second commit parents = %v, want [%s %s]", p, scanned, main2)
	}
	if got := strings.Join(r.signedFiles(), ","); !strings.Contains(got, "signer=scan") || strings.Count(got, "signer=build") != 1 {
		t.Errorf("second run files = %v, want the scanner's and one build bundle", got)
	}

	// A recovery run keeps only its own bundles.
	r.bundle("build", "run 3")
	third := r.publishRun("PODIUM_RECOVER=true")
	if got := r.signedFiles(); len(got) != 1 || got[0] != "signer=build" {
		t.Errorf("recovery run files = %v", got)
	}
	if p := r.parents(third); len(p) != 2 || p[0] != second {
		t.Errorf("recovery commit parents = %v, want the tip first", p)
	}

	// A rewritten main: the next run signs the new tip as a fast-forward of
	// the signed ref.
	rewrite := filepath.Join(r.base, "rewrite")
	r.git("", "clone", "-q", r.origin, rewrite)
	r.git(rewrite, "checkout", "-q", "--orphan", "fresh")
	r.git(rewrite, "rm", "-rq", "--cached", ".")
	newMain := r.commitFile(rewrite, "skills/greet/ARTIFACT.md", "rewritten")
	r.git(rewrite, "push", "-q", "--force", "origin", "HEAD:refs/heads/main")
	r.bundle("build", "run 4")
	fourth := r.publishRun()
	if p := r.parents(fourth); len(p) != 2 || p[0] != third || p[1] != newMain {
		t.Errorf("rewritten-main commit parents = %v, want [%s %s]", p, third, newMain)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — a push that loses the race
// to another writer of the signed ref fetches the new tip, restores the
// directory from it, and pushes again.
func TestRecipeScript_PublishRetriesALostRace(t *testing.T) {
	t.Parallel()
	r := newRecipe(t)
	r.bundle("build", "run 1")
	r.publishRun()

	race := filepath.Join(r.base, "race.sh")
	scan := filepath.Join(r.base, "scan")
	r.git("", "clone", "-q", "--branch", "podium/signed", r.origin, scan)
	script := "#!/usr/bin/env bash\nset -euo pipefail\ncd " + scan + "\nmkdir -p skills/.podium/attestations\n" +
		"printf 'signer=scan\\n' > skills/.podium/attestations/scan.sigstore.json\n" +
		"git add -A && git commit -q -m scan && git push -q origin HEAD:refs/heads/podium/signed\n"
	if err := os.WriteFile(race, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(r.base, "race-once")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	main2 := r.pushMain("v2")
	r.bundle("build", "run 2")
	tip := r.publishRun("RACE_MARKER="+marker, "RACE_SCRIPT="+race)
	scanned := r.git(r.origin, "rev-parse", tip+"^1")
	if r.git(r.origin, "log", "-1", "--format=%s", scanned) != "scan" {
		t.Errorf("the published commit's first parent is not the scanner's commit")
	}
	if p := r.parents(tip); len(p) != 2 || p[1] != main2 {
		t.Errorf("parents = %v", p)
	}
	if got := strings.Join(r.signedFiles(), ","); !strings.Contains(got, "signer=scan") {
		t.Errorf("files = %v, want the scanner's bundle restored", got)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — the guard reads the tip from
// the ls-remote line whose ref name is exactly refs/heads/<branch>, so a
// branch or a tag whose name ends in that text does not stop signing.
func TestRecipeScript_GuardReadsTheExactRef(t *testing.T) {
	t.Parallel()
	r := newRecipe(t)
	tip := r.git(r.origin, "rev-parse", "refs/heads/main")
	work := filepath.Join(r.base, "full")
	r.git("", "clone", "-q", r.origin, work)
	r.git(work, "commit", "-q", "--allow-empty", "-m", "decoy")
	r.git(work, "push", "-q", "origin", "HEAD:refs/heads/attacker/refs/heads/main")
	r.git(work, "push", "-q", "origin", "HEAD:refs/tags/refs/heads/main")
	output := filepath.Join(r.base, "out")
	code, out := r.runScript(work, r.guard, "GITHUB_REF=refs/heads/main", "GITHUB_SHA="+tip, "GITHUB_OUTPUT="+output, "GITHUB_STEP_SUMMARY="+filepath.Join(r.base, "s"))
	got, _ := os.ReadFile(output)
	if code != 0 || strings.TrimSpace(string(got)) != "sign=true" {
		t.Errorf("guard with decoy refs: exit %d, output %q\n%s", code, got, out)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — the publish step reads the
// signed ref by its exact name, so a decoy ref whose name ends in
// refs/heads/podium/signed neither fails the first run nor becomes a parent.
func TestRecipeScript_PublishReadsTheExactSignedRef(t *testing.T) {
	t.Parallel()
	r := newRecipe(t)
	main1 := r.git(r.origin, "rev-parse", "refs/heads/main")
	work := filepath.Join(r.base, "decoy")
	r.git("", "clone", "-q", r.origin, work)
	r.git(work, "commit", "-q", "--allow-empty", "-m", "decoy")
	r.git(work, "push", "-q", "origin", "HEAD:refs/heads/attacker/refs/heads/podium/signed")
	r.bundle("build", "run 1")
	first := r.publishRun()
	if p := r.parents(first); len(p) != 1 || p[0] != main1 {
		t.Errorf("first commit parents = %v, want [%s]", p, main1)
	}
	r.pushMain("v2")
	r.bundle("build", "run 2")
	second := r.publishRun()
	if p := r.parents(second); len(p) != 2 || p[0] != first {
		t.Errorf("second commit parents = %v, want the real signed tip first", p)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — a re-run of the publish job
// alone of an older run, whose commit is no longer the tip of main, ends
// successfully with a notice and leaves podium/signed where the newer run
// put it.
func TestRecipeScript_PublishRerunOfAnOlderRunPublishesNothing(t *testing.T) {
	t.Parallel()
	r := newRecipe(t)
	oldCheckout := r.checkout()
	main2 := r.pushMain("v2")
	r.bundle("build", "run B")
	newer := r.publishRun()

	r.bundle("build", "run A")
	summary := filepath.Join(r.base, "summary-rerun")
	code, out := r.runScript(oldCheckout, r.publish, "GITHUB_STEP_SUMMARY="+summary)
	if code != 0 {
		t.Fatalf("re-run exited %d:\n%s", code, out)
	}
	if got := r.git(r.origin, "rev-parse", "refs/heads/podium/signed"); got != newer {
		t.Errorf("podium/signed moved from %s to %s", newer, got)
	}
	if r.git(r.origin, "show", "refs/heads/podium/signed:skills/greet/ARTIFACT.md") != "v2" {
		t.Error("podium/signed no longer holds the newest main")
	}
	notes, _ := os.ReadFile(summary)
	if !strings.Contains(out, "::notice::podium-attest did not publish") || !strings.Contains(string(notes), "which is now "+main2) ||
		!strings.Contains(string(notes), "a manual run of this workflow on main, signs the tip") {
		t.Errorf("no notice:\n%s\n%s", out, notes)
	}
}

// stubGH records each call's arguments, one per line after a "gh" line, in
// $GH_LOG. `release download` creates the file it names; `attestation
// verify` exits with $GH_VERIFY_EXIT.
const stubGH = `#!/usr/bin/env bash
set -euo pipefail
{ echo gh; printf '%s\n' "$@"; } >> "$GH_LOG"
if [ "$1 $2" = "release download" ]; then
  dir="" pattern=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --dir) dir="$2"; shift 2 ;;
      --pattern) pattern="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  printf 'binary' > "$dir/$pattern"
  exit 0
fi
exit "${GH_VERIFY_EXIT:-0}"
`

// Spec: §7.3.5 (Writing the signing workflow) — the install step downloads
// the release and verifies it against the exact identity of the release
// workflow at the release tag, the GitHub Actions issuer, and hosted
// runners, before the binary is put on the path; the golden file pins the
// argument lists. A binary that does not verify fails the step and is not
// installed.
func TestRecipeScript_InstallVerifiesTheExactIdentity(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the recipe's scripts run under bash on a Linux runner")
	}
	doc := parseWorkflow(t, Workflow{Kind: KindRepository, Version: "0.8.0", Branch: "main"})
	script := stringOf(step(t, doc, "subjects", "Install podium")["run"])
	if publish := stringOf(step(t, doc, "publish", "Install podium")["run"]); publish != script {
		t.Fatal("the two install steps differ")
	}
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stubGH), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(verifyExit string) (int, string, string, string) {
		runner := filepath.Join(base, "runner-"+verifyExit)
		if err := os.MkdirAll(runner, 0o755); err != nil {
			t.Fatal(err)
		}
		logFile, pathFile := filepath.Join(runner, "gh.log"), filepath.Join(runner, "path")
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = []string{
			"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "RUNNER_TEMP=" + runner,
			"GITHUB_PATH=" + pathFile, "PODIUM_VERSION=v0.8.0", "GH_LOG=" + logFile, "GH_VERIFY_EXIT=" + verifyExit,
		}
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		calls, _ := os.ReadFile(logFile)
		path, _ := os.ReadFile(pathFile)
		norm := strings.ReplaceAll(string(calls), runner, "<RUNNER_TEMP>")
		norm = regexp.MustCompile(`podium-linux-(amd64|arm64)`).ReplaceAllString(norm, "podium-linux-<arch>")
		return code, string(out), norm, string(path)
	}

	code, out, calls, path := run("0")
	if code != 0 || strings.TrimSpace(path) == "" {
		t.Fatalf("install exited %d, path %q:\n%s", code, path, out)
	}
	golden := filepath.Join("testdata", "install-gh-calls.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(calls), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if want, err := os.ReadFile(golden); err != nil || string(want) != calls {
		t.Errorf("gh calls differ from %s (%v); regenerate with UPDATE_GOLDEN=1\n%s", golden, err, calls)
	}
	if !strings.Contains(calls, "--cert-identity\nhttps://github.com/lennylabs/podium/.github/workflows/release.yml@refs/tags/v0.8.0\n") {
		t.Errorf("the verification names no exact identity:\n%s", calls)
	}

	code, out, _, path = run("1")
	if code == 0 || strings.TrimSpace(path) != "" {
		t.Errorf("a binary that does not verify was installed: exit %d, path %q\n%s", code, path, out)
	}
	if _, err := os.Stat(filepath.Join(base, "runner-1", "podium-bin", "podium")); err == nil {
		t.Error("the unverified binary was moved into place")
	}
}
