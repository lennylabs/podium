// Package web exposes the static SPA assets (§13.10 web UI) to
// callers that mount them at /ui/. The contents are embedded at
// build time via go:embed so a single binary distribution carries
// the UI without a separate static-asset bundle.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.js style.css
var assets embed.FS

// Assets returns the embedded SPA file system, rooted at the
// repository's web/ directory. Callers wrap this with
// http.FileServer to serve the SPA.
func Assets() fs.FS { return assets }
