package e2e

// End-to-end tests for the §13.10 / §13.12 deployment-and-configuration
// permutations: the zero-flag standalone bootstrap defaults, the
// full config precedence chain resolved through `config show` plus a CLI-flag
// override observed at the listen line, and the standard-mode
// fail-fast that names a missing backend credential before binding.
// All three run on the PR lane with no external infrastructure;
// the only network dependency is an in-test stub Ollama embedder for the
// zero-flag search assertion.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// ---- stub Ollama embedder ------------------------------------------------

// ollamaStubDim is the dimension the stub Ollama embedder emits. The zero-flag
// standalone default embedder is ollama with model nomic-embed-text, which
// serverboot opens sqlite-vec at (768). The stub must match that dimension or
// the collocated backend rejects the vector at ingest.
const ollamaStubDim = 768

// ollamaStubVector projects text into a deterministic ollamaStubDim-element
// vector. Each lowercase word increments one bucket chosen by a stable hash, so
// texts that share words land near each other under cosine distance, and the
// same text produces the same vector at ingest and at query time. This mirrors
// the OpenAI-format semanticVector used by the pgvector search tests but at the
// Ollama default dimension, so a query with strong word overlap to one artifact
// ranks that artifact first through both the BM25 and the vector halves of the
// §4.7 hybrid fusion.
func ollamaStubVector(text string) []float32 {
	v := make([]float32, ollamaStubDim)
	for _, word := range strings.Fields(strings.ToLower(text)) {
		var sum uint32
		for _, b := range []byte(word) {
			sum = sum*131 + uint32(b)
		}
		v[sum%ollamaStubDim]++
	}
	return v
}

