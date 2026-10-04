package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §7.6.2 — the MCP server does not call the batch endpoint and performs
// no startup warm-up. A bridge started with PODIUM_PREFETCH naming a served
// artifact completes a load_artifact for it, and the stub registry records
// that request and no /v1/artifacts:batchLoad request.
func TestMCPBridge_StartupNeverCallsBatchLoad(t *testing.T) {
	t.Parallel()
	fm := "---\ntype: context\nversion: 1.0.0\ndescription: warm\n---\n\nwarm body\n"
	body, _ := json.Marshal(testharness.SealDelivery(map[string]any{
		"id": "team/warm", "type": "context", "version": "1.0.0",
		"content_hash":  "sha256:" + version.CanonicalContentHash([]byte(fm), nil, nil),
		"frontmatter":   fm,
		"manifest_body": "warm body\n",
	}))
	var mu sync.Mutex
	var paths []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/v1/load_artifact" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(stub.Close)

	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + stub.URL,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
		"PODIUM_VERIFY_SIGNATURES=never",
		"PODIUM_PREFETCH=team/warm",
	}, toolCall(1, "load_artifact", map[string]any{"id": "team/warm"}))
	result := rpcResult(t, res.Stdout, 1)
	if e, _ := result["error"].(string); e != "" || result["manifest_body"] != "warm body\n" {
		t.Fatalf("load = %v, want the served record\nstderr: %s", result, res.Stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	loaded := false
	for _, p := range paths {
		if p == "/v1/artifacts:batchLoad" {
			t.Errorf("the bridge called %s", p)
		}
		loaded = loaded || p == "/v1/load_artifact"
	}
	if !loaded {
		t.Errorf("the stub recorded no load_artifact request: %v", paths)
	}
}

// Spec: §4.7.10 — a registry booted with a registry-managed key serves a
// delivery signature the bridge verifies under always. A registry booted with
// PODIUM_SIGN=none serves the delivery hash with no signature: the bridge
// refuses the load under always with materialize.signature_missing and loads
// it under never, which shows the recomputation passing on an unsigned record.
func TestE2E_DeliverySignatureFromTheBootedRegistry(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"team/doc/ARTIFACT.md": mediumArtifact("Doc")})
	keyPath := filepath.Join(t.TempDir(), "registry-signing.key")
	signed := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_SIGN_KEY_PATH=" + keyPath},
		"serve", "--standalone", "--sign", "registry-key", "--layer-path", reg)
	kf, err := sign.ReadKeyFile(keyPath)
	if err != nil {
		t.Fatalf("read the registry key: %v", err)
	}
	pub := kf.Public
	verifyKey := "PODIUM_SIGNATURE_VERIFY_KEY=" + base64.StdEncoding.EncodeToString(pub)

	var raw map[string]any
	getJSON(t, signed.BaseURL+"/v1/load_artifact?id=team/doc", &raw)
	if s, _ := raw["delivery_signature"].(string); s == "" {
		t.Fatalf("signed registry served no delivery_signature: %v", raw)
	}
	if errStr, res := bridgeLoad(t, signed.BaseURL, "team/doc", "PODIUM_SIGNATURE_PROVIDER=registry-managed",
		verifyKey, "PODIUM_VERIFY_SIGNATURES=always"); errStr != "" {
		t.Fatalf("load from the signed registry = %q, want success\nstderr: %s", errStr, res.Stderr)
	}

	unsigned := startServerUnsigned(t, reg)
	raw = nil
	getJSON(t, unsigned.BaseURL+"/v1/load_artifact?id=team/doc", &raw)
	if h, _ := raw["delivery_hash"].(string); h == "" {
		t.Errorf("unsigned registry served no delivery_hash: %v", raw)
	}
	if _, ok := raw["delivery_signature"]; ok {
		t.Errorf("unsigned registry served a delivery_signature: %v", raw)
	}
	if errStr, res := bridgeLoad(t, unsigned.BaseURL, "team/doc", verifyKey); !strings.HasPrefix(errStr, "materialize.signature_missing") {
		t.Errorf("load from the unsigned registry under always = %q, want materialize.signature_missing\nstderr: %s", errStr, res.Stderr)
	}
	if errStr, res := bridgeLoad(t, unsigned.BaseURL, "team/doc", "PODIUM_VERIFY_SIGNATURES=never"); errStr != "" {
		t.Errorf("load from the unsigned registry under never = %q, want success\nstderr: %s", errStr, res.Stderr)
	}
}

// ----- podium artifact show follows manifest_body_url (TEST-21) -------------

