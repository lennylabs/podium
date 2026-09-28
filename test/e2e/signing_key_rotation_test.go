package e2e

// End-to-end tests for the §4.7.9 registry signing-key rotation through the
// compiled binaries: podium admin signing-key generate|rotate writes the key
// file, the registry signs under the private: key and admits a stored row any
// key of the verification key set verifies, podium-mcp resolves a
// comma-separated key set, and sign-stored-rows (§13.4) re-signs the rows a
// verification-only key signed so that key can be retired.
//
// Spec: §4.7.9, §13.4, §11.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// rotationEnv is the registry configuration every process in one rotation
// case shares: a SQLite store, a filesystem object store, and the key file,
// all under home. The key file sits in the store's directory, which is the
// co-located standalone layout.
func rotationEnv(home, keyPath string) []string {
	return []string{
		"HOME=" + home,
		"PODIUM_REGISTRY_STORE=sqlite",
		"PODIUM_SQLITE_PATH=" + filepath.Join(home, "podium.db"),
		"PODIUM_FILESYSTEM_ROOT=" + filepath.Join(home, "objects"),
		"PODIUM_SIGN=registry-key",
		"PODIUM_SIGN_KEY_PATH=" + keyPath,
	}
}

// signingKeyTool runs `podium admin signing-key <args>` under env.
func signingKeyTool(t *testing.T, env []string, args ...string) cliResult {
	t.Helper()
	return runPodium(t, "", env, append([]string{"admin", "signing-key"}, args...)...)
}

// generateKeyFile runs `signing-key generate` for path and fails the test
// unless it exits 0.
func generateKeyFile(t *testing.T, path string) cliResult {
	t.Helper()
	res := signingKeyTool(t, nil, "generate", "--key-file", path)
	if res.Exit != 0 {
		t.Fatalf("signing-key generate exited %d\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
	}
	return res
}

// firstLine returns the key tool's first stdout line: the verification key set
// as comma-separated base64, signing key first.
func firstLine(out string) string {
	line, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(line)
}

// readKeyFile reads path through the key-file codec.
func readKeyFile(t *testing.T, path string) sign.KeyFile {
	t.Helper()
	kf, err := sign.ReadKeyFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	return kf
}

func b64(k ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(k) }

// servedKeyID fetches the artifact straight from the registry and returns the
// key_id its delivery_signature envelope names.
func servedKeyID(t *testing.T, baseURL, id string) string {
	t.Helper()
	var resp struct {
		DeliverySignature string `json:"delivery_signature"`
	}
	getJSON(t, baseURL+"/v1/load_artifact?id="+id, &resp)
	var env struct {
		KeyID string `json:"key_id"`
	}
	if err := json.Unmarshal([]byte(resp.DeliverySignature), &env); err != nil {
		t.Fatalf("decode delivery signature %q: %v", resp.DeliverySignature, err)
	}
	return env.KeyID
}

// signStoredRows runs `podium admin sign-stored-rows <args>` under env.
func signStoredRows(t *testing.T, env []string, args ...string) cliResult {
	t.Helper()
	return runPodium(t, "", env, append([]string{"admin", "sign-stored-rows"}, args...)...)
}

// planDigestOf returns the digest on the dry-run: plan digest line that ends a
// sign-stored-rows dry run's stdout, and fails the test when it is absent.
func planDigestOf(t *testing.T, stdout string) string {
	t.Helper()
	const prefix = "dry-run: plan digest "
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, prefix) {
		t.Fatalf("dry-run stdout does not end with a plan digest line:\n%s", stdout)
	}
	return strings.Fields(strings.TrimPrefix(last, prefix))[0]
}

// verifyKeyLine is the per-key summary line sign-stored-rows prints.
func verifyKeyLine(prefix, keyID string, n int) string {
	return fmt.Sprintf("%s: verify key %s: %d row(s) still signed under it", prefix, keyID, n)
}

