package filesystem_test

import (
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// errBoom is the failure failFS injects.
var errBoom = errors.New("boom")

// failFS fails every Open of the name fail. It exposes Open alone, so
// fs.Stat, fs.ReadFile, and fs.ReadDir all reach the failure.
type failFS struct {
	fs.FS
	fail string
}

func (f failFS) Open(name string) (fs.File, error) {
	if name == f.fail {
		return nil, errBoom
	}
	return f.FS.Open(name)
}

// Spec: §4.1, §4.7.6 — a type: skill with no SKILL.md is refused per artifact
// under the unparseable-manifest rule, with a reason that names the missing
// file: Walk reports it through OnDrop and keeps the other artifacts, and
// without OnDrop it fails as for any refusal. The refused record still
// carries the package digest of the state unresolved.
func TestWalk_SkillWithNoSkillMDIsRefusedPerArtifact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "ops/runbook/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\n"},
		testharness.WriteTreeOption{Path: "ops/runbook/scripts/run.sh", Content: "echo\n"},
		testharness.WriteTreeOption{Path: "ops/agent/ARTIFACT.md", Content: "---\ntype: agent\nversion: 1.0.0\ndescription: d\n---\n\nBody.\n"},
	)
	reg, err := filesystem.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var drops []layer.Drop
	records, err := reg.Walk(filesystem.WalkOptions{OnDrop: func(d layer.Drop) { drops = append(drops, d) }})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(records) != 1 || records[0].ID != "ops/agent" {
		t.Errorf("records = %v, want ops/agent alone", records)
	}
	if len(drops) != 1 || drops[0].ArtifactID != "ops/runbook" || drops[0].Details["constraint"] != layer.ManifestUnparseableConstraint {
		t.Fatalf("drops = %+v, want one manifest_unparseable drop of ops/runbook", drops)
	}
	if !strings.Contains(drops[0].Reason, "ops/runbook/SKILL.md: manifest: type: skill missing SKILL.md") {
		t.Errorf("reason %q does not name the missing SKILL.md", drops[0].Reason)
	}
	if _, err := reg.Walk(filesystem.WalkOptions{}); err == nil || !strings.Contains(err.Error(), "missing SKILL.md") {
		t.Errorf("Walk without OnDrop = %v, want the refusal", err)
	}

	content, err := filesystem.LayerContent(reg.Layers[0])
	if err != nil {
		t.Fatalf("LayerContent: %v", err)
	}
	want := attest.PackageDigest(attest.Package{Entries: map[string]attest.Sum{
		"ARTIFACT.md":    sumOf("---\ntype: skill\nversion: 1.0.0\n---\n"),
		"scripts/run.sh": sumOf("echo\n"),
	}})
	for _, s := range content.Subjects {
		if s.Name == "ops/runbook" && s.Digest != want {
			t.Errorf("ops/runbook digest = %s, want the unresolved %s", s.Digest, want)
		}
		if s.Name == "ops/runbook@1.0.0" {
			t.Errorf("an unresolved package carries its version in its subject name")
		}
	}
}

func sumOf(s string) attest.Sum {
	return sha256.Sum256([]byte(s))
}

// Spec: §4.7.6 — a package whose canonical ID holds a character the subject
// name rule excludes leaves its layer with no layer digest, and the error
// names the package.
func TestLayerContent_PackageWithNoSubjectName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "ops/my runbook/SKILL.md", Content: "---\nname: my-runbook\ndescription: d\n---\n"},
	)
	_, err := filesystem.LayerContent(filesystem.Layer{ID: "L", Path: root})
	if !errors.Is(err, attest.ErrNoSubjectName) || !strings.Contains(err.Error(), "ops/my runbook") {
		t.Errorf("LayerContent = %v, want ErrNoSubjectName naming the package", err)
	}
	if _, err := filesystem.LayerContent(filesystem.Layer{ID: "L", Path: filepath.Join(root, "absent")}); err == nil {
		t.Errorf("LayerContent of an absent directory = nil, want the walk error")
	}
}