// showBigBody exceeds the inline cutoff so the registry serves the document
// by manifest_body_url.
var showBigBody = strings.Repeat("show line\n", objectstore.InlineCutoff/10+100) + "final line\n"

// showRegistry serves a two-layer registry whose merged child and skill carry
// above-cutoff manifest documents, over the filesystem object-store backend.
func showRegistry(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "base/shared/parent/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: parent\nx_owner: platform\n---\n\nparent prose\n"},
		testharness.WriteTreeOption{Path: "top/team/big/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: big child\nextends: shared/parent@1.x\n---\n\n" + showBigBody},
		testharness.WriteTreeOption{Path: "top/team/bigskill/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\ndescription: big skill\n---\n"},
		testharness.WriteTreeOption{Path: "top/team/bigskill/SKILL.md",
			Content: "---\nname: bigskill\ndescription: big skill\n---\n\n" + showBigBody},
	)
	return registryharness.NewLayered(t, root, "base", "top").URL
}

// showJSON decodes `podium artifact show --json` output.
func showJSON(t *testing.T, stdout string) (map[string]any, string) {
	t.Helper()
	var out struct {
		Frontmatter map[string]any `json:"frontmatter"`
		Body        string         `json:"body"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decode artifact show --json: %v\n%s", err, stdout)
	}
	return out.Frontmatter, out.Body
}

// Spec: §7.6.1, §13.12 — the compiled `podium artifact show` follows
// manifest_body_url: for an above-cutoff merged child both forms print the
// merged document, and for a skill whose SKILL.md exceeds the cutoff both
// forms print the inline ARTIFACT.md's frontmatter and the body ParseSkill
// splits from the fetched SKILL.md.
func TestCLI_ArtifactShowFollowsManifestBodyURL(t *testing.T) {
	t.Parallel()
	base := showRegistry(t)
	env := []string{"PODIUM_REGISTRY=" + base}

	res := runPodium(t, "", env, "artifact", "show", "team/big", "--json")
	cliWantExit(t, res, 0, "artifact show --json team/big")
	fm, body := showJSON(t, res.Stdout)
	if fm["x_owner"] != "platform" || !strings.HasSuffix(body, "final line\n") {
		t.Errorf("--json frontmatter %v body tail %q, want the merged document", fm, body[max(0, len(body)-20):])
	}
	res = runPodium(t, "", env, "artifact", "show", "team/big")
	cliWantExit(t, res, 0, "artifact show team/big")
	cliContains(t, res.Stdout, "x_owner: platform", "the merged frontmatter")
	cliContains(t, res.Stdout, "final line", "the merged body")

	res = runPodium(t, "", env, "artifact", "show", "team/bigskill", "--json")
	cliWantExit(t, res, 0, "artifact show --json team/bigskill")
	fm, body = showJSON(t, res.Stdout)
	if fm["type"] != "skill" || fm["name"] != nil || !strings.HasSuffix(body, "final line\n") || strings.Contains(body, "name: bigskill") {
		t.Errorf("--json frontmatter %v body head %q, want ARTIFACT.md's frontmatter and SKILL.md's body", fm, body[:min(len(body), 40)])
	}
	res = runPodium(t, "", env, "artifact", "show", "team/bigskill")
	cliWantExit(t, res, 0, "artifact show team/bigskill")
	cliContains(t, res.Stdout, "type: skill", "the inline ARTIFACT.md")
	if strings.Contains(res.Stdout, "name: bigskill") {
		t.Errorf("the human form printed SKILL.md's frontmatter:\n%.200s", res.Stdout)
	}
}

// showStub serves a load_artifact response whose manifest document travels by
// manifest_body_url to the stub's own /objects route, with query appended to
// the URL. The route answers status, or 404 when requireAuth is set and the
// request lacks it, and records each request's Authorization header. A
// non-empty linkHash overrides the link's content hash.
type showStub struct {
	ts    *httptest.Server
	mu    sync.Mutex
	auths []string
}

const showDoc = "---\ntype: context\nversion: 1.0.0\ndescription: stub doc\n---\n\nstub body\n"

func newShowStub(t *testing.T, query, requireAuth string, status int, linkHash string) *showStub {
	t.Helper()
	s := &showStub{}
	sum := sha256.Sum256([]byte(showDoc))
	if linkHash == "" {
		linkHash = "sha256:" + hex.EncodeToString(sum[:])
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/objects/", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		s.mu.Lock()
		s.auths = append(s.auths, auth)
		s.mu.Unlock()
		if requireAuth != "" && auth != requireAuth {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(showDoc))
	})
	mux.HandleFunc("/v1/load_artifact", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "team/stub", "type": "context", "version": "1.0.0", "content_hash": "sha256:c",
			"frontmatter": "", "manifest_body": "",
			"manifest_body_url": map[string]any{"presigned_url": "http://" + r.Host + "/objects/doc" + query, "content_hash": linkHash},
		})
	})
	s.ts = httptest.NewServer(mux)
	t.Cleanup(s.ts.Close)
	return s
}

// Spec: §13.12 — with a CLI token configured, the fetch of a URL that is not
// SigV4 presigned carries the token, which the filesystem backend's /objects
// route requires; both forms exit 0 and print the document.
func TestCLI_ArtifactShowFilesystemURLSendsToken(t *testing.T) {
	t.Parallel()
	s := newShowStub(t, "", "Bearer tok-21", http.StatusOK, "")
	env := []string{"PODIUM_REGISTRY=" + s.ts.URL, "PODIUM_SESSION_TOKEN=tok-21"}
	for _, args := range [][]string{{"artifact", "show", "team/stub"}, {"artifact", "show", "team/stub", "--json"}} {
		res := runPodium(t, "", env, args...)
		cliWantExit(t, res, 0, strings.Join(args, " "))
		cliContains(t, res.Stdout, "stub body", "the fetched document")
	}
}

// Spec: §13.12 — the fetch of a SigV4 presigned URL carries no credential, and
// the document is printed.
func TestCLI_ArtifactShowPresignedSigV4URLSendsNoCredential(t *testing.T) {
	t.Parallel()
	s := newShowStub(t, "?X-Amz-Signature=deadbeef", "", http.StatusOK, "")
	res := runPodium(t, "", []string{"PODIUM_REGISTRY=" + s.ts.URL, "PODIUM_SESSION_TOKEN=tok-21"}, "artifact", "show", "team/stub")
	cliWantExit(t, res, 0, "artifact show over a SigV4 URL")
	cliContains(t, res.Stdout, "stub body", "the fetched document")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.auths {
		if a != "" {
			t.Errorf("the object server received Authorization %q", a)
		}
	}
}

// Spec: §7.6.1, §13.12 — a manifest body that does not hash to the link's
// content hash, and one whose URL answers HTTP 500, exit 1 in both forms with
// stderr beginning `artifact show <id>: manifest body:` and nothing on stdout.
func TestCLI_ArtifactShowManifestBodyFailureExits1(t *testing.T) {
	t.Parallel()
	stubs := map[string]*showStub{
		"digest mismatch": newShowStub(t, "", "", http.StatusOK, "sha256:"+strings.Repeat("0", 64)),
		"HTTP 500":        newShowStub(t, "", "", http.StatusInternalServerError, ""),
	}
	for name, s := range stubs {
		for _, args := range [][]string{{"artifact", "show", "team/stub"}, {"artifact", "show", "team/stub", "--json"}} {
			res := runPodium(t, "", []string{"PODIUM_REGISTRY=" + s.ts.URL}, args...)
			if res.Exit != 1 || strings.TrimSpace(res.Stdout) != "" ||
				!strings.HasPrefix(strings.TrimSpace(res.Stderr), "artifact show team/stub: manifest body:") {
				t.Errorf("%s %v: exit %d stdout %q stderr %q", name, args, res.Exit, res.Stdout, res.Stderr)
			}
		}
	}
}

// ---- server-source sync delivery verification (§4.7.10, §7.5) ------------

// syncDeliveryArtifact is the artifact the sync delivery cases serve.
const syncDeliveryArtifact = "glossary"

// unkeyedSyncEnv pins every §4.7.9 signing variable empty, so a sync under
// the isolated HOME runBin gives it resolves no verification key.
func unkeyedSyncEnv() []string {
	return []string{"PODIUM_SIGNATURE_VERIFY_KEY=", "PODIUM_SIGN_KEY_PATH=", "PODIUM_VERIFY_SIGNATURES="}
}

// syncStub is an httptest registry that counts every request, /v1/events
// included, and serves one context artifact with a version.DeliveryHash-correct
// delivery_hash and no delivery_signature.
type syncStub struct {
	*httptest.Server
	requests atomic.Int64
}

// syncStubOpts selects how a syncStub misbehaves.
type syncStubOpts struct {
	// empty serves an effective view that lists no artifact.
	empty bool
	// tamper alters the served frontmatter after sealing.
	tamper bool
}

func newSyncStub(t *testing.T, opts syncStubOpts) *syncStub {
	t.Helper()
	s := &syncStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sync/manifest":
			list := []map[string]any{}
			if !opts.empty {
				list = append(list, map[string]any{"id": syncDeliveryArtifact, "type": "context", "version": "1.0.0", "layer": "team"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": list})
		case "/v1/load_artifact":
			resp := testharness.SealDelivery(map[string]any{
				"id": syncDeliveryArtifact, "version": "1.0.0", "type": "context", "layer": "team",
				"frontmatter":       contextArtifact("glossary"),
				"manifest_body":     "glossary body.\n",
				"artifact_revision": "2025-01-01T00:00:00.000000Z",
			})
			if opts.tamper {
				resp["frontmatter"] = contextArtifact("tampered")
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// assertNoRequests fails when the stub received any request.
func (s *syncStub) assertNoRequests(t *testing.T) {
	t.Helper()
	if n := s.requests.Load(); n != 0 {
		t.Errorf("stub received %d requests, want 0", n)
	}
}

// bootSyncServer boots a standalone server over a one-artifact registry.
func bootSyncServer(t *testing.T) *serverProc {
	t.Helper()
	return startServer(t, writeRegistry(t, map[string]string{
		syncDeliveryArtifact + "/ARTIFACT.md": contextArtifact("glossary"),
	}))
}

// otherVerifyKey returns a valid Ed25519 public key unrelated to any server.
func otherVerifyKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return base64.StdEncoding.EncodeToString(pub)
}

// syncArgs is the single-target server-source sync command line.
func syncArgs(registry, target string, extra ...string) []string {
	return append([]string{"sync", "--registry", registry, "--target", target, "--harness", "none"}, extra...)
}

// artifactFile is the materialized ARTIFACT.md of the delivery artifact.
func artifactFile(target string) string {
	return filepath.Join(target, syncDeliveryArtifact, "ARTIFACT.md")
}

// lockFile is the sync.lock of target.
func lockFile(target string) string {
	return filepath.Join(target, ".podium", "sync.lock")
}

// Spec: §7.5 — a sync against a booted standalone server verifies with the
// server's key file and materializes (case 1); with an unrelated key it exits
// 1 with materialize.signature_invalid and writes nothing (case 2); and with
// PODIUM_SIGNATURE_PROVIDER=sigstore-keyless set, which sync does not read, it
// still verifies (case 18).
func TestSyncDelivery_BootedServerVerifies(t *testing.T) {
	t.Parallel()
	srv := bootSyncServer(t)

	t.Run("server key verifies", func(t *testing.T) {
		tgt := t.TempDir()
		res := runPodium(t, "", srvSyncEnv(srv), syncArgs(srv.BaseURL, tgt)...)
		cliWantExit(t, res, 0, "keyed sync")
		mustExist(t, artifactFile(tgt))
		mustExist(t, lockFile(tgt))
	})
	t.Run("unrelated key refuses", func(t *testing.T) {
		tgt := t.TempDir()
		res := runPodium(t, "", append(unkeyedSyncEnv(), "PODIUM_SIGNATURE_VERIFY_KEY="+otherVerifyKey(t)), syncArgs(srv.BaseURL, tgt)...)
		cliWantExit(t, res, 1, "wrong-key sync")
		cliContains(t, res.Stderr, "materialize.signature_invalid", "wrong-key refusal")
		mustNotExist(t, artifactFile(tgt))
		mustNotExist(t, lockFile(tgt))
	})
	t.Run("signature provider is not read", func(t *testing.T) {
		tgt := t.TempDir()
		res := runPodium(t, "", append(srvSyncEnv(srv), "PODIUM_SIGNATURE_PROVIDER=sigstore-keyless"), syncArgs(srv.BaseURL, tgt)...)
		cliWantExit(t, res, 0, "sync with PODIUM_SIGNATURE_PROVIDER=sigstore-keyless")
		cliNotContains(t, res.Stderr, "config.invalid", "sync reads no PODIUM_SIGNATURE_PROVIDER")
		mustExist(t, artifactFile(tgt))
		mustExist(t, lockFile(tgt))
	})
}

// Spec: §7.5 — every route that resolves the delivery check refuses an
// unusable or missing key with exit 2 and config.signature_provider_unavailable
// before its first registry request: a malformed key (case 3), no key material
// (case 4), --dry-run (case 13), and an effective view that lists no artifact
// (case 25).
func TestSyncDelivery_UnusableKeyExits2BeforeAnyRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		env   []string
		opts  syncStubOpts
		extra []string
	}{
		{name: "malformed key", env: append(unkeyedSyncEnv(), "PODIUM_SIGNATURE_VERIFY_KEY=not-base64")},
		{name: "no key material", env: unkeyedSyncEnv()},
		{name: "dry-run", env: unkeyedSyncEnv(), extra: []string{"--dry-run"}},
		{name: "empty effective view", env: unkeyedSyncEnv(), opts: syncStubOpts{empty: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := newSyncStub(t, tc.opts)
			tgt := t.TempDir()
			res := runPodium(t, "", tc.env, syncArgs(stub.URL, tgt, tc.extra...)...)
			cliWantExit(t, res, 2, tc.name)
			cliContains(t, res.Stderr, "error: config.signature_provider_unavailable", tc.name)
			stub.assertNoRequests(t)
			mustNotExist(t, lockFile(tgt))
		})
	}
}

// Spec: §7.5 — under PODIUM_VERIFY_SIGNATURES=never the delivery hash is still
// recomputed, so a tampered body exits 1 with materialize.content_hash_mismatch
// (case 5).
func TestSyncDelivery_NeverStillChecksTheHash(t *testing.T) {
	t.Parallel()
	stub := newSyncStub(t, syncStubOpts{tamper: true})
	tgt := t.TempDir()
	res := runPodium(t, "", append(unkeyedSyncEnv(), "PODIUM_VERIFY_SIGNATURES=never"), syncArgs(stub.URL, tgt)...)
	cliWantExit(t, res, 1, "tampered sync under never")
	cliContains(t, res.Stderr, "materialize.content_hash_mismatch", "tampered body refusal")
	mustNotExist(t, artifactFile(tgt))
	mustNotExist(t, lockFile(tgt))
}

// Spec: §7.5 — cache mode offline-only loads nothing from the server, so it
// resolves no key and reports network.offline_cache_miss (case 7).
func TestSyncDelivery_OfflineOnlyResolvesNothing(t *testing.T) {
	t.Parallel()
	stub := newSyncStub(t, syncStubOpts{})
	res := runPodium(t, "", append(unkeyedSyncEnv(), "PODIUM_CACHE_MODE=offline-only"), syncArgs(stub.URL, t.TempDir())...)
	cliWantExit(t, res, 1, "offline-only sync")
	cliContains(t, res.Stderr, "network.offline_cache_miss", "offline-only miss")
	cliNotContains(t, res.Stderr, "config.signature_provider_unavailable", "offline-only resolves no key")
	stub.assertNoRequests(t)
}

// workspaceTarget is one kind: workspace target entry of a sync.yaml.
func workspaceTarget(id, target string) string {
	return "  - id: " + id + "\n    kind: workspace\n    harness: none\n    target: " + target + "\n"
}

// marketplaceTarget is one kind: marketplace target entry of a sync.yaml with
// no workflow commands.
func marketplaceTarget(id, target string) string {
	return "  - id: " + id + "\n    kind: marketplace\n    target: " + target + "\n" +
		"    git:\n      remote: git@example.com:acme/agents.git\n      branch: main\n" +
		"    harnesses: [claude-code]\n" +
		"    plugins:\n      - name: all\n        include: [\"**\"]\n"
}

// writeDeliveryConfig writes <ws>/.podium/sync.yaml naming registry and the
// given target entries, with defaultsExtra appended to the defaults block.
func writeDeliveryConfig(t *testing.T, ws, registry, defaultsExtra string, targets ...string) string {
	t.Helper()
	return writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+registry+"\n"+defaultsExtra+"targets:\n"+strings.Join(targets, ""))
}

// Spec: §7.5, §7.5.2 — the --config routes: an unkeyed workspace target exits 2
// (case 6) and so does its --check (case 14); offline-only workspace targets
// load nothing (case 8); a marketplace --check loads nothing (case 15).
func TestSyncDelivery_ConfigRoutes(t *testing.T) {
	t.Parallel()
	t.Run("workspace target unkeyed", func(t *testing.T) {
		t.Parallel()
		for _, extra := range [][]string{nil, {"--check"}} {
			stub := newSyncStub(t, syncStubOpts{})
			ws := t.TempDir()
			cfg := writeDeliveryConfig(t, ws, stub.URL, "", workspaceTarget("ws", filepath.Join(ws, "out")))
			res := runPodium(t, "", unkeyedSyncEnv(), append([]string{"sync", "--config", cfg}, extra...)...)
			cliWantExit(t, res, 2, "unkeyed workspace target "+strings.Join(extra, " "))
			cliContains(t, res.Stderr, "target ws: error: config.signature_provider_unavailable", "unkeyed workspace target")
			stub.assertNoRequests(t)
		}
	})
	t.Run("workspace target offline-only", func(t *testing.T) {
		t.Parallel()
		stub := newSyncStub(t, syncStubOpts{})
		ws := t.TempDir()
		cfg := writeDeliveryConfig(t, ws, stub.URL, "", workspaceTarget("ws", filepath.Join(ws, "out")))
		res := runPodium(t, "", append(unkeyedSyncEnv(), "PODIUM_CACHE_MODE=offline-only"), "sync", "--config", cfg)
		cliWantExit(t, res, 1, "offline-only workspace target")
		cliContains(t, res.Stderr, "network.offline_cache_miss", "offline-only workspace target")
		cliNotContains(t, res.Stderr, "config.signature_provider_unavailable", "offline-only workspace target")
		stub.assertNoRequests(t)
	})
	t.Run("marketplace check", func(t *testing.T) {
		t.Parallel()
		stub := newSyncStub(t, syncStubOpts{})
		ws := t.TempDir()
		cfg := writeDeliveryConfig(t, ws, stub.URL, "", marketplaceTarget("mk", filepath.Join(ws, "out")))
		res := runPodium(t, "", unkeyedSyncEnv(), "sync", "--config", cfg, "--check")
		cliWantExit(t, res, 0, "marketplace --check")
		cliNotContains(t, res.Stderr, "config.signature_provider_unavailable", "marketplace --check")
		stub.assertNoRequests(t)
	})
}

// Spec: §7.5, §7.5.2 — a defaults.verify_signatures never in the --config
// file's workspace applies to its targets, and the stale-never warning names
// that file (case 16).
func TestSyncDelivery_ConfigWorkspacePolicy(t *testing.T) {
	t.Parallel()
	stub := newSyncStub(t, syncStubOpts{})
	ws := t.TempDir()
	out := filepath.Join(ws, "out")
	cfg := writeDeliveryConfig(t, ws, stub.URL, "  verify_signatures: never\n", workspaceTarget("ws", out))
	res := runPodium(t, t.TempDir(), unkeyedSyncEnv(), "sync", "--config", cfg)
	cliWantExit(t, res, 0, "config-workspace never")
	mustExist(t, artifactFile(out))
	cliContains(t, res.Stderr, "WARN: signature verification is off because defaults.verify_signatures is never in "+cfg, "stale-never warning")
}

// Spec: §7.5, §7.5.2 — a single-target sync run inside a workspace whose
// sync.yaml sets defaults.verify_signatures: never verifies the hash only and
// warns; with PODIUM_VERIFY_SIGNATURES=never it prints no warning (case 17).
func TestSyncDelivery_WorkspacePolicyWarns(t *testing.T) {
	t.Parallel()
	stub := newSyncStub(t, syncStubOpts{})
	ws := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  verify_signatures: never\n")
	tgt := t.TempDir()
	res := runPodium(t, ws, unkeyedSyncEnv(), syncArgs(stub.URL, tgt)...)
	cliWantExit(t, res, 0, "workspace never")
	mustExist(t, artifactFile(tgt))
	warned := false
	for _, line := range strings.Split(res.Stderr, "\n") {
		if strings.HasPrefix(line, "WARN: signature verification is off because defaults.verify_signatures is never in ") &&
			strings.Contains(line, filepath.Join(".podium", "sync.yaml")) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("stderr carries no stale-never warning naming .podium/sync.yaml:\n%s", res.Stderr)
	}

	res = runPodium(t, ws, append(unkeyedSyncEnv(), "PODIUM_VERIFY_SIGNATURES=never"), syncArgs(stub.URL, t.TempDir())...)
	cliWantExit(t, res, 0, "environment never")
	cliNotContains(t, res.Stderr, "defaults.verify_signatures", "environment never prints no warning")
}

// Spec: §7.5 — batch podium sync override: against a booted server it
// materializes the added artifact (case 9), including under offline-only
// (case 11); --dry-run resolves nothing (case 10); and an unkeyed run exits 2
// before any request, leaving the toggle change in sync.lock (case 22).
func TestSyncDelivery_BatchOverride(t *testing.T) {
	t.Parallel()
	t.Run("booted server", func(t *testing.T) {
		t.Parallel()
		srv := bootSyncServer(t)
		for _, cacheMode := range []string{"", "offline-only"} {
			tgt := t.TempDir()
			cliWantExit(t, runPodium(t, "", srvSyncEnv(srv), syncArgs(srv.BaseURL, tgt)...), 0, "baseline sync")
			if err := os.Remove(artifactFile(tgt)); err != nil {
				t.Fatalf("remove: %v", err)
			}
			env := append(srvSyncEnv(srv), "PODIUM_CACHE_MODE="+cacheMode)
			res := runPodium(t, "", env, "sync", "override", "--add", syncDeliveryArtifact, "--registry", srv.BaseURL, "--target", tgt, "--harness", "none")
			cliWantExit(t, res, 0, "override --add cache mode "+cacheMode)
			cliNotContains(t, res.Stderr, "sync: server-source load has no delivery check configured", "override hand-off")
			mustExist(t, artifactFile(tgt))
		}
	})
	t.Run("dry-run", func(t *testing.T) {
		t.Parallel()
		stub := newSyncStub(t, syncStubOpts{})
		res := runPodium(t, "", unkeyedSyncEnv(), "sync", "override", "--add", syncDeliveryArtifact, "--dry-run", "--registry", stub.URL, "--target", t.TempDir())
		cliWantExit(t, res, 0, "override --dry-run")
		cliNotContains(t, res.Stderr, "config.signature_provider_unavailable", "override --dry-run")
		stub.assertNoRequests(t)
	})
	t.Run("unkeyed", func(t *testing.T) {
		t.Parallel()
		for _, cacheMode := range []string{"offline-only", ""} {
			stub := newSyncStub(t, syncStubOpts{})
			tgt := t.TempDir()
			res := runPodium(t, "", append(unkeyedSyncEnv(), "PODIUM_CACHE_MODE="+cacheMode), "sync", "override", "--add", syncDeliveryArtifact, "--registry", stub.URL, "--target", tgt)
			cliWantExit(t, res, 2, "unkeyed override cache mode "+cacheMode)
			cliContains(t, res.Stderr, "error: config.signature_provider_unavailable", "unkeyed override")
			stub.assertNoRequests(t)
			lock := readFile(t, lockFile(tgt))
			cliContains(t, lock, "id: "+syncDeliveryArtifact, "toggle kept in sync.lock")
			cliNotContains(t, lock, "artifacts:", "no artifact listed in sync.lock")
			mustNotExist(t, artifactFile(tgt))
		}
	})
}

// Spec: §7.5 — interactive podium sync override resolves before the
// checklist: unkeyed, it exits 2 with no request even under --dry-run
// (case 12); against a booted server it reaches both hand-offs and
// materializes the checked artifact (case 19).
func TestSyncDelivery_InteractiveOverride(t *testing.T) {
	t.Parallel()
	t.Run("unkeyed", func(t *testing.T) {
		t.Parallel()
		stub := newSyncStub(t, syncStubOpts{})
		res := runPodiumStdin(t, "", unkeyedSyncEnv(), "", "sync", "override", "--dry-run", "--registry", stub.URL, "--target", t.TempDir())
		cliWantExit(t, res, 2, "unkeyed interactive override")
		cliContains(t, res.Stderr, "error: config.signature_provider_unavailable", "unkeyed interactive override")
		stub.assertNoRequests(t)
	})
	t.Run("booted server", func(t *testing.T) {
		t.Parallel()
		srv := bootSyncServer(t)
		tgt := t.TempDir()
		res := runPodiumStdin(t, "", srvSyncEnv(srv), "1\nsave\n", "sync", "override", "--registry", srv.BaseURL, "--target", tgt, "--harness", "none")
		cliWantExit(t, res, 0, "interactive override")
		cliNotContains(t, res.Stderr, "sync: server-source load has no delivery check configured", "interactive hand-offs")
		mustExist(t, artifactFile(tgt))
		cliContains(t, readFile(t, lockFile(tgt)), "id: "+syncDeliveryArtifact, "toggle recorded")
	})
}

// Spec: §7.5 — a kind: marketplace target resolves under every cache mode: a
// never run renders through the stub, and an unkeyed run exits 2 with no
// request (case 20).
func TestSyncDelivery_MarketplaceResolvesUnderOfflineOnly(t *testing.T) {
	t.Parallel()
	stub := newSyncStub(t, syncStubOpts{})
	ws := t.TempDir()
	cfg := writeDeliveryConfig(t, ws, stub.URL, "", marketplaceTarget("mk", filepath.Join(ws, "out")))
	base := append(unkeyedSyncEnv(), "PODIUM_CACHE_MODE=offline-only")

	res := runPodium(t, "", append(base, "PODIUM_VERIFY_SIGNATURES=never"), "sync", "--config", cfg, "--dry-run")
	cliWantExit(t, res, 0, "marketplace dry-run under never")
	cliNotContains(t, res.Stderr, "sync: server-source load has no delivery check configured", "marketplace hand-off")
	if stub.requests.Load() == 0 {
		t.Error("the marketplace render sent no request")
	}

	stub.requests.Store(0)
	res = runPodium(t, "", base, "sync", "--config", cfg, "--dry-run")
	cliWantExit(t, res, 2, "unkeyed marketplace dry-run")
	cliContains(t, res.Stderr, "config.signature_provider_unavailable", "unkeyed marketplace dry-run")
	stub.assertNoRequests(t)
}

// Spec: §7.5 — a --config run stops at the first target refused for an
// unresolvable delivery check, after the earlier targets ran (case 24).
func TestSyncDelivery_ConfigStopsAtRefusedTarget(t *testing.T) {
	t.Parallel()
	stub := newSyncStub(t, syncStubOpts{})
	ws := t.TempDir()
	cfg := writeDeliveryConfig(t, ws, stub.URL, "",
		workspaceTarget("first", filepath.Join(ws, "first")),
		marketplaceTarget("second", filepath.Join(ws, "second")),
		workspaceTarget("third", filepath.Join(ws, "third")))
	res := runPodium(t, "", append(unkeyedSyncEnv(), "PODIUM_CACHE_MODE=offline-only"), "sync", "--config", cfg, "--dry-run")
	cliWantExit(t, res, 2, "config stop")
	cliContains(t, res.Stderr, "target first: network.offline_cache_miss", "first target ran")
	cliContains(t, res.Stderr, "target second: error: config.signature_provider_unavailable", "second target refused")
	cliNotContains(t, res.Stderr, "target third:", "third target never ran")
	cliContains(t, res.Stdout, "== target second ==", "second target header")
	cliNotContains(t, res.Stdout, "== target third ==", "third target header")
	stub.assertNoRequests(t)
}

// Spec: §7.5 — podium sync --watch hands the resolver to every cycle: against
// a booted server it materializes and exits 0 on interrupt, and with an
// unrelated key it logs materialize.signature_invalid, keeps running, writes
// nothing, and exits 1 on interrupt (case 21).
func TestSyncDelivery_WatchVerifies(t *testing.T) {
	t.Parallel()
	srv := bootSyncServer(t)

	tgt := t.TempDir()
	w := startWatch(t, srv.BaseURL, tgt, "none", srvSyncEnv(srv)...)
	if !pollFile(artifactFile(tgt), 20*time.Second) {
		t.Fatalf("watch did not materialize the artifact\nlog:\n%s", w.log())
	}
	if log := w.log(); strings.Contains(log, "sync: server-source load has no delivery check configured") || strings.Contains(log, "sync failed") {
		t.Errorf("watch log reports a failure:\n%s", log)
	}
	if code := w.stop(t); code != 0 {
		t.Errorf("keyed watch exit = %d, want 0\nlog:\n%s", code, w.log())
	}

	tgt = t.TempDir()
	env := append(unkeyedSyncEnv(), "PODIUM_SIGNATURE_VERIFY_KEY="+otherVerifyKey(t))
	w = startWatch(t, srv.BaseURL, tgt, "none", env...)
	if !pollLog(w, "materialize.signature_invalid", 20*time.Second) {
		t.Fatalf("wrong-key watch logged no materialize.signature_invalid\nlog:\n%s", w.log())
	}
	mustNotExist(t, artifactFile(tgt))
	mustNotExist(t, lockFile(tgt))
	if w.cmd.ProcessState != nil {
		t.Fatal("wrong-key watch exited before the interrupt")
	}
	if code := w.stop(t); code != 1 {
		t.Errorf("wrong-key watch exit = %d, want 1\nlog:\n%s", code, w.log())
	}
}

// Spec: §7.5 — an unkeyed podium sync --watch exits 2 on its own without
// opening the event stream (case 23).
func TestSyncDelivery_UnkeyedWatchExits2(t *testing.T) {
	t.Parallel()
	stub := newSyncStub(t, syncStubOpts{})
	res := runPodium(t, "", unkeyedSyncEnv(), syncArgs(stub.URL, t.TempDir(), "--watch")...)
	cliWantExit(t, res, 2, "unkeyed watch")
	cliContains(t, res.Stderr, "config.signature_provider_unavailable", "unkeyed watch")
	stub.assertNoRequests(t)
}
