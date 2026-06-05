package integration

import (
	"os"
	"sync"
	"testing"

	"github.com/lennylabs/podium/internal/testenv"
	"github.com/lennylabs/podium/internal/testpg"
)

// TestMain loads the optional test.env (see internal/testenv) before the
// integration suite runs, so the Postgres-backed and managed-backend tests
// pick up their credentials from one file. Without the file the suite runs
// unchanged; each integration test still self-skips on its own env gate. It then
// runs the suite in a private Postgres database (see internal/testpg) so a global
// reset here cannot collide with another package's test binary running
// concurrently under `go test ./...`. The pgGlobalResetMu below still serializes
// the resets within this binary.
func TestMain(m *testing.M) {
	testenv.Load()
	os.Exit(testpg.RunMain(m))
}

// pgGlobalResetMu serializes the integration tests that call
// store.Postgres.ResetForTest, which drops every org schema and truncates
// public.tenants on the one shared Postgres database. A reset is global, so two
// such tests interleaving corrupts whichever one is mid-flight: a parallel
// test's reset lands during another test's seed-then-assert window and wipes its
// rows. Each whole-database test holds this lock for its full body (reset, seed,
// and assertions) so the destructive resets cannot overlap. This is the heavier
// live-external lane's failure mode (the managed-backend and pgvector tests
// change the concurrency and timing enough to expose the race); under the
// PR/hermetic lane the same tests pass, but the guard removes the latent
// ordering dependency on both lanes.
var pgGlobalResetMu sync.Mutex

// lockPostgresReset acquires the shared whole-database lock and releases it when
// the test ends. A test that resets the shared Postgres database calls this
// before ResetForTest and holds it across every later assertion, so no other
// whole-database test can reset underneath it.
func lockPostgresReset(t *testing.T) {
	t.Helper()
	pgGlobalResetMu.Lock()
	t.Cleanup(pgGlobalResetMu.Unlock)
}
