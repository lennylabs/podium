package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
	"github.com/lennylabs/podium/pkg/version"
)

// seamRegistry serves an unattested agent record for every load and counts
// the loads.
func seamRegistry(t *testing.T, id string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	const fm = "---\ntype: agent\nversion: 1.0.0\n---\n\nBody.\n"
	hash := "sha256:" + version.CanonicalContentHash([]byte(fm), nil, nil)
	rec := version.DeliveryRecord{ID: id, Version: "1.0.0", Type: "agent", ContentHash: hash,
		ArtifactRevision: epochArtifactRevision, Frontmatter: fm, ManifestBody: "\nBody.\n"}
	body, err := json.Marshal(map[string]any{"id": id, "type": "agent", "version": "1.0.0", "content_hash": hash,
		"artifact_revision": epochArtifactRevision, "frontmatter": fm, "manifest_body": "\nBody.\n",
		"delivery_hash": version.DeliveryHash(rec)})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/load_artifact" {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

// runSeamMCP runs the tagged podium-mcp with args, the environment env, and
// one load_artifact call of id.
func runSeamMCP(t *testing.T, env []string, id string, args ...string) cliResult {
	t.Helper()
	var in bytes.Buffer
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "load_artifact", "arguments": map[string]any{"id": id}}})
	in.Write(append(b, '\n'))
	env = append([]string{"PODIUM_VERIFY_SIGNATURES=never", "PODIUM_CACHE_DIR=" + t.TempDir()}, env...)
	return runBin(t, cmdharness.Bin(t, "podium-mcp"), t.TempDir(), env, in.Bytes(), 45*time.Second, args...)
}

