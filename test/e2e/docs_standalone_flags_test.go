package e2e

// End-to-end tests for the §13.10 standalone serve flags added in batch
// fix-13.10-b: --web-ui (F-13.10.9), --no-embeddings (F-13.10.11), and
// --sign registry-key (F-13.10.14). Each drives the real `podium` binary
// through the shared standalone harness, which always binds a loopback
// address; the non-loopback web-UI refusal (F-13.10.9) is covered by the
// serverboot/config unit tests, which do not need a bound listener.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F-13.10.9 — `podium serve --web-ui` mounts the bundled SPA at /ui/.
func TestStandaloneFlags_WebUIFlagMountsUI(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	st, body := getRaw(t, srv.BaseURL+"/ui/")
	if st != 200 {
		t.Fatalf("GET /ui/ status = %d, want 200\nlog:\n%s", st, srv.log())
	}
	if !strings.Contains(string(body), "<title>Podium</title>") {
		t.Errorf("UI response missing index marker: %.200s", body)
	}
}

// F-13.10.9 — without --web-ui the UI is not mounted; /ui/ is not served.
func TestStandaloneFlags_NoWebUIByDefault(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/ui/"); st == 200 {
		t.Errorf("GET /ui/ = 200 without --web-ui; the UI must be opt-in")
	}
}

// F-13.10.11 — `podium serve --no-embeddings` boots into BM25-only search and
// search_artifacts still answers.
func TestStandaloneFlags_NoEmbeddingsSearchWorks(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"variance-skill/ARTIFACT.md": smallteamLowArtifact("variance analysis skill"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--no-embeddings", "--layer-path", reg)

	if st := getStatus(t, srv.BaseURL+"/v1/search_artifacts?query=variance"); st != 200 {
		t.Fatalf("search_artifacts status = %d, want 200 (BM25-only)\nlog:\n%s", st, srv.log())
	}
}

// F-13.10.14 — `podium serve --sign registry-key` boots with ingest signing
// enabled, logs the registry-managed-key line, and generates the signing key
// under the standalone home.
func TestStandaloneFlags_SignRegistryKey(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("signed artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + home},
		"serve", "--standalone", "--sign", "registry-key", "--layer-path", reg)

	if !strings.Contains(srv.log(), "ingest signing: registry-managed key") {
		t.Errorf("startup log missing the registry-managed signing line:\n%s", srv.log())
	}
	keyPath := filepath.Join(home, ".podium", "standalone", "registry-signing.key")
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("registry signing key not created at %s: %v", keyPath, err)
	}
}

// F-13.10.14 — an unrecognized --sign value is refused at startup; the process
// exits non-zero before binding a listener.
func TestStandaloneFlags_SignRejectsUnknown(t *testing.T) {
	t.Parallel()
	out := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--sign", "sigstore")
	if out.Exit == 0 {
		t.Fatalf("serve --sign sigstore exit = 0, want non-zero\nstderr=%s", out.Stderr)
	}
	if !strings.Contains(out.Stderr, "config.invalid_sign_mode") {
		t.Errorf("stderr missing config.invalid_sign_mode: %s", out.Stderr)
	}
}
