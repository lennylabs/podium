package filesystem

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/manifest"
)

// LayerConfigFile is the name of the layer configuration file at a layer
// root, which the §4.7.6 layer digest covers.
const LayerConfigFile = ".layer-config"

// domainFile is the name of a domain's configuration file (§4.5.1).
const domainFile = "DOMAIN.md"

// NewPackageRecord assembles the record of the package with canonical ID id
// in layer l. files holds every file of the package under its slash path
// relative to the package root, the root ARTIFACT.md and SKILL.md included,
// as the walk read it; hasArtifact and hasSkill report that the root holds
// each as a regular file. Both walks call it, so they resolve the package
// with one function, manifest.ResolvePackage, and record one package digest.
//
// A resolved package yields a record whose Artifact holds the resolved
// values, with every file other than the root manifests, and for a skill the
// root SKILL.md, as a bundled resource. An unresolved package yields an
// UnparseableRecord, which ingest and filesystem-source sync refuse under the
// §4.1 unparseable-manifest rule, including a type: skill with no SKILL.md.
// Every record carries the §4.7.6 package digest of its files, so an
// unresolved package still has the digest the layer digest lists.
//
// Spec: §4.1, §4.2, §4.3.4, §4.7.6
func NewPackageRecord(id string, l Layer, hasArtifact, hasSkill bool, files map[string][]byte) ArtifactRecord {
	res := manifest.ResolvePackage(manifest.PackageDocuments{
		ArtifactMD:    files[manifest.ArtifactFile],
		HasArtifactMD: hasArtifact,
		SkillMD:       files[manifest.SkillFile],
		HasSkillMD:    hasSkill,
	})
	sums := fileSums(files)
	digest := attest.PackageDigest(attest.NewPackage(res, sums))
	pathErr := entryPathError(files)
	if !res.Resolved {
		rec := UnparseableRecord(id, l, res.FailedFile, res.Err)
		rec.PackageDigest = digest
		rec.FileSums = sums
		rec.entryPathErr = pathErr
		return rec
	}
	isSkill := res.Artifact.Type == manifest.TypeSkill
	rec := ArtifactRecord{
		ID:              id,
		Layer:           l,
		Artifact:        res.Artifact,
		Skill:           res.Skill,
		Resources:       map[string][]byte{},
		VersionAssigned: isSkill && res.Artifact.Version == "",
		AuthoredVersion: res.Artifact.Version,
		PackageDigest:   digest,
		Fields:          res.Fields,
		FileSums:        sums,
		entryPathErr:    pathErr,
	}
	for p, b := range files {
		switch {
		case p == manifest.ArtifactFile && hasArtifact:
			rec.ArtifactBytes, rec.AuthoredBytes = b, b
		case p == manifest.SkillFile && isSkill:
			rec.SkillBytes = b
		default:
			rec.Resources[p] = b
		}
	}
	return rec
}

// entryPathError returns the attest.CheckEntryPath error of the first path
// of files, in byte order, that is not valid UTF-8, and nil when every path
// is.
func entryPathError(files map[string][]byte) error {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := attest.CheckEntryPath(p); err != nil {
			return err
		}
	}
	return nil
}

// fileSums returns the SHA-256 digest of each file in files.
func fileSums(files map[string][]byte) map[string]attest.Sum {
	sums := make(map[string]attest.Sum, len(files))
	for p, b := range files {
		sums[p] = sha256.Sum256(b)
	}
	return sums
}

// SubjectName returns the §4.7.6 subject name of the record: the canonical ID
// with the authored version for a resolved package whose version is in
// major.minor.patch form, and the bare ID otherwise. An ID that breaks the
// subject name character rule, and a package with a file whose path is not
// valid UTF-8, return an error wrapping attest.ErrNoSubjectName.
//
// Spec: §4.7.6
func (r *ArtifactRecord) SubjectName() (string, error) {
	if r.entryPathErr != nil {
		return "", r.entryPathErr
	}
	return attest.SubjectName(r.ID, r.AuthoredVersion)
}

