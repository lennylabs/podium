package e2e

// End-to-end coverage for §4.7.11 key-based signers through the compiled
// binary: `podium attest --signed-time`, `podium attest import
// --public-key`, `podium attest check` with a key-based requirement,
// `podium layer register --trust-public-key`, a reingest that verifies the
// key-signed layer subject and serves its signer, the signed_time_ahead
// refusal and its next step, and the start refusal of a
// PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE that §13.12 does not admit.

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// signedAtPattern matches the signed-time subject name of a statement file.
var signedAtPattern = regexp.MustCompile(`"name":"\.podium/signed-at/[0-9]+"`)

// signKeyStatements runs `podium attest --format statement --signed-time`
// over layer, signs each statement with key, and returns a JSON Lines file
// of the bundles, as `cosign attest-blob --key` over each file writes them.
func signKeyStatements(t *testing.T, h *sh.Harness, key *ecdsa.PrivateKey, layer string) string {
	t.Helper()
	out := t.TempDir()
	res := runPodium(t, "", nil, "attest", layer, "--format", "statement", "--signed-time", "--out", out)
	cliWantExit(t, res, 0, "attest --signed-time")
	var lines bytes.Buffer
	for _, p := range strings.Fields(res.Stdout) {
		stmt, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(stmt, []byte(`"name":".podium/signed-at/`)) {
			t.Fatalf("statement %s lists no signed-time subject", stmt)
		}
		lines.Write(h.DSSEBundle(bytes.TrimSpace(stmt), sh.WithSigningKey(key)))
		lines.WriteByte('\n')
	}
	return writeFileE2E(t, "bundles.jsonl", lines.Bytes())
}

