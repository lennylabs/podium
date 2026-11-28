package e2e

// End-to-end coverage for §4.7.11 ingest verification through the compiled
// binary: a startup ingest that the layer step refuses under require, which
// does not abort the start; the attestation record that `podium layer show`
// prints with its next step; the exit status and envelope of `podium layer
// reingest` on a refused cycle; a signed layer that a reingest accepts; and
// the mark reset flags.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// attestedLayerServer starts a registry with the declared local layer
// finance under require, signed by the harness's default email identity on a
// github instance whose trusted root the harness wrote. It returns the
// server, the layer directory, and the harness.
func attestedLayerServer(t *testing.T) (*serverProc, string, *sh.Harness) {
	t.Helper()
	h := sh.New(t)
	home := t.TempDir()
	root := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay")})
	rootFile := writeE2EFile(t, home, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	cfg := "registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: " + rootFile + "\n" +
		"  layers:\n    - id: finance\n      source:\n        local:\n          path: " + root + "\n" +
		"      trust_mode: require\n      trust_requirements:\n        - signers: [{issuer: \"" + sh.DefaultIssuer + "\", identity: " + sh.DefaultSAN + ", instance: github}]\n"
	cfgPath := writeE2EFile(t, home, "registry.yaml", []byte(cfg))
	srv := startServerArgs(t, []string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"}, "serve", "--standalone")
	return srv, root, h
}

// signE2ELayer writes into dir's attestation directory one bundle that lists
// the layer subject and every package of dir, timestamped at offset from the
// harness clock.
func signE2ELayer(t *testing.T, h *sh.Harness, dir string, offset time.Duration) {
	t.Helper()
	signE2ELayerNamed(t, h, dir, ".podium/layer", offset)
}

// signE2ELayerNamed is signE2ELayer with the layer subject named layerName,
// as a layer signed under a root lists it.
func signE2ELayerNamed(t *testing.T, h *sh.Harness, dir, layerName string, offset time.Duration) {
	t.Helper()
	content, err := filesystem.LayerContent(filesystem.Layer{ID: "finance", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	subjects := []sh.Subject{{Name: layerName, SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		subjects = append(subjects, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	bundle := h.DSSEBundle(sh.Statement(t, "https://slsa.dev/provenance/v1", subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(offset))))
	attDir := filepath.Join(dir, ".podium", "attestations")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attDir, attest.BundleFileName(bundle)), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Spec: §4.7.11 (Startup ingest, Attestation record, Mark reset), §7.3.1,
// §13.10 — a declared layer under require with no bundle is refused at the
// start, the registry still listens, and the start log names the refusal.
// `podium layer show` prints the record and its next step, and `podium
// layer reingest` exits 1 with the ingest.attestation_missing envelope. Once
// the layer is signed, a reingest stores it and the record holds ok with the
// mark. A reset reingest passes again, and --to-commit on a local layer is
// refused.
func TestAttestationIngest_StartupRefusalAndReingest(t *testing.T) {
	t.Parallel()
	srv, root, h := attestedLayerServer(t)
	cliContains(t, srv.log(), "the startup ingest of layer finance was refused with ingest.attestation_missing", "start log")
	env := brEnv(srv.BaseURL)

	res := runPodium(t, "", env, "layer", "show", "finance")
	cliWantExit(t, res, 0, "layer show of a refused layer")
	cliContains(t, res.Stdout, `"attestation_status": "ingest.attestation_missing"`, "record status")
	cliContains(t, res.Stderr, "next step: Check that the tracked ref is the signed ref", "next step")

	res = runPodium(t, "", env, "layer", "reingest", "finance")
	cliWantExit(t, res, 1, "reingest of an unsigned layer")
	cliContains(t, res.Stderr, "HTTP 422", "refusal status")
	cliContains(t, res.Stderr, `"code": "ingest.attestation_missing"`, "refusal code")
	cliContains(t, res.Stderr, `"constraint": "no_bundle"`, "refusal constraint")

	signE2ELayer(t, h, root, time.Minute)
	res = runPodium(t, "", env, "layer", "reingest", "finance")
	cliWantExit(t, res, 0, "reingest of a signed layer")
	obj := shownLayer(t, srv.BaseURL, "finance")
	if obj["attestation_status"] != "ok" || obj["attestation_mark"] == nil {
		t.Fatalf("record after the signed reingest: %v %v", obj["attestation_status"], obj["attestation_mark"])
	}

	res = runPodium(t, "", env, "layer", "reingest", "finance", "--reset-attestation-mark")
	cliWantExit(t, res, 0, "reset reingest")
	res = runPodium(t, "", env, "layer", "reingest", "finance", "--reset-attestation-mark", "--to-commit", strings.Repeat("a", 40))
	cliWantExit(t, res, 1, "--to-commit on a local layer")
	cliContains(t, res.Stderr, "registry.invalid_argument", "to-commit refusal")
	res = runPodium(t, "", env, "layer", "reingest", "finance", "--to-commit", strings.Repeat("a", 40))
	cliWantExit(t, res, 2, "--to-commit without the reset")
}

// Spec: §4.7.11 (Trust of bootstrap layers), §13.12 — a --layer-path layer
// that PODIUM_LAYER_TRUST_DEFAULT resolves to require refuses the start
// without PODIUM_LAYER_TRUST_REQUIREMENTS_FILE, and starts with it, carrying
// the file's requirement, verifying its signed content, and storing it; a
// file that is not JSON refuses the start.
func TestAttestationIngest_BootstrapRequirementsFile(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	home := t.TempDir()
	root := writeRegistry(t, map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay")})
	signE2ELayer(t, h, root, time.Minute)
	rootFile := writeE2EFile(t, home, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	cfgPath := writeE2EFile(t, home, "registry.yaml", []byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: "+rootFile+"\n"))
	base := []string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true", "PODIUM_LAYER_TRUST_DEFAULT=require"}

	res := runPodium(t, "", base, "serve", "--standalone", "--layer-path", root, "--bind", "127.0.0.1:0")
	if res.Exit == 0 || !strings.Contains(res.Stdout+res.Stderr, "config.invalid_layer_trust") ||
		!strings.Contains(res.Stdout+res.Stderr, "PODIUM_LAYER_TRUST_REQUIREMENTS_FILE") {
		t.Fatalf("start without the file: exit %d\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
	bad := writeE2EFile(t, home, "bad.json", []byte("x"))
	res = runPodium(t, "", append(base, "PODIUM_LAYER_TRUST_REQUIREMENTS_FILE="+bad), "serve", "--standalone", "--layer-path", root, "--bind", "127.0.0.1:0")
	if res.Exit == 0 || !strings.Contains(res.Stdout+res.Stderr, "config.invalid_layer_trust: PODIUM_LAYER_TRUST_REQUIREMENTS_FILE") {
		t.Fatalf("start with a bad file: exit %d\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
	reqs := writeE2EFile(t, home, "requirements.json", []byte(`{"trust_requirements":[{"signers":[{"issuer":"`+sh.DefaultIssuer+`","identity":"`+sh.DefaultSAN+`","instance":"github"}]}]}`))
	srv := startServerArgs(t, append(base, "PODIUM_LAYER_TRUST_REQUIREMENTS_FILE="+reqs), "serve", "--standalone", "--layer-path", root)
	layers := runPodium(t, "", brEnv(srv.BaseURL), "layer", "list")
	cliWantExit(t, layers, 0, "layer list")
	cliContains(t, layers.Stdout, `"attestation_status": "ok"`, "bootstrap record")
	cliContains(t, layers.Stdout, `"identity": "`+sh.DefaultSAN+`"`, "bootstrap requirement")
}
