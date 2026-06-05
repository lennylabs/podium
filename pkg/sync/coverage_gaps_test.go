package sync

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests are whitebox (package sync) because they exercise unexported
// helpers (setMapValue, removeSeqValue, splitSegments, configFileScope.String,
// reconcileOrphanConfig, serverUnreachableError) alongside the exported
// ProfileEdit and TreeWatcher surfaces. They cover error paths, empty/nil
// inputs, and edge cases left untested by the per-feature suites.

// mapNode builds a fresh empty YAML mapping node for the profile-edit helpers.
func mapNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// scalar builds a string scalar node.
func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// seqOf builds a sequence node holding the given string scalars.
func seqOf(vals ...string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range vals {
		seq.Content = append(seq.Content, scalar(v))
	}
	return seq
}

// seqValues reads the scalar values out of the sequence stored under key.
func seqValues(parent *yaml.Node, key string) []string {
	seq := findMapValue(parent, key)
	if seq == nil {
		return nil
	}
	out := make([]string, 0, len(seq.Content))
	for _, n := range seq.Content {
		out = append(out, n.Value)
	}
	return out
}

// Spec: §7.5.7 — removeSeqValue drops every entry equal to one of vals from the
// named sequence. An empty vals list, a missing key, and a key whose value is
// not a sequence are all no-ops.
func TestRemoveSeqValue(t *testing.T) {
	t.Parallel()

	t.Run("drops matching entries and keeps the rest", func(t *testing.T) {
		t.Parallel()
		prof := mapNode()
		setMapValue(prof, "include", seqOf("a", "b", "c", "b"))
		removeSeqValue(prof, "include", []string{"b"})
		got := seqValues(prof, "include")
		if !reflect.DeepEqual(got, []string{"a", "c"}) {
			t.Errorf("after removing b: include = %v, want [a c]", got)
		}
	})

	t.Run("removes several distinct values at once", func(t *testing.T) {
		t.Parallel()
		prof := mapNode()
		setMapValue(prof, "exclude", seqOf("x", "y", "z"))
		removeSeqValue(prof, "exclude", []string{"x", "z"})
		got := seqValues(prof, "exclude")
		if !reflect.DeepEqual(got, []string{"y"}) {
			t.Errorf("exclude = %v, want [y]", got)
		}
	})

	t.Run("empty vals is a no-op", func(t *testing.T) {
		t.Parallel()
		prof := mapNode()
		setMapValue(prof, "include", seqOf("a", "b"))
		removeSeqValue(prof, "include", nil)
		got := seqValues(prof, "include")
		if !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Errorf("empty vals changed the sequence: %v", got)
		}
	})

	t.Run("missing key is a no-op", func(t *testing.T) {
		t.Parallel()
		prof := mapNode()
		// No "include" key present; must not panic and must not create one.
		removeSeqValue(prof, "include", []string{"a"})
		if findMapValue(prof, "include") != nil {
			t.Errorf("removeSeqValue created a missing key")
		}
	})

	t.Run("non-sequence value is a no-op", func(t *testing.T) {
		t.Parallel()
		prof := mapNode()
		// "include" is a scalar, not a sequence.
		setMapValue(prof, "include", scalar("oops"))
		removeSeqValue(prof, "include", []string{"oops"})
		if v := findMapValue(prof, "include"); v == nil || v.Value != "oops" {
			t.Errorf("non-sequence value was mutated: %+v", v)
		}
	})
}

// Spec: §7.5.7 — setMapValue replaces the value of an existing key in place and
// appends a new key/value pair when the key is absent.
func TestSetMapValue(t *testing.T) {
	t.Parallel()

	t.Run("appends a new key", func(t *testing.T) {
		t.Parallel()
		m := mapNode()
		setMapValue(m, "a", scalar("1"))
		if len(m.Content) != 2 {
			t.Fatalf("Content len = %d, want 2", len(m.Content))
		}
		if m.Content[0].Value != "a" || m.Content[1].Value != "1" {
			t.Errorf("appended pair = %q:%q, want a:1", m.Content[0].Value, m.Content[1].Value)
		}
	})

	t.Run("replaces an existing key in place", func(t *testing.T) {
		t.Parallel()
		m := mapNode()
		setMapValue(m, "a", scalar("1"))
		setMapValue(m, "b", scalar("2"))
		// Replace a's value; the key/value count must stay the same and b must
		// keep its position.
		setMapValue(m, "a", scalar("99"))
		if len(m.Content) != 4 {
			t.Fatalf("replace grew Content to %d, want 4", len(m.Content))
		}
		if got := findMapValue(m, "a"); got == nil || got.Value != "99" {
			t.Errorf("a = %+v, want value 99", got)
		}
		if got := findMapValue(m, "b"); got == nil || got.Value != "2" {
			t.Errorf("sibling b was disturbed: %+v", got)
		}
	})
}

