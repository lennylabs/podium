package e2e

// Startup refusals for §8.6 local chain-head anchoring, and the negative
// controls that pin where each refusal stops applying.
//
// With PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS above 0, the registry refuses to
// start when the audit log file cannot be opened
// (config.audit_sink_unavailable), when the anchor key cannot be read, parsed,
// or generated (config.audit_anchor_key_unavailable), and, while registry
// signing is on, when the registry signing key file carries the anchor public
// key as its public: key or as a verify: key (config.audit_anchor_key_shared).
// Every case owns its t.TempDir for the audit log and the key files, so no
// case observes another's files.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// anchorIntervalOn enables anchoring for a case. The value is long enough
// that no periodic tick fires during a test.
const anchorIntervalOn = "PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60"

// anchorGenKey generates an Ed25519 keypair for a key-file fixture.
func anchorGenKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

// anchorWriteKey writes kf to path in the registry key-file format.
func anchorWriteKey(t *testing.T, path string, kf sign.KeyFile) {
	t.Helper()
	if err := sign.WriteKeyFile(path, kf); err != nil {
		t.Fatalf("write key file %s: %v", path, err)
	}
}

// anchorExpectRefusal runs a standalone start that must exit non-zero with
// wantCode, asserts the listener never bound, and returns the output.
func anchorExpectRefusal(t *testing.T, wantCode string, env ...string) string {
	t.Helper()
	out := gwExpectStartupFailure(t, wantCode, env...)
	if strings.Contains(out, "listening on") {
		t.Errorf("refused start bound a listener:\n%s", out)
	}
	return out
}

// anchorAssertNoAnchorEvent asserts the audit log at path is absent or
// records no audit.anchored event.
func anchorAssertNoAnchorEvent(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read audit log %s: %v", path, err)
	}
	if strings.Contains(string(body), "audit.anchored") {
		t.Errorf("refused start recorded an audit.anchored event:\n%s", body)
	}
}

// anchorStartArgs is the standalone serve invocation every case boots.
func anchorStartArgs(t *testing.T) []string {
	t.Helper()
	return []string{"serve", "--standalone", "--layer-path", writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": contextArtifact("seed"),
	})}
}

// Spec: §8.6, §13.12 — with anchoring running and registry signing on, an
// anchor public key equal to the registry public: key or to any verify: key
// refuses startup. The anchor signature carries no purpose label, so a
// verifier trusting the registry key set would accept an anchor signature as
// an artifact signature. The message names the key by key_id and carries no
// private key material.
// Matrix: §6.10 (config.audit_anchor_key_shared)
func TestAuditAnchor_SharedKeyRefusesStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// keys writes the fixture under dir and returns the registry key
		// path, the anchor key path, the shared public key, and every private
		// key the fixture holds.
		keys func(t *testing.T, dir string) (string, string, ed25519.PublicKey, []ed25519.PrivateKey)
	}{
		{
			name: "one file named by both variables",
			keys: func(t *testing.T, dir string) (string, string, ed25519.PublicKey, []ed25519.PrivateKey) {
				pub, priv := anchorGenKey(t)
				path := filepath.Join(dir, "shared.key")
				anchorWriteKey(t, path, sign.KeyFile{Private: priv, Public: pub})
				return path, path, pub, []ed25519.PrivateKey{priv}
			},
		},
		{
			name: "registry verify line equal to the anchor public key",
			keys: func(t *testing.T, dir string) (string, string, ed25519.PublicKey, []ed25519.PrivateKey) {
				regPub, regPriv := anchorGenKey(t)
				anchorPub, anchorPriv := anchorGenKey(t)
				regPath := filepath.Join(dir, "registry-signing.key")
				anchorPath := filepath.Join(dir, "audit.key")
				anchorWriteKey(t, regPath, sign.KeyFile{Private: regPriv, Public: regPub, Verify: []ed25519.PublicKey{anchorPub}})
				anchorWriteKey(t, anchorPath, sign.KeyFile{Private: anchorPriv, Public: anchorPub})
				return regPath, anchorPath, anchorPub, []ed25519.PrivateKey{regPriv, anchorPriv}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			regPath, anchorPath, shared, privs := tc.keys(t, dir)
			auditPath := filepath.Join(dir, "audit.log")
			out := anchorExpectRefusal(t, "config.audit_anchor_key_shared",
				"PODIUM_SIGN=",
				"PODIUM_SIGN_KEY_PATH="+regPath,
				"PODIUM_AUDIT_SIGNING_KEY_PATH="+anchorPath,
				"PODIUM_AUDIT_LOG_PATH="+auditPath,
				anchorIntervalOn,
			)
			if id := sign.KeyIDFor(shared); !strings.Contains(out, id) {
				t.Errorf("refusal does not name the shared key_id %s:\n%s", id, out)
			}
			for _, priv := range privs {
				if strings.Contains(out, base64.StdEncoding.EncodeToString(priv)) {
					t.Errorf("refusal output carries a base64 private key:\n%s", out)
				}
			}
			anchorAssertNoAnchorEvent(t, auditPath)
		})
	}
}