// Spec: §4.7.6, §4.5.1 — the layer files are every DOMAIN.md outside a
// dot-prefixed directory and the root .layer-config, by the tree alone. A
// .layer-config or a DOMAIN.md that is not a regular file is not one, and a
// failure to read the tree fails the list rather than dropping a file.
func TestLayerFiles_TreeRule(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{
		".layer-config":                  {Data: []byte("visibility: {public: true}\n")},
		"DOMAIN.md":                      {Data: []byte("root")},
		"a/DOMAIN.md":                    {Data: []byte("---\n[bad\n---\n")},
		"a/pkg/ARTIFACT.md":              {Data: []byte("---\ntype: rule\n---\n")},
		"a/pkg/DOMAIN.md":                {Data: []byte("in a package")},
		"a/.drafts/DOMAIN.md":            {Data: []byte("hidden")},
		".claude/DOMAIN.md":              {Data: []byte("hidden")},
		"b/DOMAIN.md/notes.txt":          {Data: []byte("a directory named DOMAIN.md")},
		"b/domain.md":                    {Data: []byte("another case")},
		".podium/attestations/x.json":    {Data: []byte("{}")},
		"c/.layer-config":                {Data: []byte("not at the root")},
		"c/README.md":                    {Data: []byte("not a layer file")},
		".podium/attestations/DOMAIN.md": {Data: []byte("hidden")},
	}
	files, err := filesystem.LayerFiles(tree)
	if err != nil {
		t.Fatalf("LayerFiles: %v", err)
	}
	want := map[string]attest.Sum{
		".layer-config":   sumOf("visibility: {public: true}\n"),
		"DOMAIN.md":       sumOf("root"),
		"a/DOMAIN.md":     sumOf("---\n[bad\n---\n"),
		"a/pkg/DOMAIN.md": sumOf("in a package"),
	}
	if len(files) != len(want) {
		t.Errorf("LayerFiles = %v, want %v", keysOf(files), keysOf(want))
	}
	for p, s := range want {
		if files[p] != s {
			t.Errorf("layer file %s missing or with another digest", p)
		}
	}

	dirConfig := fstest.MapFS{".layer-config/x": {Data: []byte("x")}}
	if files, err := filesystem.LayerFiles(dirConfig); err != nil || len(files) != 0 {
		t.Errorf("a .layer-config directory = %v, %v, want no layer file", files, err)
	}
	for _, name := range []string{".layer-config", "a", "a/DOMAIN.md"} {
		if _, err := filesystem.LayerFiles(failFS{FS: tree, fail: name}); !errors.Is(err, errBoom) {
			t.Errorf("a failure on %s = %v, want it returned", name, err)
		}
	}
}

func keysOf(m map[string]attest.Sum) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Spec: §4.7.6 — a package file the walk cannot read fails the walk, so the
// package digest never omits a file.
func TestWalk_UnreadablePackageFileFails(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "ops/a/ARTIFACT.md", Content: "---\ntype: [rule\n---\n"},
		testharness.WriteTreeOption{Path: "ops/a/secret.txt", Content: "x"},
	)
	if err := os.Chmod(filepath.Join(root, "ops", "a", "secret.txt"), 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := filesystem.LayerContent(filesystem.Layer{ID: "L", Path: root}); err == nil {
		t.Errorf("LayerContent over an unreadable file of an unparseable package = nil, want the read error")
	}
}

