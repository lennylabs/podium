package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.2, §4.4, §7.3.1 — PackageManifests reports each manifest only when
// the directory directly holds it as a regular file, so a directory named
// SKILL.md or ARTIFACT.md is not a manifest and a manifest below dir is not
// reported for dir.
func TestPackageManifests_ReportsDirectRegularManifests(t *testing.T) {
	t.Parallel()
	file := &fstest.MapFile{Data: []byte("x")}
	fsys := fstest.MapFS{
		"artifact-only/ARTIFACT.md":     file,
		"skill-only/SKILL.md":           file,
		"both/ARTIFACT.md":              file,
		"both/SKILL.md":                 file,
		"dir-named/SKILL.md/notes.md":   file,
		"dir-named/ARTIFACT.md/data.md": file,
		"neither/notes.md":              file,
		"neither/deeper/SKILL.md":       file,
		"SKILL.md":                      file,
	}
	cases := []struct {
		dir             string
		artifact, skill bool
	}{
		{dir: "artifact-only", artifact: true},
		{dir: "skill-only", skill: true},
		{dir: "both", artifact: true, skill: true},
		{dir: "dir-named"},
		{dir: "neither"},
		{dir: "missing"},
		{dir: ".", skill: true},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()
			artifact, skill, err := PackageManifests(fsys, tc.dir)
			if err != nil {
				t.Fatalf("PackageManifests(%q): %v", tc.dir, err)
			}
			if artifact != tc.artifact || skill != tc.skill {
				t.Errorf("PackageManifests(%q) = (%v, %v), want (%v, %v)",
					tc.dir, artifact, skill, tc.artifact, tc.skill)
			}
		})
	}
}

// errStatFS fails Stat for one name with a sentinel and delegates every other
// call to the wrapped MapFS.
type errStatFS struct {
	fstest.MapFS
	failName string
	err      error
}

func (f errStatFS) Stat(name string) (fs.FileInfo, error) {
	if name == f.failName {
		return nil, f.err
	}
	return f.MapFS.Stat(name)
}

// Spec: §4.2, §7.3.1 — a stat failure other than fs.ErrNotExist is returned
// rather than read as an absent manifest, so a confined source that refuses a
// manifest fails the layer instead of dropping the package.
func TestPackageManifests_ReturnsStatError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("stat refused")
	file := &fstest.MapFile{Data: []byte("x")}
	for _, failName := range []string{"x/ARTIFACT.md", "x/SKILL.md"} {
		t.Run(failName, func(t *testing.T) {
			t.Parallel()
			fsys := errStatFS{
				MapFS:    fstest.MapFS{"x/ARTIFACT.md": file, "x/SKILL.md": file},
				failName: failName,
				err:      sentinel,
			}
			artifact, skill, err := PackageManifests(fsys, "x")
			if !errors.Is(err, sentinel) {
				t.Fatalf("err = %v, want the stat sentinel", err)
			}
			if artifact || skill {
				t.Errorf("PackageManifests = (%v, %v) with an error, want (false, false)", artifact, skill)
			}
		})
	}
}

// Spec: §4.2 — a directory holding SKILL.md alone is a package only when no
// directory strictly between it and the layer root is one.
func TestWithinPackage(t *testing.T) {
	t.Parallel()
	packages := map[string]bool{"outer": true, "group/pkg": true}
	cases := []struct {
		dir  string
		want bool
	}{
		{dir: "outer", want: false},
		{dir: "outer/references", want: true},
		{dir: "outer/a/b", want: true},
		{dir: "group", want: false},
		{dir: "group/x", want: false},
		{dir: "group/pkg/sub", want: true},
		{dir: "other", want: false},
		{dir: ".", want: false},
	}
	for _, tc := range cases {
		if got := WithinPackage(packages, tc.dir); got != tc.want {
			t.Errorf("WithinPackage(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

// Spec: §4.2, §7.3.1 — a manifest stat the filesystem refuses fails the walk
// rather than dropping the package, both at a package directory and at a
// nested-package boundary check inside a package.
func TestWalk_ManifestStatFailureFailsTheWalk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	t.Parallel()
	cases := []struct {
		name   string
		tree   []testharness.WriteTreeOption
		locked string
	}{
		{
			name:   "package directory",
			tree:   []testharness.WriteTreeOption{{Path: "x/SKILL.md", Content: "---\nname: x\ndescription: d\n---\n\nbody\n"}},
			locked: "x",
		},
		{
			name: "nested boundary",
			tree: []testharness.WriteTreeOption{
				{Path: "x/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\ndescription: d\n---\n\nbody\n"},
				{Path: "x/sub/notes.md", Content: "notes\n"},
			},
			locked: "x/sub",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			testharness.WriteTree(t, root, tc.tree...)
			locked := filepath.Join(root, filepath.FromSlash(tc.locked))
			// Read without search permission lists the directory but refuses
			// a stat of anything inside it.
			if err := os.Chmod(locked, 0o600); err != nil {
				t.Fatalf("Chmod: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
			reg, err := Open(root)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, err := reg.Walk(WalkOptions{}); !errors.Is(err, fs.ErrPermission) {
				t.Errorf("Walk error = %v, want a permission failure", err)
			}
		})
	}
}
