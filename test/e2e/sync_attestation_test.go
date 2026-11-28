package e2e

// End-to-end coverage of the §4.7.11 consumer procedure in `podium sync`
// (§7.5 Author attestation, §13.11.3), in a marketplace render (§7.8), and in
// `podium verify <artifact>` (§4.7.9), through the compiled binary: exit
// statuses, the rejected lines, the kept output, and the version 2 lock.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/internal/testharness"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// consumerRuleE2E is a consumer policy rule of ids under mode with one
// signer identity of the github instance.
func consumerRuleE2E(mode, identity string, ids ...string) string {
	b, _ := json.Marshal(ids)
	return `{"ids":` + string(b) + `,"mode":"` + mode + `","requirements":[{"signers":[{"issuer":"` + sh.DefaultIssuer +
		`","identity":"` + identity + `","instance":"github"}]}]}`
}

// consumerPolicyE2E writes a consumer policy file with the github instance
// of the harness's root beside it, and returns its path.
func consumerPolicyE2E(t *testing.T, h *sh.Harness, rules ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeE2EFile(t, dir, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	return writeE2EFile(t, dir, "policy.json", []byte(`{"version":1,"instances":[{"name":"github","trusted_root_file":"github.json","transparency_log":"none"}],"rules":[`+
		strings.Join(rules, ",")+`]}`))
}

// e2eLock is the part of a sync lock the tests read.
type e2eLock struct {
	Version   int `yaml:"version"`
	Artifacts []struct {
		ID               string `yaml:"id"`
		MaterializedPath string `yaml:"materialized_path"`
		PackageDigest    string `yaml:"package_digest"`
		Attestations     []struct {
			Digest string `yaml:"digest"`
			Signer struct {
				Instance string `yaml:"instance"`
				Issuer   string `yaml:"issuer"`
				Identity string `yaml:"identity"`
			} `yaml:"signer"`
		} `yaml:"attestations"`
	} `yaml:"artifacts"`
}

// readE2ELock reads the lock of target.
func readE2ELock(t *testing.T, target string) e2eLock {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(target, ".podium", "sync.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var l e2eLock
	if err := yaml.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// Spec: §7.5 (Author attestation), §7.5.3 (Attestation members), §4.7.9 —
// against a registry that verified a signed layer: a server-source sync
// under require writes the attested records and a version 2 lock with the
// package digest and the signer it verified; a policy whose rule for one ID
// trusts another signer refuses that record, keeps its previous output, and
// exits 1; under warn the record is written and the sync exits 0; a policy
// that does not load exits 2, --check included; and `podium verify` reports
// the attestation check apart from the delivery signature.
func TestSyncAttestation_ServerSource(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	home := t.TempDir()
	root := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": agentArtifact("pay"),
		"finance/close/books/ARTIFACT.md":    agentArtifact("books"),
	})
	signE2ELayer(t, h, root, time.Minute)
	rootFile := writeE2EFile(t, home, "github.json", h.TrustedRootJSON(sh.GitHubStyleRoot()...))
	cfgPath := writeE2EFile(t, home, "registry.yaml", []byte("registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: "+rootFile+"\n"))
	srv := startServerArgs(t, []string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"}, "serve", "--standalone")
	env := brEnv(srv.BaseURL)
	cliWantExit(t, runPodium(t, "", env, "layer", "register", "--id", "team", "--local", root, "--trust", "require",
		"--trust-signer", sh.DefaultIssuer+"="+sh.DefaultSAN, "--trust-instance", "github"), 0, "register")
	cliWantExit(t, runPodium(t, "", env, "layer", "reingest", "team"), 0, "reingest")

	clientHome := t.TempDir()
	target := t.TempDir()
	sync := func(extra ...string) cliResult {
		t.Helper()
		e := append([]string{"HOME=" + clientHome, "USERPROFILE=" + clientHome}, env...)
		return runPodium(t, "", append(e, extra...), "sync", "--target", target, "--harness", "none")
	}
	trusted := consumerPolicyE2E(t, h, consumerRuleE2E("require", sh.DefaultSAN, "finance/**"))
	res := sync("PODIUM_ATTESTATION_POLICY_FILE=" + trusted)
	cliWantExit(t, res, 0, "sync under require")
	lock := readE2ELock(t, target)
	if lock.Version != 2 || len(lock.Artifacts) == 0 {
		t.Fatalf("lock = %+v", lock)
	}
	for _, a := range lock.Artifacts {
		if !strings.HasPrefix(a.PackageDigest, "sha256:") || len(a.Attestations) != 1 || a.Attestations[0].Signer.Identity != sh.DefaultSAN ||
			a.Attestations[0].Signer.Instance != "github" {
			t.Fatalf("lock entry = %+v", a)
		}
	}
	books := filepath.Join(target, "finance", "close", "books", "ARTIFACT.md")
	if _, err := os.Stat(books); err != nil {
		t.Fatal(err)
	}

	split := consumerPolicyE2E(t, h, consumerRuleE2E("require", sh.DefaultSAN, "finance/ap/**"),
		consumerRuleE2E("require", "bob@acme.com", "finance/close/**"))
	res = sync("PODIUM_ATTESTATION_POLICY_FILE=" + split)
	cliWantExit(t, res, 1, "sync with an untrusted signer for one ID")
	cliContains(t, res.Stderr, "rejected: finance/close/books (materialize.attestation_untrusted)", "rejected line")
	cliContains(t, res.Stderr, "the previous sync output was kept", "kept output")
	if _, err := os.Stat(books); err != nil {
		t.Fatalf("the refused record's previous output: %v", err)
	}

	warn := consumerPolicyE2E(t, h, consumerRuleE2E("require", sh.DefaultSAN, "finance/ap/**"),
		consumerRuleE2E("warn", "bob@acme.com", "finance/close/**"))
	res = sync("PODIUM_ATTESTATION_POLICY_FILE=" + warn)
	cliWantExit(t, res, 0, "sync under warn")
	cliContains(t, res.Stderr, "warning: finance/close/books: materialize.attestation_untrusted (constraint signer)", "warning")

	res = sync("PODIUM_ATTESTATION_POLICY=strict")
	cliWantExit(t, res, 2, "an invalid mode")
	cliContains(t, res.Stderr, "config.attestation_policy_unavailable", "policy refusal")
	res = runPodium(t, t.TempDir(), []string{"HOME=" + clientHome, "PODIUM_ATTESTATION_POLICY=strict"}, "sync", "--check")
	cliWantExit(t, res, 2, "--check with an invalid mode")

	verify := func(policy string) cliResult {
		t.Helper()
		return runPodium(t, "", []string{"HOME=" + clientHome, "PODIUM_REGISTRY=" + srv.BaseURL,
			"PODIUM_SIGN_KEY_PATH=" + filepath.Join(home, ".podium", "standalone", "registry-signing.key"),
			"PODIUM_ATTESTATION_POLICY_FILE=" + policy}, "verify", "finance/ap/pay-invoice")
	}
	res = verify(trusted)
	cliWantExit(t, res, 0, "verify under a trusted policy")
	cliContains(t, res.Stdout, "request binding: ok", "binding")
	cliContains(t, res.Stdout, "attestation: ok", "attestation result")
	cliContains(t, res.Stdout, "package_digest: sha256:", "package digest")
	cliContains(t, res.Stdout, `identity="`+sh.DefaultSAN+`"`, "signer")
	cliContains(t, res.Stderr, "verify ok", "delivery result")
	res = verify(consumerPolicyE2E(t, h, consumerRuleE2E("require", "bob@acme.com", "finance/**")))
	cliWantExit(t, res, 1, "verify under an untrusted signer")
	cliContains(t, res.Stdout, "attestation: refused: materialize.attestation_untrusted", "refusal")
	res = runPodium(t, "", []string{"HOME=" + clientHome, "PODIUM_REGISTRY=" + srv.BaseURL}, "verify", "GIT~1")
	cliWantExit(t, res, 1, "verify of an ill-formed ID")
	cliContains(t, res.Stderr, "materialize.request_mismatch (id_form)", "id_form")
	res = runPodium(t, "", []string{"HOME=" + clientHome, "PODIUM_REGISTRY=" + srv.BaseURL, "PODIUM_ATTESTATION_POLICY=strict"}, "verify", "finance/ap/pay-invoice")
	cliWantExit(t, res, 2, "verify with a policy that does not load")
}

// Spec: §4.7.11 (Request binding), §7.5 — a server-source sync against a
// registry that answers one manifest entry with the record of another ID
// refuses that record under no policy, writes the other, and exits 1.
func TestSyncAttestation_ServerSubstitution(t *testing.T) {
	t.Parallel()
	record := func(id string) map[string]any {
		return testharness.SealDelivery(map[string]any{
			"id": id, "version": "1.0.0", "type": "agent", "layer": "team",
			"frontmatter": agentArtifact(id), "manifest_body": id + " body.\n",
		})
	}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sync/manifest":
			_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": []map[string]string{
				{"id": "finance/close-books", "type": "agent", "version": "1.0.0", "layer": "team"},
				{"id": "personal/x", "type": "agent", "version": "1.0.0", "layer": "team"},
			}})
		case "/v1/load_artifact":
			// Both requests are answered with the record of personal/x.
			_ = json.NewEncoder(w).Encode(record("personal/x"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(stub.Close)
	target := t.TempDir()
	res := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_VERIFY_SIGNATURES=never"},
		"sync", "--registry", stub.URL, "--target", target, "--harness", "none")
	cliWantExit(t, res, 1, "a substituted record")
	cliContains(t, res.Stderr, "rejected: finance/close-books (materialize.request_mismatch)", "binding refusal")
	if _, err := os.Stat(filepath.Join(target, "finance")); !os.IsNotExist(err) {
		t.Fatalf("the substituted record was written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "personal", "x", "ARTIFACT.md")); err != nil {
		t.Fatalf("the answering record was not written: %v", err)
	}
}

// Spec: §13.11.3, §7.5 (Author attestation) — a filesystem-source sync
// refuses a layer signed under another root until layer_roots in the user's
// sync.yaml names it, and refuses an attestation directory over
// PODIUM_ATTESTATION_MAX_BUNDLES with limits.
func TestSyncAttestation_FilesystemSource(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := filepath.Join(t.TempDir(), "team")
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: "finance/ap/pay/ARTIFACT.md", Content: agentArtifact("pay")},
		testharness.WriteTreeOption{Path: "finance/close/books/ARTIFACT.md", Content: agentArtifact("books")},
	)
	signE2ELayerAs(t, h, reg, ".podium/layer/agents")
	home := t.TempDir()
	policy := consumerPolicyE2E(t, h, consumerRuleE2E("require", sh.DefaultSAN, "finance/**"))
	sync := func(extra ...string) cliResult {
		t.Helper()
		env := append([]string{"HOME=" + home, "USERPROFILE=" + home, "PODIUM_ATTESTATION_POLICY_FILE=" + policy}, extra...)
		return runPodium(t, "", env, "sync", "--registry", reg, "--target", t.TempDir(), "--harness", "none")
	}
	res := sync()
	cliWantExit(t, res, 1, "another root without layer_roots")
	cliContains(t, res.Stderr, "materialize.attestation_mismatch (layer_digest)", "layer digest refusal")
	writeE2EFile(t, mkdirE2E(t, filepath.Join(home, ".podium")), "sync.yaml", []byte("defaults:\n  attestation:\n    layer_roots:\n      team: agents\n"))
	cliWantExit(t, sync(), 0, "with layer_roots")
	signE2ELayerAs(t, h, reg, ".podium/layer/agents")
	res = sync("PODIUM_ATTESTATION_MAX_BUNDLES=1")
	cliWantExit(t, res, 1, "over the bundle limit")
	cliContains(t, res.Stderr, "materialize.attestation_invalid (limits)", "limits")
}

