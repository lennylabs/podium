package sync

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/version"
)

// spec: §7.5.3, §14.11 — the lock file pins each artifact's content_hash so the
// committed lock captures reproducible (id, version, content_hash) triples.
// contentHashFor hashes the manifest bytes, then the SKILL.md bytes when the
// artifact carries one, then every resource in sorted key order, in the
// "sha256:<hex>" form.
func TestContentHashFor_SkillBytesAndResources(t *testing.T) {
	t.Parallel()
	art := []byte("---\ntype: skill\n---")
	skill := []byte("---\nname: pay\n---\nbody")
	rec := materialRecord{
		ID:            "finance/ap/pay-invoice",
		ArtifactBytes: art,
		AuthoredBytes: art,
		SkillBytes:    skill,
		Resources:     map[string][]byte{"ref.md": []byte("R"), "a.md": []byte("A")},
	}
	got := contentHashFor(rec)
	// The manifest leads, the SKILL.md bytes follow, and resources contribute in
	// sorted key order: a.md, then ref.md.
	want := "sha256:" + version.CanonicalContentHash(art, skill,
		map[string][]byte{"a.md": []byte("A"), "ref.md": []byte("R")})
	if got != want {
		t.Errorf("contentHashFor = %q, want %q", got, want)
	}
	if got == "sha256:" {
		t.Error("content hash is empty")
	}
}

