package objectstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/objectstore"
)

// suite runs the conformance set against any Provider. Backends call
// this from their package tests so Memory, Filesystem, and S3 all
// satisfy the same contract.
type factory func(t *testing.T) objectstore.Provider

func runSuite(t *testing.T, name string, f factory) {
	t.Helper()
	t.Run(name+"/PutGetRoundTrip", func(t *testing.T) { putGetRoundTrip(t, f(t)) })
	t.Run(name+"/PutIdempotentSameBytes", func(t *testing.T) { putIdempotentSameBytes(t, f(t)) })
	t.Run(name+"/PutRejectsConflictingBytes", func(t *testing.T) { putRejectsConflictingBytes(t, f(t)) })
	t.Run(name+"/GetMissingReturnsErrNotFound", func(t *testing.T) { getMissingReturnsErrNotFound(t, f(t)) })
	t.Run(name+"/DeleteRemovesObject", func(t *testing.T) { deleteRemovesObject(t, f(t)) })
	t.Run(name+"/DeleteMissingIsNoop", func(t *testing.T) { deleteMissingIsNoop(t, f(t)) })
	t.Run(name+"/RejectsEmptyKey", func(t *testing.T) { rejectsEmptyKey(t, f(t)) })
	t.Run(name+"/RejectsPathTraversal", func(t *testing.T) { rejectsPathTraversal(t, f(t)) })
}

// Spec: §4.1 — Put + Get round-trip preserves bytes verbatim.
func putGetRoundTrip(t *testing.T, p objectstore.Provider) {
	t.Helper()
	body := []byte("podium artifact resource bytes")
	if err := p.Put(context.Background(), "obj-1", body, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := p.Get(context.Background(), "obj-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("Get returned %q, want %q", got, body)
	}
}

// Spec: §4.7 immutability — re-Putting the same key with identical
// bytes is idempotent (handles ingest retries cleanly).
func putIdempotentSameBytes(t *testing.T, p objectstore.Provider) {
	t.Helper()
	body := []byte("idempotent")
	for i := 0; i < 3; i++ {
		if err := p.Put(context.Background(), "obj-2", body, "text/plain"); err != nil {
			t.Fatalf("Put #%d: %v", i, err)
		}
	}
}

// Spec: §4.7 immutability — re-Putting the same key with different
// bytes fails. The canonical key is the content hash; same key with
// different bytes signals corruption upstream and must surface.
func putRejectsConflictingBytes(t *testing.T, p objectstore.Provider) {
	t.Helper()
	if err := p.Put(context.Background(), "obj-3", []byte("alpha"), ""); err != nil {
		t.Fatalf("Put alpha: %v", err)
	}
	err := p.Put(context.Background(), "obj-3", []byte("beta"), "")
	if err == nil {
		t.Fatalf("Put beta on existing key should fail")
	}
}

// Spec: §6.10 — Get on a missing key returns ErrNotFound.
func getMissingReturnsErrNotFound(t *testing.T, p objectstore.Provider) {
	t.Helper()
	_, err := p.Get(context.Background(), "missing-key")
	if !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// Spec: §8.5 — Delete removes the object so subsequent Gets surface
// ErrNotFound.
func deleteRemovesObject(t *testing.T, p objectstore.Provider) {
	t.Helper()
	if err := p.Put(context.Background(), "obj-4", []byte("body"), ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := p.Delete(context.Background(), "obj-4"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := p.Get(context.Background(), "obj-4"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// Delete on a missing key is a no-op (handles double-erasure cleanly).
func deleteMissingIsNoop(t *testing.T, p objectstore.Provider) {
	t.Helper()
	if err := p.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
}

// Empty keys are rejected with ErrInvalidKey.
func rejectsEmptyKey(t *testing.T, p objectstore.Provider) {
	t.Helper()
	if err := p.Put(context.Background(), "", []byte("x"), ""); !errors.Is(err, objectstore.ErrInvalidKey) {
		t.Errorf("got %v, want ErrInvalidKey", err)
	}
}

// Path-traversal keys are rejected so Filesystem cannot escape its
// root and S3 cannot violate bucket boundaries.
func rejectsPathTraversal(t *testing.T, p objectstore.Provider) {
	t.Helper()
	if err := p.Put(context.Background(), "../escape", []byte("x"), ""); !errors.Is(err, objectstore.ErrInvalidKey) {
		t.Errorf("got %v, want ErrInvalidKey", err)
	}
}

// Spec: §4.1 — InlineCutoff matches the documented 256 KB threshold.
// Phase: 2
func TestInlineCutoff_Matches256KB(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	if objectstore.InlineCutoff != 256*1024 {
		t.Errorf("InlineCutoff = %d, want %d", objectstore.InlineCutoff, 256*1024)
	}
}

// Spec: §6.2 — DefaultPresignTTL matches PODIUM_PRESIGN_TTL_SECONDS
// default (3600s).
// Phase: 2
func TestDefaultPresignTTL_Matches3600s(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	if objectstore.DefaultPresignTTL != 3600*time.Second {
		t.Errorf("DefaultPresignTTL = %v, want 3600s", objectstore.DefaultPresignTTL)
	}
}

// Spec: §9.1 — Memory backend satisfies the SPI conformance contract.
// Phase: 2
func TestMemory_Conformance(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	runSuite(t, "Memory", func(t *testing.T) objectstore.Provider {
		return objectstore.NewMemory()
	})
}

// Spec: §13.10 — Filesystem backend satisfies the SPI conformance
// contract.
// Phase: 2
func TestFilesystem_Conformance(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	runSuite(t, "Filesystem", func(t *testing.T) objectstore.Provider {
		fs, err := objectstore.Open(filepath.Join(t.TempDir(), "obj"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		fs.BaseURL = "https://test.example"
		return fs
	})
}

// Spec: §13.10 — Filesystem.Presign returns BaseURL/objects/<key>
// with no embedded signature or expiry.
// Phase: 2
func TestFilesystem_PresignBearsNoSignature(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	fs, err := objectstore.Open(filepath.Join(t.TempDir(), "obj"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	fs.BaseURL = "https://example.test"
	if err := fs.Put(context.Background(), "abcd", []byte("body"), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	url, err := fs.Presign(context.Background(), "abcd", 5*time.Minute)
	if err != nil {
		t.Fatalf("Presign: %v", err)
	}
	want := "https://example.test/objects/abcd"
	if url != want {
		t.Errorf("Presign = %q, want %q", url, want)
	}
	if strings.Contains(url, "?") {
		t.Errorf("Presign URL should carry no query parameters; got %q", url)
	}
}

// Spec: §4.1 — Filesystem.ContentTypeOf returns the type passed to
// Put so the HTTP route can set Content-Type on responses.
// Phase: 2
func TestFilesystem_ContentTypeRoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	fs, err := objectstore.Open(filepath.Join(t.TempDir(), "obj"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := fs.Put(context.Background(), "img", []byte{0x89, 0x50, 0x4e, 0x47}, "image/png"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got := fs.ContentTypeOf("img"); got != "image/png" {
		t.Errorf("ContentTypeOf = %q, want image/png", got)
	}
}

// Spec: §13.10 — Filesystem.Presign requires BaseURL to be set;
// callers without a configured BaseURL get a clear error.
// Phase: 2
func TestFilesystem_PresignRequiresBaseURL(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	fs, err := objectstore.Open(filepath.Join(t.TempDir(), "obj"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = fs.Put(context.Background(), "abcd", []byte("body"), "")
	if _, err := fs.Presign(context.Background(), "abcd", 0); err == nil {
		t.Fatalf("Presign without BaseURL should fail")
	}
}
