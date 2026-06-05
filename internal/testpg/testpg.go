// Package testpg gives a test binary its own Postgres database. `go test ./...`
// runs package test binaries concurrently, and the live-Postgres suites all
// reach one database through PODIUM_POSTGRES_DSN. The conformance suite resets
// that database globally (it drops every per-org schema and truncates the shared
// tables), so without isolation one binary's reset wipes another binary's
// fixtures mid-test and the suites fail nondeterministically. A binary that
// opens live Postgres calls RunMain (or Isolate) from its TestMain so its tests
// run in a private database no other binary can touch.
package testpg

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// dsnEnv names the connection-string variable the live-Postgres tests read.
const dsnEnv = "PODIUM_POSTGRES_DSN"

// RunMain runs a package's tests inside a private Postgres database. A TestMain
// delegates to it:
//
//	func TestMain(m *testing.M) { os.Exit(testpg.RunMain(m)) }
//
// It isolates the database, runs the suite, drops the database, and returns the
// suite's exit code.
func RunMain(m *testing.M) int {
	cleanup := Isolate()
	code := m.Run()
	cleanup()
	return code
}

// Isolate creates a database unique to this process, rewrites PODIUM_POSTGRES_DSN
// to point at it, and returns a function that drops it. Every later
// store.OpenPostgres in the process, and any subprocess that reads the variable,
// then uses the private database. When PODIUM_POSTGRES_DSN is unset Isolate is a
// no-op returning a no-op cleanup, so a hermetic run is unchanged and the live
// tests still self-skip. A provisioning failure logs and falls back to the
// shared database rather than failing the suite.
func Isolate() func() {
	base := os.Getenv(dsnEnv)
	if base == "" {
		return func() {}
	}
	u, err := url.Parse(base)
	if err != nil {
		log.Printf("testpg: %s is not a URL (%v); using the shared database", dsnEnv, err)
		return func() {}
	}
	name := dbName()
	if err := adminExec(base, "CREATE DATABASE "+quoteIdent(name)); err != nil {
		log.Printf("testpg: CREATE DATABASE %s failed (%v); using the shared database", name, err)
		return func() {}
	}
	u.Path = "/" + name
	_ = os.Setenv(dsnEnv, u.String())
	return func() {
		// FORCE terminates any backend still attached so the drop succeeds even
		// when a pooled connection outlived its store.Close.
		if err := adminExec(base, "DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)"); err != nil {
			log.Printf("testpg: DROP DATABASE %s failed: %v", name, err)
		}
	}
}

// adminExec opens a short-lived connection to the base database and runs one
// statement. CREATE DATABASE and DROP DATABASE cannot run inside a transaction,
// so each executes on its own connection against a database other than the one
// being created or dropped.
func adminExec(baseDSN, stmt string) error {
	db, err := sql.Open("postgres", baseDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(stmt)
	return err
}

// dbName returns a database name unique to this process. A leftover from a
// crashed run is harmless: the name embeds the pid and a nanosecond timestamp,
// so it never collides with a live binary's database.
func dbName() string {
	return fmt.Sprintf("podium_test_%d_%d", os.Getpid(), time.Now().UnixNano())
}

// quoteIdent double-quotes a SQL identifier. dbName emits only [a-z0-9_], so the
// value carries no quote to escape.
func quoteIdent(s string) string {
	return `"` + s + `"`
}
