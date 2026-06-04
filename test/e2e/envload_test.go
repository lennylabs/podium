package e2e

import (
	"os"
	"testing"

	"github.com/lennylabs/podium/internal/testenv"
)

// TestMain loads the optional test.env (see internal/testenv) before the e2e
// suite runs, so the live Postgres, S3, and managed-backend journeys pick up
// their credentials from one file. Without the file the suite runs unchanged;
// each live test still self-skips on its own env gate.
func TestMain(m *testing.M) {
	testenv.Load()
	os.Exit(m.Run())
}
