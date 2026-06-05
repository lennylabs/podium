package vector

import (
	"testing"
)

// spec: §9.1/§9.2 — a custom RegistrySearchProvider imported into a source
// build registers by id and the bootstrap constructs it from resolved
// settings plus the embedding dimension.
func TestRegistry_RegisterAndNew(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("acme-vec", func(_ map[string]string, dim int) (Provider, error) {
		return NewMemory(dim), nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	p, ok, err := r.New("acme-vec", map[string]string{"url": "x"}, 16)
	if err != nil || !ok {
		t.Fatalf("New: ok=%v err=%v", ok, err)
	}
	if p == nil {
		t.Fatal("New returned nil provider")
	}
}

// spec: §9.2 — an unregistered id falls through to the built-in switch.
func TestRegistry_FallThrough(t *testing.T) {
	r := NewRegistry()
	p, ok, err := r.New("pgvector", nil, 8)
	if ok || p != nil || err != nil {
		t.Errorf("New(unregistered) = (%v, %v, %v), want (nil, false, nil)", p, ok, err)
	}
}

func TestRegistry_Duplicate(t *testing.T) {
	r := NewRegistry()
	f := func(map[string]string, int) (Provider, error) { return NewMemory(8), nil }
	if err := r.Register("x", f); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("x", f); err == nil {
		t.Error("duplicate: want error")
	}
}
