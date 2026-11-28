package sign

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/tufharness"
)

// rootWithVersion returns the TUF root raw with its signed version set to v
// and its signatures kept, so it no longer verifies.
func rootWithVersion(t *testing.T, raw []byte, v int) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["signed"].(map[string]any)["version"] = v
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Spec: §4.7.11 (Trusted root refresh) — the cached TUF root is the embedded
// one, or its next version signed by it; an unsigned next version, a root
// two versions ahead, and a missing root do not verify.
func TestCachedTUFRoot(t *testing.T) {
	t.Parallel()
	repo := tufharness.New(t, []byte(`{}`))
	embedded := repo.RootJSON()
	var m map[string]any
	_ = json.Unmarshal(embedded, &m)
	v := int(m["signed"].(map[string]any)["version"].(float64))
	read := func(b []byte) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return b, nil }
	}
	if r, err := cachedTUFRoot(embedded, read(embedded)); err != nil || int(r.Signed.Version) != v {
		t.Fatalf("the embedded root: %v", err)
	}
	same := rootWithVersion(t, embedded, v)
	if _, err := cachedTUFRoot(embedded, read(same)); err != nil {
		t.Errorf("a re-encoded embedded version: %v", err)
	}
	if _, err := cachedTUFRoot(embedded, read(rootWithVersion(t, embedded, v+1))); err == nil || !strings.Contains(err.Error(), "cached TUF root") {
		t.Errorf("an unsigned next version: %v", err)
	}
	if _, err := cachedTUFRoot(embedded, read(rootWithVersion(t, embedded, v+2))); err == nil || !strings.Contains(err.Error(), "one step") {
		t.Errorf("a root two versions ahead: %v", err)
	}
	if _, err := cachedTUFRoot(embedded, func(string) ([]byte, error) { return nil, os.ErrNotExist }); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing root: %v", err)
	}
	if _, err := cachedTUFRoot([]byte("{"), read(embedded)); err == nil {
		t.Error("an embedded root that does not parse was used")
	}
	if err := metaMatches(nil, 1, nil); err == nil {
		t.Error("a missing meta entry matched")
	}
}

// Spec: §4.7.11 (Root copy of user-source rules) — the in-memory TUF cache
// path is absolute on every platform, so the client never reads metadata
// relative to the working directory.
func TestImpossibleDirIsAbsolute(t *testing.T) {
	t.Parallel()
	if !filepath.IsAbs(impossibleDir) {
		t.Fatalf("impossibleDir %q is relative", impossibleDir)
	}
	if _, err := os.Stat(impossibleDir); err == nil {
		t.Fatalf("impossibleDir %q exists", impossibleDir)
	}
}
