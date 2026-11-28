package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
)

// attestRunAt is the frozen clock of the --signed-time tests.
var attestRunAt = time.Date(2026, 11, 27, 9, 30, 0, 0, time.UTC)

// runAttestAtT runs `podium attest` under the frozen clock.
func runAttestAtT(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runAttestAt(context.Background(), clock.NewFrozen(attestRunAt), args, &out, &errb)
	return code, out.String(), errb.String()
}

// Spec: §7.3.5, §4.7.11 (Key-based signers) — --signed-time lists the
// signed-time subject of the run, with the layer digest, directly after the
// layer subject of each file, it counts toward --max-subjects, and every
// file of a run carries the same value.
func TestAttest_SignedTime(t *testing.T) {
	dir := importLayer(t)
	writeAttestLayer(t, dir, map[string]string{"skills/team/c/ARTIFACT.md": attestRuleMD})
	out := filepath.Join(t.TempDir(), "out")
	code, stdout, stderr := runAttestAtT(t, dir, "--root", "skills", "--out", out, "--signed-time", "--max-subjects", "3")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	paths := strings.Fields(stdout)
	if len(paths) != 3 {
		t.Fatalf("files %v, want one per artifact at 3 subjects a file", paths)
	}
	subs := layerSubjectsOf(t, dir, "skills")
	signedAt := ".podium/signed-at/" + strconv.FormatInt(attestRunAt.Unix(), 10)
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
		if len(lines) != 3 || lines[0] != subs[0].SHA256+"  "+subs[0].Name || lines[1] != subs[0].SHA256+"  "+signedAt {
			t.Errorf("%s:\n%s", p, body)
		}
	}
	code, stdout, stderr = runAttestAtT(t, dir, "--root", "skills", "--out", out, "--signed-time", "--format", "statement")
	if code != 0 {
		t.Fatalf("statement exit %d, stderr %s", code, stderr)
	}
	var st struct {
		Subject []struct {
			Name   string            `json:"name"`
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
	}
	body, err := os.ReadFile(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &st); err != nil || len(st.Subject) != 5 || st.Subject[1].Name != signedAt || st.Subject[1].Digest["sha256"] != subs[0].SHA256 {
		t.Errorf("statement %s: %v", body, err)
	}
	if code, _, stderr := runAttestAtT(t, dir, "--root", "skills", "--out", out, "--signed-time", "--max-subjects", "2"); code != 2 || !strings.Contains(stderr, "at least 3 subjects") {
		t.Errorf("--max-subjects 2 with --signed-time: exit %d, stderr %s", code, stderr)
	}
	var o, e bytes.Buffer
	if code := runAttestAt(context.Background(), clock.NewFrozen(time.Unix(0, 0)), []string{dir, "--root", "skills", "--out", out, "--signed-time"}, &o, &e); code != 1 {
		t.Errorf("a clock at the epoch: exit %d, stderr %s", code, e.String())
	}
}

// keySignedBundle signs subjects, with a signed-time subject after the
// layer subject, with key.
func keySignedBundle(t *testing.T, h *sh.Harness, key *ecdsa.PrivateKey, pt string, subjects []sh.Subject, at time.Time) []byte {
	t.Helper()
	all := append([]sh.Subject{subjects[0], sh.SignedTimeSubject(at, subjects[0].SHA256)}, subjects[1:]...)
	return h.DSSEBundle(sh.Statement(t, pt, all...), sh.WithSigningKey(key), sh.WithTimestamp(sh.TSAPrimary, sh.WithEmbeddedChain()))
}

// writeKeyPEM writes the public key of key to a new file.
func writeKeyPEM(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	return writeFileT(t, "cosign.pub", sh.PublicKeyPEM(t, key.Public()))
}

