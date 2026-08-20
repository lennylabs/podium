package integration

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sync"
)

// writeExtendsRegistry lays out a one-layer filesystem registry holding a
// parent and a child that extends it. The parent declares a frontmatter key
// `manifest.Artifact` does not, and the child authors one of its own and no
// prose, so the fixture exercises both the inherited-key and the empty-body
// paths that the two extends resolvers must agree on.
func writeExtendsRegistry(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("shared/base/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the base context\n"+
			"tags: [shared]\nsensitivity: low\nx_review_board: platform\n---\n\nbase prose\n")
	write("team/derived/ARTIFACT.md",
		"---\ntype: context\nversion: 2.0.0\ndescription: the derived context\n"+
			"tags: [team]\nx_runbook: ops/derived.md\nextends: shared/base@1.x\n---\n")
	return root
}

// Spec: §11 (filesystem ↔ server equivalence) / §2.2 (shared library) — a
// `podium sync` against a filesystem-source registry and one against a server
// pointed at the same directory materialize byte-identical output. The two
// extends resolvers are separate implementations (`pkg/registry/filesystem`
// serves the first, `pkg/registry/core` the second) that serialize the merged
// manifest through the same call, so repairing one alone makes the two modes
// serve different bytes for the same artifact.
//
// This case passes against the behavior that preceded it, where both resolvers
// dropped the undeclared keys identically. It exists to fail the moment one
// resolver is repaired without the other, which no other test in the tree
// would catch.
func TestSyncEquivalence_ExtendsChildMatchesAcrossModes(t *testing.T) {
	t.Parallel()
	dir := writeExtendsRegistry(t)

	fsTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{
		RegistryPath: dir,
		Target:       fsTarget,
		AdapterID:    "none",
	}); err != nil {
		t.Fatalf("filesystem sync.Run: %v", err)
	}

	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	srvTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{
		RegistryPath: ts.URL,
		Target:       srvTarget,
		AdapterID:    "none",
	}); err != nil {
		t.Fatalf("server sync.Run: %v", err)
	}

	fsTree := materializedTree(t, fsTarget)
	srvTree := materializedTree(t, srvTarget)
	if len(fsTree) == 0 {
		t.Fatalf("filesystem sync materialized nothing")
	}
	assertTreesEqual(t, fsTree, srvTree)
}
