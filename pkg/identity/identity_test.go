package identity

import (
	"context"
	"errors"
	"testing"
)

// Spec: §6.3.2 — InjectedSessionToken returns the verified Identity
// when both TokenSource and Verify succeed.
func TestInjectedSessionToken_Verifies(t *testing.T) {
	t.Parallel()
	want := Identity{Sub: "joan", IsAuthenticated: true, Groups: []string{"finance"}}
	p := InjectedSessionToken{
		TokenSource: func() (string, error) { return "fake-jwt", nil },
		Verify: func(jwt string) (Identity, error) {
			if jwt != "fake-jwt" {
				t.Fatalf("unexpected jwt: %q", jwt)
			}
			return want, nil
		},
	}
	got, err := p.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Sub != want.Sub {
		t.Errorf("Sub = %q, want %q", got.Sub, want.Sub)
	}
}

// Spec: §6.3.2 / §6.10 — unsigned token rejection surfaces as
// ErrUntrustedRuntime.
// Matrix: §6.10 (auth.untrusted_runtime)
func TestInjectedSessionToken_RejectsUntrusted(t *testing.T) {
	t.Parallel()
	p := InjectedSessionToken{
		TokenSource: func() (string, error) { return "fake-jwt", nil },
		Verify: func(string) (Identity, error) {
			return Identity{}, ErrUntrustedRuntime
		},
	}
	_, err := p.Resolve(context.Background())
	if !errors.Is(err, ErrUntrustedRuntime) {
		t.Fatalf("got %v, want ErrUntrustedRuntime", err)
	}
}

// Spec: §6.3 — OAuthDeviceCode without an AcquireToken function returns
// ErrDeviceCodeRequired so the caller can drive the device-code flow.
func TestOAuthDeviceCode_RequiresFlow(t *testing.T) {
	t.Parallel()
	p := OAuthDeviceCode{
		VerificationURL: "https://example/device",
		Code:            "ABCD-1234",
	}
	_, err := p.Resolve(context.Background())
	if !errors.Is(err, ErrDeviceCodeRequired) {
		t.Errorf("got %v, want ErrDeviceCodeRequired", err)
	}
}

// Spec: §6.3 — provider IDs are stable and match the documented values.
func TestProvider_IDs(t *testing.T) {
	t.Parallel()
	if (InjectedSessionToken{}).ID() != "injected-session-token" {
		t.Errorf("InjectedSessionToken.ID changed")
	}
	if (OAuthDeviceCode{}).ID() != "oauth-device-code" {
		t.Errorf("OAuthDeviceCode.ID changed")
	}
}
