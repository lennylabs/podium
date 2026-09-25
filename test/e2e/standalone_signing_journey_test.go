package e2e

// End-to-end tests for the consumer signing defaults on a standalone machine.
// The registry signs at ingest by default and generates its key file under its
// home; the bridge defaults to the registry-managed provider under the always
// policy and resolves that same key file through sign.KeyFilePath, so a
// zero-configuration standalone journey verifies every load with no signature
// environment. The standalone bootstrap writes the registry pointer alone
// into ~/.podium/sync.yaml, with no policy line and no key material.
//
// The e2e harness runs the server and the bridge under different homes by
// default: startServer pins HOME to a fresh temp dir, while mcpExec and
// runPodium run under cmdharness.IsolatedHome. The journey cases below start
// the server through startServerSharedHome, which puts it on the bridge's
// home with the standalone bootstrap enabled.
//
// Spec: §4.7.9, §6.2, §6.4, §6.9, §7.5.2, §13.10.

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// mediumArtifact is a context artifact labeled medium, the sensitivity at
// which the deleted medium-and-above policy first required a signature.
func mediumArtifact(desc string) string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: medium\n---\n\n" + desc + " body.\n"
}

// startServerSharedHome starts `podium serve --standalone` on the isolated
// home the bridge and the CLI use in the same test, with the standalone
// bootstrap enabled, so the key file the registry generates and the
// ~/.podium/sync.yaml it bootstraps are the ones the consumers read.
func startServerSharedHome(t *testing.T, registry string, extra ...string) *serverProc {
	t.Helper()
	env := append([]string{"HOME=" + cmdharness.IsolatedHome(t), "PODIUM_NO_AUTOSTANDALONE="}, extra...)
	args := []string{"serve", "--standalone"}
	if registry != "" {
		args = append(args, "--layer-path", registry)
	}
	return startServerArgs(t, env, args...)
}

// bridgeLoad runs one load_artifact through a bridge that carries no signature
// environment beyond extra, and returns the result's error string (empty on
// success) with the raw result.
func bridgeLoad(t *testing.T, baseURL, id string, extra ...string) (string, cliResult) {
	t.Helper()
	env := append([]string{
		"PODIUM_REGISTRY=" + baseURL,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT=" + t.TempDir(),
	}, extra...)
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": id}))
	errStr, _ := rpcResult(t, res.Stdout, 1)["error"].(string)
	return errStr, res
}

// homeKeyFile returns the default registry key file path under home.
func homeKeyFile(home string) string {
	return filepath.Join(home, ".podium", "standalone", "registry-signing.key")
}

