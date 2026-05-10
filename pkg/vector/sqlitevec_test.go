package vector_test

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/vector/vectortest"
)

// Spec: §4.7 — sqlite-vec backend satisfies the SPI conformance
// suite. Runs in-process via the cgo-bound sqlite-vec extension; no
// external infra needed.
func TestSQLiteVec_Conformance(t *testing.T) {
	t.Parallel()
	dim := 8
	vectortest.Suite(t, dim, func(t *testing.T) vector.Provider {
		path := filepath.Join(t.TempDir(), "vec.db")
		v, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{
			Path: path, Dimensions: dim,
		})
		if err != nil {
			t.Fatalf("OpenSQLiteVec: %v", err)
		}
		t.Cleanup(func() { _ = v.Close() })
		return v
	})
}
