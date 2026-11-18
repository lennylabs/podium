package vector

import "testing"

// Spec: §7.3.1 — a stored vector that does not decode fails the read rather
// than returning a wrong vector: pgvector's text form must be a bracketed
// list of numbers, and a sqlite-vec blob must hold whole float32 values.
func TestStoredVectorDecoding(t *testing.T) {
	t.Parallel()
	if v, err := parseVecLiteral(" [] "); err != nil || len(v) != 0 {
		t.Errorf("parseVecLiteral([]) = %v, %v; want an empty vector", v, err)
	}
	if v, err := parseVecLiteral("[1, 2.5]"); err != nil || len(v) != 2 || v[1] != 2.5 {
		t.Errorf("parseVecLiteral([1, 2.5]) = %v, %v", v, err)
	}
	for _, bad := range []string{"1,2", "[1,x]"} {
		if _, err := parseVecLiteral(bad); err == nil {
			t.Errorf("parseVecLiteral(%q) succeeded", bad)
		}
	}
	if _, err := deserialize([]byte{1, 2, 3}); err == nil {
		t.Error("deserialize of a 3-byte blob succeeded")
	}
}