// Spec: §4.7.9, §13.4, §11 — the rotation journey. After generate and an
// ingest, rotate prints the new set with the new key first and keeps the old
// public key on one verify: line. A restarted registry serves the
// pre-rotation row with a delivery signature under the new key_id, so a
// consumer holding the printed set loads it and a consumer holding only the
// retired key is refused. sign-stored-rows re-signs the row under the new
// key, which is what lets the verify: line go: after its removal and a
// restart the load still succeeds.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigningKeyRotation_Journey(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	keyPath := filepath.Join(home, "registry-signing.key")
	env := rotationEnv(home, keyPath)
	reg := rehashRegistry(t)

	gen := generateKeyFile(t, keyPath)
	oldKey := firstLine(gen.Stdout)
	original := readKeyFile(t, keyPath)
	if oldKey != b64(original.Public) {
		t.Fatalf("generate printed %q, want the file's public key %q", oldKey, b64(original.Public))
	}
	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	stopProc(first.cmd)

	rot := signingKeyTool(t, nil, "rotate", "--key-file", keyPath)
	if rot.Exit != 0 {
		t.Fatalf("signing-key rotate exited %d\nstderr:\n%s", rot.Exit, rot.Stderr)
	}
	rotated := readKeyFile(t, keyPath)
	newKey := b64(rotated.Public)
	if got, want := firstLine(rot.Stdout), newKey+","+oldKey; got != want {
		t.Fatalf("rotate first line = %q, want %q", got, want)
	}
	if n := strings.Count(readFile(t, keyPath), "verify:"); n != 1 {
		t.Fatalf("rotated key file carries %d verify: line(s), want 1", n)
	}
	newID, oldID := sign.KeyIDFor(rotated.Public), sign.KeyIDFor(original.Public)

	// The restart ingests nothing, so the served row is the one the
	// pre-rotation start stored and signed under the old key.
	second := startServerArgs(t, env, "serve", "--standalone")
	if got := servedKeyID(t, second.BaseURL, rehashSkillID); got != newID {
		t.Errorf("served delivery signature names key_id %q, want the new key's %q", got, newID)
	}
	if errStr, res := bridgeLoad(t, second.BaseURL, rehashSkillID, "PODIUM_SIGNATURE_VERIFY_KEY="+firstLine(rot.Stdout)); errStr != "" {
		t.Fatalf("load under the rotated set = %q, want success\nstderr: %s", errStr, res.Stderr)
	}
	if errStr, res := bridgeLoad(t, second.BaseURL, rehashSkillID, "PODIUM_SIGNATURE_VERIFY_KEY="+oldKey); !strings.HasPrefix(errStr, "materialize.signature_invalid") {
		t.Errorf("load under the retired key alone = %q, want materialize.signature_invalid\nstderr: %s", errStr, res.Stderr)
	}

	// The record is present, so the command runs beside the serving
	// registry: every write is a compare-and-swap.
	// The dry run lists the row as signed under the retired key and planned
	// for a signing write.
	dry := signStoredRows(t, env, "--dry-run")
	if dry.Exit != 0 || !strings.Contains(dry.Stdout, "sign=true signed_by="+oldID) {
		t.Fatalf("dry run exit=%d, want 0 and a signing write for a row under %s\nstdout:\n%s\nstderr:\n%s", dry.Exit, oldID, dry.Stdout, dry.Stderr)
	}
	run := signStoredRows(t, env, "--plan-digest="+planDigestOf(t, dry.Stdout))
	if run.Exit != 0 || !strings.Contains(run.Stdout, verifyKeyLine("rehash", oldID, 0)) {
		t.Fatalf("sign-stored-rows exit=%d, want 0 and no row left under %s\nstdout:\n%s\nstderr:\n%s", run.Exit, oldID, run.Stdout, run.Stderr)
	}
	stopProc(second.cmd)

	rotated.Verify = nil
	if err := sign.WriteKeyFile(keyPath, rotated); err != nil {
		t.Fatalf("drop the verify: line: %v", err)
	}
	third := startServerArgs(t, env, "serve", "--standalone")
	if errStr, res := bridgeLoad(t, third.BaseURL, rehashSkillID, "PODIUM_SIGNATURE_VERIFY_KEY="+newKey); errStr != "" {
		t.Fatalf("load after the old key was retired = %q, want success\nstderr: %s\nlog:\n%s", errStr, res.Stderr, third.log())
	}
}

// Spec: §4.7.9 — generate refuses an existing file, because the key it holds
// may have signed stored rows: it exits non-zero and leaves the file
// byte-identical.
func TestSigningKeyTool_GenerateRefusesAnExistingFile(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "registry-signing.key")
	generateKeyFile(t, keyPath)
	before := readFileBytes(t, keyPath)
	res := signingKeyTool(t, nil, "generate", "--key-file", keyPath)
	if res.Exit == 0 {
		t.Fatalf("a second generate exited 0\nstdout:\n%s", res.Stdout)
	}
	if after := readFileBytes(t, keyPath); string(after) != string(before) {
		t.Error("a refused generate changed the key file")
	}
}

