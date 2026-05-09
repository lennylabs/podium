package testharness

import (
	"path/filepath"
	"testing"
)

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §15).
// Phase: 0
func TestWriteTree_AndReadTree_RoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	WriteTree(t, root,
		WriteTreeOption{Path: "a/b/c.txt", Content: "first\n"},
		WriteTreeOption{Path: "a/b/d.txt", Content: "second\n"},
		WriteTreeOption{Path: "top.txt", Content: "third\n"},
	)
	got := ReadTree(t, root)
	want := map[string]string{
		"a/b/c.txt": "first\n",
		"a/b/d.txt": "second\n",
		"top.txt":   "third\n",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d", len(got), len(want))
	}
	for path, content := range want {
		if got[path] != content {
			t.Fatalf("%s = %q, want %q", path, got[path], content)
		}
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §15).
// Phase: 0
func TestWriteTree_CreatesIntermediateDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	WriteTree(t, root, WriteTreeOption{
		Path:    "deep/down/in/the/tree/file.txt",
		Content: "ok\n",
	})
	got, err := readFile(filepath.Join(root, "deep/down/in/the/tree/file.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "ok\n" {
		t.Fatalf("got %q, want %q", got, "ok\n")
	}
}

func readFile(path string) (string, error) {
	data, err := readFileImpl(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
