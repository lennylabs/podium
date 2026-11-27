package attest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// ErrNoEntryList reports a stored row whose files give no §4.7.6 entry list:
// two resources under one path, a resource under the path of a root document
// the row stores, or a resource content hash that is not sha256: followed by
// 64 lowercase hexadecimal digits. Ingest never stores such a row, and the
// §13.4 fill stores no package digest for it rather than one that drops or
// misreads a file.
var ErrNoEntryList = errors.New("attest: the stored files give no entry list")

// StoredResource is one bundled resource of a stored row: its path relative
// to the package root and its stored content hash, sha256:<hex>.
type StoredResource struct {
	Path        string
	ContentHash string
}

// StoredPackageDigest returns the §4.7.6 package digest of a stored row from
// its stored manifest, its stored SKILL.md, and the path and content hash of
// each bundled resource, with no resource body. An empty manifest or SKILL.md
// stands for an absent one, as manifest.StoredDocuments reads a row. The
// resolved fields come from manifest.ResolvePackage over the two documents,
// the resolution §13.4 stored-row admission applies, so the digest frames the
// type, sensitivity, deprecation flag, and replacement a load of the row
// serves. A row whose documents do not resolve gets the digest of an
// unresolved package. A row whose files give no entry list returns an error
// wrapping ErrNoEntryList.
//
// Spec: §4.7.6, §13.4
func StoredPackageDigest(artifactMD, skillMD []byte, resources []StoredResource) (string, error) {
	files, err := storedFileSums(artifactMD, skillMD, resources)
	if err != nil {
		return "", err
	}
	res := manifest.ResolvePackage(manifest.StoredDocuments(artifactMD, skillMD))
	return PackageDigest(NewPackage(res, files)), nil
}

// storedFileSums lists the digest of each file of a stored row under its
// path: the stored root documents, which a row holds only when they are
// non-empty, and each resource under its stored content hash.
func storedFileSums(artifactMD, skillMD []byte, resources []StoredResource) (map[string]Sum, error) {
	files := make(map[string]Sum, len(resources)+2)
	if len(artifactMD) != 0 {
		files[manifest.ArtifactFile] = sha256.Sum256(artifactMD)
	}
	if len(skillMD) != 0 {
		files[manifest.SkillFile] = sha256.Sum256(skillMD)
	}
	for _, r := range resources {
		if _, dup := files[r.Path]; dup {
			return nil, fmt.Errorf("%w: two files under %q", ErrNoEntryList, r.Path)
		}
		sum, err := parseStoredSum(r.ContentHash)
		if err != nil {
			return nil, fmt.Errorf("%w: resource %q: %v", ErrNoEntryList, r.Path, err)
		}
		files[r.Path] = sum
	}
	return files, nil
}

// parseStoredSum reads a stored content hash in the one form ingest writes,
// sha256: followed by 64 lowercase hexadecimal digits. Admission compares the
// stored value with that form of the body's digest, so a row with any other
// form is refused when it is loaded.
func parseStoredSum(h string) (Sum, error) {
	var sum Sum
	hexPart, ok := strings.CutPrefix(h, digestPrefix)
	if !ok || len(hexPart) != 2*len(sum) || strings.ToLower(hexPart) != hexPart {
		return sum, fmt.Errorf("content hash %q is not sha256: and 64 lowercase hexadecimal digits", h)
	}
	if _, err := hex.Decode(sum[:], []byte(hexPart)); err != nil {
		return sum, fmt.Errorf("content hash %q: %v", h, err)
	}
	return sum, nil
}