// writeSync writes <dir>/.podium/sync.yaml.
func writeSync(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".podium", "sync.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Spec: §4.7.11 (User home, Test seam) — podium-mcp reads ~/.podium/sync.yaml,
// its defaults.registry included, under the account home the test seam
// names, and ignores the sync.yaml under a HOME, USERPROFILE, and
// XDG_CONFIG_HOME that a project configuration sets.
func TestMCPAttestation_AccountHomeIgnoresHOME(t *testing.T) {
	t.Parallel()
	want, wantHits := seamRegistry(t, "finance/pay")
	other, otherHits := seamRegistry(t, "finance/pay")
	account := t.TempDir()
	writeSync(t, account, "defaults:\n  registry: "+want.URL+"\n")
	planted := t.TempDir()
	writeSync(t, planted, "defaults:\n  registry: "+other.URL+"\n  attestation:\n    overlay: allow\n")
	res := runSeamMCP(t, []string{"PODIUM_REGISTRY=", "HOME=" + planted, "USERPROFILE=" + planted, "XDG_CONFIG_HOME=" + planted, "XDG_CACHE_HOME=" + planted},
		"finance/pay", "--test-account-home", account, "--test-machine-config-dir", filepath.Join(account, "none"))
	cliWantExit(t, res, 0, "podium-mcp load")
	if wantHits.Load() != 1 || otherHits.Load() != 0 {
		t.Fatalf("account-home registry hits %d, planted registry hits %d\nstderr: %s", wantHits.Load(), otherHits.Load(), res.Stderr)
	}
	if got := rpcResult(t, res.Stdout, 1); got["id"] != "finance/pay" || got["attestation_warning"] != nil {
		t.Errorf("result = %v", got)
	}
}

// Spec: §4.7.11 (User home, Machine file) — a failed account lookup and a
// machine file another user can write are named on standard error and in
// the attestation_warning member of the load result, and the load goes on.
// Matrix: §6.10 (config.attestation_policy_unavailable)
func TestMCPAttestation_NoticesOfLookupAndMachineFile(t *testing.T) {
	t.Parallel()
	reg, _ := seamRegistry(t, "finance/pay")
	machine := t.TempDir()
	p := filepath.Join(machine, "sync.yaml")
	if err := os.WriteFile(p, []byte("defaults:\n  attestation:\n    policy: require\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	res := runSeamMCP(t, []string{"PODIUM_REGISTRY=" + reg.URL}, "finance/pay", "--test-account-home=", "--test-machine-config-dir", machine)
	cliWantExit(t, res, 0, "podium-mcp load")
	got := rpcResult(t, res.Stdout, 1)
	w, _ := got["attestation_warning"].(string)
	if !strings.Contains(w, "constraint user_home") {
		t.Errorf("attestation_warning = %q, want user_home", w)
	}
	if !strings.Contains(res.Stderr, "no home directory") {
		t.Errorf("stderr does not name the failed lookup: %s", res.Stderr)
	}
	if !strings.Contains(w, "constraint machine_file") {
		t.Errorf("attestation_warning = %q, want machine_file", w)
	}
	if got["id"] != "finance/pay" {
		t.Errorf("the ignored machine policy refused the load: %v", got)
	}
}

// Spec: §4.7.11 (Machine file, User mode) — a machine file that passes its
// check applies: its default of require refuses an unattested record, which
// no rule of a user-source file covers.
// Matrix: §6.10 (materialize.attestation_untrusted)
func TestMCPAttestation_MachineFileApplies(t *testing.T) {
	t.Parallel()
	reg, _ := seamRegistry(t, "finance/pay")
	machine := t.TempDir()
	if err := os.WriteFile(filepath.Join(machine, "sync.yaml"), []byte("defaults:\n  attestation:\n    policy: require\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	res := runSeamMCP(t, []string{"PODIUM_REGISTRY=" + reg.URL}, "finance/pay", "--test-account-home", home, "--test-machine-config-dir", machine)
	cliWantExit(t, res, 0, "podium-mcp load")
	got := rpcResult(t, res.Stdout, 1)
	d, _ := got["details"].(map[string]any)
	if got["code"] != "materialize.attestation_untrusted" || d["constraint"] != "user_rule" {
		t.Fatalf("result = %v\nstderr: %s", got, res.Stderr)
	}
}

// Spec: §4.7.11 (Policy load) — a policy file that cannot be loaded refuses
// the start with config.attestation_policy_unavailable.
func TestMCPAttestation_StartRefusal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := runSeamMCP(t, []string{"PODIUM_REGISTRY=http://127.0.0.1:1", "PODIUM_ATTESTATION_POLICY_FILE=" + filepath.Join(home, "absent.json")},
		"finance/pay", "--test-account-home", home, "--test-machine-config-dir", filepath.Join(home, "none"))
	cliWantExit(t, res, 2, "podium-mcp with a missing policy file")
	if !strings.Contains(res.Stderr, "config.attestation_policy_unavailable") {
		t.Errorf("stderr = %s", res.Stderr)
	}
}

// Spec: §4.7.11 (Test seam) — a release build of podium-mcp refuses each
// hidden test flag as undefined and exits 2, so no project configuration
// can replace the account home or the machine file of a shipped binary.
func TestMCPAttestation_ReleaseBuildRefusesTestFlags(t *testing.T) {
	t.Parallel()
	bin := cmdharness.ReleaseBin(t, "podium-mcp")
	for _, flag := range []string{"test-account-home", "test-machine-config-dir"} {
		res := runBin(t, bin, t.TempDir(), []string{"PODIUM_REGISTRY=http://127.0.0.1:1"}, nil, 45*time.Second, "--"+flag, t.TempDir())
		cliWantExit(t, res, 2, "release podium-mcp --"+flag)
		if !strings.Contains(res.Stderr, "flag provided but not defined: -"+flag) {
			t.Errorf("stderr = %s", res.Stderr)
		}
	}
}

// Spec: §4.7.11 (Test seam) — a test flag with no value is a usage error of
// the podiumtest build.
func TestMCPAttestation_TestFlagNeedsAValue(t *testing.T) {
	t.Parallel()
	res := runSeamMCP(t, []string{"PODIUM_REGISTRY=http://127.0.0.1:1"}, "finance/pay", "--test-account-home")
	cliWantExit(t, res, 2, "podium-mcp --test-account-home with no value")
	if !strings.Contains(res.Stderr, "flag needs an argument: -test-account-home") {
		t.Errorf("stderr = %s", res.Stderr)
	}
}