// startStubOllama starts an httptest server speaking the Ollama /api/embeddings
// wire format (POST {"model","prompt"} -> {"embedding":[...]}). Ollama embeds
// one text per call, so the handler returns a single ollamaStubVector for the
// request's prompt. Wiring it through PODIUM_OLLAMA_URL keeps the standalone
// default embedder offline while still producing vectors that rank meaningfully.
func startStubOllama(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": ollamaStubVector(req.Prompt)})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// ---- zero-flag standalone bootstrap defaults -----------------

// startZeroFlagServer boots `podium serve` with no flags at all under a clean
// env and a temp HOME, so the §13.10 zero-flag auto-bootstrap path runs: it
// must emit the first-run notice, write ~/.podium/registry.yaml, and bind the
// loopback default 127.0.0.1:8080. The e2e startServerArgs helper always
// appends --bind, which counts as explicit server config and suppresses the
// banner, so this launcher is bespoke. Because the bind is the fixed default
// port, the test is not parallel and skips when 8080 is already taken.
//
// extraEnv is appended after the clean base (HOME + the merge scrub), so a
// caller can point the default ollama embedder at a stub without making the
// server config "explicit" (PODIUM_OLLAMA_URL is not a hasExplicitServerConfig
// key).
func startZeroFlagServer(t *testing.T, extraEnv ...string) *serverProc {
	t.Helper()
	if !portFree(t, "127.0.0.1:8080") {
		t.Skip("127.0.0.1:8080 is already in use; skipping zero-flag bootstrap e2e (needs the default bind)")
	}
	home := t.TempDir()
	env := append([]string{"HOME=" + home}, extraEnv...)

	logf, err := os.CreateTemp(t.TempDir(), "zeroflag-*.log")
	if err != nil {
		t.Fatalf("server log: %v", err)
	}
	cmd := exec.Command(cmdharness.Bin(t, "podium"), "serve")
	cmd.Env = mergeEnv(env...)
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start zero-flag server: %v", err)
	}
	s := &serverProc{BaseURL: "http://127.0.0.1:8080", Home: home, logPath: logf.Name(), cmd: cmd}
	t.Cleanup(func() { stopProc(s.cmd) })

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(s.BaseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return s
			}
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			t.Fatalf("zero-flag server exited before ready\nlog:\n%s", s.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("zero-flag server not ready within deadline\nlog:\n%s", s.log())
	return nil
}

// portFree reports whether addr can be bound right now (so the fixed-port
// zero-flag server has a chance of coming up).
func portFree(t *testing.T, addr string) bool {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// TestConfigZeroFlag_BootstrapDefaults closes the bootstrap half.
// It boots `podium serve` with no flags and an empty environment under a temp
// HOME and asserts the §13.10 zero-flag policy: the first-run notice on stderr,
// the loopback default bind 127.0.0.1:8080 on the listen line, the
// standalone-default backend selection in the hybrid-search startup log
// (sqlite-vec + ollama at dim 768), and the bootstrapped ~/.podium/registry.yaml
// describing the sqlite metadata store and the filesystem object store. This is
// the empty-environment defaults verification the standalone deployment
// promises; TestDeployment_StandaloneExplicit covers only the explicit
// --standalone --layer-path form.
//
// Spec: §13.10 (zero-flag standalone bootstrap: first-run notice, default
// backends, loopback bind), §13.12 (the resolved registry.yaml document).
func TestConfigZeroFlag_BootstrapDefaults(t *testing.T) {
	// Not parallel: binds the fixed default port 127.0.0.1:8080.
	srv := startZeroFlagServer(t)

	log := srv.log()

	// First-run notice: the §13.10 banner names the missing config, the
	// standalone mode, and the loopback URL.
	if !strings.Contains(log, "No config found at ~/.podium/registry.yaml") {
		t.Errorf("missing the §13.10 first-run notice:\n%s", log)
	}
	// Loopback default bind on the listen line.
	if !strings.Contains(log, "listening on 127.0.0.1:8080") {
		t.Errorf("server did not bind the loopback default 127.0.0.1:8080:\n%s", log)
	}
	if !strings.Contains(log, "mode=standalone") {
		t.Errorf("startup mode is not standalone:\n%s", log)
	}
	// Default backend selection: the hybrid-search line records the collocated
	// sqlite-vec backend, the ollama embedder, and the nomic-embed-text
	// dimension (768).
	if !strings.Contains(log, "vector=sqlite-vec") {
		t.Errorf("zero-flag vector backend is not sqlite-vec:\n%s", log)
	}
	if !strings.Contains(log, "embedder=ollama") {
		t.Errorf("zero-flag embedder is not ollama:\n%s", log)
	}
	if !strings.Contains(log, "dim=768") {
		t.Errorf("zero-flag embedder dimension is not 768 (nomic-embed-text):\n%s", log)
	}

	// /healthz reports the §13.2.1 health state (ready), confirming the server
	// is actually serving on the default port. The standalone deployment mode is
	// the listen-line banner asserted above, not the /healthz state field.
	var health map[string]any
	getJSON(t, srv.BaseURL+"/healthz", &health)
	if health["mode"] != "ready" {
		t.Errorf("/healthz mode = %v, want ready", health["mode"])
	}

	// The bootstrap wrote ~/.podium/registry.yaml describing the standalone
	// defaults: sqlite metadata + filesystem objects, both under
	// ~/.podium/standalone/.
	regYAML := readFile(t, filepath.Join(srv.Home, ".podium", "registry.yaml"))
	for _, want := range []string{
		"bind: 127.0.0.1:8080",
		"type: sqlite",
		"type: filesystem",
		filepath.Join(srv.Home, ".podium", "standalone", "podium.db"),
		filepath.Join(srv.Home, ".podium", "standalone", "objects"),
	} {
		if !strings.Contains(regYAML, want) {
			t.Errorf("bootstrapped registry.yaml missing %q:\n%s", want, regYAML)
		}
	}
}

// zeroFlagSearchRegistry stages three context artifacts whose descriptions are
// lexically and semantically distinct, so a query matching one of them ranks
// that artifact first through the standalone default sqlite-vec + ollama path.
func zeroFlagSearchRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"finance/reconcile/ARTIFACT.md": contextArtifact("invoice reconciliation matching vendor payments against purchase orders"),
		"hr/onboarding/ARTIFACT.md":     contextArtifact("employee onboarding checklist for new hire orientation"),
		"infra/deploy/ARTIFACT.md":      contextArtifact("kubernetes deployment rollout and pod autoscaling"),
	})
}

