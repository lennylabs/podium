package e2e

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
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
// carries content_hash per artifact so a pre-flight check can verify the full
// §14.11 (artifact_id, version, content_hash) triple before the lock file is
// committed. The dry-run hash must equal the hash the subsequent committed lock
// records for the same artifact.
func TestSync_DryRunJSONEnvelopeIncludesContentHash(t *testing.T) {
	reg := cliReg(t)
	ws := t.TempDir()
	tgt := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n  harness: none\n")

	dry := runPodium(t, ws, nil, "sync", "--target", tgt, "--dry-run", "--json")
	cliWantExit(t, dry, 0, "sync --dry-run --json")

	var env struct {
		Artifacts []struct {
			ID          string `json:"id"`
			Version     string `json:"version"`
			ContentHash string `json:"content_hash"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(dry.Stdout), &env); err != nil {
		t.Fatalf("dry-run envelope not valid JSON: %v\n%s", err, dry.Stdout)
	}
	if len(env.Artifacts) == 0 {
		t.Fatalf("no artifacts in dry-run envelope:\n%s", dry.Stdout)
	}
	want := map[string]string{}
	for _, a := range env.Artifacts {
		if !strings.HasPrefix(a.ContentHash, "sha256:") {
			t.Errorf("artifact %q content_hash = %q, want sha256: prefix", a.ID, a.ContentHash)
		}
		want[a.ID] = a.ContentHash
	}

	// The committed lock records the same content_hash the dry-run reported, so
	// the pre-flight triple matches what lands in the image (§14.11).
	full := runPodium(t, ws, nil, "sync", "--target", tgt, "--harness", "none")
	cliWantExit(t, full, 0, "full sync")
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	for id, hash := range want {
		cliContains(t, lock, "content_hash: "+hash, "lock content_hash for "+id+" matches the dry-run pre-flight")
	}
}
