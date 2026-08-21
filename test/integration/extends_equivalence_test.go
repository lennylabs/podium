package integration

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sync"
)

// writeExtendsRegistry lays out a two-layer filesystem registry covering both
// §4.6 parent forms. `team/derived` extends a different canonical ID, and
// `team/overlay` extends its own ID so the parent is the same artifact in the
// lower-precedence layer. Each parent declares a frontmatter key
// `manifest.Artifact` does not, each child authors one of its own and no prose,
// so the fixture exercises the inherited-key path, the empty-body path, and
// both parent resolutions that the two extends resolvers must agree on.
//
// `shared/base` extends `shared/anchor`, which makes `team/derived` a
// three-level chain. The filesystem resolver rewrites a record in place as it
// processes it, so a chain deeper than one link is what separates a resolver
// that reads the chain from each record's parsed manifest from one that reads
// it back out of bytes it has already rewritten.
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
	write(".registry-config", "multi_layer: true\nlayer_order:\n  - base\n  - top\n")
	write("base/.layer-config", "visibility:\n  public: true\n")
	write("top/.layer-config", "visibility:\n  public: true\n")

	write("base/shared/anchor/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the anchor context\n"+
			"tags: [anchor]\nsensitivity: low\nx_charter: docs/charter.md\n---\n\nanchor prose\n")
	write("base/shared/base/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the base context\n"+
			"tags: [shared]\nsensitivity: low\nx_review_board: platform\n"+
			"extends: shared/anchor@1.x\n---\n\nbase prose\n")
	write("top/team/derived/ARTIFACT.md",
		"---\ntype: context\nversion: 2.0.0\ndescription: the derived context\n"+
			"tags: [team]\nx_runbook: ops/derived.md\nextends: shared/base@1.x\n---\n")

	write("base/team/overlay/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the overlay base\n"+
			"tags: [shared]\nsensitivity: low\nx_review_board: platform\n---\n\noverlay base prose\n")
	write("top/team/overlay/ARTIFACT.md",
		"---\ntype: context\nversion: 2.0.0\ndescription: the overlay child\n"+
			"tags: [team]\nx_runbook: ops/overlay.md\nextends: team/overlay@1.x\n---\n")
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
// The case runs through both a no-op adapter and a harness adapter, because the
// harness path lays the merged bytes out under its own target paths and a
// resolver divergence surfaces there too.
func TestSyncEquivalence_ExtendsChildMatchesAcrossModes(t *testing.T) {
	t.Parallel()
	dir := writeExtendsRegistry(t)

	for _, adapterID := range []string{"none", "claude-code"} {
		adapterID := adapterID
		t.Run(adapterID, func(t *testing.T) {
			t.Parallel()

			fsTarget := t.TempDir()
			fsRes, err := sync.Run(sync.Options{
				RegistryPath: dir,
				Target:       fsTarget,
				AdapterID:    adapterID,
			})
			if err != nil {
				t.Fatalf("filesystem sync.Run: %v", err)
			}

			srv, err := server.NewFromFilesystem(dir)
			if err != nil {
				t.Fatalf("NewFromFilesystem: %v", err)
			}
			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)

			srvTarget := t.TempDir()
			srvRes, err := sync.Run(sync.Options{
				RegistryPath: ts.URL,
				Target:       srvTarget,
				AdapterID:    adapterID,
			})
			if err != nil {
				t.Fatalf("server sync.Run: %v", err)
			}

			fsTree := materializedTree(t, fsTarget)
			srvTree := materializedTree(t, srvTarget)

			// Both extends children must be present and carry merged output,
			// so the byte comparison below cannot pass on a tree that dropped
			// them or served them unmerged.
			for _, child := range []string{"team/derived", "team/overlay"} {
				content := soleMaterializedEntry(t, fsTree, child)
				if !strings.Contains(content, "x_review_board: platform") {
					t.Errorf("%s: merged frontmatter lost the parent-inherited key:\n%s", child, content)
				}
				if !strings.Contains(content, "x_runbook:") {
					t.Errorf("%s: merged frontmatter lost the child's own key:\n%s", child, content)
				}
			}
			// team/derived sits two links below shared/anchor, so the
			// grandparent's inherited key pins the deeper chain both
			// resolvers have to restore identically. Its value names no
			// ancestor, so §4.6's omitted-field rule carries it through both
			// modes rather than the hidden-parent test leaving it out.
			if content := soleMaterializedEntry(t, fsTree, "team/derived"); !strings.Contains(content, "x_charter: docs/charter.md") {
				t.Errorf("team/derived: merged frontmatter lost the grandparent-inherited key:\n%s", content)
			}

			assertTreesEqual(t, fsTree, srvTree)

			if got, want := artifactKeys(srvRes), artifactKeys(fsRes); !equalStringSlices(got, want) {
				t.Errorf("artifacts list mismatch:\n filesystem=%v\n server=    %v", want, got)
			}
		})
	}
}