// TestConfigZeroFlag_SearchAgainstStubOllama closes the search half of
// It boots the standalone server on the standalone defaults
// (sqlite-vec + ollama, neither overridden) but points the default ollama
// embedder at an in-test stub through PODIUM_OLLAMA_URL, ingests a filesystem
// layer, and asserts a semantic query returns the matching artifact at rank 1
// through the HTTP search surface. This proves the zero-flag default backend
// pairing actually produces working §4.7 hybrid search end to end without a
// real Ollama daemon. A known --bind is used (the banner is covered by the
// sibling bootstrap test); the backend selection stays at the standalone
// default because nothing overrides it.
//
// Spec: §13.10 (standalone default embedder ollama + collocated sqlite-vec),
// §4.7 (hybrid retrieval through the embedding provider).
func TestConfigZeroFlag_SearchAgainstStubOllama(t *testing.T) {
	t.Parallel()
	stub := startStubOllama(t)
	reg := zeroFlagSearchRegistry(t)

	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		// Standalone defaults: do not set PODIUM_VECTOR_BACKEND or
		// PODIUM_EMBEDDING_PROVIDER, so the zero-flag sqlite-vec + ollama pairing
		// is what runs. Only redirect the ollama endpoint to the stub.
		"PODIUM_OLLAMA_URL=" + stub.URL,
	}, "serve", "--standalone", "--layer-path", reg)

	// Confirm the default backends are actually wired (a silent degrade to
	// BM25-only would mask the vector path the gap exercises).
	log := srv.log()
	if !strings.Contains(log, "vector=sqlite-vec") || !strings.Contains(log, "embedder=ollama") {
		t.Fatalf("standalone defaults not wired (want sqlite-vec + ollama):\n%s", log)
	}
	if strings.Contains(log, "vector search disabled") {
		t.Fatalf("vector search was disabled at startup:\n%s", log)
	}

	// The query shares its strong content words ("invoice", "reconciliation",
	// "vendor", "payments") with the finance target and nothing with the two
	// distractors, so both the BM25 and the stub-vector halves rank it first.
	resp := assertSemanticTopHit(t, srv.BaseURL,
		"invoice reconciliation vendor payments", "finance/reconcile")
	if resp.TotalMatched == 0 {
		t.Errorf("search returned no matches against the stub ollama embedder")
	}
}

// ---- full config precedence chain ----------------------------

// configShowSettings runs `podium config show --server` under the given env and
// returns the resolved settings table text. It fails the test on a non-zero
// exit. The command opens no backends, so a postgres store selection with no
// DSN resolves through the table without a connection attempt.
func configShowSettings(t *testing.T, env []string) string {
	t.Helper()
	res := runPodium(t, "", env, "config", "show", "--server")
	if res.Exit != 0 {
		t.Fatalf("config show --server exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	return res.Stdout
}

// settingRow returns the resolved value and source columns for the named
// setting from a `config show --server` table. The table is whitespace-aligned
// as `name   value   source`; an absent value column (an empty resolved value)
// collapses to `name   source`, which the caller distinguishes by asserting the
// expected non-empty value.
func settingRow(t *testing.T, table, name string) (value, source string) {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 && fields[0] == name {
			switch len(fields) {
			case 3:
				return fields[1], fields[2]
			case 2:
				// Empty value column: only the source remains.
				return "", fields[1]
			}
		}
	}
	t.Fatalf("config show table missing row %q:\n%s", name, table)
	return "", ""
}

// TestConfigPrecedence_FullChainThroughConfigShow closes the env-over-yaml-over-
// default half. In one `config show --server` run it stages a
// registry.yaml that sets the store, the bind, and a yaml-only field
// (vector_backend), overrides the store and the bind with environment
// variables, and leaves a fourth field (object_store.type) at its default. It
// asserts the table resolves: the store to the env value sourced from
// PODIUM_REGISTRY_STORE (env beats yaml), the bind to the env value sourced from
// PODIUM_BIND (env beats yaml), the vector_backend to the yaml value sourced
// from registry.yaml (yaml beats default), and the object_store.type to the
// hardcoded default sourced "default" (the bottom of the chain). config show
// resolves the precedence without opening a backend, so the postgres store
// selection never attempts a connection.
//
// Spec: §13.12 (config resolution precedence env > registry.yaml > default with
// per-key provenance), §7.7 (`config show` provenance surface).
func TestConfigPrecedence_FullChainThroughConfigShow(t *testing.T) {
	t.Parallel()
	cfgFile := filepath.Join(t.TempDir(), "registry.yaml")
	body := "registry:\n" +
		"  bind: 10.0.0.5:7000\n" +
		"  store:\n" +
		"    type: sqlite\n" +
		"  vector_backend:\n" +
		"    type: pgvector\n"
	if err := os.WriteFile(cfgFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	table := configShowSettings(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_CONFIG_FILE=" + cfgFile,
		// Env layer overrides the store and the bind for the same fields the
		// yaml sets.
		"PODIUM_REGISTRY_STORE=postgres",
		"PODIUM_BIND=127.0.0.1:7001",
		// Ensure the yaml-only and default fields are not shadowed by ambient env.
		"PODIUM_VECTOR_BACKEND=",
		"PODIUM_OBJECT_STORE=",
		"PODIUM_REGISTRY=",
	})

	// store.type: env wins over the yaml sqlite, sourced from the env var.
	if v, src := settingRow(t, table, "store.type"); v != "postgres" || src != "PODIUM_REGISTRY_STORE" {
		t.Errorf("store.type = %q (src %q), want postgres from PODIUM_REGISTRY_STORE (env beats yaml)", v, src)
	}
	// bind: env wins over the yaml 10.0.0.5:7000, sourced from the env var.
	if v, src := settingRow(t, table, "bind"); v != "127.0.0.1:7001" || src != "PODIUM_BIND" {
		t.Errorf("bind = %q (src %q), want 127.0.0.1:7001 from PODIUM_BIND (env beats yaml)", v, src)
	}
	// vector_backend: yaml-only field, the yaml value used and attributed to the
	// config file (yaml beats default).
	if v, src := settingRow(t, table, "vector_backend"); v != "pgvector" || src != "registry.yaml" {
		t.Errorf("vector_backend = %q (src %q), want pgvector from registry.yaml (yaml beats default)", v, src)
	}
	// object_store.type: neither env nor yaml set it, so the hardcoded default
	// (filesystem) sits at the bottom of the chain sourced "default".
	if v, src := settingRow(t, table, "object_store.type"); v != "filesystem" || src != "default" {
		t.Errorf("object_store.type = %q (src %q), want filesystem from default (bottom of chain)", v, src)
	}
}

// startServerCLIBind boots `podium serve --standalone --bind <cliBind>` with the
// given env and waits for /healthz on cliBind. Unlike startServerArgs (which
// chooses its own --bind port), this launcher pins the CLI bind so a test can
// set PODIUM_BIND to a different port and observe which one wins.
func startServerCLIBind(t *testing.T, env []string, cliBind string) *serverProc {
	t.Helper()
	logf, err := os.CreateTemp(t.TempDir(), "server-*.log")
	if err != nil {
		t.Fatalf("server log: %v", err)
	}
	cmd := exec.Command(cmdharness.Bin(t, "podium"), "serve", "--standalone", "--bind", cliBind)
	cmd.Env = mergeEnv(env...)
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	s := &serverProc{BaseURL: "http://" + cliBind, logPath: logf.Name(), cmd: cmd}
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			s.Home = strings.TrimPrefix(kv, "HOME=")
		}
	}
	t.Cleanup(func() { stopProc(s.cmd) })
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(s.BaseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return s
			}
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			t.Fatalf("server exited before ready\nlog:\n%s", s.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server not ready at %s within deadline\nlog:\n%s", s.BaseURL, s.log())
	return nil
}

