package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/podium/internal/testenv"
)

// TestMain loads the optional test.env (see internal/testenv) before the e2e
// suite runs, so the live Postgres, S3, and managed-backend journeys pick up
// their credentials from one file. Without the file the suite runs unchanged;
// each live test still self-skips on its own env gate.
//
// It also creates the directory msSigningKeyPath writes the shared
// standard-stack signing key into, and removes it once the suite returns.
func TestMain(m *testing.M) {
	testenv.Load()
	dir, err := os.MkdirTemp("", "podium-e2e-signing-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: create the signing-key directory: %v\n", err)
		os.Exit(1)
	}
	msSigningKeyDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