// Spec: §4.7.9, §6.9 — a bridge under the defaults with no verification
// material anywhere refuses to start with config.signature_provider_unavailable.
// Matrix: §6.10 (config.signature_provider_unavailable)
func TestSignedArtifact_NoVerifyKeyRefusesUnderTheDefaults(t *testing.T) {
	t.Parallel()
	res := mcpExec(t, []string{"PODIUM_REGISTRY=http://127.0.0.1:1", "PODIUM_CACHE_DIR=" + t.TempDir()},
		toolCall(1, "load_artifact", map[string]any{"id": "x"}))
	if res.Exit == 0 {
		t.Fatalf("bridge started with no verification material under the defaults\nstdout: %s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "config.signature_provider_unavailable") {
		t.Errorf("stderr %q does not name config.signature_provider_unavailable", res.Stderr)
	}
}

// Spec: §4.7.9 — a home whose key file an earlier standalone run generated
// resolves that stale key when the bridge points at a registry signing under a
// different key: the bridge starts and the load is refused with
// materialize.signature_invalid. Setting PODIUM_SIGNATURE_VERIFY_KEY to the
// remote registry's public key, which the order places ahead of the key file,
// makes the same load succeed.
func TestSignedArtifact_StaleLocalKeyRefusesTheLoad(t *testing.T) {
	t.Parallel()
	writeHomeKeyFile(t, cmdharness.IsolatedHome(t))
	srv := startServer(t, writeRegistry(t, map[string]string{"team/doc/ARTIFACT.md": mediumArtifact("Remote doc")}))

	errStr, res := bridgeLoad(t, srv.BaseURL, "team/doc")
	if !strings.HasPrefix(errStr, "materialize.signature_invalid") {
		t.Fatalf("load under the stale local key = %q, want materialize.signature_invalid\nstderr: %s", errStr, res.Stderr)
	}

	remote, err := sign.PublicKeyFromKeyFile(homeKeyFile(srv.Home))
	if err != nil {
		t.Fatalf("read the remote registry's key: %v", err)
	}
	errStr, res = bridgeLoad(t, srv.BaseURL, "team/doc",
		"PODIUM_SIGNATURE_VERIFY_KEY="+base64.StdEncoding.EncodeToString(remote))
	if errStr != "" {
		t.Fatalf("load under the remote key = %q, want success\nstderr: %s", errStr, res.Stderr)
	}
}

// Spec: §13.10, §4.7.9 — a zero-flag standalone start bootstraps a sync.yaml
// that carries the registry pointer and nothing else: no signature policy and
// no key material.
func TestStandaloneBootstrap_WritesNoPolicyLine(t *testing.T) {
	t.Parallel()
	startServerSharedHome(t, "")
	body := readFile(t, filepath.Join(cmdharness.IsolatedHome(t), ".podium", "sync.yaml"))
	if !strings.Contains(body, "registry:") {
		t.Errorf("sync.yaml carries no defaults.registry:\n%s", body)
	}
	for _, banned := range []string{"verify_signatures", "public:", "private:", "key"} {
		if strings.Contains(body, banned) {
			t.Errorf("sync.yaml carries %q:\n%s", banned, body)
		}
	}
}

// Spec: §4.7.9, §4.7.10, §6.9, §13.10 — under PODIUM_SIGN=none the registry
// mints no envelope, serves no delivery signature for any row, and generates
// no key file. With no earlier key file the bridge on that home refuses to
// start, and PODIUM_VERIFY_SIGNATURES=never lets it load. With a key file an
// earlier signing start generated, the bridge starts on that stale key and
// refuses every row under always with materialize.signature_missing, a row the
// earlier start signed included, and the same row loads under never.
func TestStandaloneBootstrap_SignNoneRefusesTheBridge(t *testing.T) {
	t.Parallel()
	t.Run("no key file", func(t *testing.T) {
		t.Parallel()
		home := cmdharness.IsolatedHome(t)
		srv := startServerSharedHome(t, writeRegistry(t, map[string]string{"team/doc/ARTIFACT.md": mediumArtifact("Doc")}), "PODIUM_SIGN=none")
		body := readFile(t, filepath.Join(home, ".podium", "sync.yaml"))
		if !strings.Contains(body, "registry:") || strings.Contains(body, "verify_signatures") {
			t.Errorf("sync.yaml = %q, want the registry pointer and no policy line", body)
		}
		mustNotExist(t, homeKeyFile(home))
		res := bridgeStart(t, srv.BaseURL, "team/doc")
		if res.Exit == 0 || !strings.Contains(res.Stderr, "config.signature_provider_unavailable") {
			t.Errorf("bridge exit=%d stderr=%q, want a refusal naming config.signature_provider_unavailable", res.Exit, res.Stderr)
		}
		if errStr, res := bridgeLoad(t, srv.BaseURL, "team/doc", "PODIUM_VERIFY_SIGNATURES=never"); errStr != "" {
			t.Errorf("load under never = %q, want success\nstderr: %s", errStr, res.Stderr)
		}
	})
	t.Run("stale key file", func(t *testing.T) {
		t.Parallel()
		reg := writeRegistry(t, map[string]string{"team/signed/ARTIFACT.md": mediumArtifact("Signed")})
		first := startServerSharedHome(t, reg)
		stopProc(first.cmd)
		mkArtifact(t, filepath.Join(reg, "team", "unsigned"), mediumArtifact("Unsigned"))
		srv := startServerSharedHome(t, reg, "PODIUM_SIGN=none")
		if errStr, res := bridgeLoad(t, srv.BaseURL, "team/unsigned"); !strings.HasPrefix(errStr, "materialize.signature_missing") {
			t.Errorf("unsigned row = %q, want materialize.signature_missing\nstderr: %s", errStr, res.Stderr)
		}
		if errStr, res := bridgeLoad(t, srv.BaseURL, "team/signed"); !strings.HasPrefix(errStr, "materialize.signature_missing") {
			t.Errorf("row the first start signed = %q, want materialize.signature_missing\nstderr: %s", errStr, res.Stderr)
		}
		if errStr, res := bridgeLoad(t, srv.BaseURL, "team/signed", "PODIUM_VERIFY_SIGNATURES=never"); errStr != "" {
			t.Errorf("row the first start signed under never = %q, want success\nstderr: %s", errStr, res.Stderr)
		}
	})
}

// bridgeStart is bridgeLoad for a bridge expected to refuse its start: it
// returns the raw result without decoding a response.
func bridgeStart(t *testing.T, baseURL, id string) cliResult {
	t.Helper()
	return mcpExec(t, []string{"PODIUM_REGISTRY=" + baseURL, "PODIUM_CACHE_DIR=" + t.TempDir()},
		toolCall(1, "load_artifact", map[string]any{"id": id}))
}

// Spec: §4.7.9, §6.2 — the zero-configuration standalone journey loads a
// medium artifact under the always default with no signature environment:
// the registry signs at ingest and the bridge resolves the key file the
// registry generated.
func TestStandaloneFirstRun_LoadsUnderTheAlwaysDefault(t *testing.T) {
	t.Parallel()
	srv := startServerSharedHome(t, writeRegistry(t, map[string]string{"team/doc/ARTIFACT.md": mediumArtifact("Doc")}))
	if errStr, res := bridgeLoad(t, srv.BaseURL, "team/doc"); errStr != "" {
		t.Fatalf("first-run load = %q, want success\nstderr: %s", errStr, res.Stderr)
	}
}

// Spec: §6.4, §4.7.9 — a workspace overlay record loads under the always
// default with no signature anywhere, because the overlay is the developer's
// own files and §6.6 step-2 verification covers registry-returned bytes.
func TestStandaloneOverlay_LoadsUnderTheAlwaysDefault(t *testing.T) {
	t.Parallel()
	srv := startServerSharedHome(t, writeRegistry(t, map[string]string{"team/doc/ARTIFACT.md": mediumArtifact("Doc")}))
	overlay := writeRegistry(t, map[string]string{"personal/draft/ARTIFACT.md": mediumArtifact("Draft")})
	errStr, res := bridgeLoad(t, srv.BaseURL, "personal/draft", "PODIUM_OVERLAY_PATH="+overlay)
	if errStr != "" {
		t.Fatalf("overlay load = %q, want success\nstderr: %s", errStr, res.Stderr)
	}
}

// Spec: §4.7.9, §13.10 — a home whose ~/.podium/sync.yaml `podium init
// --global` wrote keeps that file byte for byte after a zero-flag serve, and a
// medium artifact loads under the always default, because the registry signs
// and the bridge resolves the registry's key file.
func TestStandaloneInitWrittenHome_LoadsUnderTheAlwaysDefault(t *testing.T) {
	t.Parallel()
	if res := runPodium(t, "", nil, "init", "--global", "--registry", "http://127.0.0.1:1"); res.Exit != 0 {
		t.Fatalf("init --global exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	syncPath := filepath.Join(cmdharness.IsolatedHome(t), ".podium", "sync.yaml")
	before := readFile(t, syncPath)
	srv := startServerSharedHome(t, writeRegistry(t, map[string]string{"team/doc/ARTIFACT.md": mediumArtifact("Doc")}))
	if after := readFile(t, syncPath); after != before {
		t.Errorf("sync.yaml rewritten:\nbefore: %q\nafter:  %q", before, after)
	}
	if errStr, res := bridgeLoad(t, srv.BaseURL, "team/doc"); errStr != "" {
		t.Fatalf("load on an init-written home = %q, want success\nstderr: %s", errStr, res.Stderr)
	}
}

// Spec: §4.7.9, §7.5.2 — a pre-existing ~/.podium/sync.yaml carrying
// verify_signatures: never is not rewritten, the load succeeds without
// verification, and the bridge announces the stale never with one stderr
// line naming the file and defaults.verify_signatures.
func TestStandaloneBootstrap_PreservesAnExistingNever(t *testing.T) {
	t.Parallel()
	syncPath := filepath.Join(cmdharness.IsolatedHome(t), ".podium", "sync.yaml")
	const existing = "defaults:\n  registry: http://127.0.0.1:1\n  verify_signatures: never\n"
	if err := os.MkdirAll(filepath.Dir(syncPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(syncPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := startServerSharedHome(t, writeRegistry(t, map[string]string{"team/doc/ARTIFACT.md": mediumArtifact("Doc")}))
	if got := readFile(t, syncPath); got != existing {
		t.Errorf("sync.yaml rewritten: %q", got)
	}
	errStr, res := bridgeLoad(t, srv.BaseURL, "team/doc")
	if errStr != "" {
		t.Fatalf("load under the file's never = %q, want success\nstderr: %s", errStr, res.Stderr)
	}
	if !strings.Contains(res.Stderr, filepath.Join(".podium", "sync.yaml")) || !strings.Contains(res.Stderr, "defaults.verify_signatures") {
		t.Errorf("stderr %q does not announce the stale never naming the file and defaults.verify_signatures", res.Stderr)
	}
}

// Spec: §4.7.9 — on a home whose standalone registry generated its key file,
// `podium sign` and `podium verify` with no flags and no signing variables
// resolve the two halves of that file, so an envelope sign produces verifies.
func TestStandaloneCLI_SignAndVerifyWithTheGeneratedKey(t *testing.T) {
	t.Parallel()
	startServerSharedHome(t, "")
	mustExist(t, homeKeyFile(cmdharness.IsolatedHome(t)))
	hash := "sha256:" + strings.Repeat("7", 64)
	signRes := runPodium(t, "", nil, "sign", "--content-hash", hash)
	if signRes.Exit != 0 {
		t.Fatalf("sign exit=%d stderr=%s", signRes.Exit, signRes.Stderr)
	}
	verifyRes := runPodium(t, "", nil, "verify", "--content-hash", hash, "--signature", strings.TrimSpace(signRes.Stdout))
	if verifyRes.Exit != 0 || !strings.Contains(verifyRes.Stderr, "verify ok") {
		t.Fatalf("verify exit=%d stderr=%s, want 0 with verify ok", verifyRes.Exit, verifyRes.Stderr)
	}
}
