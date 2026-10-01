package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