// TestConfigPrecedence_CLIFlagBeatsEnv closes the CLI-over-env half of
// The serve command maps --bind onto PODIUM_BIND before config
// resolution, so a --bind flag wins over an already-set PODIUM_BIND. It boots a
// standalone server with PODIUM_BIND pointed at one loopback port and --bind at
// a distinct port, then asserts the listen line names the CLI port, /healthz
// serves on the CLI port, and nothing answers on the env port, proving the CLI
// flag sits above the environment in the chain.
//
// Spec: §13.12 (precedence CLI flag > env), §13.10 (the serve --bind override).
func TestConfigPrecedence_CLIFlagBeatsEnv(t *testing.T) {
	t.Parallel()
	// Two distinct ports. freePort releases immediately, so reserve the env
	// port by holding its listener open for the life of the test; that both
	// guarantees the two ports differ and proves the env port was never bound
	// by the server (the server would fail to bind a held port, but the CLI
	// flag means it never tries).
	envLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve env port: %v", err)
	}
	defer envLn.Close()
	envBind := envLn.Addr().String()
	cliBind := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	srv := startServerCLIBind(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_BIND=" + envBind,
	}, cliBind)

	// The server is healthy on the CLI bind (startServerCLIBind waited there),
	// which is itself proof the CLI flag won. Corroborate with the listen line.
	log := srv.log()
	if !strings.Contains(log, "listening on "+cliBind) {
		t.Errorf("listen line does not show the CLI --bind %s (CLI must beat env):\n%s", cliBind, log)
	}
	if strings.Contains(log, "listening on "+envBind) {
		t.Errorf("server bound the env PODIUM_BIND %s; the CLI --bind must win:\n%s", envBind, log)
	}
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Errorf("/healthz on the CLI bind = %d, want 200", st)
	}
}

