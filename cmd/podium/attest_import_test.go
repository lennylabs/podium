package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

const (
	importSLSA = "https://slsa.dev/provenance/v1"
	importScan = "https://acme.test/scan/v1"
)

// importLayer writes a layer with two packages under dir/skills.
func importLayer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{
		"skills/team/a/ARTIFACT.md": attestRuleMD,
		"skills/team/b/ARTIFACT.md": attestRuleMD,
	})
	return dir
}

// layerSubjectsOf returns the layer subject and the artifact subjects that
// the filesystem walk gives for dir/root, as harness subjects.
func layerSubjectsOf(t *testing.T, dir, root string) []sh.Subject {
	t.Helper()
	content, err := filesystem.LayerContent(filesystem.Layer{ID: "l", Path: filepath.Join(dir, root)})
	if err != nil {
		t.Fatalf("LayerContent: %v", err)
	}
	name, err := attest.LayerSubjectName(root)
	if err != nil {
		t.Fatal(err)
	}
	out := []sh.Subject{{Name: name, SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		out = append(out, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	return out
}

// ghBundleOf signs subjects as a statement of predicate type pt with a
// timestamp that embeds the authority's chain, as GitHub's instance does.
func ghBundleOf(t *testing.T, h *sh.Harness, pt string, subjects []sh.Subject, opts ...sh.BundleOpt) []byte {
	t.Helper()
	return h.DSSEBundle(sh.Statement(t, pt, subjects...), append([]sh.BundleOpt{sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain())}, opts...)...)
}

// writeInput writes raw to a new file and returns its path.
func writeInput(t *testing.T, raw []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.jsonl")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runImportT runs `podium attest import` and returns its exit code and
// streams.
func runImportT(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runAttestImport(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

// bundleFiles returns the names of the bundle files in dir.
func bundleFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Spec: §7.3.5 (Importing bundles) — the command writes each bundle of a
// JSON Lines file to <hex>.sigstore.json, named by the SHA-256 of the bytes
// it writes, with the timestamp certificates removed, and leaves a file that
// already holds those bytes.
func TestAttestImport_WritesNamedBundles(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	b1 := ghBundleOf(t, h, importSLSA, subs[:2])
	b2 := ghBundleOf(t, h, importSLSA, append([]sh.Subject{subs[0]}, subs[2]))
	in := writeInput(t, append(append(append(append([]byte(nil), b1...), '\n'), b2...), '\n'))
	code, stdout, stderr := runImportT(t, in, "--dir", dir, "--root", "skills")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	attDir := filepath.Join(dir, "skills", ".podium", "attestations")
	names := bundleFiles(t, attDir)
	if len(names) != 2 || strings.Count(stdout, "wrote ") != 2 {
		t.Fatalf("files %v, stdout %q", names, stdout)
	}
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(attDir, n))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if n != hex.EncodeToString(sum[:])+".sigstore.json" {
			t.Errorf("%s is not named by the digest of its bytes", n)
		}
		if _, changed, err := attest.RemoveTimestampCertificates(raw); err != nil || changed {
			t.Errorf("%s still embeds timestamp certificates (%v)", n, err)
		}
	}
	code, stdout, _ = runImportT(t, in, "--dir", dir, "--root", "skills")
	if code != 0 || strings.Count(stdout, "unchanged ") != 2 || strings.Contains(stdout, "removed") {
		t.Fatalf("second import = %d, stdout %q", code, stdout)
	}
}

// Spec: §7.3.5 (Pruning) — an import removes a bundle that matches no
// current subject, one whose matching subjects the imported bundles of its
// signer and type list together, a file that does not parse, and a file over
// the size limit. Another signer's bundle, a bundle of another predicate
// type, and a file of another name stay. --no-prune removes nothing.
func TestAttestImport_Prunes(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	attDir := filepath.Join(dir, "skills", ".podium", "attestations")
	stale := []sh.Subject{{Name: subs[0].Name, SHA256: strings.Repeat("0", 64)}, {Name: "team/gone@1.0.0", SHA256: strings.Repeat("1", 64)}}
	existing := map[string][]byte{
		"stale.sigstore.json":      ghBundleOf(t, h, importSLSA, stale),
		"superseded.sigstore.json": ghBundleOf(t, h, importSLSA, subs[:2]),
		"scanner.sigstore.json":    ghBundleOf(t, h, importSLSA, subs[1:], sh.WithLeaf(sh.LeafEmail("bob@acme.com"))),
		"review.sigstore.json":     ghBundleOf(t, h, importScan, subs[1:]),
		"garbage.sigstore.json":    []byte("not a bundle"),
		"big.sigstore.json":        bytes.Repeat([]byte(" "), attest.DefaultMaxBundleBytes+1),
		"notes.md":                 []byte("keep"),
	}
	write := func() {
		if err := os.MkdirAll(attDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for n, b := range existing {
			if err := os.WriteFile(filepath.Join(attDir, n), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write()
	in := writeInput(t, ghBundleOf(t, h, importSLSA, subs))
	code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills", "--no-prune")
	if code != 0 || len(bundleFiles(t, attDir)) != len(existing)+1 {
		t.Fatalf("--no-prune = %d, files %v, stderr %s", code, bundleFiles(t, attDir), stderr)
	}
	code, stdout, stderr := runImportT(t, in, "--dir", dir, "--root", "skills")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	got := strings.Join(bundleFiles(t, attDir), ",")
	for _, keep := range []string{"scanner.sigstore.json", "review.sigstore.json", "notes.md"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%s was removed; files %s", keep, got)
		}
	}
	for n, reason := range map[string]string{
		"stale":      string(attest.PruneNoCurrentSubject),
		"superseded": string(attest.PruneSuperseded),
		"garbage":    "does not parse as a bundle (parse)",
		"big":        "holds 1048577 bytes, above PODIUM_ATTESTATION_MAX_BYTES",
	} {
		if strings.Contains(got, n+".sigstore.json") || !strings.Contains(stdout, n+".sigstore.json: "+reason) {
			t.Errorf("%s: files %s, stdout %q, want it removed with %q", n, got, stdout, reason)
		}
	}
	if len(bundleFiles(t, attDir)) != 4 {
		t.Errorf("files %s, want the import, the scanner, the review, and notes.md", got)
	}
}

// Spec: §7.3.5 — the import reads the current subjects from the commit's
// tree with --commit, so an uncommitted edit does not make the signed
// bundles stale, and warns when the directory holds more bundle files than
// PODIUM_ATTESTATION_MAX_BUNDLES.
func TestAttestImport_CommitAndCountWarning(t *testing.T) {
	dir := t.TempDir()
	commitAttestRepo(t, dir, map[string]string{"skills/team/a/ARTIFACT.md": attestRuleMD})
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	attDir := filepath.Join(dir, "skills", ".podium", "attestations")
	writeAttestLayer(t, dir, map[string]string{"skills/team/a/notes.md": "uncommitted\n"})
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := ghBundleOf(t, h, importScan, subs)
	if err := os.WriteFile(filepath.Join(attDir, "scan.sigstore.json"), other, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(attestMaxBundlesEnv, "1")
	code, stdout, stderr := runImportT(t, writeInput(t, ghBundleOf(t, h, importSLSA, subs)), "--dir", dir, "--root", "skills", "--commit", "HEAD")
	if code != 0 || strings.Contains(stdout, "removed") {
		t.Fatalf("exit %d, stdout %q, stderr %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "holds 2 bundle files, above PODIUM_ATTESTATION_MAX_BUNDLES (1)") {
		t.Errorf("stderr = %q, want the count warning", stderr)
	}
}

// Spec: §7.3.5 — usage errors exit 2, and a refused input, a refused layer,
// or a failed write exits 1; a refusal before the write leaves the directory
// as it was.
func TestAttestImport_Refusals(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	subs := layerSubjectsOf(t, dir, "skills")
	good := writeInput(t, ghBundleOf(t, h, importSLSA, subs))
	for _, args := range [][]string{{}, {good, "--root", "a/../b"}, {good, "--bogus"}} {
		if code, _, stderr := runImportT(t, args...); code != 2 || stderr == "" {
			t.Errorf("%v = %d, stderr %q, want 2", args, code, stderr)
		}
	}
	t.Run("bad limit", func(t *testing.T) {
		t.Setenv(attestMaxBytesEnv, "0")
		if code, _, _ := runImportT(t, good, "--dir", dir); code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})
	t.Run("bundle over the limit", func(t *testing.T) {
		t.Setenv(attestMaxBytesEnv, "100")
		if code, _, stderr := runImportT(t, good, "--dir", dir, "--root", "skills"); code != 1 || !strings.Contains(stderr, "above PODIUM_ATTESTATION_MAX_BYTES") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	badTimestamp := bytes.Replace(ghBundleOf(t, h, importSLSA, subs), []byte(`"signedTimestamp": "`), []byte(`"signedTimestamp": "AAAA`), 1)
	for name, in := range map[string]string{
		"missing input":   filepath.Join(t.TempDir(), "none.json"),
		"empty input":     writeInput(t, nil),
		"not a bundle":    writeInput(t, []byte(`{"mediaType":"x"}`)),
		"not JSON":        writeInput(t, []byte("x")),
		"bad timestamp":   writeInput(t, badTimestamp),
		"missing layer":   good,
		"package no name": good,
	} {
		t.Run(name, func(t *testing.T) {
			args := []string{in, "--dir", dir, "--root", "skills"}
			switch name {
			case "missing layer":
				args = []string{in, "--dir", dir, "--root", "absent"}
			case "package no name":
				d := t.TempDir()
				writeAttestLayer(t, d, map[string]string{"team/a b/ARTIFACT.md": attestRuleMD})
				args = []string{in, "--dir", d}
			}
			if code, _, stderr := runImportT(t, args...); code != 1 || stderr == "" {
				t.Errorf("exit %d, stderr %q, want 1", code, stderr)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", ".podium")); !os.IsNotExist(err) {
		t.Errorf("a refused import created the attestation directory: %v", err)
	}
	// A file where the directory belongs fails the write.
	writeAttestLayer(t, dir, map[string]string{"skills/.podium": "file"})
	if code, _, stderr := runImportT(t, good, "--dir", dir, "--root", "skills"); code != 1 || !strings.Contains(stderr, "create") {
		t.Errorf("blocked directory = %d, stderr %q", code, stderr)
	}
	// --no-prune reads no layer, so a missing layer root writes the bundle.
	d := t.TempDir()
	if code, _, stderr := runImportT(t, good, "--dir", d, "--root", "x", "--no-prune"); code != 0 {
		t.Errorf("--no-prune without a layer = %d, stderr %s", code, stderr)
	}
}

// Spec: §13.12 — the limits take their defaults when unset and refuse a
// value that is not a decimal integer of at least 1.
func TestReadAttestLimits(t *testing.T) {
	l, err := readAttestLimits(func(string) string { return "" })
	if err != nil || l.maxBytes != attest.DefaultMaxBundleBytes || l.maxBundles != attest.DefaultMaxBundles {
		t.Fatalf("defaults = %+v, %v", l, err)
	}
	env := map[string]string{attestMaxBytesEnv: " 2048 ", attestMaxBundlesEnv: "3"}
	if l, err := readAttestLimits(func(k string) string { return env[k] }); err != nil || l.maxBytes != 2048 || l.maxBundles != 3 {
		t.Fatalf("set = %+v, %v", l, err)
	}
	for _, v := range []string{"0", "-1", "1k", "99999999999999999999"} {
		for _, k := range []string{attestMaxBytesEnv, attestMaxBundlesEnv} {
			if _, err := readAttestLimits(func(n string) string {
				if n == k {
					return v
				}
				return ""
			}); err == nil {
				t.Errorf("%s=%s accepted", k, v)
			}
		}
	}
}
