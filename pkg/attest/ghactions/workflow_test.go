package ghactions

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/attest"
)

// goldenWorkflows are the workflows the golden files under testdata pin.
// Regenerate them with UPDATE_GOLDEN=1 go test ./pkg/attest/ghactions/.
var goldenWorkflows = map[string]Workflow{
	"repository.yml":       {Kind: KindRepository, Version: "1.2.3", Branch: "main"},
	"repository-roots.yml": {Kind: KindRepository, Version: "v1.2.3", Branch: "trunk", Roots: []string{"skills", "team/tools"}},
	"caller.yml":           {Kind: KindCaller, Version: "1.2.3", Branch: "main", Roots: []string{"skills"}, Shared: "acme/.github"},
	"reusable.yml":         {Kind: KindReusable, Version: "1.2.3"},
}

// Spec: §7.3.5 (Writing the signing workflow) — the rendered workflow of
// each kind is the file the golden pins, so a change to the recipe is a
// reviewed change of the golden.
func TestRender_Golden(t *testing.T) {
	t.Parallel()
	for name, w := range goldenWorkflows {
		got, err := Render(w)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		path := filepath.Join("testdata", name)
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (regenerate with UPDATE_GOLDEN=1)", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from the golden file; regenerate with UPDATE_GOLDEN=1 and review the diff\ngot:\n%s", name, got)
		}
	}
}

// pinnedAction is the form of every action reference: a commit hash.
var pinnedAction = regexp.MustCompile(`\Aactions/[a-z-]+@[0-9a-f]{40}\z`)

// parseWorkflow decodes a rendered workflow.
func parseWorkflow(t *testing.T, w Workflow) map[string]any {
	t.Helper()
	b, err := Render(w)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the workflow is no YAML document: %v\n%s", err, b)
	}
	return doc
}

// at walks doc along keys, failing the test at a missing member.
func at(t *testing.T, doc any, keys ...any) any {
	t.Helper()
	cur := doc
	for _, k := range keys {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[k.(string)]
			if !ok {
				t.Fatalf("no member %v in %v", k, keys)
			}
			cur = v
		case []any:
			cur = c[k.(int)]
		default:
			t.Fatalf("cannot index %T with %v", cur, k)
		}
	}
	return cur
}