// Spec: §4.7.9 — rotate takes --key-file only. Without it the tool exits 2
// and writes nothing, neither at the default path under HOME nor at the path
// PODIUM_SIGN_KEY_PATH names, so a bare invocation cannot rotate an operator's
// personal key.
func TestSigningKeyTool_RotateRequiresKeyFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	envPath := filepath.Join(t.TempDir(), "env-signing.key")
	res := signingKeyTool(t, []string{"HOME=" + home, "PODIUM_SIGN_KEY_PATH=" + envPath}, "rotate")
	if res.Exit != 2 {
		t.Fatalf("rotate without --key-file exited %d, want 2\nstderr:\n%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--key-file") {
		t.Errorf("stderr does not name --key-file:\n%s", res.Stderr)
	}
	mustNotExist(t, envPath)
	var created []string
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			created = append(created, path)
		}
		return nil
	})
	if len(created) > 0 {
		t.Errorf("rotate without --key-file created files under HOME: %v", created)
	}
}

// Spec: §4.7.9 — a second rotation carries every previous key forward, the
// first rotation's signing key followed by the original key. --staged-out
// writes the previous file plus a verify: line for the new key, with mode
// 0600, which a multi-replica roll deploys before the rotated file.
func TestSigningKeyTool_RotateCarriesKeysForward(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "registry-signing.key")
	generateKeyFile(t, keyPath)
	original := readKeyFile(t, keyPath)
	if res := signingKeyTool(t, nil, "rotate", "--key-file", keyPath); res.Exit != 0 {
		t.Fatalf("first rotate exited %d\nstderr:\n%s", res.Exit, res.Stderr)
	}
	firstRotation := readKeyFile(t, keyPath)
	if res := signingKeyTool(t, nil, "rotate", "--key-file", keyPath); res.Exit != 0 {
		t.Fatalf("second rotate exited %d\nstderr:\n%s", res.Exit, res.Stderr)
	}
	second := readKeyFile(t, keyPath)
	assertKeys(t, "second rotation verify:", second.Verify, firstRotation.Public, original.Public)

	stagedPath := filepath.Join(dir, "staged.key")
	previous := readFileBytes(t, keyPath)
	if res := signingKeyTool(t, nil, "rotate", "--key-file", keyPath, "--staged-out", stagedPath); res.Exit != 0 {
		t.Fatalf("staged rotate exited %d\nstderr:\n%s", res.Exit, res.Stderr)
	}
	third := readKeyFile(t, keyPath)
	staged := readKeyFile(t, stagedPath)
	if !staged.Private.Equal(second.Private) || !staged.Public.Equal(second.Public) {
		t.Error("the staged file does not keep the previous private: and public: lines")
	}
	assertKeys(t, "staged verify:", staged.Verify, firstRotation.Public, original.Public, third.Public)
	if !strings.HasPrefix(readFile(t, stagedPath), strings.TrimRight(string(previous), "\n")) {
		t.Errorf("the staged file does not begin with the previous file's lines:\nprevious:\n%s\nstaged:\n%s", previous, readFile(t, stagedPath))
	}
	info, err := os.Stat(stagedPath)
	if err != nil {
		t.Fatalf("stat staged file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("staged file mode = %o, want 600", mode)
	}
}

// Spec: §4.7.9 — rotate on an absent file exits 1 and creates neither the key
// file nor the staged file.
func TestSigningKeyTool_RotateRefusesAnAbsentFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "absent.key")
	stagedPath := filepath.Join(dir, "staged.key")
	res := signingKeyTool(t, nil, "rotate", "--key-file", keyPath, "--staged-out", stagedPath)
	if res.Exit != 1 {
		t.Fatalf("rotate on an absent file exited %d, want 1\nstderr:\n%s", res.Exit, res.Stderr)
	}
	mustNotExist(t, keyPath)
	mustNotExist(t, stagedPath)
}

// assertKeys fails unless got holds want in order.
func assertKeys(t *testing.T, label string, got []ed25519.PublicKey, want ...ed25519.PublicKey) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s holds %d key(s), want %d", label, len(got), len(want))
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("%s key %d = %s, want %s", label, i, b64(got[i]), b64(want[i]))
		}
	}
}

// runServerBin runs the compiled podium-server binary under env.
func runServerBin(t *testing.T, env []string, args ...string) cliResult {
	t.Helper()
	return runBin(t, cmdharness.Bin(t, "podium-server"), "", env, nil, 90*time.Second, args...)
}