// Spec: §7.8 (Author attestation, Plugin names) — a marketplace render under
// a policy that the environment names refuses an unattested record beside a
// committed sync.yaml whose policy holds a none rule for every ID: no file is
// written, publish does not run, and the run exits 1; a signed layer
// renders and publishes; and a plugin named con is refused with
// config.invalid.
func TestSyncAttestation_MarketplaceRender(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := filepath.Join(t.TempDir(), "team")
	testharness.WriteTree(t, reg, testharness.WriteTreeOption{Path: "finance/ap/pay/ARTIFACT.md", Content: agentArtifact("pay")})
	ws := t.TempDir()
	workdir := filepath.Join(ws, "out")
	marker := filepath.Join(ws, "published")
	podiumDir := mkdirE2E(t, filepath.Join(ws, ".podium"))
	writeE2EFile(t, podiumDir, "none.json", []byte(`{"version":1,"rules":[{"ids":["**"],"mode":"none"}]}`))
	config := func(plugin string) string {
		return writeE2EFile(t, podiumDir, "sync.yaml", []byte("defaults:\n  registry: "+reg+"\n  attestation:\n    policy_file: none.json\n"+
			"targets:\n  - id: m\n    kind: marketplace\n    target: "+workdir+"\n    harnesses: [claude-code]\n"+
			"    git:\n      remote: git@example.com:acme/agents.git\n      branch: main\n"+
			"    plugins:\n      - name: "+plugin+"\n        include: [\"finance/**\"]\n"+
			"    workflow:\n      publish:\n        - run: [\"touch\", \""+marker+"\"]\n"))
	}
	env := []string{"HOME=" + t.TempDir(), "PODIUM_ATTESTATION_POLICY=require",
		"PODIUM_ATTESTATION_POLICY_FILE=" + consumerPolicyE2E(t, h, consumerRuleE2E("require", sh.DefaultSAN, "finance/**"))}
	res := runPodium(t, ws, env, "sync", "--config", config("pack"))
	cliWantExit(t, res, 1, "an unattested record")
	cliContains(t, res.Stderr, "finance/ap/pay refused: materialize.attestation_missing", "refusal")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("publish ran: %v", err)
	}
	if entries, _ := os.ReadDir(workdir); len(entries) != 0 {
		t.Fatalf("the failed render wrote %v", entries)
	}
	signE2ELayerAs(t, h, reg, ".podium/layer")
	cliWantExit(t, runPodium(t, ws, env, "sync", "--config", config("pack")), 0, "a signed layer")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("publish did not run: %v", err)
	}
	res = runPodium(t, ws, env, "sync", "--config", config("con"))
	cliWantExit(t, res, 2, "a plugin named con")
	cliContains(t, res.Stderr, "config.invalid", "plugin name refusal")
}

// signE2ELayerAs is signE2ELayer with the layer subject named layerName.
func signE2ELayerAs(t *testing.T, h *sh.Harness, dir, layerName string) {
	t.Helper()
	signE2ELayerNamed(t, h, dir, layerName, time.Minute)
}

// mkdirE2E creates dir and returns it.
func mkdirE2E(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
