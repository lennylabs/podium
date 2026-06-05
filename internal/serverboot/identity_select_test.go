package serverboot

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// spec: §9.1/§9.2 — the bootstrap selects the IdentityProvider for
// cfg.identityProvider from the process-global identity.Default registry,
// the build-path consumer that makes an imported custom provider change
// behavior. The built-ins are resolvable; the empty / server-side modes
// stay on the anonymous resolver (nil provider).
func TestSelectIdentityProvider_Builtins(t *testing.T) {
	for _, id := range []string{"oauth-device-code", "injected-session-token"} {
		p, err := selectIdentityProvider(&Config{identityProvider: id})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if p == nil {
			t.Fatalf("%s: provider is nil", id)
		}
		if p.ID() != id {
			t.Errorf("%s: ID() = %q", id, p.ID())
		}
	}
}

// spec: §9.2 — server-side identity modes that are not MCP-server providers
// (the empty standalone default, OIDC) are absent from the registry and
// resolve to nil so the server stays on the anonymous resolver.
func TestSelectIdentityProvider_NonProviderModes(t *testing.T) {
	for _, id := range []string{"", "oidc", "public"} {
		p, err := selectIdentityProvider(&Config{identityProvider: id})
		if err != nil {
			t.Fatalf("%q: unexpected err %v", id, err)
		}
		if p != nil {
			t.Errorf("%q: provider = %v, want nil", id, p)
		}
	}
}

// spec: §9.2 — a custom IdentityProvider registered via
// identity.Default.Register is selected by its id in a source build.
func TestSelectIdentityProvider_CustomRegistered(t *testing.T) {
	const id = "acme-sso"
	if !identity.Default.Has(id) {
		if err := identity.Default.Register(id, func(identity.Config) (identity.Provider, error) {
			return acmeIdentity{}, nil
		}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	p, err := selectIdentityProvider(&Config{identityProvider: id})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if p == nil || p.ID() != id {
		t.Fatalf("custom provider = %v", p)
	}
}

// acmeIdentity is a stub custom IdentityProvider proving the registry seam.
type acmeIdentity struct{}

func (acmeIdentity) ID() string { return "acme-sso" }
func (acmeIdentity) Resolve(context.Context) (identity.Identity, error) {
	return identity.Identity{Sub: "alice@acme.com", IsAuthenticated: true}, nil
}

// guard against an accidental error-wrapping regression in New.
func TestIdentityRegistry_UnknownIsError(t *testing.T) {
	_, err := identity.NewRegistry().New("nope", identity.Config{})
	if !errors.Is(err, identity.ErrUnknownProvider) {
		t.Errorf("err = %v, want ErrUnknownProvider", err)
	}
}