// LayerSubjects returns one subject per record, its subject name and its
// package digest, for the §4.7.6 layer digest. records is the walk of one
// layer taken before any refusal, so a package that ingest refuses is listed.
// A record with no subject name fails the list with an error that names the
// package, because a layer that holds one has no layer digest.
//
// Spec: §4.7.6
func LayerSubjects(records []ArtifactRecord) ([]attest.Subject, error) {
	subjects := make([]attest.Subject, 0, len(records))
	for i := range records {
		name, err := records[i].SubjectName()
		if err != nil {
			return nil, fmt.Errorf("filesystem: package %q: %w", records[i].ID, err)
		}
		subjects = append(subjects, attest.Subject{Name: name, Digest: records[i].PackageDigest})
	}
	return subjects, nil
}

// LayerFiles returns the §4.7.6 layer files of the layer rooted at fsys,
// keyed by slash path relative to the layer root: every regular file named
// DOMAIN.md in the root or in a directory below it whose path has no segment
// that begins with a dot, and the .layer-config file at the root when it is
// a regular file. The rule reads the tree alone, so it lists a root
// DOMAIN.md that ingest stores no domain record for, a DOMAIN.md that does
// not parse, and a DOMAIN.md inside a package. Both walks call it, so they
// list one set. fs.Stat follows a symbolic link, as PackageManifests does.
//
// Spec: §4.5.1, §4.7.6
func LayerFiles(fsys fs.FS) (map[string]attest.Sum, error) {
	files := map[string]attest.Sum{}
	if err := addLayerFile(fsys, LayerConfigFile, files); err != nil {
		return nil, err
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != domainFile {
			return nil
		}
		return addLayerFile(fsys, p, files)
	})
	if err != nil {
		return nil, fmt.Errorf("filesystem: list layer files: %w", err)
	}
	return files, nil
}

// addLayerFile records the digest of name in files when it resolves to a
// regular file.
func addLayerFile(fsys fs.FS, name string, files map[string]attest.Sum) error {
	ok, err := regularFile(fsys, name)
	if err != nil || !ok {
		return err
	}
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("filesystem: read layer file %s: %w", name, err)
	}
	files[path.Clean(name)] = sha256.Sum256(b)
	return nil
}

// LayerDigestInput returns the input of the §4.7.6 layer digest: the
// subjects of records, the walk of one layer before any refusal, and the
// layer files of fsys, the layer's tree.
//
// Spec: §4.7.6
func LayerDigestInput(records []ArtifactRecord, fsys fs.FS) (attest.Layer, error) {
	subjects, err := LayerSubjects(records)
	if err != nil {
		return attest.Layer{}, err
	}
	files, err := LayerFiles(fsys)
	if err != nil {
		return attest.Layer{}, err
	}
	return attest.Layer{Subjects: subjects, Files: files}, nil
}

// LayerContent walks the layer l with the filesystem-source walk and returns
// the input of its §4.7.6 layer digest. Every package discovery finds is
// listed, before the §4.1 and §4.6 refusals that Walk applies.
//
// Spec: §4.7.6
func LayerContent(l Layer) (attest.Layer, error) {
	records, fsys, err := LayerRecords(l)
	if err != nil {
		return attest.Layer{}, err
	}
	return LayerDigestInput(records, fsys)
}

// LayerRecords walks the layer l with the filesystem-source walk and returns
// every package discovery finds, before the §4.1 and §4.6 refusals that Walk
// applies, with the layer's tree read through its resolved root. A caller
// that needs more of each package than its subject, such as `podium attest`,
// which warns on names that are not portable, reads the records and passes
// both to LayerDigestInput.
//
// Spec: §4.2, §4.7.6
func LayerRecords(l Layer) ([]ArtifactRecord, fs.FS, error) {
	return walkLayer(l)
}
