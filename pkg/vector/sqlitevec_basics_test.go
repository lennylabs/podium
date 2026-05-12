package vector_test

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

func TestSQLiteVec_IDAndDimensions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "vec.db")
	v, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Path: path, Dimensions: 4})
	if err != nil {
		t.Fatalf("OpenSQLiteVec: %v", err)
	}
	defer v.Close()
	if v.ID() != "sqlite-vec" {
		t.Errorf("ID = %q", v.ID())
	}
	if v.Dimensions() != 4 {
		t.Errorf("Dimensions = %d", v.Dimensions())
	}
	if v.DB() == nil {
		t.Errorf("DB returned nil")
	}
}

func TestSQLiteVec_OpenInMemoryDefault(t *testing.T) {
	t.Parallel()
	v, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Dimensions: 4}) // empty path → :memory:
	if err != nil {
		t.Fatalf("OpenSQLiteVec: %v", err)
	}
	defer v.Close()
	if v.ID() != "sqlite-vec" {
		t.Errorf("ID = %q", v.ID())
	}
}

func TestSQLiteVec_BadDimensionsErrors(t *testing.T) {
	t.Parallel()
	if _, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Dimensions: 0}); err == nil {
		t.Errorf("expected error for dim 0")
	}
	if _, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Dimensions: -1}); err == nil {
		t.Errorf("expected error for dim -1")
	}
}
