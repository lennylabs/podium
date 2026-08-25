// Package web exposes the built §13.10 web-UI bundle to callers that
// mount it at /ui/. The React source lives in web/ui and its build
// output is committed under web/bundle, because go:embed resolves at
// compile time and a clean clone must build with only a Go toolchain.
// The embed directive names the bundle directory alone, so neither the
// design brief nor the design pass's output ships inside the binary.
package web

import (
	"embed"
	"io/fs"
)

// The pattern carries the all: prefix because a bare directory pattern
// drops every matched file whose name begins with _ or . , and the
// bundler emits _-prefixed names for the shared chunks it splits out. A
// dropped chunk is invisible to the entry document, so the page would
// 404 at runtime on a path the mount cannot serve.
//
//go:embed all:bundle
var assets embed.FS

// bundle is the built UI rooted at its own entry document, so
// index.html sits at the root callers serve.
var bundle = mustSub(assets, "bundle")

// Assets returns the built UI file system, rooted at the served bundle:
// index.html is at its root and every asset the index references
// resolves under it. Callers wrap this with http.FileServer to serve the
// UI at /ui/.
func Assets() fs.FS { return bundle }

// mustSub roots the embedded file system at dir. The directory is
// embedded at compile time, so a failure here means the binary was built
// without the bundle, which go:embed already rejects.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("web: rooting the embedded bundle at " + dir + ": " + err.Error())
	}
	return sub
}
