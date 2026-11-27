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
