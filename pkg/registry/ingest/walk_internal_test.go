package ingest

import (
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/version"
)

const ctxArtifactInternal = "---\n" +
	"type: context\n" +
	"version: 1.0.0\n" +
	"description: d\n" +
	"sensitivity: low\n" +
	"---\n\nbody\n"

// TestLoadOne_RootLevelRejected covers
// spec: §4.2 — loadOne rejects a root-level ARTIFACT.md (empty canonical ID)
// the same way the filesystem-source registry does, so both ingest paths
// share the invariant that an artifact has an addressable canonical home.
func TestLoadOne_RootLevelRejected(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"ARTIFACT.md": &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
	}
	if _, err := loadOne(fsys, "ARTIFACT.md", "layer"); err == nil {
		t.Fatalf("loadOne accepted a root-level ARTIFACT.md (empty canonical ID)")
	} else if !strings.Contains(err.Error(), "subdirectory") {
		t.Errorf("error %q missing 'subdirectory'", err)
	}
}

// TestLoadOne_AtSegmentRejected covers
// spec: §4.2 — "@" is reserved for the @version/@sha256 reference suffix, so a
// directory name containing "@" is an invalid canonical-ID segment.
func TestLoadOne_AtSegmentRejected(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"finance/pay@v2/ARTIFACT.md": &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
	}
	if _, err := loadOne(fsys, "finance/pay@v2/ARTIFACT.md", "layer"); err == nil {
		t.Fatalf("loadOne accepted '@' in a canonical-ID segment")
	} else if !strings.Contains(err.Error(), "@") {
		t.Errorf("error %q missing '@'", err)
	}
}

// TestLoadOne_NestedArtifactNotCaptured covers
// spec: §4.2/§4.4 — the ingest resource walk stops at a nested artifact
// boundary, so a child artifact's files are not captured as the parent's
// bundled resources.
func TestLoadOne_NestedArtifactNotCaptured(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"outer/ARTIFACT.md":       &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
		"outer/notes.md":          &fstest.MapFile{Data: []byte("outer notes")},
		"outer/inner/ARTIFACT.md": &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
		"outer/inner/data.txt":    &fstest.MapFile{Data: []byte("inner data")},
	}
	rec, err := loadOne(fsys, "outer/ARTIFACT.md", "layer")
	if err != nil {
		t.Fatalf("loadOne: %v", err)
	}
	if _, ok := rec.Resources["notes.md"]; !ok {
		t.Errorf("outer missing its own resource notes.md (got %v)", mapKeys(rec.Resources))
	}
	for k := range rec.Resources {
		if strings.HasPrefix(k, "inner/") {
			t.Errorf("outer captured nested-artifact file %q as a resource", k)
		}
	}
}

func mapKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLoadOne_MalformedFrontmatterIsInvalidArtifact covers
// spec: §7.3.1 — a manifest the parser cannot decode is a fault in the layer's
// content, so the walk error carries ErrInvalidArtifact. The server maps that
// sentinel to a non-retryable ingest refusal; without it the failure falls
// through to the unclassified default, which reports the registry as
// unavailable and advises a retry that repeats the same parse failure.
func TestLoadOne_MalformedFrontmatterIsInvalidArtifact(t *testing.T) {
	t.Parallel()
	const malformed = "---\n" +
		"type: context\n" +
		"version: 1.0.0\n" +
		"description: d\n" +
		"weird: [unclosed\n" +
		"---\n\nbody\n"
	fsys := fstest.MapFS{
		"x/y/ARTIFACT.md": &fstest.MapFile{Data: []byte(malformed)},
	}
	_, err := loadOne(fsys, "x/y/ARTIFACT.md", "layer")
	if err == nil {
		t.Fatalf("loadOne accepted malformed YAML frontmatter")
	}
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Errorf("error %v does not wrap ErrInvalidArtifact", err)
	}
	if !errors.Is(err, manifest.ErrInvalidYAML) {
		t.Errorf("error %v lost the underlying parse cause", err)
	}
	if !strings.Contains(err.Error(), "x/y") {
		t.Errorf("error %q does not name the offending artifact", err)
	}
}

