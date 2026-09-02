package server

import (
	"io/fs"

	"github.com/lennylabs/podium/pkg/layer/source"
)

// newDirFS returns the tree the filesystem bootstrap hands to the ingest
// pipeline. It is source.ConfinedFS, the module's single confinement
// implementation, so a bootstrapped layer is confined to its own directory the
// way an API-registered one is (§11 deployment-mode equivalence).
//
// Spec: §7.3.1 (local-source ingest confinement)
func newDirFS(root string) fs.FS { return source.ConfinedFS(root) }