// step returns the step of job whose name or uses prefix is key.
func step(t *testing.T, doc map[string]any, job, key string) map[string]any {
	t.Helper()
	for _, s := range at(t, doc, "jobs", job, "steps").([]any) {
		m := s.(map[string]any)
		if m["name"] == key || strings.HasPrefix(stringOf(m["uses"]), key) {
			return m
		}
	}
	t.Fatalf("job %s has no step %s", job, key)
	return nil
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// Spec: §7.3.5 (Writing the signing workflow) — the workflow follows the
// recommended steps: a push trigger on the default branch that ignores each
// attestation directory, a manual run with a recover input, a concurrency
// group that keeps a pending run, a guard before any signing, actions
// pinned to commits, the signing permissions on the signing job alone, and
// a publish job after it.
func TestRender_RepositoryStructure(t *testing.T) {
	t.Parallel()
	doc := parseWorkflow(t, Workflow{Kind: KindRepository, Version: "0.7.0", Branch: "main", Roots: []string{"skills", "/team/tools/"}})
	if got := at(t, doc, "on", "push", "branches"); len(got.([]any)) != 1 || got.([]any)[0] != "main" {
		t.Errorf("push branches = %v", got)
	}
	ignore := at(t, doc, "on", "push", "paths-ignore").([]any)
	if len(ignore) != 2 || ignore[0] != "skills/.podium/attestations/**" || ignore[1] != "team/tools/.podium/attestations/**" {
		t.Errorf("paths-ignore = %v", ignore)
	}
	if at(t, doc, "on", "workflow_dispatch", "inputs", "recover", "type") != "boolean" {
		t.Error("workflow_dispatch has no boolean recover input")
	}
	if at(t, doc, "concurrency", "group") != "podium-attest-${{ github.ref }}" || at(t, doc, "concurrency", "cancel-in-progress") != false {
		t.Errorf("concurrency = %v", doc["concurrency"])
	}
	if p := at(t, doc, "permissions").(map[string]any); len(p) != 0 {
		t.Errorf("workflow permissions = %v, want none", p)
	}
	env := at(t, doc, "env").(map[string]any)
	if env["PODIUM_VERSION"] != MinInstallVersion || env["PODIUM_ROOTS"] != "skills team/tools" || env["PODIUM_MAIN_BRANCH"] != "main" || env["PODIUM_SIGNED_REF"] != "podium/signed" {
		t.Errorf("env = %v", env)
	}
	wantPerms := map[string]map[string]any{
		"subjects": {"contents": "read"},
		"sign":     {"contents": "read", "id-token": "write", "attestations": "write"},
		"publish":  {"contents": "write"},
	}
	for job, want := range wantPerms {
		got := at(t, doc, "jobs", job, "permissions").(map[string]any)
		if len(got) != len(want) {
			t.Errorf("%s permissions = %v, want %v", job, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s permission %s = %v, want %v", job, k, got[k], v)
			}
		}
		for _, s := range at(t, doc, "jobs", job, "steps").([]any) {
			if uses := stringOf(s.(map[string]any)["uses"]); uses != "" && !pinnedAction.MatchString(uses) {
				t.Errorf("%s uses %q, which is not pinned to a commit", job, uses)
			}
		}
	}
	if at(t, doc, "jobs", "sign", "if") != "needs.subjects.outputs.sign == 'true'" {
		t.Error("the sign job does not wait on the guard")
	}
	if !strings.Contains(stringOf(step(t, doc, "sign", "actions/attest-build-provenance")["with"].(map[string]any)["subject-checksums"]), "matrix.file") {
		t.Error("the attestation step does not sign each subjects file")
	}
	if needs := at(t, doc, "jobs", "publish", "needs").([]any); len(needs) != 2 {
		t.Errorf("publish needs = %v", needs)
	}
	for _, key := range []string{"Guard", "Install podium", "Write subjects"} {
		step(t, doc, "subjects", key)
	}
	if !strings.Contains(stringOf(step(t, doc, "subjects", "Write subjects")["run"]), "podium attest --commit HEAD --root") {
		t.Error("the subjects step does not run podium attest --commit HEAD")
	}
	if !strings.Contains(stringOf(step(t, doc, "publish", "Publish")["run"]), "podium attest import --root") {
		t.Error("the publish step does not run podium attest import")
	}
	for _, job := range []string{"subjects", "publish"} {
		run := stringOf(step(t, doc, job, "Install podium")["run"])
		verify := strings.Index(run, "gh attestation verify")
		if verify < 0 || verify > strings.Index(run, `mv "$dir/podium-linux-$arch"`) ||
			!strings.Contains(run, `--cert-identity "https://github.com/lennylabs/podium/.github/workflows/release.yml@refs/tags/$PODIUM_VERSION"`) ||
			!strings.Contains(run, "--deny-self-hosted-runners") || strings.Contains(run, "--signer-workflow") {
			t.Errorf("the %s job installs podium without verifying its attestation first:\n%s", job, run)
		}
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — a caller workflow carries
// the triggers and calls the shared workflow at its ref with the roots; the
// shared workflow takes the roots and the recover input from its caller
// and reads the default branch from the calling repository's record.
func TestRender_CallerAndReusable(t *testing.T) {
	t.Parallel()
	doc := parseWorkflow(t, Workflow{Kind: KindCaller, Version: "0.7.0", Branch: "main", Shared: "acme/.github"})
	if at(t, doc, "jobs", "attest", "uses") != "acme/.github/.github/workflows/podium-attest.yml@main" || at(t, doc, "jobs", "attest", "with", "roots") != "/" {
		t.Errorf("caller job = %v", at(t, doc, "jobs", "attest"))
	}
	perms := at(t, doc, "permissions").(map[string]any)
	if perms["id-token"] != "write" || perms["attestations"] != "write" || perms["contents"] != "write" {
		t.Errorf("caller permissions = %v", perms)
	}
	if at(t, doc, "on", "push", "paths-ignore").([]any)[0] != ".podium/attestations/**" {
		t.Error("the caller does not ignore the top's attestation directory")
	}

	doc = parseWorkflow(t, Workflow{Kind: KindReusable, Version: "0.7.0", Branch: "ignored", Roots: []string{"ignored"}})
	if at(t, doc, "on", "workflow_call", "inputs", "roots", "default") != "/" || at(t, doc, "on", "workflow_call", "inputs", "recover", "type") != "boolean" {
		t.Errorf("workflow_call = %v", at(t, doc, "on"))
	}
	if _, ok := doc["concurrency"]; ok {
		t.Error("the shared workflow sets a concurrency group, which belongs to each caller")
	}
	env := at(t, doc, "env").(map[string]any)
	if env["PODIUM_MAIN_BRANCH"] != "${{ github.event.repository.default_branch }}" || env["PODIUM_ROOTS"] != "${{ inputs.roots }}" {
		t.Errorf("env = %v", env)
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — a value the workflow
// cannot carry in YAML and shell is refused.
func TestRender_Refusals(t *testing.T) {
	t.Parallel()
	for name, w := range map[string]Workflow{
		"kind":          {Kind: "other", Version: "1", Branch: "main"},
		"version":       {Kind: KindRepository, Version: "1.0 $(x)", Branch: "main"},
		"version slash": {Kind: KindRepository, Version: "1/2", Branch: "main"},
		"branch":        {Kind: KindRepository, Version: "1", Branch: "${{ secrets.x }}"},
		"empty branch":  {Kind: KindRepository, Version: "1"},
		"root":          {Kind: KindRepository, Version: "1", Branch: "main", Roots: []string{"a'b"}},
		"root segment":  {Kind: KindRepository, Version: "1", Branch: "main", Roots: []string{"a/../b"}},
		"shared":        {Kind: KindCaller, Version: "1", Branch: "main", Shared: "acme"},
	} {
		if _, err := Render(w); err == nil {
			t.Errorf("%s: rendered", name)
		}
	}
	if _, err := CheckRoot("a b"); err == nil {
		t.Error("a root with a space was accepted")
	}
	if _, err := CheckRoot("a/./b"); !errors.Is(err, attest.ErrLayerRoot) {
		t.Errorf("CheckRoot(a/./b) = %v", err)
	}
	if got, err := CheckRoot("/skills/"); err != nil || got != "skills" {
		t.Errorf("CheckRoot(/skills/) = %q, %v", got, err)
	}
	if _, err := CheckRoot("sk$ills"); !errors.Is(err, ErrWorkflowValue) {
		t.Errorf("CheckRoot(sk$ills) = %v", err)
	}
	if _, err := ParseShared("acme/.github/x"); err == nil {
		t.Error("a shared repository of three segments was accepted")
	}
}

// Spec: §7.3.5 (Writing the signing workflow) — the workflow installs the
// version that wrote it, or the first release with attested binaries when
// that version is older.
func TestInstallVersion(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"v0.7.0":       MinInstallVersion,
		"v0.7.9":       MinInstallVersion,
		"v0.8.0-rc.1":  MinInstallVersion,
		"v0.8.0":       "v0.8.0",
		"v0.8.1":       "v0.8.1",
		"v0.10.0":      "v0.10.0",
		"v1.0.0+build": "v1.0.0+build",
	} {
		if got := installVersion(in); got != want {
			t.Errorf("installVersion(%s) = %s, want %s", in, got, want)
		}
	}
	doc := parseWorkflow(t, Workflow{Kind: KindReusable, Version: "0.7.0"})
	if at(t, doc, "env", "PODIUM_VERSION") != MinInstallVersion {
		t.Errorf("PODIUM_VERSION = %v", at(t, doc, "env", "PODIUM_VERSION"))
	}
}
