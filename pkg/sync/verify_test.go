package sync_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	podsync "github.com/lennylabs/podium/pkg/sync"
)

// writeVerifySyncFile writes a sync.yaml at path whose defaults block sets
// verify_signatures to value.
func writeVerifySyncFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "defaults:\n  verify_signatures: " + value + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// verifyScopes returns a workspace with a nested start directory and a
// separate home directory, with the paths of the three §7.5.2 scope files.
func verifyScopes(t *testing.T) (start, home, local, shared, global string) {
	t.Helper()
	ws := t.TempDir()
	home = t.TempDir()
	start = filepath.Join(ws, "nested", "deeper")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	return start, home,
		filepath.Join(ws, ".podium", "sync.local.yaml"),
		filepath.Join(ws, ".podium", "sync.yaml"),
		filepath.Join(home, ".podium", "sync.yaml")
}

// Spec: §7.5.2 — defaults.verify_signatures resolves by per-key precedence:
// the workspace's sync.local.yaml, then its sync.yaml, then the home-global
// file, with the workspace found by walking up from the start directory.
func TestResolveVerifyPolicy_ScopePrecedence(t *testing.T) {
	t.Parallel()
	start, home, local, shared, global := verifyScopes(t)

	policy, source, warning, err := podsync.ResolveVerifyPolicy("", start, home)
	if err != nil || policy != sign.PolicyAlways || source != "" || warning != "" {
		t.Fatalf("no scope = %q, %q, %q, %v; want the always default", policy, source, warning, err)
	}

	writeVerifySyncFile(t, global, "never")
	policy, source, _, err = podsync.ResolveVerifyPolicy("", start, home)
	if err != nil || policy != sign.PolicyNever || source != global {
		t.Errorf("home only = %q from %q, %v; want never from %s", policy, source, err, global)
	}

	writeVerifySyncFile(t, shared, "always")
	policy, source, _, err = podsync.ResolveVerifyPolicy("", start, home)
	if err != nil || policy != sign.PolicyAlways || source != shared {
		t.Errorf("shared over home = %q from %q, %v; want always from %s", policy, source, err, shared)
	}

	writeVerifySyncFile(t, local, "never")
	policy, source, _, err = podsync.ResolveVerifyPolicy("", start, home)
	if err != nil || policy != sign.PolicyNever || source != local {
		t.Errorf("local over shared = %q from %q, %v; want never from %s", policy, source, err, local)
	}
}

// Spec: §4.7.9, §7.5.2 — a non-empty environment value outranks every
// sync.yaml scope and logs no warning, and an empty one counts as unset.
func TestResolveVerifyPolicy_EnvironmentValue(t *testing.T) {
	t.Parallel()
	start, home, _, _, global := verifyScopes(t)
	writeVerifySyncFile(t, global, "never")

	policy, source, warning, err := podsync.ResolveVerifyPolicy("always", start, home)
	if err != nil || policy != sign.PolicyAlways || source != "" || warning != "" {
		t.Errorf("env always = %q, %q, %q, %v; want always with no source or warning", policy, source, warning, err)
	}
	_, _, warning, err = podsync.ResolveVerifyPolicy("never", start, home)
	if err != nil || warning != "" {
		t.Errorf("env never = %q, %v; want no warning", warning, err)
	}
	policy, source, _, err = podsync.ResolveVerifyPolicy("", start, home)
	if err != nil || policy != sign.PolicyNever || source != global {
		t.Errorf("empty env = %q from %q, %v; want never from %s", policy, source, err, global)
	}
}

// Spec: §4.7.9 — a never a sync.yaml file supplied returns the stale-never
// warning naming that file and the key to remove.
func TestResolveVerifyPolicy_StaleNeverWarning(t *testing.T) {
	t.Parallel()
	start, home, _, _, global := verifyScopes(t)
	writeVerifySyncFile(t, global, "never")
	_, _, warning, err := podsync.ResolveVerifyPolicy("", start, home)
	if err != nil {
		t.Fatal(err)
	}
	want := "signature verification is off because defaults.verify_signatures is never in " + global +
		"; remove defaults.verify_signatures from that file to verify under the always default (§4.7.9)"
	if warning != want {
		t.Errorf("warning = %q, want %q", warning, want)
	}
}

// Spec: §4.7.9, §6.2 — an invalid value refuses with the must be never |
// always message, naming the file when a sync.yaml supplied it.
func TestResolveVerifyPolicy_Refusals(t *testing.T) {
	t.Parallel()
	start, home, _, _, global := verifyScopes(t)
	_, _, _, err := podsync.ResolveVerifyPolicy("medium-and-above", start, home)
	if err == nil || err.Error() != `PODIUM_VERIFY_SIGNATURES must be never | always, got "medium-and-above"` {
		t.Errorf("env invalid = %v", err)
	}
	writeVerifySyncFile(t, global, "bogus")
	_, _, _, err = podsync.ResolveVerifyPolicy("", start, home)
	if err == nil || err.Error() != `PODIUM_VERIFY_SIGNATURES must be never | always, got "bogus" from defaults.verify_signatures in `+global {
		t.Errorf("sync.yaml invalid = %v", err)
	}
}

