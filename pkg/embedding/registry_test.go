package embedding

import (
	"context"
	"testing"
)

// spec: §9.1/§9.2 — a custom EmbeddingProvider imported into a source build
// registers by id and the bootstrap constructs it from resolved settings.
func TestRegistry_RegisterAndNew(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("acme-embed", func(s map[string]string) (Provider, error) {
		return stubEmbed{id: "acme-embed", model: s["model"]}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	p, ok, err := r.New("acme-embed", map[string]string{"model": "m1"})
	if err != nil || !ok {
		t.Fatalf("New: ok=%v err=%v", ok, err)
	}
	if p.ID() != "acme-embed" {
		t.Errorf("ID = %q", p.ID())
	}
}

// spec: §9.2 — an unregistered id falls through so the built-in switch
// keeps its per-provider validation.
func TestRegistry_FallThrough(t *testing.T) {
	r := NewRegistry()
	p, ok, err := r.New("openai", nil)
	if ok || p != nil || err != nil {
		t.Errorf("New(unregistered) = (%v, %v, %v), want (nil, false, nil)", p, ok, err)
	}
}

func TestRegistry_Duplicate(t *testing.T) {
	r := NewRegistry()
	f := func(map[string]string) (Provider, error) { return stubEmbed{id: "x"}, nil }
	if err := r.Register("x", f); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("x", f); err == nil {
		t.Error("duplicate: want error")
	}
}

type stubEmbed struct {
	id    string
	model string
}

func (s stubEmbed) ID() string                                           { return s.id }
func (s stubEmbed) Model() string                                        { return s.model }
func (s stubEmbed) Embed(context.Context, []string) ([][]float32, error) { return nil, nil }
func (s stubEmbed) Dimensions() int                                      { return 8 }
