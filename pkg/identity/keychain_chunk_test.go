package identity

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// Chunking tests run against go-keyring's in-memory mock. The chunk
// threshold is enforced by KeychainStore itself, so the mock exercises the
// same code paths the OS backends do.

func TestKeychainStore_SmallTokenSingleEntry(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	if err := k.Save("small", "tok-value"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := keyring.Get("podium-test", "small")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if raw != "tok-value" {
		t.Errorf("stored value = %q, want the token verbatim", raw)
	}
	got, err := k.Load("small")
	if err != nil || got != "tok-value" {
		t.Errorf("Load = %q, %v; want tok-value, nil", got, err)
	}
}

func TestKeychainStore_ChunkedRoundTrip(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	// An AD FS refresh token measures ~4.7 KB; build a token over the
	// per-entry ceiling so Save splits it.
	token := strings.Repeat("r", 3*keychainChunkSize+17)
	if err := k.Save("big", token); err != nil {
		t.Fatalf("Save: %v", err)
	}

	marker, err := keyring.Get("podium-test", "big")
	if err != nil {
		t.Fatalf("Get marker: %v", err)
	}
	if marker != keychainChunkMarker+"4" {
		t.Errorf("marker = %q, want %q", marker, keychainChunkMarker+"4")
	}

	got, err := k.Load("big")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != token {
		t.Errorf("Load reassembled %d bytes, want %d and identical content", len(got), len(token))
	}

	if err := k.Delete("big"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Load("big"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("Load after Delete: %v, want ErrTokenNotFound", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := keyring.Get("podium-test", fmt.Sprintf("big#chunk%d", i)); !errors.Is(err, keyring.ErrNotFound) {
			t.Errorf("chunk %d survived Delete: %v", i, err)
		}
	}
}

func TestKeychainStore_MissingChunkIsAnError(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	token := strings.Repeat("r", 2*keychainChunkSize)
	if err := k.Save("gap", token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := keyring.Delete("podium-test", "gap#chunk1"); err != nil {
		t.Fatalf("Delete chunk: %v", err)
	}
	if _, err := k.Load("gap"); err == nil || !strings.Contains(err.Error(), "chunk") {
		t.Errorf("Load with a missing chunk = %v, want a chunk error", err)
	}
}

func TestKeychainStore_EmptyServiceRejected(t *testing.T) {
	k := KeychainStore{}
	if _, err := k.Load("x"); err == nil {
		t.Errorf("Load with empty Service succeeded, want error")
	}
	if err := k.Delete("x"); err == nil {
		t.Errorf("Delete with empty Service succeeded, want error")
	}
}

func TestKeychainStore_DeleteMissingIsNil(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	if err := k.Delete("absent"); err != nil {
		t.Errorf("Delete of a missing entry = %v, want nil", err)
	}
}