// Spec: §8.6, §13.12 — with anchoring running, an anchor key file that the
// registry cannot use to sign refuses startup. A public-only file parses but
// carries no private: line, and the registry never overwrites it with a
// generated key.
// Matrix: §6.10 (config.audit_anchor_key_unavailable)
func TestAuditAnchor_UnusableKeyRefusesStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pub, _ := anchorGenKey(t)
	keyPath := filepath.Join(dir, "audit.key")
	anchorWriteKey(t, keyPath, sign.KeyFile{Public: pub})
	auditPath := filepath.Join(dir, "audit.log")
	out := anchorExpectRefusal(t, "config.audit_anchor_key_unavailable",
		"PODIUM_AUDIT_SIGNING_KEY_PATH="+keyPath,
		"PODIUM_AUDIT_LOG_PATH="+auditPath,
		anchorIntervalOn,
	)
	if !strings.Contains(out, keyPath) {
		t.Errorf("refusal does not name %s:\n%s", keyPath, out)
	}
	anchorAssertNoAnchorEvent(t, auditPath)
}

// Spec: §8.6, §13.12 — with anchoring running, a PODIUM_AUDIT_LOG_PATH file
// sink that cannot be opened refuses startup, and the sink check precedes the
// anchor key load, so the refused start generates no anchor key.
// Matrix: §6.10 (config.audit_sink_unavailable)
func TestAuditAnchor_UnopenableSinkRefusesStart(t *testing.T) {
	t.Parallel()
	logDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "audit.key")
	anchorExpectRefusal(t, "config.audit_sink_unavailable",
		"PODIUM_AUDIT_LOG_PATH="+logDir,
		"PODIUM_AUDIT_SIGNING_KEY_PATH="+keyPath,
		anchorIntervalOn,
	)
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("refused start touched the absent anchor key %s (stat err %v)", keyPath, err)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("refused start wrote into the log directory: %v", entries)
	}
}

// Spec: §8.6, §13.12 — the shared-key refusal applies only while registry
// signing is on. With PODIUM_SIGN=none the same shared key file starts.
func TestAuditAnchor_SharedKeyStartsWithSigningOff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pub, priv := anchorGenKey(t)
	keyPath := filepath.Join(dir, "shared.key")
	anchorWriteKey(t, keyPath, sign.KeyFile{Private: priv, Public: pub})
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_SIGN=none",
		"PODIUM_SIGN_KEY_PATH=" + keyPath,
		"PODIUM_AUDIT_SIGNING_KEY_PATH=" + keyPath,
		"PODIUM_AUDIT_LOG_PATH=" + filepath.Join(dir, "audit.log"),
		anchorIntervalOn,
	}, anchorStartArgs(t)...)
	resp, err := httpClient.Get(srv.BaseURL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
}

// Spec: §8.6, §13.12 — with the interval at 0, the anchor key is never read,
// so a public-only key file starts and is left byte-identical.
func TestAuditAnchor_PublicOnlyKeyIgnoredWithAnchoringOff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pub, _ := anchorGenKey(t)
	keyPath := filepath.Join(dir, "audit.key")
	anchorWriteKey(t, keyPath, sign.KeyFile{Public: pub})
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_AUDIT_SIGNING_KEY_PATH=" + keyPath,
		"PODIUM_AUDIT_LOG_PATH=" + filepath.Join(dir, "audit.log"),
		"PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=0",
	}, anchorStartArgs(t)...)
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reread key: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("anchor key file changed with anchoring off:\nbefore: %q\nafter:  %q", before, after)
	}
}

// Spec: §8.6, §13.12 — with the interval at 0 and no
// PODIUM_AUDIT_SIGNING_KEY_PATH, a fresh HOME starts without generating the
// default anchor key.
func TestAuditAnchor_NoDefaultKeyWithAnchoringOff(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_AUDIT_SIGNING_KEY_PATH=",
		"PODIUM_AUDIT_LOG_PATH=",
		"PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=0",
	}, anchorStartArgs(t)...)
	keyPath := filepath.Join(home, ".podium", "standalone", "audit.key")
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("start with anchoring off created %s (stat err %v)", keyPath, err)
	}
}

// Spec: §8.6, §13.12 — with the interval at 0, a file sink that cannot be
// opened logs a warning and the registry starts.
func TestAuditAnchor_UnopenableSinkWarnsWithAnchoringOff(t *testing.T) {
	t.Parallel()
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_AUDIT_LOG_PATH=" + t.TempDir(),
		"PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=0",
	}, anchorStartArgs(t)...)
	if !strings.Contains(srv.log(), "warning: audit sink disabled") {
		t.Errorf("start log does not warn that the audit sink is disabled:\n%s", srv.log())
	}
}

// Spec: §8.6, §13.12 — an http(s) audit sink never refuses a start. With
// anchoring on, the endpoint leaves no local chain to anchor, so the registry
// logs that anchoring is disabled and starts.
func TestAuditAnchor_EndpointSinkDisablesAnchoring(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(recorder.Close)
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_AUDIT_LOG_PATH=" + recorder.URL,
		"PODIUM_AUDIT_SIGNING_KEY_PATH=" + filepath.Join(t.TempDir(), "audit.key"),
		anchorIntervalOn,
	}, anchorStartArgs(t)...)
	if !strings.Contains(srv.log(), "audit anchor disabled (no sink)") {
		t.Errorf("start log does not report anchoring disabled:\n%s", srv.log())
	}
}
