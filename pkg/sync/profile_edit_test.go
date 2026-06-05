package sync_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §7.5.7 — `podium profile edit` round-trips sync.yaml through a
// comment-preserving YAML parser, so comments and formatting around the edited
// keys survive. Before the fix a plain yaml.Marshal stripped them.
func TestProfileEdit_PreservesComments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filepath.Join(dir, ".podium")
	if err := os.MkdirAll(pod, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := `# top of file comment
defaults:
  registry: https://podium.acme.com # registry endpoint
  harness: claude-code

profiles:
  # finance team scope
  finance:
    include:
      - "finance/**" # all of finance
`
	path := filepath.Join(pod, "sync.yaml")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target:     dir,
		Profile:    "finance",
		AddInclude: []string{"shared/policies/*"},
	})
	if err != nil {
		t.Fatalf("ProfileEdit: %v", err)
	}
	if !res.Wrote {
		t.Fatalf("expected Wrote=true")
	}

	out := readString(t, path)
	for _, want := range []string{
		"# top of file comment",
		"# registry endpoint",
		"# finance team scope",
		"# all of finance",
		"shared/policies/*",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("edited file missing %q:\n%s", want, out)
		}
	}
}

// Spec: §7.5.7 — editing a profile in a file that does not exist yet creates it
// with the named profile and an empty defaults: block.
func TestProfileEdit_CreatesFileWithDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target:     dir,
		Profile:    "newteam",
		AddInclude: []string{"x/**"},
	})
	if err != nil {
		t.Fatalf("ProfileEdit: %v", err)
	}
	if len(res.Profile.Include) != 1 || res.Profile.Include[0] != "x/**" {
		t.Errorf("profile include = %v", res.Profile.Include)
	}
	out := readString(t, filepath.Join(dir, ".podium", "sync.yaml"))
	if !strings.Contains(out, "defaults:") || !strings.Contains(out, "newteam") {
		t.Errorf("created file missing defaults/profile:\n%s", out)
	}
}

// Spec: §7.5.7 — --dry-run computes the resulting profile without writing.
func TestProfileEdit_DryRunDoesNotWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target:     dir,
		Profile:    "team",
		AddInclude: []string{"x/**"},
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("ProfileEdit: %v", err)
	}
	if res.Wrote {
		t.Errorf("dry-run must not write")
	}
	if _, err := os.Stat(filepath.Join(dir, ".podium", "sync.yaml")); err == nil {
		t.Errorf("dry-run created a file")
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
