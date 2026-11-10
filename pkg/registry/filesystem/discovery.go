package filesystem

import (
	"errors"
	"io/fs"
	"path"
)

// PackageManifests reports whether dir directly holds a regular file named
// ARTIFACT.md and whether it directly holds one named SKILL.md. Both Go
// walkers use it: a directory holding ARTIFACT.md is a package and a
// nested-package boundary (§4.4), and a directory holding SKILL.md alone is a
// package only when no directory between it and the layer root is one (§4.2).
// A stat failure other than fs.ErrNotExist is returned rather than read as an
// absent manifest, so a confined source that refuses a manifest fails the
// layer's ingest instead of silently dropping the package (§7.3.1).
//
// fs.Stat follows a symbolic link, so a link that resolves to a regular file
// is a manifest, and one that resolves to a directory or to nothing is not.
// dir is a slash-separated path relative to the root of fsys, with "." naming
// the root.
//
// Spec: §4.2, §4.4, §7.3.1
func PackageManifests(fsys fs.FS, dir string) (artifact, skill bool, err error) {
	artifact, err = regularFile(fsys, path.Join(dir, "ARTIFACT.md"))
	if err != nil {
		return false, false, err
	}
	skill, err = regularFile(fsys, path.Join(dir, "SKILL.md"))
	if err != nil {
		return false, false, err
	}
	return artifact, skill, nil
}

// regularFile reports whether name resolves to a regular file. An absent name
// is not an error; every other stat failure is.
func regularFile(fsys fs.FS, name string) (bool, error) {
	info, err := fs.Stat(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

// WithinPackage reports whether a directory strictly between dir and the layer
// root is in packages, the set of package directories a walk has recorded so
// far. fs.WalkDir and filepath.WalkDir visit a directory before its contents,
// so the set holds every ancestor package by the time dir is visited. A
// directory holding only SKILL.md below such an ancestor is a bundled resource
// of that package rather than a package of its own (§4.2). dir and the keys of
// packages are slash-separated paths relative to the layer root.
//
// Spec: §4.2
func WithinPackage(packages map[string]bool, dir string) bool {
	for p := path.Dir(dir); p != "." && p != "/"; p = path.Dir(p) {
		if packages[p] {
			return true
		}
	}
	return false
}