// Spec: §13.4, §13.12 — podium-server sign-stored-rows with signing off runs
// the rewrite without a signer and refuses --include-unsigned with
// config.signature_provider_unavailable. With signing on it refuses with that
// code when the key file is absent. It exits without serving and generates no
// key. With a key file it exits 0 and prints the summary.
// Matrix: §6.10 (config.signature_provider_unavailable)
func TestSignStoredRows_PodiumServerEntry(t *testing.T) {
	t.Parallel()
	t.Run("signing off", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		keyPath := filepath.Join(home, "registry-signing.key")
		env := append(rotationEnv(home, keyPath), "PODIUM_SIGN=none")
		res := runServerBin(t, env, "sign-stored-rows")
		if res.Exit != 0 || !strings.Contains(res.Stdout, "rehash: ") {
			t.Fatalf("exit=%d, want 0 and a rehash: summary line\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
		}
		if listeningAddr.MatchString(res.Stdout + res.Stderr) {
			t.Errorf("the command bound a listener:\n%s%s", res.Stdout, res.Stderr)
		}
		mustNotExist(t, keyPath)
		mustNotExist(t, homeKeyFile(home))
	})
	t.Run("signing off with include-unsigned", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		keyPath := filepath.Join(home, "registry-signing.key")
		env := append(rotationEnv(home, keyPath), "PODIUM_SIGN=none")
		res := runServerBin(t, env, "sign-stored-rows", "--include-unsigned", "--dry-run")
		if res.Exit != 1 || !strings.Contains(res.Stderr, "config.signature_provider_unavailable") || !strings.Contains(res.Stderr, "--include-unsigned") {
			t.Fatalf("exit=%d stderr=%q, want exit 1 naming config.signature_provider_unavailable and --include-unsigned", res.Exit, res.Stderr)
		}
		if listeningAddr.MatchString(res.Stdout + res.Stderr) {
			t.Errorf("the command bound a listener:\n%s%s", res.Stdout, res.Stderr)
		}
		mustNotExist(t, keyPath)
		mustNotExist(t, homeKeyFile(home))
	})
	t.Run("absent key file", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		keyPath := filepath.Join(home, "absent-signing.key")
		res := runServerBin(t, rotationEnv(home, keyPath), "sign-stored-rows")
		if res.Exit != 1 || !strings.Contains(res.Stderr, "config.signature_provider_unavailable") || !strings.Contains(res.Stderr, keyPath) {
			t.Fatalf("exit=%d stderr=%q, want exit 1 naming config.signature_provider_unavailable and %s", res.Exit, res.Stderr, keyPath)
		}
		mustNotExist(t, keyPath)
	})
	t.Run("key file present", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		keyPath := filepath.Join(home, "registry-signing.key")
		env := rotationEnv(home, keyPath)
		generateKeyFile(t, keyPath)
		srv := startServerArgs(t, env, "serve", "--standalone", "--layer-path", rehashRegistry(t))
		stopProc(srv.cmd)
		res := runServerBin(t, env, "sign-stored-rows")
		if res.Exit != 0 {
			t.Fatalf("exit=%d, want 0\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "rehash: 0 unsigned left") {
			t.Errorf("stdout carries no summary:\n%s", res.Stdout)
		}
	})
}

// Spec: §13.4 — an unknown flag, a positional argument, --include-unsigned
// alone, --plan-digest with --dry-run, and a malformed digest are usage errors
// on both binaries: each exits 2 with the usage text on stderr.
func TestSignStoredRows_UsageErrors(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := rotationEnv(home, filepath.Join(home, "registry-signing.key"))
	zeros := "--plan-digest=sha256:" + strings.Repeat("0", 64)
	for name, res := range map[string]cliResult{
		"podium-server --bogus":                  runServerBin(t, env, "sign-stored-rows", "--bogus"),
		"podium admin extra-arg":                 signStoredRows(t, env, "extra-arg"),
		"podium-server --include-unsigned":       runServerBin(t, env, "sign-stored-rows", "--include-unsigned"),
		"podium admin --dry-run --plan-digest":   signStoredRows(t, env, "--dry-run", zeros),
		"podium-server --plan-digest=sha256:XYZ": runServerBin(t, env, "sign-stored-rows", "--plan-digest=sha256:XYZ"),
	} {
		if res.Exit != 2 || !strings.Contains(res.Stderr, "usage: sign-stored-rows") {
			t.Errorf("%s: exit=%d stderr=%q, want exit 2 with the usage text", name, res.Exit, res.Stderr)
		}
	}
}