// Spec: §7.5.7 — ProfileEdit rejects an empty target and an empty profile name
// before touching the filesystem.
func TestProfileEdit_ArgumentValidation(t *testing.T) {
	t.Parallel()

	t.Run("empty target", func(t *testing.T) {
		t.Parallel()
		_, err := ProfileEdit(ProfileEditOptions{Profile: "team"})
		if !errors.Is(err, ErrNoTarget) {
			t.Fatalf("got %v, want ErrNoTarget", err)
		}
	})

	t.Run("empty profile name", func(t *testing.T) {
		t.Parallel()
		_, err := ProfileEdit(ProfileEditOptions{Target: t.TempDir()})
		if err == nil {
			t.Fatalf("expected an error for an empty profile name")
		}
	})

	t.Run("malformed existing sync.yaml surfaces a parse error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		pod := filepath.Join(dir, ".podium")
		if err := os.MkdirAll(pod, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// A YAML scalar where a mapping is expected still parses, so use content
		// that the YAML decoder rejects outright.
		if err := os.WriteFile(filepath.Join(pod, "sync.yaml"), []byte("profiles: [unterminated\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := ProfileEdit(ProfileEditOptions{Target: dir, Profile: "team", AddInclude: []string{"x/**"}}); err == nil {
			t.Fatalf("expected a parse error for malformed sync.yaml")
		}
	})
}

// Spec: §7.5.2 — configFileScope.String renders each precedence scope for the
// collision warning, and an out-of-range value renders as "unknown".
func TestConfigFileScope_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		scope configFileScope
		want  string
	}{
		{scopeUserGlobal, "user-global (~/.podium/sync.yaml)"},
		{scopeProjectShared, "project-shared (.podium/sync.yaml)"},
		{scopeProjectLocal, "project-local (.podium/sync.local.yaml)"},
		{configFileScope(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.scope.String(); got != c.want {
			t.Errorf("configFileScope(%d).String() = %q, want %q", int(c.scope), got, c.want)
		}
	}
}

// Spec: §7.5.1 — splitSegments splits a glob or id on "/" and maps the empty
// string to nil so an empty pattern segment is not treated as a single empty
// segment.
func TestSplitSegments(t *testing.T) {
	t.Parallel()
	if got := splitSegments(""); got != nil {
		t.Errorf("splitSegments(\"\") = %v, want nil", got)
	}
	if got := splitSegments("finance"); !reflect.DeepEqual(got, []string{"finance"}) {
		t.Errorf("splitSegments(\"finance\") = %v, want [finance]", got)
	}
	if got := splitSegments("finance/ap/pay"); !reflect.DeepEqual(got, []string{"finance", "ap", "pay"}) {
		t.Errorf("splitSegments = %v, want [finance ap pay]", got)
	}
	// A leading slash yields a leading empty segment rather than nil.
	if got := splitSegments("/x"); !reflect.DeepEqual(got, []string{"", "x"}) {
		t.Errorf("splitSegments(\"/x\") = %v, want [\"\" x]", got)
	}
}

// Spec: §7.4 — serverUnreachableError wraps the underlying transport error so
// Run can unwrap it with errors.As to apply the degraded-network cache mode.
func TestServerUnreachableError_Unwrap(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("dial tcp: connection refused")
	wrapped := &serverUnreachableError{err: sentinel}

	if wrapped.Error() != sentinel.Error() {
		t.Errorf("Error() = %q, want %q", wrapped.Error(), sentinel.Error())
	}
	if errors.Unwrap(wrapped) != sentinel {
		t.Errorf("errors.Unwrap did not return the wrapped error")
	}
	// errors.Is reaches the sentinel through Unwrap.
	if !errors.Is(wrapped, sentinel) {
		t.Errorf("errors.Is(wrapped, sentinel) = false, want true")
	}
	// errors.As recovers the concrete type, mirroring sync.go's dispatch.
	var target *serverUnreachableError
	if !errors.As(error(wrapped), &target) || target.err != sentinel {
		t.Errorf("errors.As did not recover *serverUnreachableError")
	}
}

// Spec: §6.7 — reconcileOrphanConfig strips Podium's contribution from a config
// file whose last contributing artifact is gone, leaving operator content in
// place. A missing file and an unknown merge kind are no-ops.
func TestReconcileOrphanConfig(t *testing.T) {
	t.Parallel()

	t.Run("missing file is a no-op", func(t *testing.T) {
		t.Parallel()
		full := filepath.Join(t.TempDir(), "settings.json")
		reconcileOrphanConfig(full, "json")
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Errorf("missing file must not be created; stat err = %v", err)
		}
	})

	t.Run("unknown merge kind leaves the file untouched", func(t *testing.T) {
		t.Parallel()
		full := filepath.Join(t.TempDir(), "config")
		original := "operator content\n"
		if err := os.WriteFile(full, []byte(original), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		reconcileOrphanConfig(full, "bogus")
		got, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != original {
			t.Errorf("unknown merge kind rewrote the file: %q", string(got))
		}
	})

	t.Run("json strips the Podium-owned entry and keeps operator keys", func(t *testing.T) {
		t.Parallel()
		full := filepath.Join(t.TempDir(), "settings.json")
		// A Podium-owned entry carries the x-podium-id marker; the operator's
		// theme key must survive the strip.
		content := `{"theme":"dark","hooks":{"x-podium-id":"audit/stop","run":"echo done"}}`
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		reconcileOrphanConfig(full, "json")
		got, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		s := string(got)
		if !contains2(s, `"theme"`) {
			t.Errorf("operator theme key lost: %s", s)
		}
		if contains2(s, "x-podium-id") || contains2(s, "echo done") {
			t.Errorf("Podium-owned entry not stripped: %s", s)
		}
	})

	t.Run("unreadable file logs and returns without writing", func(t *testing.T) {
		t.Parallel()
		// A directory at the config path makes os.ReadFile fail with a
		// non-ENOENT error, exercising the read-error branch. The function logs
		// to stderr and returns; the path is left in place.
		dirAsFile := filepath.Join(t.TempDir(), "settings.json")
		if err := os.Mkdir(dirAsFile, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		reconcileOrphanConfig(dirAsFile, "json")
		if info, err := os.Stat(dirAsFile); err != nil || !info.IsDir() {
			t.Errorf("path should be unchanged; stat err=%v", err)
		}
	})

	t.Run("unwritable file logs the write failure", func(t *testing.T) {
		t.Parallel()
		full := filepath.Join(t.TempDir(), "settings.json")
		// Valid Podium-owned JSON so the strip produces output to write back.
		if err := os.WriteFile(full, []byte(`{"hooks":{"x-podium-id":"a","run":"echo"}}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chmod(full, 0o444); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		// Confirm the read-only mode actually blocks a rewrite in this
		// environment; root (common in CI containers) bypasses the bit.
		if probe := os.WriteFile(full, []byte("x"), 0o644); probe == nil {
			_ = os.Chmod(full, 0o644)
			t.Skip("filesystem permits writing a read-only file; cannot exercise the write-error branch")
		}
		// reconcileOrphanConfig reads (succeeds), strips, then fails to write and
		// logs to stderr without panicking.
		reconcileOrphanConfig(full, "json")
		_ = os.Chmod(full, 0o644)
	})

	t.Run("inject strips Podium blocks and keeps operator prose", func(t *testing.T) {
		t.Parallel()
		full := filepath.Join(t.TempDir(), "AGENTS.md")
		content := "operator intro\n\n<!-- podium:begin:audit -->\nmanaged line\n<!-- podium:end:audit -->\n\noperator outro\n"
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		reconcileOrphanConfig(full, "inject")
		got, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		s := string(got)
		if !contains2(s, "operator intro") || !contains2(s, "operator outro") {
			t.Errorf("operator prose lost: %q", s)
		}
		if contains2(s, "podium:begin") || contains2(s, "managed line") {
			t.Errorf("Podium block not stripped: %q", s)
		}
	})
}

// contains2 is a local substring check to avoid importing strings just for
// these assertions.
func contains2(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// Spec: §13.11.4 / §6.4 — AddTree adds a directory and every subdirectory under
// it to the watcher. A missing root or a non-directory root falls back to
// watching the parent directory so the path's later creation is observed. The
// watcher is in-memory; no events are required, only that AddTree returns
// without error against each input.
func TestTreeWatcher_AddTree(t *testing.T) {
	t.Parallel()

	t.Run("directory root with nested subdirectories", func(t *testing.T) {
		t.Parallel()
		tw, err := NewTreeWatcher()
		if err != nil {
			t.Skipf("fsnotify unavailable in this environment: %v", err)
		}
		defer tw.Close()

		root := t.TempDir()
		nested := filepath.Join(root, "a", "b", "c")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// Walks the tree without panicking; the directories now exist so each is
		// added to the underlying watcher.
		tw.AddTree(root)
	})

	t.Run("missing root falls back to the parent", func(t *testing.T) {
		t.Parallel()
		tw, err := NewTreeWatcher()
		if err != nil {
			t.Skipf("fsnotify unavailable: %v", err)
		}
		defer tw.Close()

		parent := t.TempDir()
		missing := filepath.Join(parent, "does-not-exist")
		// The parent exists, so the fallback watch on it succeeds; the call must
		// not panic on the missing root.
		tw.AddTree(missing)
	})

	t.Run("file root falls back to the parent", func(t *testing.T) {
		t.Parallel()
		tw, err := NewTreeWatcher()
		if err != nil {
			t.Skipf("fsnotify unavailable: %v", err)
		}
		defer tw.Close()

		dir := t.TempDir()
		file := filepath.Join(dir, "f.txt")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		// A regular file is not a directory; AddTree watches its parent instead.
		tw.AddTree(file)
	})

	t.Run("NewTreeWatcher skips empty paths and adds the rest", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		tw, err := NewTreeWatcher("", dir)
		if err != nil {
			t.Skipf("fsnotify unavailable: %v", err)
		}
		defer tw.Close()
		// The empty path was skipped and dir was added; the constructor returning
		// a usable watcher is the assertion.
		if tw.Events() == nil || tw.Errors() == nil {
			t.Errorf("watcher channels are nil")
		}
	})
}
