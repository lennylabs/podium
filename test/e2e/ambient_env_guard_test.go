package e2e

// Meta-test for the e2e harness's ambient-backend-env scrub. The shared
// mergeEnv helper (helpers_test.go) strips PODIUM_POSTGRES_DSN[_VECTOR],
// PODIUM_PGVECTOR_DSN, and the PODIUM_S3_* family from every subprocess
// environment so that an ambient backend configuration set by `make test-live`
// (or the gap-remediation harness) cannot leak into a "backend absent"
// CLI/serve test. Inherited into a bare `podium serve`, any of those vars would
// count as explicit server configuration (hasExplicitServerConfig) and suppress
// the §13.10 PODIUM_NO_AUTOSTANDALONE refusal that the deployment tests assert.
//
// These tests fail closed: a future regression that drops a clause from the
// scrub, or a new ambient backend var that the scrub does not cover, re-enables
// the suppression and is caught here rather than as a silent behavior change in
// an unrelated deployment test.
//
// Spec: §13.10 (Standalone Deployment; PODIUM_NO_AUTOSTANDALONE makes a missing
// server config a hard error rather than a cue to auto-bootstrap). Gap G-INFRA-2.

import (
	"strings"
	"testing"
)

// ambientBackendEnv is the set of backend selectors that `make test-live` and
// the live-test harness export, and that mergeEnv must keep out of a subprocess
// environment. Each entry is a var the scrub at helpers_test.go strips; the
// PODIUM_S3_* members below also exercise the prefix clause. PODIUM_S3_FUTUREVAR
// pins the prefix itself, so a hypothetical future S3 var is covered without
// editing this list.
var ambientBackendEnv = []string{
	"PODIUM_POSTGRES_DSN",
	"PODIUM_POSTGRES_DSN_VECTOR",
	"PODIUM_PGVECTOR_DSN",
	"PODIUM_S3_ENDPOINT",
	"PODIUM_S3_BUCKET",
	"PODIUM_S3_ACCESS_KEY_ID",
	"PODIUM_S3_SECRET_ACCESS_KEY",
	"PODIUM_S3_USE_SSL",
	"PODIUM_S3_FUTUREVAR",
}

// envHasKey reports whether key is present as "key=..." in a mergeEnv result. It
// mirrors mergeEnv's own IndexByte(kv, '=') parsing so a value that itself
// contains '=' (a DSN with query params) is matched on the key alone.
func envHasKey(env []string, key string) bool {
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 && kv[:i] == key {
			return true
		}
	}
	return false
}

// envValue returns the value mergeEnv resolved for key, or "" if absent.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 && kv[:i] == key {
			return kv[i+1:]
		}
	}
	return ""
}

// TestMergeEnv_StripsAmbientBackendConfig asserts that mergeEnv removes every
// ambient backend selector from the subprocess environment while leaving an
// unrelated PODIUM_* var in place. The surviving-var assertion stops a future
// "fix" that broadens the scrub into a blanket PODIUM_-strip, which would break
// tests that rely on inherited client configuration.
//
// Spec: §13.10.
func TestMergeEnv_StripsAmbientBackendConfig(t *testing.T) {
	for _, k := range ambientBackendEnv {
		t.Setenv(k, "ambient-should-be-scrubbed")
	}
	// A non-backend PODIUM_* var must survive untouched: the scrub is specific
	// to the backend selectors, not to the PODIUM_ namespace.
	t.Setenv("PODIUM_REGISTRY", "https://registry.acme.example")

	got := mergeEnv() // no explicit overrides
	for _, k := range ambientBackendEnv {
		if envHasKey(got, k) {
			t.Errorf("mergeEnv leaked ambient backend var %q into the subprocess env; "+
				"an ambient backend would suppress the §13.10 no-autostandalone refusal", k)
		}
	}
	if got, want := envValue(got, "PODIUM_REGISTRY"), "https://registry.acme.example"; got != want {
		t.Errorf("mergeEnv dropped a non-backend var PODIUM_REGISTRY=%q, want %q; the scrub is over-broad", got, want)
	}
}

