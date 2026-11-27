package attest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

func storedHash(b []byte) string {
	s := sha256.Sum256(b)
	return digestPrefix + hex.EncodeToString(s[:])
}

// treeDigest is the package digest of a package read from a tree with the
// given files, the computation the walks make.
func treeDigest(files map[string][]byte) string {
	docs := manifest.PackageDocuments{}
	docs.ArtifactMD, docs.HasArtifactMD = files[manifest.ArtifactFile]
	docs.SkillMD, docs.HasSkillMD = files[manifest.SkillFile]
	sums := map[string]Sum{}
	for p, b := range files {
		sums[p] = sha256.Sum256(b)
	}
	return PackageDigest(NewPackage(manifest.ResolvePackage(docs), sums))
}

// Spec: §4.7.6, §13.4 — the digest of a stored row, computed from its
// documents and its resource content hashes, equals the digest of the same
// package read from a tree: for a skill with both documents, a plain skill,
// a rule that bundles a zero-length SKILL.md, a type: skill whose SKILL.md
// does not parse, and a manifest that does not parse.
func TestStoredPackageDigest_EqualsTheTreeDigest(t *testing.T) {
	t.Parallel()
	skill := []byte("---\nname: s\ndescription: A skill.\nmetadata:\n  sensitivity: high\n---\nbody\n")
	cases := map[string]struct {
		artifact, skillDoc []byte
		resources          map[string][]byte
	}{
		"skill":          {[]byte("---\ntype: skill\nversion: 1.0.0\n---\n"), skill, map[string][]byte{"scripts/a.py": []byte("print(1)\n"), ".cfg/x": nil}},
		"plain skill":    {nil, skill, map[string][]byte{"notes.md": []byte("n\n")}},
		"rule":           {[]byte("---\ntype: rule\nversion: 1.0.0\n---\nrule\n"), nil, map[string][]byte{"SKILL.md": {}}},
		"broken skill":   {[]byte("---\ntype: skill\nversion: 1.0.0\n---\n"), []byte("---\nname: [x\n---\n"), nil},
		"broken partial": {[]byte("---\ntype: [x\n---\n"), nil, map[string][]byte{"a": []byte("a")}},
	}
	for name, c := range cases {
		files := map[string][]byte{}
		var refs []StoredResource
		for p, b := range c.resources {
			files[p] = b
			refs = append(refs, StoredResource{Path: p, ContentHash: storedHash(b)})
		}
		if c.artifact != nil {
			files[manifest.ArtifactFile] = c.artifact
		}
		if c.skillDoc != nil {
			files[manifest.SkillFile] = c.skillDoc
		}
		got, err := StoredPackageDigest(c.artifact, c.skillDoc, refs)
		if want := treeDigest(files); err != nil || got != want {
			t.Errorf("%s: StoredPackageDigest = %q, %v; want the tree digest %q", name, got, err, want)
		}
	}
}

// Spec: §13.4 — a row whose stored files give no entry list has no package
// digest: two resources under one path, a resource under the path of a
// stored root document, and a content hash outside the form ingest writes.
func TestStoredPackageDigest_NoEntryList(t *testing.T) {
	t.Parallel()
	ok := storedHash([]byte("a"))
	doc := []byte("---\ntype: rule\n---\n")
	cases := map[string]struct {
		artifact, skillDoc []byte
		refs               []StoredResource
	}{
		"duplicate path":   {doc, nil, []StoredResource{{"a", ok}, {"a", ok}}},
		"manifest path":    {doc, nil, []StoredResource{{manifest.ArtifactFile, ok}}},
		"skill path":       {doc, []byte("x"), []StoredResource{{manifest.SkillFile, ok}}},
		"no prefix":        {doc, nil, []StoredResource{{"a", strings.TrimPrefix(ok, digestPrefix)}}},
		"uppercase":        {doc, nil, []StoredResource{{"a", strings.ToUpper(ok[:9]) + ok[9:]}}},
		"uppercase digits": {doc, nil, []StoredResource{{"a", digestPrefix + strings.ToUpper(ok[7:])}}},
		"short":            {doc, nil, []StoredResource{{"a", ok[:len(ok)-2]}}},
		"not hex":          {doc, nil, []StoredResource{{"a", digestPrefix + strings.Repeat("zz", 32)}}},
	}
	for name, c := range cases {
		if got, err := StoredPackageDigest(c.artifact, c.skillDoc, c.refs); !errors.Is(err, ErrNoEntryList) || got != "" {
			t.Errorf("%s: StoredPackageDigest = %q, %v; want ErrNoEntryList", name, got, err)
		}
	}
}
