package identity_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/identity"
)

// Spec: §6.3 — Save then Load round-trips a token under a label.
// Phase: 11
func TestMemoryStore_RoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	s := identity.NewMemoryStore()
	if err := s.Save("podium-prod", "tok-abc"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load("podium-prod")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "tok-abc" {
		t.Errorf("got %q, want tok-abc", got)
	}
}

// Spec: §6.3 — Load on a missing label returns ErrTokenNotFound so
// callers can distinguish "no cached token" from "keychain failure."
// Phase: 11
func TestMemoryStore_LoadMissing(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	s := identity.NewMemoryStore()
	_, err := s.Load("absent")
	if !errors.Is(err, identity.ErrTokenNotFound) {
		t.Fatalf("got %v, want ErrTokenNotFound", err)
	}
}

// Spec: §6.3 — Delete removes the entry; Delete on a missing entry
// is a no-op (idempotent rotation).
// Phase: 11
func TestMemoryStore_Delete(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	s := identity.NewMemoryStore()
	_ = s.Save("x", "tok")
	if err := s.Delete("x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Load("x"); !errors.Is(err, identity.ErrTokenNotFound) {
		t.Errorf("after delete: got %v, want ErrTokenNotFound", err)
	}
	if err := s.Delete("x"); err != nil {
		t.Errorf("idempotent delete failed: %v", err)
	}
}

// Spec: §6.3 — Service is required for KeychainStore.
// Phase: 11
func TestKeychainStore_RequiresService(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	k := identity.KeychainStore{}
	if err := k.Save("x", "tok"); err == nil {
		t.Errorf("expected error when Service is empty")
	}
}