// TestMergeEnv_ExplicitBackendOverrideSurvives asserts that an explicit backend
// value passed in `extra` reaches the subprocess even when an ambient value of
// the same key is being scrubbed. A test that genuinely needs a backend (the
// managed-stack parity test) passes the DSN explicitly, and that re-appended
// override must win over the ambient scrub.
func TestMergeEnv_ExplicitBackendOverrideSurvives(t *testing.T) {
	t.Setenv("PODIUM_POSTGRES_DSN", "postgres://ambient-leak@127.0.0.1/should-not-win")

	got := mergeEnv("PODIUM_POSTGRES_DSN=postgres://explicit@127.0.0.1/wins")

	count := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "PODIUM_POSTGRES_DSN=") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("PODIUM_POSTGRES_DSN appears %d times in mergeEnv output, want exactly 1 "+
			"(the ambient value must be scrubbed and only the explicit override re-appended)", count)
	}
	if v := envValue(got, "PODIUM_POSTGRES_DSN"); v != "postgres://explicit@127.0.0.1/wins" {
		t.Errorf("PODIUM_POSTGRES_DSN = %q, want the explicit override; the ambient value leaked through", v)
	}
}

// TestServe_AmbientBackendEnvDoesNotSuppressNoAutostandalone is the real-behavior
// guard: it sets the backend selectors in the test process, then boots a bare
// `podium serve` through the harness (runPodium forces PODIUM_NO_AUTOSTANDALONE=1
// and routes every var through mergeEnv). With the scrub intact the subprocess
// sees no backend config, so hasExplicitServerConfig() is false and the §13.10
// refusal fires (non-zero exit, the documented stderr message). Were the scrub
// to regress and let PODIUM_POSTGRES_DSN (or any PODIUM_S3_*) reach the
// subprocess, that var would count as explicit server config, the refusal would
// be suppressed, and the server would attempt to start against the (absent)
// backend instead — failing this assertion.
//
// This asserts observable subprocess behavior, not mergeEnv's return value, so
// it catches a leak that bypasses mergeEnv as well as one inside it.
//
// Spec: §13.10 (PODIUM_NO_AUTOSTANDALONE: a missing server config is a hard
// error, not a cue to auto-bootstrap).
func TestServe_AmbientBackendEnvDoesNotSuppressNoAutostandalone(t *testing.T) {
	// Ambient backend configuration, as `make test-live` exports it. If the
	// harness scrub stops removing these, they leak into the serve subprocess.
	for _, k := range ambientBackendEnv {
		t.Setenv(k, "ambient-should-be-scrubbed")
	}
	// Give the var a value plausible enough that a leak would actually drive the
	// server past the gate rather than fail validation early: PODIUM_POSTGRES_DSN
	// pairs with PODIUM_REGISTRY_STORE=postgres in serverboot, but on its own it
	// is enough to make hasExplicitServerConfig() true and suppress the refusal.
	t.Setenv("PODIUM_POSTGRES_DSN", "postgres://ambient@127.0.0.1:1/leak?sslmode=disable")

	// HOME is an explicit override (an empty temp dir) so registryYAMLExists()
	// is false: the only thing that could mark the deployment "explicitly
	// configured" is a leaked ambient backend var, which is exactly what the
	// scrub must prevent. Bare `serve` (no --standalone, no --layer-path, no
	// --config) keeps the zero-flag path in play.
	home := t.TempDir()
	res := runPodium(t, "", []string{"HOME=" + home}, "serve")

	// Positive control + the guard: the refusal must fire. Exit is non-zero and
	// the stderr carries the documented §13.10 message. If a leak suppressed the
	// refusal the server would instead try to start (and either bind or fail on
	// the bogus DSN), so neither condition would hold.
	if res.Exit == 0 {
		t.Fatalf("bare `serve` under PODIUM_NO_AUTOSTANDALONE exited 0; the §13.10 refusal did not fire. "+
			"An ambient backend var likely leaked through the mergeEnv scrub and suppressed it.\nstdout:\n%s\nstderr:\n%s",
			res.Stdout, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	if !strings.Contains(out, "requires explicit setup") {
		t.Fatalf("bare `serve` did not print the §13.10 no-autostandalone refusal; "+
			"an ambient backend var may have leaked through the mergeEnv scrub and marked the deployment configured.\n"+
			"exit=%d\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
	}
}
