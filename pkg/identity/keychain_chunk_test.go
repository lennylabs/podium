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

func TestKeychainStore_ChunkSaveErrorSurfaces(t *testing.T) {
	keyring.MockInitWithError(errors.New("backend unavailable"))
	t.Cleanup(keyring.MockInit)
	k := KeychainStore{Service: "podium-test"}
	if err := k.Save("big", strings.Repeat("r", 2*keychainChunkSize)); err == nil {
		t.Errorf("Save with a failing backend succeeded, want error")
	}
	if err := k.Save("small", "tok"); err == nil {
		t.Errorf("small Save with a failing backend succeeded, want error")
	}
	if _, err := k.Load("big"); err == nil {
		t.Errorf("Load with a failing backend succeeded, want error")
	}
}

// failSetAfter makes the nth chunk write fail, modelling a Save interrupted
// partway: a signal during `podium login`, a keychain prompt the user denies,
// or a session bus that drops. go-keyring's mock fails every operation at once,
// so the failure is injected through the package's backend variables instead.
func failSetAfter(t *testing.T, n int) {
	t.Helper()
	real := keyringSet
	calls := 0
	keyringSet = func(service, label, value string) error {
		calls++
		if calls > n {
			return errors.New("keychain: backend interrupted")
		}
		return real(service, label, value)
	}
	t.Cleanup(func() { keyringSet = real })
}

// chunkEntries returns the chunk indices that still exist under label.
func chunkEntries(t *testing.T, service, label string) []int {
	t.Helper()
	var found []int
	for i := 0; i < 16; i++ {
		if _, err := keyring.Get(service, fmt.Sprintf("%s#chunk%d", label, i)); err == nil {
			found = append(found, i)
		}
	}
	return found
}

// A re-save that no longer needs chunks removes the previous token's chunks.
// The MCP bridge re-saves the access token under the same label on every silent
// refresh, so a large token followed by a smaller one is the common path, and
// the leftover chunks hold the whole earlier token.
func TestKeychainStore_ReSaveRemovesPreviousChunks(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	if err := k.Save("reg", strings.Repeat("A", 3*keychainChunkSize+10)); err != nil {
		t.Fatalf("Save chunked: %v", err)
	}
	if err := k.Save("reg", "small-access-token"); err != nil {
		t.Fatalf("Save small: %v", err)
	}
	if got, err := k.Load("reg"); err != nil || got != "small-access-token" {
		t.Errorf("Load = %q, %v; want small-access-token, nil", got, err)
	}
	if left := chunkEntries(t, "podium-test", "reg"); len(left) != 0 {
		t.Errorf("chunks %v from the previous token survive the re-save", left)
	}
}

// A re-save with fewer chunks drops the tail of the previous token.
func TestKeychainStore_ReSaveDropsSurplusChunks(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	if err := k.Save("reg", strings.Repeat("A", 4*keychainChunkSize)); err != nil {
		t.Fatalf("Save 4 chunks: %v", err)
	}
	shorter := strings.Repeat("B", 2*keychainChunkSize)
	if err := k.Save("reg", shorter); err != nil {
		t.Fatalf("Save 2 chunks: %v", err)
	}
	if got, err := k.Load("reg"); err != nil || got != shorter {
		t.Errorf("Load returned %d bytes (err %v), want the %d-byte token", len(got), err, len(shorter))
	}
	if left := chunkEntries(t, "podium-test", "reg"); len(left) != 2 {
		t.Errorf("chunk entries after shrinking = %v, want exactly [0 1]", left)
	}
}