// Spec: §11 (filesystem ↔ server equivalence) / §2.2 (shared library), §4.6
// hidden parents — a child whose merged frontmatter cannot be rewritten into
// one that names no parent ends the sync in both modes. The filesystem resolver
// fails the walk with manifest.ErrUnhidableParent and the server mode fails the
// load with registry.invalid_argument, which `pkg/sync` reports without
// materializing anything, so neither mode writes a tree the other refuses.
func TestSyncEquivalence_UnhidableParentFailsBothModes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// The child carries its extends reference under an anchor, so deleting the
	// entry strands the alias into it and the merged block cannot be read back
	// to be checked against §4.6.
	write("shared/base/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the base context\n---\n\nbase prose\n")
	write("team/derived/ARTIFACT.md",
		"---\ntype: context\nversion: 2.0.0\ndescription: the derived context\n"+
			"extends: &b shared/base@1.x\nnote: *b\n---\n\nderived prose\n")
	write("team/other/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: an unrelated context\n---\n\nother prose\n")

	fsTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: dir, Target: fsTarget}); err == nil {
		t.Fatalf("filesystem sync.Run succeeded on a child that cannot hide its parent:\n%v", materializedTree(t, fsTarget))
	}

	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	srvTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: ts.URL, Target: srvTarget}); err == nil {
		t.Fatalf("server sync.Run succeeded on a child that cannot hide its parent:\n%v", materializedTree(t, srvTarget))
	}

	for name, target := range map[string]string{"filesystem": fsTarget, "server": srvTarget} {
		for path, content := range materializedTree(t, target) {
			if strings.Contains(content, "shared/base") {
				t.Errorf("%s mode materialized %s naming the hidden parent:\n%s", name, path, content)
			}
		}
	}
}

// Spec: §11 (filesystem ↔ server equivalence) / §2.2 (shared library), §4.6
// hidden parents — an inherited key naming the parent ends the sync in both
// modes. Serving the key would name the hidden parent, and dropping it would
// serve a key §4.6 makes inheritable as nothing, so both resolvers refuse and
// neither mode materializes a tree the other refuses.
func TestSyncEquivalence_InheritedKeyNamingTheParentFailsBothModes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// The parent authors a key holding its own canonical ID, which the child
	// inherits under §4.6's omitted-field rule and cannot be served.
	write("shared/base/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the base context\n"+
			"x_base: shared/base\n---\n\nbase prose\n")
	write("team/derived/ARTIFACT.md",
		"---\ntype: context\nversion: 2.0.0\ndescription: the derived context\n"+
			"extends: shared/base@1.x\n---\n\nderived prose\n")

	fsTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: dir, Target: fsTarget}); err == nil {
		t.Fatalf("filesystem sync.Run succeeded on a child that cannot hide its parent:\n%v", materializedTree(t, fsTarget))
	}

	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	srvTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{RegistryPath: ts.URL, Target: srvTarget}); err == nil {
		t.Fatalf("server sync.Run succeeded on a child that cannot hide its parent:\n%v", materializedTree(t, srvTarget))
	}

	for name, target := range map[string]string{"filesystem": fsTarget, "server": srvTarget} {
		for path, content := range materializedTree(t, target) {
			if strings.Contains(content, "shared/base") {
				t.Errorf("%s mode materialized %s naming the hidden parent:\n%s", name, path, content)
			}
		}
	}
}

// soleMaterializedEntry returns the content of the single materialized path
// containing want, failing when the tree holds no such path or more than one.
// The target path of an artifact depends on the adapter, so the assertion
// matches on the canonical ID rather than on a fixed path.
func soleMaterializedEntry(t testing.TB, tree map[string]string, want string) string {
	t.Helper()
	var matches []string
	for path := range tree {
		if strings.Contains(path, want) {
			matches = append(matches, path)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly one materialized path containing %q, got %v", want, matches)
	}
	return tree[matches[0]]
}
