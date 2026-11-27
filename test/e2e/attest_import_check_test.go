package e2e

// `podium attest import`, `podium attest check`, and `podium layer show
// --requirements`, end to end.
//
// Spec §7.3.5 defines the import, which copies signed bundles into a layer's
// attestation directory under <hex>.sigstore.json with the certificates of
// each timestamp removed and prunes the bundles the import supersedes, and
// the check, which compares the directory with the layer. §4.7.11 defines
// the attestation directory and the requirements file that `podium layer
// show --requirements` (§7.3.1) writes and the check reads. The tests run
// the real binary over statements that `podium attest --format statement`
// wrote and that the Sigstore test harness signed.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
)

// signedStatements runs `podium attest --format statement` over layer and
// returns a JSON Lines file of bundles, one per statement file, each with a
// timestamp that embeds the authority's chain, as GitHub's instance writes.
func signedStatements(t *testing.T, h *sh.Harness, layer string, opts ...sh.BundleOpt) string {
	t.Helper()
	out := t.TempDir()
	res := runPodium(t, "", nil, "attest", layer, "--format", "statement", "--max-subjects", "2", "--out", out)
	cliWantExit(t, res, 0, "attest --format statement")
	var lines bytes.Buffer
	for _, p := range strings.Fields(res.Stdout) {
		stmt, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(h.DSSEBundle(bytes.TrimSpace(stmt), append([]sh.BundleOpt{sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain())}, opts...)...))
		lines.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "bundles.jsonl")
	if err := os.WriteFile(p, lines.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeFileE2E writes body to a new file and returns its path.
func writeFileE2E(t *testing.T, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Spec: §7.3.5, §4.7.11 (Attestation directory) — `podium attest`, `podium
// attest import`, and `podium attest check` over one fixture layer: the
// import writes each bundle to <hex>.sigstore.json, named by the SHA-256 of
// the bytes it wrote, with no certificate in its timestamp, and the check
// passes with and without --requirements. A package named `aux` and a
// resource named `GIT~1` draw the portable-name warning from each command,
// and none changes its exit status.
func TestAttestImportCheck_FixtureLayer(t *testing.T) {
	t.Parallel()
	layer := t.TempDir()
	writeTree(t, layer, map[string]string{
		"team/a/ARTIFACT.md":      attestRuleArtifact,
		"team/aux/ARTIFACT.md":    attestRuleArtifact,
		"team/hooks/ARTIFACT.md":  attestRuleArtifact,
		"team/hooks/GIT~1/config": "x\n",
	})
	h := sh.New(t)
	bundles := signedStatements(t, h, layer)

	res := runPodium(t, "", nil, "attest", "import", bundles, "--dir", layer)
	cliWantExit(t, res, 0, "attest import")
	assertPortableWarnings(t, res.Stderr, "attest import")
	attDir := filepath.Join(layer, ".podium", "attestations")
	entries, err := os.ReadDir(attDir)
	// --max-subjects 2 gives one statement per package, each with the
	// layer subject.
	if err != nil || len(entries) != 3 || strings.Count(res.Stdout, "wrote ") != 3 {
		t.Fatalf("attestation directory %v (%v), stdout %q", entries, err, res.Stdout)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(attDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if e.Name() != hex.EncodeToString(sum[:])+".sigstore.json" {
			t.Errorf("%s is not named by the digest of its bytes", e.Name())
		}
		if _, changed, err := attest.RemoveTimestampCertificates(raw); err != nil || changed {
			t.Errorf("%s still embeds timestamp certificates (%v)", e.Name(), err)
		}
	}

	res = runPodium(t, "", nil, "attest", "check", layer)
	cliWantExit(t, res, 0, "attest check")
	cliContains(t, res.Stdout, "note: no signature was verified", "unverified note")
	cliContains(t, res.Stdout, "ok: the bundles list the layer subject .podium/layer and the 3 packages", "pass line")
	assertPortableWarnings(t, res.Stderr, "attest check")

	root := writeFileE2E(t, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	reqs := writeFileE2E(t, "requirements.json", []byte(`{"trust_requirements":[{"signers":[{"issuer":"`+sh.DefaultIssuer+`","identity":"`+sh.DefaultSAN+`","instance":"github"}]}]}`))
	res = runPodium(t, "", nil, "attest", "check", layer, "--requirements", reqs, "--trusted-root", "github="+root)
	cliWantExit(t, res, 0, "attest check --requirements")
	if strings.Contains(res.Stdout, "refused:") || strings.Contains(res.Stdout, "note:") {
		t.Errorf("verified check stdout = %q", res.Stdout)
	}
}

// Spec: §4.2, §7.3.5 — a package directory named `CON .txt` draws the
// portable-name warning from `podium attest import` and `podium attest
// check`, and each exits 1 as it would without the warning, because the
// space leaves the package with no subject name; the import writes nothing.
func TestAttestImportCheck_CONPackage(t *testing.T) {
	t.Parallel()
	layer := t.TempDir()
	writeTree(t, layer, map[string]string{
		"team/a/ARTIFACT.md":        attestRuleArtifact,
		"team/CON .txt/ARTIFACT.md": attestRuleArtifact,
	})
	h := sh.New(t)
	bundle := writeFileE2E(t, "b.json", h.DSSEBundle(sh.Statement(t, "https://slsa.dev/provenance/v1",
		sh.Subject{Name: "team/a@1.0.0", SHA256: strings.Repeat("0", 64)}), sh.WithTimestamp(sh.TSAPrimary)))
	for _, args := range [][]string{{"attest", "import", bundle, "--dir", layer}, {"attest", "check", layer}} {
		res := runPodium(t, "", nil, args...)
		cliWantExit(t, res, 1, strings.Join(args[:2], " ")+" with a package named CON .txt")
		cliContains(t, res.Stderr, `warning: team/CON .txt: the canonical ID has the segment "CON .txt"`, "portable-name warning")
		cliContains(t, res.Stderr, `error: the package at "team/CON .txt" has no subject name`, "refusal")
	}
	if _, err := os.Stat(filepath.Join(layer, ".podium")); !os.IsNotExist(err) {
		t.Errorf("a refused import created the attestation directory: %v", err)
	}
}

// assertPortableWarnings checks the two portable-name warnings of the
// fixture layer.
func assertPortableWarnings(t *testing.T, stderr, what string) {
	t.Helper()
	cliContains(t, stderr, `warning: team/aux: the canonical ID has the segment "aux"`, what+" ID warning")
	cliContains(t, stderr, `warning: team/hooks: bundled resource "GIT~1/config" has the segment "GIT~1"`, what+" resource warning")
}

// Spec: §7.3.5 (Pruning) — a second import after a change removes the
// bundle that matches no current subject and the bundle its own signer
// superseded, and keeps another signer's bundle while it covers current
// content.
func TestAttestImport_PrunesAcrossRuns(t *testing.T) {
	t.Parallel()
	layer := t.TempDir()
	writeTree(t, layer, map[string]string{
		"team/a/ARTIFACT.md": attestRuleArtifact,
		"team/b/ARTIFACT.md": attestRuleArtifact,
		"team/c/ARTIFACT.md": attestRuleArtifact,
	})
	h := sh.New(t)
	res := runPodium(t, "", nil, "attest", "import", signedStatements(t, h, layer), "--dir", layer)
	cliWantExit(t, res, 0, "first import")
	res = runPodium(t, "", nil, "attest", "import", signedStatements(t, h, layer, sh.WithLeaf(sh.LeafEmail("bob@acme.com"))), "--dir", layer)
	cliWantExit(t, res, 0, "a second signer's import")
	if strings.Contains(res.Stdout, "removed") {
		t.Fatalf("a second signer's import removed the first signer's bundles:\n%s", res.Stdout)
	}

	// Each signing run writes one statement per package, each with the
	// layer subject. After team/c is edited, the first signer's bundles of
	// team/a and team/b match only their artifact subjects, which the new
	// import lists, and both signers' bundles of team/c match nothing. The
	// second signer's bundles of team/a and team/b stay.
	writeTree(t, layer, map[string]string{"team/c/ARTIFACT.md": strings.Replace(attestRuleArtifact, "Body.", "Edited.", 1)})
	res = runPodium(t, "", nil, "attest", "import", signedStatements(t, h, layer), "--dir", layer)
	cliWantExit(t, res, 0, "import after an edit")
	if n := strings.Count(res.Stdout, ": "+string(attest.PruneSuperseded)); n != 2 {
		t.Errorf("import after an edit superseded %d bundles, want 2:\n%s", n, res.Stdout)
	}
	if n := strings.Count(res.Stdout, ": "+string(attest.PruneNoCurrentSubject)); n != 2 {
		t.Errorf("import after an edit removed %d stale bundles, want 2:\n%s", n, res.Stdout)
	}
	entries, _ := os.ReadDir(filepath.Join(layer, ".podium", "attestations"))
	if len(entries) != 5 {
		t.Errorf("directory holds %d bundles, want the three new ones and the second signer's two", len(entries))
	}
	res = runPodium(t, "", nil, "attest", "check", layer)
	cliWantExit(t, res, 0, "check after the import")
}

// Spec: §7.3.5 — a package whose digest matches no subject fails the check,
// which prints the resolution state and the resolved fields of the package.
// A layer with a package directory whose name holds a line feed cannot be
// checked or pruned against, and both commands exit 1 writing nothing.
func TestAttestCheck_MismatchAndRefusals(t *testing.T) {
	t.Parallel()
	layer := t.TempDir()
	writeTree(t, layer, map[string]string{"team/a/ARTIFACT.md": attestRuleArtifact})
	h := sh.New(t)
	res := runPodium(t, "", nil, "attest", "import", signedStatements(t, h, layer), "--dir", layer)
	cliWantExit(t, res, 0, "import")
	writeTree(t, layer, map[string]string{"team/a/ARTIFACT.md": strings.Replace(attestRuleArtifact, "type: rule", "type: rule\nsensitivity: high\ndeprecated: true", 1)})
	res = runPodium(t, "", nil, "attest", "check", layer)
	cliWantExit(t, res, 1, "check after a change")
	cliContains(t, res.Stdout, "missing: no bundle lists team/a@1.0.0 with the package digest sha256:", "missing package")
	cliContains(t, res.Stdout, "  state: resolved\n  type: \"rule\"\n  sensitivity: \"high\"\n  deprecated: true\n  replaced_by: \"\"\n  listed with: sha256:", "resolved fields")
	cliContains(t, res.Stdout, "missing: no bundle lists the layer subject .podium/layer", "layer subject")

	bad := t.TempDir()
	writeTree(t, bad, map[string]string{"team/a\nb/ARTIFACT.md": attestRuleArtifact})
	res = runPodium(t, "", nil, "attest", "check", bad)
	cliWantExit(t, res, 1, "check of a layer with a line feed in a package name")
	cliContains(t, res.Stderr, `"team/a\nb"`, "escaped path")
	res = runPodium(t, "", nil, "attest", "import", writeFileE2E(t, "b.json", h.DSSEBundle(sh.Statement(t, "https://slsa.dev/provenance/v1", sh.Subject{Name: "x", SHA256: strings.Repeat("0", 64)}), sh.WithTimestamp(sh.TSAPrimary))), "--dir", bad)
	cliWantExit(t, res, 1, "import into a layer with a line feed in a package name")
	if _, err := os.Stat(filepath.Join(bad, ".podium")); !os.IsNotExist(err) {
		t.Errorf("a refused import created the attestation directory: %v", err)
	}
	res = runPodium(t, "", nil, "attest", "import")
	cliWantExit(t, res, 2, "import with no bundle file")
}

// Spec: §7.3.5 — with --commit the check reads the commit's tree and lists
// the package and the path the working tree changes, and the import prunes
// against the commit.
func TestAttestCheck_CommitAndWorkingTree(t *testing.T) {
	t.Parallel()
	requireGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-q")
	writeTree(t, repo, map[string]string{"skills/team/a/ARTIFACT.md": attestRuleArtifact})
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "-c", "user.name=alice", "-c", "user.email=alice@acme.com", "commit", "-q", "-m", "c")
	h := sh.New(t)
	out := t.TempDir()
	res := runPodium(t, "", nil, "attest", repo, "--root", "skills", "--commit", "HEAD", "--format", "statement", "--out", out)
	cliWantExit(t, res, 0, "attest --commit")
	stmt, err := os.ReadFile(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	bundle := writeFileE2E(t, "b.json", h.DSSEBundle(bytes.TrimSpace(stmt), sh.WithTimestamp(sh.TSAPrimary)))
	writeTree(t, repo, map[string]string{"skills/team/a/notes.md": "uncommitted\n"})
	res = runPodium(t, "", nil, "attest", "import", bundle, "--dir", repo, "--root", "skills", "--commit", "HEAD")
	cliWantExit(t, res, 0, "import --commit")
	res = runPodium(t, "", nil, "attest", "check", repo, "--root", "skills", "--commit", "HEAD")
	cliWantExit(t, res, 0, "check --commit")
	cliContains(t, res.Stdout, "the working tree differs from commit HEAD:\n  package team/a: notes.md\n", "working tree difference")
	res = runPodium(t, "", nil, "attest", "check", repo, "--root", "skills")
	cliWantExit(t, res, 1, "check of the working tree")
}

// Spec: §7.3.1 (Layer CLI), §4.7.11 (Requirements file), §7.3.5 — `podium
// layer show <id> --requirements` writes a requirements file that `podium
// attest check --requirements` accepts, and the check then counts only the
// bundles of the registered signer.
func TestLayerShowRequirements_AcceptedByCheck(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	env := brEnv(srv.BaseURL)
	res := runPodium(t, "", env, "layer", "register", "--id", "finance", "--repo", e2eTrustRepo, "--ref", "podium/signed",
		"--trust", "require", "--trust-signer", sh.DefaultIssuer+"="+sh.DefaultSAN)
	cliWantExit(t, res, 0, "register with a trust policy")
	res = runPodium(t, "", env, "layer", "show", "finance", "--requirements")
	cliWantExit(t, res, 0, "layer show --requirements")
	cliContains(t, res.Stdout, `"trust_requirements": [`, "requirements file")
	reqs := writeFileE2E(t, "requirements.json", []byte(res.Stdout))

	layer := t.TempDir()
	writeTree(t, layer, map[string]string{"team/a/ARTIFACT.md": attestRuleArtifact})
	h := sh.New(t)
	out := t.TempDir()
	res = runPodium(t, "", nil, "attest", layer, "--format", "statement", "--out", out)
	cliWantExit(t, res, 0, "attest")
	stmt, err := os.ReadFile(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	signed := h.DSSEBundle(bytes.TrimSpace(stmt), sh.WithSCT(), sh.WithRekorV1Entry())
	other := h.DSSEBundle(bytes.TrimSpace(stmt), sh.WithSCT(), sh.WithRekorV1Entry(), sh.WithLeaf(sh.LeafEmail("mallory@acme.com")))
	res = runPodium(t, "", nil, "attest", "import", writeFileE2E(t, "b.jsonl", append(append(signed, '\n'), other...)), "--dir", layer, "--no-prune")
	cliWantExit(t, res, 0, "import")

	pgRoot := writeFileE2E(t, "pg.json", h.TrustedRootJSON(sh.WithCTLogKey()))
	res = runPodium(t, "", []string{"PODIUM_ATTESTATION_TRUSTED_ROOT_FILE=" + pgRoot}, "attest", "check", layer, "--requirements", reqs)
	cliWantExit(t, res, 0, "check with the layer's requirements")
	cliContains(t, res.Stdout, ": identity\n", "the other signer's bundle is refused")

	res = runPodium(t, "", env, "layer", "register", "--id", "open", "--repo", e2eTrustRepo, "--ref", "main")
	cliWantExit(t, res, 0, "register with no policy")
	res = runPodium(t, "", env, "layer", "show", "open", "--requirements")
	cliWantExit(t, res, 1, "show --requirements of a layer with no requirement")
	cliContains(t, res.Stderr, "has no trust requirement", "refusal")
}
