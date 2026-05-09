package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Filesystem stores objects under a configured root directory.
// Bytes are served back to consumers via the registry's authenticated
// /objects/{key} route; the URL Presign returns has no embedded
// signature or expiry — the consumer's session token is what
// authorizes the read (§13.10 spec clarification).
//
// Filesystem is safe for concurrent use. Each Put writes to a tmp
// file and atomic-renames so partial writes don't surface to readers.
type Filesystem struct {
	// Root is the directory objects are stored in. Required.
	Root string
	// BaseURL is the public URL prefix the registry serves
	// /objects/{key} under. Presign returns BaseURL+"/objects/"+key.
	// Required when Presign is used; tests may leave it empty if
	// they only exercise Put/Get.
	BaseURL string
	// ContentTypes overrides the default per-object content-type
	// metadata. Filesystem stores the type alongside the body in a
	// sidecar so Get can return it; this field is unused after Open.
	mu sync.Mutex
}

// Open returns a Filesystem rooted at dir, creating the directory
// (and parents) if missing.
func Open(dir string) (*Filesystem, error) {
	if dir == "" {
		return nil, fmt.Errorf("objectstore.filesystem: Root is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &Filesystem{Root: dir}, nil
}

// ID returns "filesystem".
func (f *Filesystem) ID() string { return "filesystem" }

// Put writes body to <Root>/<key> via tmp + rename. Re-Putting the
// same key with identical bytes is idempotent; re-Putting with
// different bytes is rejected to preserve the §4.7 invariant that
// the canonical key (content hash) maps to one body forever.
func (f *Filesystem) Put(_ context.Context, key string, body []byte, contentType string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	path := filepath.Join(f.Root, key)
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, body) {
			return fmt.Errorf("objectstore.filesystem: key %q already exists with different body", key)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if contentType != "" {
		if err := os.WriteFile(path+".ct", []byte(contentType), 0o644); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, path)
}

// Get reads the body for key. Returns ErrNotFound when missing.
func (f *Filesystem) Get(_ context.Context, key string) ([]byte, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(f.Root, key))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return body, err
}

// ContentTypeOf returns the recorded content type for key, or "" when
// none was supplied at Put time. Used by the registry's HTTP route
// to set the correct Content-Type header on responses.
func (f *Filesystem) ContentTypeOf(key string) string {
	body, err := os.ReadFile(filepath.Join(f.Root, key+".ct"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// Presign returns a URL of the form BaseURL/objects/key. The URL
// has no embedded signature; consumers send the same session token
// they used for load_artifact. ttl is ignored (filesystem has no
// clock-bound TTL).
func (f *Filesystem) Presign(_ context.Context, key string, _ time.Duration) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if f.BaseURL == "" {
		return "", fmt.Errorf("objectstore.filesystem: BaseURL is required to presign")
	}
	return strings.TrimRight(f.BaseURL, "/") + "/objects/" + key, nil
}

// Delete removes the object. Missing key is a no-op.
func (f *Filesystem) Delete(_ context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := filepath.Join(f.Root, key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(path + ".ct")
	return nil
}
