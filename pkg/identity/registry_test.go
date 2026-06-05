package identity

import (
	"context"
	"errors"
	"testing"
)

// spec: §9.1/§9.2 — the built-in providers are seeded into identity.Default
// so a registry build selects them by id without editing the server.
func TestDefaultRegistry_SeedsBuiltins(t *testing.T) {
	for _, id := range []string{"oauth-device-code", "injected-session-token"} {
		if !Default.Has(id) {
			t.Errorf("Default registry missing built-in %q", id)
		}
	}
}

// spec: §9.2 — an imported plugin registers a custom IdentityProvider by id
// and the server constructs it via the same seam.
func TestRegistry_RegisterAndNew(t *testing.T) {
	r := NewRegistry()
	want := Identity{Sub: "alice@acme.com", IsAuthenticated: true}
	if err := r.Register("acme-sso", func(Config) (Provider, error) {
		return stubProvider{id: "acme-sso", identity: want}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	p, err := r.New("acme-sso", Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.ID() != "acme-sso" {
		t.Errorf("ID = %q, want acme-sso", p.ID())
	}
	got, err := p.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Sub != want.Sub {
		t.Errorf("Resolve sub = %q, want %q", got.Sub, want.Sub)
	}
}

// spec: §9.2 — two providers MUST NOT silently claim the same id (mirrors
// typeprovider's register-time conflict).
func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewRegistry()
	f := func(Config) (Provider, error) { return stubProvider{id: "x"}, nil }
	if err := r.Register("x", f); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register("x", f); err == nil {
		t.Fatal("duplicate Register: want error, got nil")
	}
}

// spec: §9.2 — an unknown provider id is a fatal misconfiguration, not a
// silent anonymous fallback.
func TestRegistry_UnknownProvider(t *testing.T) {
	r := NewRegistry()
	_, err := r.New("nope", Config{})
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("New(unknown) err = %v, want ErrUnknownProvider", err)
	}
}

func TestRegistry_EmptyIDAndNilFactory(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", func(Config) (Provider, error) { return nil, nil }); err == nil {
		t.Error("empty id: want error")
	}
	if err := r.Register("x", nil); err == nil {
		t.Error("nil factory: want error")
	}
}

type stubProvider struct {
	id       string
	identity Identity
}

func (s stubProvider) ID() string { return s.id }
func (s stubProvider) Resolve(context.Context) (Identity, error) {
	return s.identity, nil
}
