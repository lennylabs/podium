package e2e

import (
	"net/url"
	"path/filepath"
	"testing"
)

// spec: §7.4 (F-7.4.3) — always-revalidate (the default cache mode) surfaces the
// structured network.registry_unreachable code when the server-source registry
// is unreachable and there is no cache, rather than the raw transport error.
// podium sync keeps no offline content cache, so the no-cache branch always
// applies for an unreachable server. The registry points at an unbound port so
// the dial is refused; the CLI must exit non-zero with the namespaced code.
func TestSyncServerSource_AlwaysRevalidateUnreachable(t *testing.T) {
	tgt := t.TempDir()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir()},
		"sync", "--registry", "http://127.0.0.1:1", "--target", tgt, "--harness", "none")
	cliWantExit(t, res, 1, "always-revalidate unreachable sync")
	cliContains(t, res.Stderr, "network.registry_unreachable", "structured unreachable code")
	// The raw transport message must not be the only thing surfaced; the
	// namespaced code leads. (offline-only would say network.offline_cache_miss.)
	cliNotContains(t, res.Stderr, "network.offline_cache_miss", "not the offline-only code")
}

// spec: §14.11 / §7.5.3 (F-14.11.1) — a server-source sync pins the registry's
// authoritative content_hash into the committed lock, not a digest recomputed
// from the served bytes. Drives the real podium binary against a standalone
// server, then asserts the lock's content_hash for an artifact equals the value
// the registry serves from /v1/load_artifact.
func TestSyncServerSource_LockContentHashIsRegistryAuthoritative(t *testing.T) {
	srv := startServer(t, cliReg(t))
	tgt := t.TempDir()
	res := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"sync", "--registry", srv.BaseURL, "--target", tgt, "--harness", "none")
	cliWantExit(t, res, 0, "server-source sync")

	var loaded struct {
		ContentHash string `json:"content_hash"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+url.QueryEscape("finance/invoice"), &loaded)
	if loaded.ContentHash == "" {
		t.Fatal("registry served empty content_hash for finance/invoice")
	}
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock, "content_hash: "+loaded.ContentHash,
		"lock pins the registry's authoritative content_hash")
}

// spec: §7.5 / §14.11 (F-14.11.3) — the --dry-run --json pre-flight envelope
// emits exactly {id, version, type, layer} per artifact (§7.5), so it carries
// no content_hash field. The (id, version, content_hash) triple §14.11
// emphasizes lives in the committed lock, not the pre-flight envelope. This
// guards the §7.5 envelope contract against an incidental content_hash addition.
func TestSync_DryRunJSONEnvelopeOmitsContentHash(t *testing.T) {
	reg := cliReg(t)
	ws := t.TempDir()
	tgt := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n  harness: none\n")

	dry := runPodium(t, ws, nil, "sync", "--target", tgt, "--dry-run", "--json")
	cliWantExit(t, dry, 0, "sync --dry-run --json")
	cliNotContains(t, dry.Stdout, "content_hash", "dry-run envelope omits content_hash per §7.5")

	// The committed lock, by contrast, does record the triple's content_hash.
	full := runPodium(t, ws, nil, "sync", "--target", tgt, "--harness", "none")
	cliWantExit(t, full, 0, "full sync")
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock, "content_hash: sha256:", "lock records the content_hash half of the triple")
}
