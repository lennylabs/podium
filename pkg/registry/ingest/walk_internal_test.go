package ingest

import (
	"strings"
	"testing"
	"testing/fstest"
)

const ctxArtifactInternal = "---\n" +
	"type: context\n" +
	"version: 1.0.0\n" +
	"description: d\n" +
	"sensitivity: low\n" +
	"---\n\nbody\n"

// TestLoadOne_RootLevelRejected covers F-4.2.1.
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

// TestLoadOne_AtSegmentRejected covers F-4.2.2.
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

// TestLoadOne_NestedArtifactNotCaptured covers F-4.2.3.
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
