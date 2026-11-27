package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
)

// runCheckT runs `podium attest check` under env and returns its exit code
// and streams.
func runCheckT(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runAttestCheck(context.Background(), args, func(k string) string { return env[k] }, &out, &errb)
	return code, out.String(), errb.String()
}

// placeBundles writes each bundle into the attestation directory of
// dir/root under its import name.
func placeBundles(t *testing.T, dir, root string, bundles ...[]byte) {
	t.Helper()
	attDir := filepath.Join(dir, root, ".podium", "attestations")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles {
		out, _, err := attest.RemoveTimestampCertificates(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(attDir, attest.BundleFileName(out)), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeFileT writes body to a new file and returns its path.
func writeFileT(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// emailRequirements names the default harness signer under instance.
func emailRequirements(t *testing.T, instance, predicateType string) string {
	t.Helper()
	pt := ""
	if predicateType != "" {
		pt = `,"predicate_type":"` + predicateType + `"`
	}
	inst := ""
	if instance != "" {
		inst = `,"instance":"` + instance + `"`
	}
	return writeFileT(t, "requirements.json", `{"trust_requirements":[{"signers":[{"issuer":"`+sh.DefaultIssuer+`","identity":"`+sh.DefaultSAN+`"`+inst+`}]`+pt+`}],"layer_roots":{"x":1}}`)
}

// Spec: §7.3.5 (Checking bundles) — a layer whose bundles list the layer
// subject and every package passes, and the output states that no signature
// was verified without --requirements.
func TestAttestCheck_Passes(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	placeBundles(t, dir, "skills", ghBundleOf(t, h, importSLSA, subs[:2]), ghBundleOf(t, h, importSLSA, append([]sh.Subject{subs[0]}, subs[2])))
	code, stdout, stderr := runCheckT(t, nil, dir, "--root", "skills")
	if code != 0 || !strings.Contains(stdout, "note: no signature was verified") || !strings.Contains(stdout, "ok: the bundles list the layer subject .podium/layer/skills and the 2 packages") {
		t.Fatalf("exit %d, stdout %q, stderr %s", code, stdout, stderr)
	}
}

// Spec: §7.3.5 — a package whose digest matches no subject fails the check,
// and the output names its resolution state, its resolved fields, and the
// digests the bundles list for its name; a layer digest no bundle lists
// fails it as well. An unresolved package prints its state.
func TestAttestCheck_MissingSubjects(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	placeBundles(t, dir, "skills", ghBundleOf(t, h, importSLSA, layerSubjectsOf(t, dir, "skills")))
	writeAttestLayer(t, dir, map[string]string{
		"skills/team/a/ARTIFACT.md": strings.Replace(attestRuleMD, "Body.", "Changed body.", 1),
		"skills/team/c/ARTIFACT.md": "---\ntype: [unclosed\n---\n",
	})
	code, stdout, _ := runCheckT(t, nil, dir, "--root", "skills")
	if code != 1 {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	for _, want := range []string{
		"missing: no bundle lists the layer subject .podium/layer/skills with the layer digest sha256:",
		"missing: no bundle lists team/a@1.0.0 with the package digest sha256:",
		"  state: resolved\n  type: \"rule\"\n  sensitivity: \"\"\n  deprecated: false\n  replaced_by: \"\"\n  listed with: sha256:",
		"missing: no bundle lists team/c with the package digest",
		"  state: unresolved (",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "team/b@1.0.0") || strings.Contains(stdout, "ok:") {
		t.Errorf("stdout = %q, want team/b to pass and no ok line", stdout)
	}
}

// Spec: §7.3.5 — with --requirements only the bundles a signer of the
// requirement accepts count: the github instance takes its root from
// --trusted-root, a bundle of another identity is refused with its reason,
// and a requirement's predicate type refuses a bundle of another type.
func TestAttestCheck_Requirements(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	rootFile := writeFileT(t, "github.json", string(h.TrustedRootJSON(sh.GitHubStyleRoot()...)))
	placeBundles(t, dir, "skills",
		ghBundleOf(t, h, importSLSA, subs),
		ghBundleOf(t, h, importSLSA, subs, sh.WithLeaf(sh.LeafEmail("mallory@acme.com"))))
	reqs := emailRequirements(t, "github", "")
	code, stdout, stderr := runCheckT(t, nil, dir, "--root", "skills", "--requirements", reqs, "--trusted-root", "github="+rootFile)
	if code != 0 || !strings.Contains(stdout, ": identity\n") || strings.Contains(stdout, "note:") {
		t.Fatalf("exit %d, stdout %q, stderr %s", code, stdout, stderr)
	}
	code, stdout, _ = runCheckT(t, nil, dir, "--root", "skills", "--requirements", emailRequirements(t, "github", importScan), "--trusted-root", "github="+rootFile)
	if code != 1 || strings.Count(stdout, ": predicate_type\n") != 1 || !strings.Contains(stdout, ": identity\n") {
		t.Fatalf("predicate type: exit %d, stdout %q", code, stdout)
	}
	// A signer of an instance with no trusted root is refused when the
	// file loads, before the layer is read.
	if code, _, stderr := runCheckT(t, nil, dir, "--root", "skills", "--requirements", reqs); code != 1 || !strings.Contains(stderr, "--trusted-root") {
		t.Fatalf("no root: exit %d, stderr %q", code, stderr)
	}
	for name, body := range map[string]string{
		"not JSON":     "x",
		"no binding":   `{"trust_requirements":[{"signers":[{"issuer":"i","identity":"https://acme.test/w"}]}]}`,
		"two members":  `{"trust_requirements":[],"extra":1}`,
		"no signature": `{"trust_requirements":[]}`,
	} {
		if code, _, stderr := runCheckT(t, nil, dir, "--root", "skills", "--requirements", writeFileT(t, "r.json", body)); code != 1 || stderr == "" {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr)
		}
	}
	if code, _, _ := runCheckT(t, nil, dir, "--requirements", filepath.Join(t.TempDir(), "none.json")); code != 1 {
		t.Errorf("missing requirements file: exit %d", code)
	}
	// An unreadable root file fails the check when a bundle is verified.
	if code, _, stderr := runCheckT(t, nil, dir, "--root", "skills", "--requirements", reqs, "--trusted-root", "github="+filepath.Join(t.TempDir(), "none")); code != 1 || !strings.Contains(stderr, `Sigstore instance "github"`) {
		t.Errorf("unreadable root: exit %d, stderr %q", code, stderr)
	}
}

// Spec: §7.3.5 — the public-good instance takes the root that
// PODIUM_ATTESTATION_TRUSTED_ROOT_FILE names, which lists both logs, and
// another instance requires each log its root lists.
func TestAttestCheck_PublicGoodAndOtherInstances(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	placeBundles(t, dir, "skills", h.DSSEBundle(sh.Statement(t, importSLSA, subs...), sh.WithSCT(), sh.WithRekorV1Entry()))
	pgRoot := writeFileT(t, "pg.json", string(h.TrustedRootJSON(sh.WithCTLogKey())))
	env := map[string]string{attestRootFileEnv: pgRoot}
	if code, stdout, stderr := runCheckT(t, env, dir, "--root", "skills", "--requirements", emailRequirements(t, "", "")); code != 0 {
		t.Fatalf("public-good: exit %d, stdout %q, stderr %s", code, stdout, stderr)
	}
	// --trusted-root public-good replaces the variable.
	if code, stdout, _ := runCheckT(t, nil, dir, "--root", "skills", "--requirements", emailRequirements(t, "public-good", ""), "--trusted-root", "public-good="+pgRoot); code != 0 {
		t.Fatalf("--trusted-root public-good: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, _ := runCheckT(t, nil, dir, "--root", "skills", "--requirements", emailRequirements(t, "acme", ""), "--trusted-root", "acme="+pgRoot); code != 0 {
		t.Fatalf("operator instance: exit %d, stdout %q", code, stdout)
	}
	// A public-good root without a certificate transparency log is refused.
	noCT := writeFileT(t, "noct.json", string(h.TrustedRootJSON()))
	if code, _, stderr := runCheckT(t, map[string]string{attestRootFileEnv: noCT}, dir, "--root", "skills", "--requirements", emailRequirements(t, "", "")); code != 1 || !strings.Contains(stderr, "certificate transparency") {
		t.Fatalf("no CT log: exit %d, stderr %q", code, stderr)
	}
}

// Spec: §7.3.5, §13.12 — without a root file the check refreshes the
// public-good root once through TUF into the home directory, warns when the
// refresh fails, and refuses a root past PODIUM_SIGSTORE_ROOT_MAX_AGE; with
// no limit it verifies against the embedded root, which trusts no harness
// bundle.
func TestAttestCheck_RefreshedPublicGood(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	placeBundles(t, dir, "skills", h.DSSEBundle(sh.Statement(t, importSLSA, layerSubjectsOf(t, dir, "skills")...), sh.WithSCT(), sh.WithRekorV1Entry()))
	t.Setenv("HOME", t.TempDir())
	prev := attestTUFMirror
	attestTUFMirror = "http://127.0.0.1:1"
	t.Cleanup(func() { attestTUFMirror = prev })
	reqs := emailRequirements(t, "", "")
	code, _, stderr := runCheckT(t, map[string]string{attestRootTimeoutEnv: "2s"}, dir, "--root", "skills", "--requirements", reqs)
	if code != 1 || !strings.Contains(stderr, "warning: attestation: refresh the public-good root") || !strings.Contains(stderr, "maximum age") {
		t.Fatalf("stale embedded root: exit %d, stderr %q", code, stderr)
	}
	code, stdout, _ := runCheckT(t, map[string]string{attestRootTimeoutEnv: "2s", attestRootMaxAgeEnv: "0"}, dir, "--root", "skills", "--requirements", reqs)
	if code != 1 || !strings.Contains(stdout, "refused: ") {
		t.Fatalf("embedded root: exit %d, stdout %q", code, stdout)
	}
}

// Spec: §7.3.5 — with --commit the check reads the commit's tree and lists
// the packages, paths, and layer files the working tree changes, without
// changing the exit status.
func TestAttestCheck_CommitDiff(t *testing.T) {
	dir := t.TempDir()
	commitAttestRepo(t, dir, map[string]string{
		"skills/team/a/ARTIFACT.md": attestRuleMD,
		"skills/team/b/ARTIFACT.md": attestRuleMD,
		"skills/team/DOMAIN.md":     "---\ndescription: Team.\n---\n",
	})
	h := sh.New(t)
	placeBundles(t, dir, "skills", ghBundleOf(t, h, importSLSA, layerSubjectsOf(t, dir, "skills")))
	code, stdout, stderr := runCheckT(t, nil, dir, "--root", "skills", "--commit", "HEAD")
	if code != 0 || !strings.Contains(stdout, "the working tree matches commit HEAD") {
		t.Fatalf("clean: exit %d, stdout %q, stderr %s", code, stdout, stderr)
	}
	writeAttestLayer(t, dir, map[string]string{
		"skills/team/a/notes.md":      "uncommitted\n",
		"skills/team/new/ARTIFACT.md": attestRuleMD,
		"skills/team/DOMAIN.md":       "---\ndescription: Edited.\n---\n",
	})
	if err := os.RemoveAll(filepath.Join(dir, "skills", "team", "b")); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runCheckT(t, nil, dir, "--root", "skills", "--commit", "HEAD")
	want := "the working tree differs from commit HEAD:\n  package team/a: notes.md\n  package team/b: only in the commit\n  package team/new: only in the working tree\n  layer file team/DOMAIN.md\n"
	if code != 0 || !strings.Contains(stdout, want) {
		t.Fatalf("edited: exit %d, stdout %q, want %q", code, stdout, want)
	}
	// A bare repository has no working tree to compare.
	bare := filepath.Join(t.TempDir(), "bare.git")
	if _, err := git.PlainClone(bare, true, &git.CloneOptions{URL: dir}); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runCheckT(t, nil, bare, "--root", "skills", "--commit", "HEAD"); !strings.Contains(stdout, "no working tree at") {
		t.Fatalf("bare: exit %d, stdout %q", code, stdout)
	}
}

// Spec: §7.3.5 — a file over the size limit and a directory over the count
// limit fail the check; a file that does not parse is a warning and counts
// as no bundle.
func TestAttestCheck_Limits(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	placeBundles(t, dir, "skills", ghBundleOf(t, h, importSLSA, layerSubjectsOf(t, dir, "skills")))
	writeAttestLayer(t, dir, map[string]string{"skills/.podium/attestations/x.sigstore.json": "not a bundle"})
	code, stdout, stderr := runCheckT(t, nil, dir, "--root", "skills")
	if code != 0 || !strings.Contains(stderr, "warning: x.sigstore.json does not parse as a bundle (parse), so it counts as no bundle") {
		t.Fatalf("garbage: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, _ = runCheckT(t, map[string]string{attestMaxBundlesEnv: "1", attestMaxBytesEnv: "20"}, dir, "--root", "skills")
	if code != 1 || !strings.Contains(stdout, "limits: the attestation directory holds 2 bundle files, above PODIUM_ATTESTATION_MAX_BUNDLES (1)") ||
		!strings.Contains(stdout, "above PODIUM_ATTESTATION_MAX_BYTES (20)") {
		t.Fatalf("limits: exit %d, stdout %q", code, stdout)
	}
	// No attestation directory lists nothing.
	if code, stdout, _ := runCheckT(t, nil, importLayer(t), "--root", "skills"); code != 1 || !strings.Contains(stdout, "missing: no bundle lists the layer subject") {
		t.Fatalf("no directory: exit %d, stdout %q", code, stdout)
	}
}

// Spec: §7.3.5 — usage errors exit 2, and a layer that cannot be read or
// that holds a package with no subject name exits 1.
func TestAttestCheck_UsageAndLayerErrors(t *testing.T) {
	dir := importLayer(t)
	for name, c := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"two dirs":          {nil, []string{"a", "b"}},
		"bad root":          {nil, []string{"--root", "a//b"}},
		"root without file": {nil, []string{"--trusted-root", "github"}},
		"bad instance name": {nil, []string{"--trusted-root", "GitHub=x"}},
		"instance twice":    {nil, []string{"--trusted-root", "github=x", "--trusted-root", "github=y"}},
		"bad flag":          {nil, []string{"--bogus"}},
		"bad limit":         {map[string]string{attestMaxBundlesEnv: "x"}, nil},
		"bad timeout":       {map[string]string{attestRootTimeoutEnv: "0s"}, []string{"--requirements", "r.json"}},
		"bad max age":       {map[string]string{attestRootMaxAgeEnv: "-1h"}, []string{"--requirements", "r.json"}},
	} {
		if code, _, stderr := runCheckT(t, c.env, append([]string{dir}, c.args...)...); code != 2 || stderr == "" {
			t.Errorf("%s: exit %d, stderr %q, want 2", name, code, stderr)
		}
	}
	if code, _, _ := runCheckT(t, nil, dir, "--root", "absent"); code != 1 {
		t.Errorf("missing layer root: exit %d", code)
	}
	d := t.TempDir()
	writeAttestLayer(t, d, map[string]string{"team/a b/ARTIFACT.md": attestRuleMD})
	if code, _, stderr := runCheckT(t, nil, d); code != 1 || !strings.Contains(stderr, "no subject name") {
		t.Errorf("no subject name: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := runCheckT(t, nil, dir, "--help"); code != 0 {
		t.Errorf("--help: exit %d", code)
	}
}

// Spec: §13.12 — the public-good settings of the check take their defaults
// when unset.
func TestReadRootSettings(t *testing.T) {
	s, err := readRootSettings(func(string) string { return "" })
	if err != nil || s.timeout <= 0 || s.maxAge <= 0 || s.rootFile != "" {
		t.Fatalf("defaults = %+v, %v", s, err)
	}
	env := map[string]string{attestRootFileEnv: " f ", attestRootTimeoutEnv: "5s", attestRootMaxAgeEnv: "0"}
	if s, err := readRootSettings(func(k string) string { return env[k] }); err != nil || s.rootFile != "f" || s.maxAge != 0 || s.timeout.String() != "5s" {
		t.Fatalf("set = %+v, %v", s, err)
	}
}