const skillArtifactInternal = "---\n" +
	"type: skill\n" +
	"version: 1.0.0\n" +
	"---\n\n"

const skillBodyInternal = "---\n" +
	"name: outer\n" +
	"description: Outer skill\n" +
	"---\n\nBody for outer.\n"

// resourceSetTree is the tree both walks are compared over: a CRLF body, a
// dot-prefixed file, a dot-prefixed directory with no manifest, a SKILL.md
// below the package root, a nested package, and a dot-prefixed nested
// package.
var resourceSetTree = []testharness.WriteTreeOption{
	{Path: "outer/ARTIFACT.md", Content: skillArtifactInternal},
	{Path: "outer/SKILL.md", Content: skillBodyInternal},
	{Path: "outer/notes.md", Content: "line one\r\nline two\r\n"},
	{Path: "outer/.hidden-note", Content: "hidden note body\n"},
	{Path: "outer/.tooling/config.json", Content: "{\"tool\":\"config\"}\n"},
	{Path: "outer/references/SKILL.md", Content: "reference skill body\n"},
	{Path: "outer/inner/ARTIFACT.md", Content: ctxArtifactInternal},
	{Path: "outer/inner/data.txt", Content: "inner data\n"},
	{Path: "outer/.nested/ARTIFACT.md", Content: ctxArtifactInternal},
	{Path: "outer/.nested/note.txt", Content: "nested note\n"},
}

// TestLoadOne_ResourceSetMatchesTheFilesystemWalk covers
// spec: §4.4, §4.7.6 — the digest's resource set has two implementations,
// loadOne here and captureResources in pkg/registry/filesystem, and §4.7.6
// requires every party to compute one digest for a package. The two maps and
// the two digests are compared over one tree, so a filter, a rename, or a
// normalization added to one walk and not the other fails here rather than
// surfacing as a content_hash disagreement between a filesystem-source
// consumer and the registry.
func TestLoadOne_ResourceSetMatchesTheFilesystemWalk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root, resourceSetTree...)

	ingestRec, err := loadOne(os.DirFS(root), "outer/ARTIFACT.md", "L")
	if err != nil {
		t.Fatalf("loadOne: %v", err)
	}
	reg, err := filesystem.Open(root)
	if err != nil {
		t.Fatalf("filesystem.Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{})
	if err != nil {
		t.Fatalf("filesystem Walk: %v", err)
	}
	var fsRec filesystem.ArtifactRecord
	var found bool
	for _, r := range records {
		if r.ID == "outer" {
			fsRec, found = r, true
		}
	}
	if !found {
		t.Fatalf("filesystem walk produced no outer record")
	}

	want := map[string]string{
		"notes.md":             "line one\r\nline two\r\n",
		".hidden-note":         "hidden note body\n",
		".tooling/config.json": "{\"tool\":\"config\"}\n",
		"references/SKILL.md":  "reference skill body\n",
	}
	assertResourceSet(t, "ingest walk", ingestRec.Resources, want)
	assertResourceSet(t, "filesystem walk", fsRec.Resources, want)

	fsHash := version.CanonicalContentHash(fsRec.ArtifactBytes, fsRec.SkillBytes, fsRec.Resources)
	if got := contentHashOf(ingestRec); got != fsHash {
		t.Errorf("ingest content hash %s, filesystem walk %s: the two walks disagree on the §4.7.6 digest", got, fsHash)
	}
}

func assertResourceSet(t *testing.T, who string, got map[string][]byte, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s resource keys = %v, want %d keys", who, mapKeys(got), len(want))
	}
	for k, v := range want {
		body, ok := got[k]
		if !ok {
			t.Errorf("%s missing resource %q (got %v)", who, k, mapKeys(got))
			continue
		}
		if string(body) != v {
			t.Errorf("%s resource %q = %q, want %q", who, k, body, v)
		}
	}
}
