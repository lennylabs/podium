package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	synccfg "github.com/lennylabs/podium/pkg/sync"
)

// attestPredicate is the predicate type the fixture signs.
const attestPredicate = "https://slsa.dev/provenance/v1"

// attestRegistry is a registry over HTTP that serves a signed layer under
// require: finance/pay and finance/close, both agents at 1.0.0, whose one
// bundle a cycle ingested and linked. It counts the load and bundle
// requests it answers.
type attestRegistry struct {
	ts         *httptest.Server
	rootFile   string
	bundle     []byte
	loads      atomic.Int64
	bundleHits atomic.Int64
	// down makes every request fail at the transport.
	down atomic.Bool
}

// newAttestRegistry builds and serves the signed layer.
func newAttestRegistry(t *testing.T) *attestRegistry {
	t.Helper()
	ctx := context.Background()
	h := sh.New(t)
	rootJSON := h.TrustedRootJSON(sh.GitHubStyleRoot()...)
	root, err := sign.ParseAttestationRoot(rootJSON)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"finance/pay/ARTIFACT.md":   "---\ntype: agent\nversion: 1.0.0\ndescription: Pay an invoice\nsensitivity: low\n---\n\nPay.\n",
		"finance/close/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Close the books\nsensitivity: low\n---\n\nClose.\n",
	})
	content, err := ingest.LayerContent(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	subjects := []sh.Subject{{Name: ".podium/layer", SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		subjects = append(subjects, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	bundle := h.DSSEBundle(sh.Statement(t, attestPredicate, subjects...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(time.Minute))))
	writeFiles(t, dir, map[string]string{filepath.Join(".podium", "attestations", attest.BundleFileName(bundle)): string(bundle)})
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", LocalPath: dir,
		Users: []string{"alice"}, TrustMode: "require", TrustRequirements: []store.TrustRequirement{{
			PredicateType: attestPredicate,
			Signers:       []store.TrustSigner{{Issuer: sh.DefaultIssuer, Identity: sh.DefaultSAN, Instance: "github"}},
		}}}); err != nil {
		t.Fatal(err)
	}
	cfg := &ingest.AttestationConfig{Instances: map[string]sign.AttestationInstance{"github": {Name: "github", Root: root}}}
	claim, _, claimed, err := ingest.ClaimLayer(ctx, st, "default", "team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Ingest(ctx, st, ingest.Request{TenantID: "default", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(dir), Attestation: cfg.Cycle(claimed, claim)}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	reg := core.New(st, "default", nil).WithAttestationPolicyCheck(cfg.PolicyConstraint)
	srv := server.New(reg, server.WithIdentityResolver(func(*http.Request) layer.Identity {
		return layer.Identity{Sub: "alice", IsAuthenticated: true}
	}))
	r := &attestRegistry{bundle: bundle}
	inner := srv.Handler()
	r.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.down.Load() {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		switch {
		case req.URL.Path == "/v1/load_artifact" && req.Method == http.MethodGet:
			r.loads.Add(1)
		case strings.HasPrefix(req.URL.Path, "/v1/attestations/"):
			r.bundleHits.Add(1)
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(r.ts.Close)
	r.rootFile = filepath.Join(t.TempDir(), "github-root.json")
	writeFiles(t, filepath.Dir(r.rootFile), map[string]string{"github-root.json": string(rootJSON)})
	return r
}

// writeFiles writes path → content under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// policyJSON is a policy file of one rule for finance/** under mode with a
// signer of identity on the github instance whose root is rootFile.
func policyJSON(rootFile, mode, identity string) string {
	return `{"version":1,"instances":[{"name":"github","trusted_root_file":"` + rootFile + `","transparency_log":"none"}],` +
		`"rules":[{"ids":["finance/**"],"mode":"` + mode + `","requirements":[{"predicate_type":"` + attestPredicate +
		`","signers":[{"issuer":"` + sh.DefaultIssuer + `","identity":"` + identity + `","instance":"github"}]}]}]}`
}

// consumerEnv is the inputs of loadConsumerAttestation for a test: env
// holds the environment, home the account home (empty with homeErr set
// when the lookup fails), and the machine directory is machineDir.
type consumerEnv struct {
	env        map[string]string
	flagMode   string
	flagFile   string
	home       string
	homeErr    error
	machineDir string
	cwd        string
	stderr     *strings.Builder
}

// newConsumerEnv returns inputs with an empty account home, an absent
// machine file, and an empty working directory.
func newConsumerEnv(t *testing.T) *consumerEnv {
	t.Helper()
	return &consumerEnv{env: map[string]string{}, home: t.TempDir(), machineDir: filepath.Join(t.TempDir(), "machine"),
		cwd: t.TempDir(), stderr: &strings.Builder{}}
}

// load builds the consumer attestation of the inputs.
func (e *consumerEnv) load() (*consumerAttestation, error) {
	uid := uint32(os.Getuid())
	return loadConsumerAttestation(attestationInputs{
		getenv:   func(k string) string { return e.env[k] },
		flagMode: e.flagMode, flagFile: e.flagFile,
		paths: systemPaths{machine: synccfg.MachineFileOptions{Dir: e.machineDir, TrustedUIDs: []uint32{uid}, AllowSticky: true}},
		home:  e.home, homeErr: e.homeErr, cwd: e.cwd, now: time.Now(),
		stderr: func(format string, args ...any) { _, _ = io.WriteString(e.stderr, fmt.Sprintf(format, args...)) },
	})
}

// mustLoad builds the consumer attestation or fails the test.
func (e *consumerEnv) mustLoad(t *testing.T) *consumerAttestation {
	t.Helper()
	a, err := e.load()
	if err != nil {
		t.Fatalf("loadConsumerAttestation: %v", err)
	}
	return a
}

// writeUserSync writes ~/.podium/sync.yaml under the account home.
func (e *consumerEnv) writeUserSync(t *testing.T, body string) {
	t.Helper()
	writeFiles(t, e.home, map[string]string{".podium/sync.yaml": body})
}

// attestServer is an MCP server over registry r under the consumer
// attestation a, with a cache under cacheDir in cacheMode.
func attestServer(t *testing.T, r *attestRegistry, a *consumerAttestation, cacheDir, cacheMode string) *mcpServer {
	t.Helper()
	cache, err := newContentCache(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	res := newResolutionCache(cacheDir)
	t.Cleanup(func() { _ = res.Close() })
	s := &mcpServer{
		cfg: &config{registry: r.ts.URL, harness: "none", cacheDir: cacheDir, cacheMode: cacheMode,
			resolutionTTL: 30 * time.Second, verifyPolicy: sign.PolicyNever, attestation: a},
		http:        &http.Client{},
		cache:       cache,
		resolutions: res,
		adapters:    adapter.DefaultRegistry(),
	}
	s.checker = s.newAttestationChecker()
	return s
}