// Spec: §7.3.5, §7.3.1 (Layer CLI), §4.7.11 (Key-based signers, Layer step,
// Attestation storage), §7.2 (Attestation members) — a layer signed with a
// key over `podium attest --signed-time` statements is imported with
// --public-key, checked against a key-based requirement, registered with
// --trust-public-key, and verified by a reingest; the served link names the
// key's identifier with an empty instance and issuer.
func TestKeySigners_SignImportCheckRegisterServe(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	key := sh.NewSigningKey(t)
	layer := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay")})
	pub := writeFileE2E(t, "cosign.pub", []byte(sh.PublicKeyPEM(t, key.Public())))
	bundles := signKeyStatements(t, h, key, layer)

	res := runPodium(t, "", nil, "attest", "import", bundles, "--dir", layer)
	cliWantExit(t, res, 1, "import without --public-key")
	cliContains(t, res.Stderr, "--public-key", "import refusal")
	res = runPodium(t, "", nil, "attest", "import", bundles, "--dir", layer, "--public-key", pub)
	cliWantExit(t, res, 0, "import --public-key")

	reqs, err := json.Marshal(map[string]any{"trust_requirements": []any{map[string]any{"signers": []any{map[string]any{"public_key": sh.PublicKeyPEM(t, key.Public())}}}}})
	if err != nil {
		t.Fatal(err)
	}
	res = runPodium(t, "", nil, "attest", "check", layer, "--requirements", writeFileE2E(t, "requirements.json", reqs))
	cliWantExit(t, res, 0, "attest check with a key-based signer")

	home := t.TempDir()
	srv := startServerArgs(t, []string{"HOME=" + home, "PODIUM_INGEST_OFFLINE=true"}, "serve", "--standalone")
	env := brEnv(srv.BaseURL)
	res = runPodium(t, "", env, "layer", "register", "--id", "team", "--local", layer, "--trust", "require", "--trust-public-key", pub)
	cliWantExit(t, res, 0, "register --trust-public-key")
	cliWantExit(t, runPodium(t, "", env, "layer", "reingest", "team"), 0, "reingest")

	obj := shownLayer(t, srv.BaseURL, "team")
	if obj["attestation_status"] != "ok" || !strings.Contains(obj["attestation_mark"].(string), "T") {
		t.Errorf("record %v %v", obj["attestation_status"], obj["attestation_mark"])
	}
	res = runPodium(t, "", env, "layer", "show", "team", "--requirements")
	cliWantExit(t, res, 0, "layer show --requirements")
	cliContains(t, res.Stdout, `"public_key": "-----BEGIN PUBLIC KEY-----\n`, "requirements file key")

	res = runPodium(t, "", env, "artifact", "show", "finance/ap/pay-invoice", "--json")
	cliWantExit(t, res, 0, "artifact show --json")
	var shown struct {
		Attestations []struct {
			Signer struct {
				Instance, Issuer, Identity string
			} `json:"signer"`
			AttestedAt string `json:"attested_at"`
		} `json:"attestations"`
		AttestationOutcome struct {
			Outcome string `json:"outcome"`
		} `json:"attestation_outcome"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &shown); err != nil || len(shown.Attestations) != 1 || shown.AttestationOutcome.Outcome != "verified" {
		t.Fatalf("artifact show --json: %v\n%s", err, res.Stdout)
	}
	if s := shown.Attestations[0].Signer; s.Instance != "" || s.Issuer != "" || s.Identity != sh.KeyIdentifier(t, key.Public()) || shown.Attestations[0].AttestedAt == "" {
		t.Errorf("served signer %+v attested_at %q", s, shown.Attestations[0].AttestedAt)
	}
}

// Spec: §4.7.11 (Layer step), §13.12, §7.3.1 (Layer CLI) — under
// PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE=0s, a key-signed layer subject
// whose signed time lies an hour ahead of the registry's clock is refused
// with ingest.attestation_stale and signed_time_ahead, and `podium layer
// show` names the next step.
func TestKeySigners_SignedTimeAhead(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	key := sh.NewSigningKey(t)
	layer := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay")})
	out := t.TempDir()
	res := runPodium(t, "", nil, "attest", layer, "--format", "statement", "--signed-time", "--out", out)
	cliWantExit(t, res, 0, "attest --signed-time")
	stmt, err := os.ReadFile(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	ahead := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	stmt = []byte(signedAtPattern.ReplaceAllString(string(stmt), `"name":".podium/signed-at/`+ahead+`"`))
	attDir := filepath.Join(layer, ".podium", "attestations")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := h.DSSEBundle(bytes.TrimSpace(stmt), sh.WithSigningKey(key))
	writeE2EFile(t, attDir, "ahead.sigstore.json", bundle)
	pub := writeFileE2E(t, "cosign.pub", []byte(sh.PublicKeyPEM(t, key.Public())))

	home := t.TempDir()
	srv := startServerArgs(t, []string{"HOME=" + home, "PODIUM_INGEST_OFFLINE=true", "PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE=0s"}, "serve", "--standalone")
	env := brEnv(srv.BaseURL)
	cliWantExit(t, runPodium(t, "", env, "layer", "register", "--id", "team", "--local", layer, "--trust", "require", "--trust-public-key", pub), 0, "register")
	res = runPodium(t, "", env, "layer", "reingest", "team")
	cliWantExit(t, res, 1, "reingest ahead of the clock")
	cliContains(t, res.Stderr, `"code": "ingest.attestation_stale"`, "reingest code")
	cliContains(t, res.Stderr, `"constraint": "signed_time_ahead"`, "reingest constraint")
	cliContains(t, res.Stderr, `"allowance": "0s"`, "reingest allowance")
	res = runPodium(t, "", env, "layer", "show", "team")
	cliWantExit(t, res, 0, "layer show")
	cliContains(t, res.Stderr, "PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE", "next step")
}

// Spec: §13.12, §4.7.11 (Start checks) — a
// PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE outside 0s to 24h, or that is no
// duration, refuses the start with config.invalid naming the setting.
// Matrix: §6.10 (config.invalid)
func TestKeySigners_AllowanceStartRefusal(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"25h", "-1s", "soon"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			res := runPodium(t, "", []string{"HOME=" + home, "PODIUM_INGEST_OFFLINE=true", "PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE=" + value},
				"serve", "--standalone", "--bind", "127.0.0.1:0")
			out := res.Stdout + res.Stderr
			if res.Exit == 0 || !strings.Contains(out, "config.invalid: PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE must be a duration from 0s to 24h0m0s") {
				t.Fatalf("exit %d, want the config.invalid refusal:\n%s", res.Exit, out)
			}
		})
	}
}
