package webhook

import (
	"errors"
	"testing"
)

// spec: §9.1/§9.2 — the built-in GitProviders are seeded into
// webhook.Default so the inbound ingest path selects them by id.
func TestDefaultRegistry_SeedsBuiltins(t *testing.T) {
	for _, id := range []string{"github", "gitlab", "bitbucket"} {
		if _, ok := Default.Get(id); !ok {
			t.Errorf("Default registry missing built-in %q", id)
		}
	}
}

// spec: §9.2/§7.3.1 — a registered provider verifies a delivery through the
// registry seam; the github built-in round-trips Sign/Verify.
func TestRegistry_VerifyRoundTrip(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "s3cr3t"
	sig, err := Sign("github", body, secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Default.Verify("github", body, sig, secret); err != nil {
		t.Errorf("Verify valid signature: %v", err)
	}
	if err := Default.Verify("github", body, sig, "wrong"); err == nil {
		t.Error("Verify with wrong secret: want error")
	}
}

// spec: §9.2 — an imported plugin registers a custom GitProvider and the
// path verifies through it.
func TestRegistry_RegisterCustom(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(GitHub{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register(GitHub{}); err == nil {
		t.Fatal("duplicate Register: want error")
	}
	body := []byte("x")
	sig, _ := Sign("github", body, "k")
	if err := r.Verify("github", body, sig, "k"); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// spec: §6.10 — a delivery naming an unregistered provider is rejected
// (ingest.webhook_invalid) rather than ingested.
func TestRegistry_UnknownProvider(t *testing.T) {
	r := NewRegistry()
	err := r.Verify("self-hosted-forge", []byte("x"), "sig", "secret")
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("Verify(unknown) = %v, want ErrUnknownProvider", err)
	}
}

func TestRegistry_NilAndEmpty(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("nil provider: want error")
	}
}
