package e2e

// End-to-end coverage for §4.7.11 attestation storage and serving through the
// compiled binary: the provenance `podium artifact show` prints after a
// signed reingest, the bundle endpoint, and the re-evaluation that a
// `podium layer update` of the trust policy runs.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
)

// Spec: §7.2 (Attestation members), §7.6.1, §7.3.5 (Serving bundles),
// §4.7.11 (Re-evaluation) — a registered local layer under require that a
// reingest verifies serves its link and outcome, which `podium artifact
// show` prints on standard error and carries under --json; the bundle
// endpoint serves the linked bundle; and a `podium layer update` that drops
// the signer downgrades the link and the outcome without a reingest.
func TestAttestationServing_ShowEndpointAndPolicyChange(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	home := t.TempDir()
	root := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay")})
	signE2ELayer(t, h, root, time.Minute)
	rootFile := writeE2EFile(t, home, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	cfgPath := writeE2EFile(t, home, "registry.yaml", []byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: "+rootFile+"\n"))
	srv := startServerArgs(t, []string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"}, "serve", "--standalone")
	env := brEnv(srv.BaseURL)

	res := runPodium(t, "", env, "layer", "register", "--id", "team", "--local", root, "--trust", "require",
		"--trust-signer", sh.DefaultIssuer+"="+sh.DefaultSAN, "--trust-instance", "github")
	cliWantExit(t, res, 0, "register")
	cliWantExit(t, runPodium(t, "", env, "layer", "reingest", "team"), 0, "reingest")

	res = runPodium(t, "", env, "artifact", "show", "finance/ap/pay-invoice")
	cliWantExit(t, res, 0, "artifact show")
	cliContains(t, res.Stderr, `attestation_outcome: "verified"`, "outcome line")
	cliContains(t, res.Stderr, `"verified" predicate_type="https://slsa.dev/provenance/v1" instance="github"`, "link line")
	res = runPodium(t, "", env, "artifact", "show", "finance/ap/pay-invoice", "--json")
	cliWantExit(t, res, 0, "artifact show --json")
	var shown struct {
		Attestations []struct {
			Digest string `json:"digest"`
		} `json:"attestations"`
		AttestationOutcome struct {
			Outcome string `json:"outcome"`
		} `json:"attestation_outcome"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &shown); err != nil || len(shown.Attestations) != 1 || shown.AttestationOutcome.Outcome != "verified" {
		t.Fatalf("artifact show --json: %v\n%s", err, res.Stdout)
	}
	if strings.Contains(res.Stderr, "attestation") {
		t.Errorf("--json repeated the members on standard error:\n%s", res.Stderr)
	}

	digest := shown.Attestations[0].Digest
	entries, err := os.ReadDir(filepath.Join(root, ".podium", "attestations"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("attestation directory: %v %v", entries, err)
	}
	want, err := os.ReadFile(filepath.Join(root, ".podium", "attestations", entries[0].Name()))
	if err != nil || attest.AttestationDigest(want) != digest {
		t.Fatalf("served digest %s, bundle file %s: %v", digest, entries[0].Name(), err)
	}
	resp, err := http.Get(srv.BaseURL + "/v1/attestations/" + digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var body struct {
		Bundle string `json:"bundle"`
	}
	got, _ := base64.StdEncoding.DecodeString(func() string { _ = json.Unmarshal(raw, &body); return body.Bundle }())
	if resp.StatusCode != http.StatusOK || string(got) != string(want) || resp.Header.Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("GET bundle = %d %q: %s", resp.StatusCode, resp.Header.Get("Cache-Control"), raw)
	}

	res = runPodium(t, "", env, "layer", "update", "--id", "team", "--trust-signer", sh.DefaultIssuer+"=carol@acme.com", "--trust-instance", "github")
	cliWantExit(t, res, 0, "update the signer")
	res = runPodium(t, "", env, "artifact", "show", "finance/ap/pay-invoice")
	cliWantExit(t, res, 0, "artifact show after the policy change")
	cliContains(t, res.Stderr, `"unverified" predicate_type="https://slsa.dev/provenance/v1"`, "downgraded link")
	cliContains(t, res.Stderr, `attestation_outcome: "failed" code="ingest.attestation_missing" scope="artifact" constraint="link_downgraded" requirement=0`, "downgraded outcome")
}