// Spec: §7.5.2 — a scope file that does not parse contributes nothing, so
// resolution falls through to the next scope that sets the key.
func TestResolveVerifyPolicy_UnparseableScopeSkipped(t *testing.T) {
	t.Parallel()
	start, home, local, shared, global := verifyScopes(t)
	if err := os.WriteFile(local, []byte("defaults: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	writeVerifySyncFile(t, global, "never")
	policy, source, _, err := podsync.ResolveVerifyPolicy("", start, home)
	if err != nil || policy != sign.PolicyNever || source != global {
		t.Errorf("unparseable and unreadable scopes = %q from %q, %v; want never from %s", policy, source, err, global)
	}
}

// Spec: §7.5.2 — with no start directory and no home, no scope is read.
func TestVerifySignaturesSetting_NoScopes(t *testing.T) {
	t.Parallel()
	value, path, err := podsync.VerifySignaturesSetting("", "")
	if value != "" || path != "" || err != nil {
		t.Errorf("VerifySignaturesSetting(\"\", \"\") = %q, %q, %v; want empty", value, path, err)
	}
}

// testKeyFile writes a registry key file holding a fresh public key and
// returns its path.
func testKeyFile(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := os.WriteFile(path, []byte("public: "+base64.StdEncoding.EncodeToString(pub)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mapEnv returns a getenv over m.
func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// Spec: §4.7.9, §7.5 — server-source sync reads no PODIUM_SIGNATURE_PROVIDER:
// with sigstore-keyless or bogus set, a sync.yaml never resolves a nil
// verifier and the always default resolves the registry-managed verifier from
// the key file, and neither returns config.invalid.
func TestResolveDeliveryCheck_IgnoresSignatureProvider(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"sigstore-keyless", "bogus"} {
		start, home, _, _, global := verifyScopes(t)
		env := map[string]string{"PODIUM_SIGNATURE_PROVIDER": provider, "PODIUM_SIGN_KEY_PATH": testKeyFile(t)}

		check, _, err := podsync.ResolveDeliveryCheck(start, home, mapEnv(env))
		if err != nil {
			t.Fatalf("%s: always default = %v", provider, err)
		}
		if _, ok := check.Verifier.(sign.RegistryManagedKey); check.Policy != sign.PolicyAlways || !ok {
			t.Errorf("%s: always default = %+v, want always with a RegistryManagedKey", provider, check)
		}

		writeVerifySyncFile(t, global, "never")
		check, warning, err := podsync.ResolveDeliveryCheck(start, home, mapEnv(env))
		if err != nil || check.Policy != sign.PolicyNever || check.Verifier != nil || warning == "" {
			t.Errorf("%s: sync.yaml never = %+v, %q, %v; want never, a nil verifier, and the warning", provider, check, warning, err)
		}
	}
}

// Spec: §4.7.9, §6.9 — under always with no material the resolution refuses
// with config.signature_provider_unavailable.
func TestResolveDeliveryCheck_NoMaterial(t *testing.T) {
	t.Parallel()
	start, home, _, _, _ := verifyScopes(t)
	env := map[string]string{"PODIUM_SIGN_KEY_PATH": filepath.Join(t.TempDir(), "absent.key")}
	if _, _, err := podsync.ResolveDeliveryCheck(start, home, mapEnv(env)); err == nil ||
		!strings.HasPrefix(err.Error(), "config.signature_provider_unavailable: ") {
		t.Errorf("err = %v, want config.signature_provider_unavailable", err)
	}
}

// Spec: §4.7.9, §7.5 — the resolver reads nothing when built, resolves once,
// passes the stale-never warning to warn once, and returns the same result on
// every call.
func TestNewDeliveryCheckFunc_MemoizesAndWarnsOnce(t *testing.T) {
	t.Parallel()
	start, home, _, _, global := verifyScopes(t)
	writeVerifySyncFile(t, global, "never")
	reads := 0
	getenv := func(k string) string { reads++; return "" }
	var warnings []string
	f := podsync.NewDeliveryCheckFunc(start, home, getenv, func(s string) { warnings = append(warnings, s) })
	if reads != 0 {
		t.Fatalf("building the resolver read %d variables, want 0", reads)
	}
	first, err := f()
	if err != nil || first == nil || first.Policy != sign.PolicyNever {
		t.Fatalf("first call = %+v, %v; want never", first, err)
	}
	afterFirst := reads
	second, err := f()
	if err != nil || second != first || reads != afterFirst {
		t.Errorf("second call = %p, %v after %d reads; want the memoized %p with no new read", second, err, reads-afterFirst, first)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], global) {
		t.Errorf("warnings = %q, want one naming %s", warnings, global)
	}
}

// Spec: §4.7.9, §7.5 — a resolution failure is a *DeliveryConfigError whose
// message is the underlying message unchanged and which unwraps to it, and
// the memoized failure repeats on every call.
func TestNewDeliveryCheckFunc_ConfigError(t *testing.T) {
	t.Parallel()
	f := podsync.NewDeliveryCheckFunc(t.TempDir(), t.TempDir(), mapEnv(map[string]string{"PODIUM_VERIFY_SIGNATURES": "bogus"}), nil)
	for i := 0; i < 2; i++ {
		check, err := f()
		var cfgErr *podsync.DeliveryConfigError
		if check != nil || !errors.As(err, &cfgErr) {
			t.Fatalf("call %d = %+v, %v; want a *DeliveryConfigError", i, check, err)
		}
		if !strings.HasPrefix(err.Error(), "PODIUM_VERIFY_SIGNATURES must be never | always") || errors.Unwrap(err) != cfgErr.Err {
			t.Errorf("call %d: message %q or unwrap differs from the inner error", i, err)
		}
	}
}
