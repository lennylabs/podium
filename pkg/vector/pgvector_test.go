package vector_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/vector/vectortest"
)

// Spec: §4.7 — pgvector backend satisfies the SPI conformance suite.
// Gated on PODIUM_POSTGRES_DSN_VECTOR (separate from the metadata-
// store DSN so operators can point them at different databases) so
// CI without a Postgres+pgvector instance skips cleanly.
// Phase: 5
func TestPgVector_Conformance(t *testing.T) {
	testharness.RequirePhase(t, 5)
	dsn := os.Getenv("PODIUM_POSTGRES_DSN_VECTOR")
	if dsn == "" {
		dsn = os.Getenv("PODIUM_POSTGRES_DSN")
	}
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN[_VECTOR] unset; skipping pgvector conformance")
	}
	dim := 8
	bootstrap, err := vector.OpenPgVector(vector.PgVectorConfig{DSN: dsn, Dimensions: dim})
	if err != nil {
		t.Skipf("pgvector unreachable: %v", err)
	}
	t.Cleanup(func() { _ = bootstrap.Close() })

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
