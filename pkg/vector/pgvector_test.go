package vector_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/vector/vectortest"
)

// pgVectorDSN returns the DSN to drive pgvector tests, preferring
// PODIUM_POSTGRES_DSN_VECTOR (so a deployment can split metadata and
// vectors across databases) and falling back to PODIUM_POSTGRES_DSN.
// The ok return is false when both variables are unset or empty.
func pgVectorDSN() (string, bool) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN_VECTOR")
	if dsn == "" {
		dsn = os.Getenv("PODIUM_POSTGRES_DSN")
	}
	return dsn, dsn != ""
}

// Spec: §4.7 — pgvector backend satisfies the SPI conformance suite.
// Gated on PODIUM_POSTGRES_DSN_VECTOR (separate from the metadata-
// store DSN so operators can point them at different databases) so
// CI without a Postgres+pgvector instance skips cleanly.
func TestPgVector_Conformance(t *testing.T) {
	dim := 8
	// Run inside a uniquely-named, ephemeral schema (the same isolation the
	// depth tests use) rather than the shared public.vec_artifacts table. The
	// conformance suite needs the table at dim 8, but OpenPgVector's
	// CREATE TABLE IF NOT EXISTS is a no-op against a pre-existing
	// public.vec_artifacts left at a different dimension by another lane (the
	// managed-stack e2e and the §13.12 backends create it at the 1536
	// production dimension). A dedicated schema makes the conformance run
	// self-contained on the heavier live-external lane and immune to that
	// leftover table.
	scope := openPgSchema(t, dim)
	t.Cleanup(scope.close)
	bootstrap := scope.pg

	vectortest.Suite(t, dim, func(t *testing.T) vector.Provider {
		// Truncate before each sub-test for isolation.
		if _, err := truncate(bootstrap); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		return bootstrap
	})
}

// truncate clears the vec_artifacts table; used between sub-tests.
func truncate(p *vector.PgVector) (string, error) {
	_, err := p.DB().ExecContext(context.Background(), `TRUNCATE vec_artifacts`)
	return uniqueSuffix(), err
}

func uniqueSuffix() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// Spec: n/a — pgVectorDSN's resolution order. The integration test
// above gates on real Postgres availability; this test exercises the
// env-var resolution branch independently using t.Setenv.
func TestPgVectorDSN_Resolution(t *testing.T) {
	cases := []struct {
		name      string
		vectorDSN string
		baseDSN   string
		want      string
		wantOK    bool
	}{
		{"both unset", "", "", "", false},
		{"only base set", "", "postgres://base", "postgres://base", true},
		{"only vector set", "postgres://vec", "", "postgres://vec", true},
		{"both set — vector wins", "postgres://vec", "postgres://base", "postgres://vec", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PODIUM_POSTGRES_DSN_VECTOR", tc.vectorDSN)
			t.Setenv("PODIUM_POSTGRES_DSN", tc.baseDSN)
			got, ok := pgVectorDSN()
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("pgVectorDSN() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
