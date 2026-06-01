package sync

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/version"
)

// spec: §7.5.3, §14.11 — the lock file pins each artifact's content_hash so the
// committed lock captures reproducible (id, version, content_hash) triples.
// contentHashFor hashes the served content bytes (a skill's frontmatter+body
// when present) plus resources in sorted key order, in the "sha256:<hex>" form.
func TestContentHashFor_SkillBytesAndResources(t *testing.T) {
	t.Parallel()
	skill := []byte("---\nname: pay\n---\nbody")
	rec := materialRecord{
		ID:         "finance/ap/pay-invoice",
		SkillBytes: skill,
		Resources:  map[string][]byte{"ref.md": []byte("R"), "a.md": []byte("A")},
	}
	got := contentHashFor(rec)
	// Resources contribute in sorted key order: a.md, then ref.md.
	want := "sha256:" + version.ContentHash(
		skill,
		[]byte("a.md"), []byte("A"),
		[]byte("ref.md"), []byte("R"),
	)
	if got != want {
		t.Errorf("contentHashFor = %q, want %q", got, want)
	}
	if got == "sha256:" {
		t.Error("content hash is empty")
	}
}

// When SkillBytes is empty the artifact frontmatter bytes are hashed instead.
// spec: §7.5.3.
func TestContentHashFor_FallsBackToArtifactBytes(t *testing.T) {
	t.Parallel()
	art := []byte("---\ntype: agent\n---")
	rec := materialRecord{ID: "x", ArtifactBytes: art}
	got := contentHashFor(rec)
	want := "sha256:" + version.ContentHash(art)
	if got != want {
		t.Errorf("contentHashFor = %q, want %q", got, want)
	}
}

// Distinct content yields distinct hashes; identical content yields identical
// hashes (idempotent re-sync). spec: §7.5.3.
func TestContentHashFor_DistinctAndStable(t *testing.T) {
	t.Parallel()
	a := materialRecord{ID: "a", SkillBytes: []byte("one")}
	b := materialRecord{ID: "b", SkillBytes: []byte("two")}
	if contentHashFor(a) == contentHashFor(b) {
		t.Error("distinct content produced identical hashes")
	}
	if contentHashFor(a) != contentHashFor(materialRecord{ID: "a2", SkillBytes: []byte("one")}) {
		t.Error("identical content produced different hashes")
	}
}

// spec: §7.5.3, §14.11 — Run writes version and content_hash into every lock
// artifact entry, so the committed <target>/.podium/sync.lock pins reproducible
// (id, version, content_hash) triples rather than leaving the fields empty.
func TestRun_LockRecordsVersionAndContentHash(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/greet/hello/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.2.0\n---\n\n"},
		testharness.WriteTreeOption{Path: "team/greet/hello/SKILL.md", Content: "---\nname: hello\ndescription: hi\n---\n\nBody.\n"},
	)
	if _, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "none"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, err := ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock == nil || len(lock.Artifacts) == 0 {
		t.Fatalf("lock missing or has no artifacts: %+v", lock)
	}
	for _, la := range lock.Artifacts {
		if la.Version == "" {
			t.Errorf("lock artifact %s has empty version", la.ID)
		}
		if !strings.HasPrefix(la.ContentHash, "sha256:") || len(la.ContentHash) <= len("sha256:") {
			t.Errorf("lock artifact %s content_hash = %q, want sha256:<hex>", la.ID, la.ContentHash)
		}
	}
}
