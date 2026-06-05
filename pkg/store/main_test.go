package store_test

import (
	"os"
	"testing"

	"github.com/lennylabs/podium/internal/testpg"
)

// TestMain runs this binary's tests in a private Postgres database (see
// internal/testpg), so the conformance suite's global reset cannot collide with
// another package's test binary running concurrently under `go test ./...`.
// Without PODIUM_POSTGRES_DSN the live Postgres tests self-skip and this is a
// no-op.
func TestMain(m *testing.M) { os.Exit(testpg.RunMain(m)) }