// A Save interrupted between chunk writes must not leave the previous marker
// pointing at a mix of new and old chunks. Load would otherwise splice them
// into a token that authenticates nowhere and report no error, and the only
// way out would be a manual keychain edit.
func TestKeychainStore_InterruptedSaveDoesNotSplice(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	first := strings.Repeat("A", 2*keychainChunkSize)
	if err := k.Save("reg", first); err != nil {
		t.Fatalf("Save: %v", err)
	}

	failSetAfter(t, 1) // the second chunk write fails
	if err := k.Save("reg", strings.Repeat("B", 2*keychainChunkSize)); err == nil {
		t.Fatal("interrupted Save returned nil, want the backend error")
	}

	got, err := k.Load("reg")
	if err == nil {
		t.Errorf("Load after an interrupted Save returned %d bytes (%d new, %d old), want an error so the caller re-authenticates",
			len(got), strings.Count(got, "B"), strings.Count(got, "A"))
	}
	if !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("Load after an interrupted Save = %v, want ErrTokenNotFound", err)
	}
}

// An unchunked entry keeps its value when a Save fails, so a backend blip does
// not evict a usable cached token.
func TestKeychainStore_FailedSaveKeepsUnchunkedToken(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	if err := k.Save("reg", "previous-token"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	failSetAfter(t, 0)
	if err := k.Save("reg", strings.Repeat("B", 2*keychainChunkSize)); err == nil {
		t.Fatal("Save with a failing backend returned nil, want an error")
	}
	if got, err := k.Load("reg"); err != nil || got != "previous-token" {
		t.Errorf("Load = %q, %v; want the previous token preserved", got, err)
	}
}

// Delete sweeps chunk entries no marker names, which is the state an
// interrupted Save leaves behind. Logout has to remove that token material.
func TestKeychainStore_DeleteSweepsUnmarkedChunks(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	for i := 0; i < 3; i++ {
		if err := keyring.Set("podium-test", fmt.Sprintf("reg#chunk%d", i), strings.Repeat("A", 32)); err != nil {
			t.Fatalf("seed chunk %d: %v", i, err)
		}
	}
	if err := keyring.Set("podium-test", "reg", "plain-token"); err != nil {
		t.Fatalf("seed label: %v", err)
	}
	if err := k.Delete("reg"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if left := chunkEntries(t, "podium-test", "reg"); len(left) != 0 {
		t.Errorf("chunks %v survive Delete", left)
	}
}

// A value that merely starts with the marker prefix is an ordinary token: the
// count after the prefix is parsed exactly rather than scanned.
func TestKeychainStore_MalformedMarkerIsAnOrdinaryToken(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	for _, value := range []string{
		keychainChunkMarker + "2junk",
		keychainChunkMarker + "notanumber",
		keychainChunkMarker + "0",
		keychainChunkMarker + "-1",
	} {
		if err := keyring.Set("podium-test", "odd", value); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, err := k.Load("odd")
		if err != nil || got != value {
			t.Errorf("Load(%q) = %q, %v; want the value verbatim", value, got, err)
		}
	}
}

// failDelete makes every chunk removal fail with a backend error rather than
// ErrNotFound, so the sweep reports it instead of treating the entry as gone.
func failDelete(t *testing.T) {
	t.Helper()
	real := keyringDelete
	keyringDelete = func(service, label string) error {
		if strings.Contains(label, "#chunk") {
			return errors.New("keychain: backend refused the delete")
		}
		return real(service, label)
	}
	t.Cleanup(func() { keyringDelete = real })
}

// A sweep that cannot remove a stale chunk surfaces the error rather than
// leaving the old token's material behind under a fresh marker.
func TestKeychainStore_SweepErrorSurfaces(t *testing.T) {
	keyring.MockInit()
	k := KeychainStore{Service: "podium-test"}
	if err := k.Save("reg", strings.Repeat("A", 2*keychainChunkSize)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	failDelete(t)
	if err := k.Delete("reg"); err == nil {
		t.Errorf("Delete with a failing chunk removal returned nil, want the backend error")
	}
	// The bare label goes first, so logout still removed the entry the rest of
	// the CLI reads even though the sweep could not finish.
	if _, err := keyring.Get("podium-test", "reg"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("Delete left the bare label in place when the sweep failed: %v", err)
	}
	if err := k.Save("reg", "small"); err == nil {
		t.Errorf("Save with a failing chunk removal returned nil, want the backend error")
	}
}