// Spec: §4.2, §4.4, §4.7.6 — a directory below a dot-prefixed directory of a
// package is no package, because discovery never visits it, so its files are
// bundled resources of the enclosing package even when it carries an
// ARTIFACT.md. A nested directory that discovery finds stays a package of its
// own and keeps its files.
func TestWalk_DotDirectoryWithArtifactMDStaysInThePackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "team/pkg/ARTIFACT.md", Content: "---\ntype: rule\nversion: 1.0.0\ndescription: d\nrule_mode: always\n---\n\nBody.\n"},
		testharness.WriteTreeOption{Path: "team/pkg/.claude/ARTIFACT.md", Content: "---\ntype: rule\n---\n"},
		testharness.WriteTreeOption{Path: "team/pkg/.claude/settings.json", Content: "{}\n"},
		testharness.WriteTreeOption{Path: "team/pkg/.cfg/inner/ARTIFACT.md", Content: "x"},
		testharness.WriteTreeOption{Path: "team/pkg/.cfg/inner/data.txt", Content: "y"},
		testharness.WriteTreeOption{Path: "team/pkg/sub/ARTIFACT.md", Content: "---\ntype: rule\nversion: 1.0.0\ndescription: d\nrule_mode: always\n---\n\nBody.\n"},
		testharness.WriteTreeOption{Path: "team/pkg/sub/x.txt", Content: "z"},
	)
	reg := &filesystem.Registry{Layers: []filesystem.Layer{{ID: "L", Path: root}}}
	records, err := reg.Walk(filesystem.WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	byID := map[string]filesystem.ArtifactRecord{}
	for _, r := range records {
		byID[r.ID] = r
	}
	outer, inner := byID["team/pkg"], byID["team/pkg/sub"]
	for _, p := range []string{".claude/ARTIFACT.md", ".claude/settings.json", ".cfg/inner/ARTIFACT.md", ".cfg/inner/data.txt"} {
		if _, ok := outer.Resources[p]; !ok {
			t.Errorf("team/pkg resources %v lack %s", keysOfBytes(outer.Resources), p)
		}
	}
	if _, ok := outer.Resources["sub/x.txt"]; ok {
		t.Errorf("team/pkg bundles the file of its nested package team/pkg/sub")
	}
	if _, ok := inner.Resources["x.txt"]; !ok {
		t.Errorf("team/pkg/sub resources = %v, want x.txt", keysOfBytes(inner.Resources))
	}
}

func keysOfBytes(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Spec: §4.7.6 — a package that holds a file whose path is not valid UTF-8
// has no subject name, whether it resolves or not, because no Unicode reader
// reproduces the framing of the path. Its package digest is still recorded.
func TestNewPackageRecord_NonUTF8PathHasNoSubjectName(t *testing.T) {
	t.Parallel()
	for _, manifestMD := range []string{"---\ntype: rule\nversion: 1.0.0\n---\nBody.\n", "---\ntype: [rule\n---\n"} {
		rec := filesystem.NewPackageRecord("ops/a", filesystem.Layer{ID: "L"}, true, false, map[string][]byte{
			"ARTIFACT.md":  []byte(manifestMD),
			"ok.txt":       []byte("x"),
			"bad-\xff.txt": []byte("y"),
		})
		if _, err := rec.SubjectName(); !errors.Is(err, attest.ErrNoSubjectName) {
			t.Errorf("SubjectName of %q = %v, want ErrNoSubjectName", manifestMD, err)
		}
		if rec.PackageDigest == "" {
			t.Errorf("no package digest recorded for %q", manifestMD)
		}
	}
	rec := filesystem.NewPackageRecord("ops/a", filesystem.Layer{ID: "L"}, true, false, map[string][]byte{
		"ARTIFACT.md": []byte("---\ntype: rule\nversion: 1.0.0\n---\nBody.\n"),
		"é.txt":       []byte("y"),
	})
	if name, err := rec.SubjectName(); err != nil || name != "ops/a@1.0.0" {
		t.Errorf("SubjectName with a UTF-8 path = %q, %v", name, err)
	}
}

// Spec: §4.2 — discovery visits the root and every directory below it whose
// path has no dot-prefixed segment.
func TestDiscoverable(t *testing.T) {
	t.Parallel()
	for dir, want := range map[string]bool{
		".": true, "": true, "a": true, "a/b.c/d": true,
		".claude": false, "a/.claude": false, "a/.cfg/inner": false,
	} {
		if got := filesystem.Discoverable(dir); got != want {
			t.Errorf("Discoverable(%q) = %v, want %v", dir, got, want)
		}
	}
}