// Spec: §7.3.5 (Importing bundles), §4.7.11 (Key-based signers) — a
// key-signed bundle is imported only with --public-key and only when its
// signature verifies under that key; the pruning groups key-signed bundles
// by the key their signature verifies under.
func TestAttestImport_KeySigned(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	key, other := sh.NewSigningKey(t), sh.NewSigningKey(t)
	subs := layerSubjectsOf(t, dir, "skills")
	first := keySignedBundle(t, h, key, importSLSA, subs, attestRunAt.Add(-time.Hour))
	in := writeInput(t, first)
	pub := writeKeyPEM(t, key)
	if code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills"); code != 1 || !strings.Contains(stderr, "--public-key") {
		t.Fatalf("no --public-key: exit %d, stderr %s", code, stderr)
	}
	if code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills", "--public-key", writeKeyPEM(t, other)); code != 1 || !strings.Contains(stderr, "signature") {
		t.Fatalf("another key: exit %d, stderr %s", code, stderr)
	}
	for name, file := range map[string]string{
		"unreadable": filepath.Join(t.TempDir(), "absent.pub"),
		"not a key":  writeFileT(t, "bad.pub", "not a key"),
	} {
		if code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills", "--public-key", file); code != 1 || !strings.Contains(stderr, "--public-key") {
			t.Errorf("%s key file: exit %d, stderr %s", name, code, stderr)
		}
	}
	attDir := filepath.Join(dir, "skills", ".podium", "attestations")
	if _, err := os.Stat(attDir); err == nil {
		t.Fatal("a refused import wrote the attestation directory")
	}
	if code, _, stderr := runImportT(t, in, "--dir", dir, "--root", "skills", "--public-key", pub); code != 0 {
		t.Fatalf("import: exit %d, stderr %s", code, stderr)
	}
	// A bundle of another key, which this run cannot verify, stays beside a
	// newer bundle of the run's key, which replaces the first one.
	foreign := keySignedBundle(t, h, other, importSLSA, subs, attestRunAt.Add(-2*time.Hour))
	placeBundles(t, dir, "skills", foreign)
	second := keySignedBundle(t, h, key, importSLSA, subs, attestRunAt)
	code, stdout, stderr := runImportT(t, writeInput(t, second), "--dir", dir, "--root", "skills", "--public-key", pub)
	if code != 0 {
		t.Fatalf("second import: exit %d, stderr %s", code, stderr)
	}
	names := strings.Join(bundleFiles(t, attDir), " ")
	firstName, secondName, foreignName := importedName(t, first), importedName(t, second), importedName(t, foreign)
	if strings.Contains(names, firstName) || !strings.Contains(names, secondName) || !strings.Contains(names, foreignName) {
		t.Errorf("directory %s after the second import (stdout %s); want %s replaced by %s and %s kept", names, stdout, firstName, secondName, foreignName)
	}
}

// importedName is the file name an import gives raw.
func importedName(t *testing.T, raw []byte) string {
	t.Helper()
	out, _, err := attest.RemoveTimestampCertificates(raw)
	if err != nil {
		t.Fatal(err)
	}
	return attest.BundleFileName(out)
}

// Spec: §7.3.5 (Checking bundles), §4.7.11 (Key-based signers) — with a
// requirements file that names a key-based signer, the check verifies
// key-signed bundles under the key with no trusted root, and refuses one of
// another key with the reason signature.
func TestAttestCheck_KeySigner(t *testing.T) {
	dir := importLayer(t)
	h := sh.New(t)
	key := sh.NewSigningKey(t)
	subs := layerSubjectsOf(t, dir, "skills")
	reqs := map[string]any{"trust_requirements": []any{map[string]any{"signers": []any{map[string]any{"public_key": sh.PublicKeyPEM(t, key.Public())}}}}}
	raw, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	reqFile := writeFileT(t, "requirements.json", string(raw))
	placeBundles(t, dir, "skills", keySignedBundle(t, h, key, importSLSA, subs, attestRunAt))
	code, stdout, stderr := runCheckT(t, nil, dir, "--root", "skills", "--requirements", reqFile)
	if code != 0 || strings.Contains(stdout, "no signature was verified") {
		t.Fatalf("exit %d, stdout %s, stderr %s", code, stdout, stderr)
	}
	other := t.TempDir()
	writeAttestLayer(t, other, map[string]string{"skills/team/a/ARTIFACT.md": attestRuleMD, "skills/team/b/ARTIFACT.md": attestRuleMD})
	placeBundles(t, other, "skills", keySignedBundle(t, h, sh.NewSigningKey(t), importSLSA, layerSubjectsOf(t, other, "skills"), attestRunAt))
	code, stdout, _ = runCheckT(t, nil, other, "--root", "skills", "--requirements", reqFile)
	if code != 1 || !strings.Contains(stdout, "signature") {
		t.Fatalf("another key: exit %d, stdout %s", code, stdout)
	}
	// Two requirements of the key read its verification once per bundle.
	k := strconv.Quote(sh.PublicKeyPEM(t, key.Public()))
	twice := writeFileT(t, "twice.json", `{"trust_requirements":[{"covers_layer":true,"signers":[{"public_key":`+k+`}]},{"optional":true,"signers":[{"public_key":`+k+`}]}]}`)
	if code, stdout, stderr := runCheckT(t, nil, dir, "--root", "skills", "--requirements", twice); code != 0 {
		t.Fatalf("two requirements of one key: exit %d, stdout %s, stderr %s", code, stdout, stderr)
	}
}