// An artifact with no SKILL.md frames a zero-length value in that slot, so the
// digest is the §4.7.6 canonical one over the manifest with a nil SKILL.md and
// no resources rather than a digest over the manifest bytes alone. spec:
// §7.5.3.
func TestContentHashFor_NoSkillFramesAnEmptySkillSlot(t *testing.T) {
	t.Parallel()
	art := []byte("---\ntype: agent\n---")
	rec := materialRecord{ID: "x", ArtifactBytes: art, AuthoredBytes: art}
	got := contentHashFor(rec)
	want := "sha256:" + version.CanonicalContentHash(art, nil, nil)
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

// spec: §14.11, §7.5.3 — for a server source, lockContentHash pins
// the registry's authoritative content_hash verbatim. The registry value wins
// even when it diverges from a digest recomputed over the served bytes, which
// is the §4.6 extends-merge case the registry itself flags ("served bytes no
// longer reproduce ContentHash").
func TestLockContentHash_PrefersRegistryValue(t *testing.T) {
	t.Parallel()
	rec := materialRecord{ID: "x", ArtifactBytes: []byte("served bytes"), AuthoredBytes: []byte("served bytes"), ContentHash: "sha256:authoritative"}
	if got := lockContentHash(rec); got != "sha256:authoritative" {
		t.Errorf("lockContentHash = %q, want the registry value sha256:authoritative", got)
	}
	if lockContentHash(rec) == contentHashFor(rec) {
		t.Error("lockContentHash returned the recomputed digest, not the registry value")
	}
}

// Spec: §4.7.6, §7.5.3 — a filesystem source carries no recorded hash, so
// lockContentHash computes one, and it computes it over the authored
// ARTIFACT.md rather than over the merged, parent-hidden re-serialization the
// extends: resolver leaves in ArtifactBytes.
func TestLockContentHash_FilesystemSourceHashesTheAuthoredManifest(t *testing.T) {
	t.Parallel()
	authored := []byte("---\ntype: context\nversion: 2.0.0\ndescription: child desc\nextends: acme/base\n---\n\nChild body.\n")
	merged := []byte("---\ntype: context\nversion: 2.0.0\ndescription: child desc\nname: Base\n---\n\nChild body.\n")
	rec := materialRecord{ID: "acme/child", ArtifactBytes: merged, AuthoredBytes: authored} // no ContentHash
	want := "sha256:" + version.CanonicalContentHash(authored, nil, nil)
	if got := lockContentHash(rec); got != want {
		t.Errorf("lockContentHash = %q, want the authored-bytes digest %q", got, want)
	}
	if overMerged := "sha256:" + version.CanonicalContentHash(merged, nil, nil); want == overMerged {
		t.Fatal("test setup: the authored and the merged bytes hash alike; the fixture proves nothing")
	}
}

// spec: §14.11, §7.5.3 — a server-source sync pins the registry's
// authoritative content_hash (from /v1/load_artifact) into the committed lock
// rather than recomputing one from the served bytes. The stub serves a hash
// that deliberately diverges from contentHashFor(served frontmatter), so the
// test fails if Run recomputes instead of using the registry's value.
func TestRun_ServerSourceLockPinsRegistryContentHash(t *testing.T) {
	t.Parallel()
	const authoritative = "sha256:deadbeefcafef00d"
	// Guard the fixture: the recomputed digest over the served frontmatter must
	// differ from the authoritative stub value, or the test proves nothing.
	if contentHashFor(materialRecord{AuthoredBytes: []byte(contextArtifactSrc)}) == authoritative {
		t.Fatal("test setup: served frontmatter hashes to the authoritative value; pick a distinct stub hash")
	}
	srv := newStubRegistry(t, map[string]stubArtifact{
		"team/glossary": {
			typ:          "context",
			layer:        "team-shared",
			frontmatter:  contextArtifactSrc,
			manifestBody: "Glossary body.\n",
			contentHash:  authoritative,
		},
	})
	target := t.TempDir()
	res, err := Run(Options{RegistryPath: srv.URL, Target: target, AdapterID: "none", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].ContentHash != authoritative {
		t.Fatalf("Result content hash = %+v, want %q", res.Artifacts, authoritative)
	}
	lock, err := ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock == nil || len(lock.Artifacts) == 0 {
		t.Fatalf("lock missing artifacts: %+v", lock)
	}
	for _, la := range lock.Artifacts {
		if la.ContentHash != authoritative {
			t.Errorf("lock %s content_hash = %q, want the registry value %q", la.ID, la.ContentHash, authoritative)
		}
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

// Spec: §4.7.6, §7.5.3 — a filesystem-source sync pins the digest over the
// authored ARTIFACT.md for an extends: child, which is the value ingest stores
// and a server-source sync records, rather than the digest over the merged
// re-serialization the extends: resolver writes into ArtifactBytes. The parent
// sits in the layer layer_order: places first, so the resolver finds it through
// the effective view.
func TestRun_FilesystemSourceLockHashesAnExtendsChildBeforeTheMerge(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	parent := "---\ntype: context\nversion: 1.0.0\nname: Base\ndescription: parent desc\n---\n\nParent body.\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child desc\nextends: acme/base\n---\n\nChild body.\n"
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{Path: "team-shared/acme/base/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "personal/acme/child/ARTIFACT.md", Content: child},
	)
	if _, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "none"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, err := ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	want := "sha256:" + version.CanonicalContentHash([]byte(child), nil, nil)
	// Guard the fixture: the merged re-serialization must hash differently, or
	// the assertion below passes whichever bytes the composer read.
	recs, err := filesystemRecords(Options{RegistryPath: registry})
	if err != nil {
		t.Fatalf("filesystemRecords: %v", err)
	}
	for _, rec := range recs {
		if rec.ID != "acme/child" {
			continue
		}
		mergedBytes := rec.ArtifactBytes
		merged := "sha256:" + version.CanonicalContentHash(mergedBytes, nil, nil)
		if merged == want {
			t.Fatal("test setup: the merged bytes hash to the authored digest; the fixture proves nothing")
		}
	}
	var seen int
	for _, la := range lock.Artifacts {
		if la.ID != "acme/child" {
			continue
		}
		seen++
		if la.ContentHash != want {
			t.Errorf("lock content_hash = %q, want the authored-bytes digest %q", la.ContentHash, want)
		}
	}
	if seen == 0 {
		t.Fatalf("lock has no acme/child entry: %+v", lock.Artifacts)
	}
}
