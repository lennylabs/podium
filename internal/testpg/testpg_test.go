package testpg

import (
	"os"
	"testing"
)

// Isolate is a no-op when PODIUM_POSTGRES_DSN is unset: it returns a no-op
// cleanup and leaves the variable empty, so a hermetic run is unaffected.
func TestIsolate_NoopWhenDSNUnset(t *testing.T) {
	t.Setenv(dsnEnv, "")
	cleanup := Isolate()
	if got := os.Getenv(dsnEnv); got != "" {
		t.Errorf("%s = %q, want empty (unchanged)", dsnEnv, got)
	}
	cleanup() // must not panic
}

// An unparsable DSN cannot be rewritten, so Isolate logs and leaves it unchanged.
func TestIsolate_FallsBackOnUnparsableDSN(t *testing.T) {
	const bad = "postgres://[::1" // unterminated IPv6 host, rejected by url.Parse
	t.Setenv(dsnEnv, bad)
	cleanup := Isolate()
	if got := os.Getenv(dsnEnv); got != bad {
		t.Errorf("%s = %q, want %q unchanged after a parse error", dsnEnv, got, bad)
	}
	cleanup()
}

// When the base server is unreachable, CREATE DATABASE fails; Isolate logs and
// falls back to the shared DSN so the suite still runs and self-skips on the
// unreachable backend.
func TestIsolate_FallsBackWhenCreateFails(t *testing.T) {
	const dsn = "postgres://podium:podium@127.0.0.1:1/podium?sslmode=disable"
	t.Setenv(dsnEnv, dsn)
	cleanup := Isolate()
	if got := os.Getenv(dsnEnv); got != dsn {
		t.Errorf("%s = %q, want the base DSN unchanged after CREATE failed", dsnEnv, got)
	}
	cleanup() // a best-effort drop against the unreachable server must not panic
}
