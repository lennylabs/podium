package objectstore

import (
	"context"
	"errors"
	"io"
	"testing"
)

// GetStream and Stat are the §7.2 data-plane streaming reads. The test
// covers the Memory and Filesystem backends (S3 needs a live endpoint):
// a stored object streams its exact bytes and reports its metadata, and
// a missing key surfaces ErrNotFound from both methods.
func TestGetStreamAndStat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	body := []byte("streamed bundled resource bytes")

	backends := map[string]Provider{
		"memory":     NewMemory(),
		"filesystem": mustOpenFS(t),
	}
	for name, store := range backends {
		store := store
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := store.Put(ctx, "deadbeef", body, "text/plain"); err != nil {
				t.Fatalf("Put: %v", err)
			}

			// Stat reports size and content type without reading the body.
			info, err := store.Stat(ctx, "deadbeef")
			if err != nil {
				t.Fatalf("Stat: %v", err)
			}
			if info.Size != int64(len(body)) {
				t.Errorf("Stat Size = %d, want %d", info.Size, len(body))
			}
			if info.ContentType != "text/plain" {
				t.Errorf("Stat ContentType = %q, want text/plain", info.ContentType)
			}

			// GetStream returns the exact bytes and the same metadata.
			rc, sInfo, err := store.GetStream(ctx, "deadbeef")
			if err != nil {
				t.Fatalf("GetStream: %v", err)
			}
			got, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if string(got) != string(body) {
				t.Errorf("streamed bytes = %q, want %q", got, body)
			}
			if sInfo.Size != int64(len(body)) {
				t.Errorf("GetStream Size = %d, want %d", sInfo.Size, len(body))
			}

			// A missing key is ErrNotFound from both methods.
			if _, _, err := store.GetStream(ctx, "0000"); !errors.Is(err, ErrNotFound) {
				t.Errorf("GetStream(missing) err = %v, want ErrNotFound", err)
			}
			if _, err := store.Stat(ctx, "0000"); !errors.Is(err, ErrNotFound) {
				t.Errorf("Stat(missing) err = %v, want ErrNotFound", err)
			}
		})
	}
}

func mustOpenFS(t *testing.T) *Filesystem {
	t.Helper()
	fs, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return fs
}