// ---- standard-mode fail-fast names the missing credential -----

// serveExpectStartupError runs `podium serve --bind <freeport>` with the given
// env and a short deadline, expecting the process to exit non-zero before it
// binds. It returns the combined stderr/stdout. The bind never succeeds, so the
// readiness loop is replaced by a bounded Wait. A still-running process after
// the deadline is a failure (the server bound instead of refusing).
func serveExpectStartupError(t *testing.T, env []string) (exitCode int, output string) {
	t.Helper()
	port := freePort(t)
	bind := fmt.Sprintf("127.0.0.1:%d", port)
	// Confirm nothing answers on the port before and after, so "exited before
	// bind" is meaningful.
	res := runBin(t, cmdharness.Bin(t, "podium"), "", append(env, "PODIUM_NO_AUTOSTANDALONE=1"), nil, 20*time.Second,
		"serve", "--bind", bind)
	if st := getStatusNoFatal("http://" + bind + "/healthz"); st == 200 {
		t.Fatalf("server bound %s instead of refusing to start; stderr=%s", bind, res.Stderr)
	}
	return res.Exit, res.Stderr + res.Stdout
}

// getStatusNoFatal GETs url and returns its status, or 0 on any transport error
// (connection refused, the expected outcome when no server bound).
func getStatusNoFatal(url string) int {
	resp, err := httpClient.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// serveNoBindExpectRefusal runs `podium serve` with the given args and env but
// no --bind, expecting the §13.10 zero-flag refusal (--strict /
// PODIUM_NO_AUTOSTANDALONE with no explicit config) to exit non-zero before the
// server starts. Omitting --bind is required: PODIUM_BIND counts as explicit
// server config, which routes around the refusal. The env must pin HOME to an
// empty temp dir so no ~/.podium/registry.yaml is found. It returns the exit
// code and combined output.
func serveNoBindExpectRefusal(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	res := runBin(t, cmdharness.Bin(t, "podium"), "", env, nil, 20*time.Second,
		append([]string{"serve"}, args...)...)
	return res.Exit, res.Stderr + res.Stdout
}

// TestConfigStandardFailFast_NamesMissingCredential. It boots
// the real `podium serve` twice with a backend explicitly selected and that
// backend's required credential absent: PODIUM_REGISTRY_STORE=postgres with no
// PODIUM_POSTGRES_DSN, and PODIUM_OBJECT_STORE=s3 with no PODIUM_S3_BUCKET. Each
// run must exit non-zero before binding with a startup error that names the
// specific missing variable, the §13.12 contract that a configured-but-
// incomplete backend is a hard startup error rather than a silent degrade. The
// postgres case also confirms a standard (postgres) store selection never falls
// into the zero-flag standalone auto-bootstrap.
//
// Spec: §13.12 (the registry refuses to start when a backend is selected but its
// required values are missing, naming the missing keys).
func TestConfigStandardFailFast_NamesMissingCredential(t *testing.T) {
	t.Parallel()

	t.Run("postgres store without DSN", func(t *testing.T) {
		t.Parallel()
		exit, out := serveExpectStartupError(t, []string{
			"HOME=" + t.TempDir(),
			"PODIUM_REGISTRY_STORE=postgres",
			"PODIUM_POSTGRES_DSN=",
		})
		if exit == 0 {
			t.Fatalf("serve exited 0; expected a non-zero fail-fast\noutput:\n%s", out)
		}
		if !strings.Contains(out, "PODIUM_POSTGRES_DSN") {
			t.Errorf("startup error does not name the missing PODIUM_POSTGRES_DSN:\n%s", out)
		}
	})

	t.Run("s3 object store without bucket", func(t *testing.T) {
		t.Parallel()
		exit, out := serveExpectStartupError(t, []string{
			"HOME=" + t.TempDir(),
			"PODIUM_OBJECT_STORE=s3",
			// A region is set so the bucket is the single named missing value.
			"PODIUM_S3_REGION=us-east-1",
			"PODIUM_S3_BUCKET=",
		})
		if exit == 0 {
			t.Fatalf("serve exited 0; expected a non-zero fail-fast\noutput:\n%s", out)
		}
		if !strings.Contains(out, "PODIUM_S3_BUCKET") {
			t.Errorf("startup error does not name the missing PODIUM_S3_BUCKET:\n%s", out)
		}
	})
}
