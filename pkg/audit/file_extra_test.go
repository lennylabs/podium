package audit

import (
	"os"
	"path/filepath"
	"testing"
)

// NewFileSink with an explicit path that doesn't exist creates the file.
func TestNewFileSink_CreatesParentDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "audit.log")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if sink == nil {
		t.Fatal("nil sink")
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("Stat dir: %v", err)
	}
}

// NewFileSink recovers the last chain hash from existing entries.
func TestNewFileSink_RecoverFromExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	// Seed a valid JSON-lines entry.
	body := `{"type":"x","timestamp":"2026-01-01T00:00:00Z","hash":"sha256:abc","prev_hash":""}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if sink.lastHash != "sha256:abc" {
		t.Errorf("lastHash = %q, want sha256:abc", sink.lastHash)
	}
}

// NewFileSink tolerates a malformed last line by recovering from the
// most-recent parseable line.
func TestNewFileSink_TolerateMalformedTail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	body := `{"type":"x","timestamp":"2026-01-01T00:00:00Z","hash":"sha256:abc"}` + "\n" +
		"not json\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if sink.lastHash != "sha256:abc" {
		t.Errorf("lastHash = %q, want sha256:abc", sink.lastHash)
	}
}

// lastChainHash with a missing file returns "".
func TestLastChainHash_MissingFile(t *testing.T) {
	t.Parallel()
	got, err := lastChainHash(filepath.Join(t.TempDir(), "absent.log"))
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// SplitLines tolerates trailing newline + missing trailing newline.
func TestSplitLines(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"":                   0,
		"a":                  1,
		"a\nb":               2,
		"a\nb\n":             2,
		"\n\nlast":           3,
		"first\nmiddle\nend": 3,
	}
	for in, want := range cases {
		got := splitLines([]byte(in))
		if len(got) != want {
			t.Errorf("splitLines(%q) = %d lines, want %d", in, len(got), want)
		}
	}
}
