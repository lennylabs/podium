package integration

import (
	"os"
	"testing"

	"github.com/lennylabs/podium/internal/testenv"
)

// TestMain loads the optional test.env (see internal/testenv) before the
// integration suite runs, so the Postgres-backed and managed-backend tests
// pick up their credentials from one file. Without the file the suite runs
// unchanged; each integration test still self-skips on its own env gate.
func TestMain(m *testing.M) {
	testenv.Load()
	os.Exit(m.Run())
}
