package integration

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sync"
)

// writeExtendsRegistry lays out a two-layer filesystem registry covering both
// §4.6 parent forms. `team/derived` extends a different canonical ID, and
// `team/overlay` extends its own ID so the parent is the same artifact in the
// lower-precedence layer. Each parent declares a frontmatter key
// `manifest.Artifact` does not, each child authors one of its own and no prose,
// so the fixture exercises the undeclared-key path, the empty-body path, and
// both parent resolutions that the two extends resolvers must agree on.
//
// §4.6 refuses a child that leaves an undeclared key to a parent in another
// layer, so the two cross-layer children restate every undeclared key their
// parent chain sets. The inherited-key path runs within the `top` layer:
// `team/beta` extends `team/alpha` and omits the `x_steward` key the parent
// sets. `team/alpha` sorts before `team/beta`, so server ingest stores the
// parent before the child resolves its pin.
//
// `shared/base` extends `shared/anchor`, which makes `team/derived` a
// three-level chain. The filesystem resolver rewrites a record in place as it
// processes it, so a chain deeper than one link is what separates a resolver
// that reads the chain from each record's parsed manifest from one that reads
// it back out of bytes it has already rewritten. The anchor's tag reaches
// `team/derived` only through that chain.
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
			"tags: [team]\nx_runbook: ops/derived.md\nx_review_board: platform\n"+
			"x_charter: docs/charter.md\nextends: shared/base@1.x\n---\n")

	write("base/team/overlay/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the overlay base\n"+
			"tags: [shared]\nsensitivity: low\nx_review_board: platform\n---\n\noverlay base prose\n")
	write("top/team/overlay/ARTIFACT.md",
		"---\ntype: context\nversion: 2.0.0\ndescription: the overlay child\n"+
			"tags: [team]\nx_runbook: ops/overlay.md\nx_review_board: platform\n"+
			"extends: team/overlay@1.x\n---\n")

	write("top/team/alpha/ARTIFACT.md",
		"---\ntype: context\nversion: 1.0.0\ndescription: the alpha context\n"+
			"tags: [team]\nsensitivity: low\nx_steward: finance-ops\n---\n\nalpha prose\n")
	write("top/team/beta/ARTIFACT.md",
		"---\ntype: context\nversion: 2.0.0\ndescription: the beta context\n"+
			"tags: [team]\nx_runbook: ops/beta.md\nextends: team/alpha@1.x\n---\n")
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
// The fixture's cross-layer children restate the undeclared keys their parent
// chains set, as §4.6 requires, and the within-layer child `team/beta`
// inherits one, so the comparison covers a restated key and an inherited key.
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
				Delivery:     neverDelivery,
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
				Delivery:     neverDelivery,
				RegistryPath: ts.URL,
				Target:       srvTarget,
				AdapterID:    adapterID,
			})
			if err != nil {
				t.Fatalf("server sync.Run: %v", err)
			}

			fsTree := materializedTree(t, fsTarget)
			srvTree := materializedTree(t, srvTarget)

			// Every extends child must be present and carry merged output,
			// so the byte comparison below cannot pass on a tree that dropped
			// them or served them unmerged. The cross-layer children restate
			// x_review_board, so the key pins that a restated value survives
			// the merge, and the child's own x_runbook pins the child's key.
			for _, child := range []string{"team/derived", "team/overlay"} {
				content := soleMaterializedEntry(t, fsTree, child)
				if !strings.Contains(content, "x_review_board: platform") {
					t.Errorf("%s: merged frontmatter lost the restated key:\n%s", child, content)
				}
				if !strings.Contains(content, "x_runbook:") {
					t.Errorf("%s: merged frontmatter lost the child's own key:\n%s", child, content)
				}
			}
			// team/beta omits the x_steward key its same-layer parent sets,
			// so §4.6's omitted-field rule carries the parent's value into
			// the merged frontmatter in both modes.
			beta := soleMaterializedEntry(t, fsTree, "team/beta")
			if !strings.Contains(beta, "x_steward: finance-ops") {
				t.Errorf("team/beta: merged frontmatter lost the parent-inherited key:\n%s", beta)
			}
			if !strings.Contains(beta, "x_runbook: ops/beta.md") {
				t.Errorf("team/beta: merged frontmatter lost the child's own key:\n%s", beta)
			}
			// team/derived sits two links below shared/anchor. The anchor's
			// tag reaches it only through the deeper chain both resolvers
			// have to resolve identically, and the restated x_charter holds
			// the value the grandparent sets.
			derived := soleMaterializedEntry(t, fsTree, "team/derived")
			if !strings.Contains(derived, "anchor") {
				t.Errorf("team/derived: merged frontmatter lost the grandparent's tag:\n%s", derived)
			}
			if !strings.Contains(derived, "x_charter: docs/charter.md") {
				t.Errorf("team/derived: merged frontmatter lost the restated grandparent key:\n%s", derived)
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
	if _, err := sync.Run(sync.Options{Delivery: neverDelivery, RegistryPath: dir, Target: fsTarget}); err == nil {
		t.Fatalf("filesystem sync.Run succeeded on a child that cannot hide its parent:\n%v", materializedTree(t, fsTarget))
	}

	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	srvTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{Delivery: neverDelivery, RegistryPath: ts.URL, Target: srvTarget}); err == nil {
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
	if _, err := sync.Run(sync.Options{Delivery: neverDelivery, RegistryPath: dir, Target: fsTarget}); err == nil {
		t.Fatalf("filesystem sync.Run succeeded on a child that cannot hide its parent:\n%v", materializedTree(t, fsTarget))
	}

	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	srvTarget := t.TempDir()
	if _, err := sync.Run(sync.Options{Delivery: neverDelivery, RegistryPath: ts.URL, Target: srvTarget}); err == nil {
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

// eqMCPServer is an mcp-server manifest with an optional extends: line and an
// optional server_identifier, the §4.6 executable field the drop cases leave
// to a parent in another layer. The equivalence fixtures use it instead of a
// hook that omits hook_action, because server lint refuses such a hook before
// the executable-field rule runs and the case would pass on lint alone.
func eqMCPServer(version, desc, extends, serverIdentifier string) string {
	src := "---\ntype: mcp-server\nversion: " + version + "\ndescription: " + desc + "\nsensitivity: low\n"
	if extends != "" {
		src += "extends: " + extends + "\n"
	}
	if serverIdentifier != "" {
		src += "server_identifier: " + serverIdentifier + "\n"
	}
	return src + "---\n\n" + desc + "\n"
}

// eqAgent is an agent manifest with an optional extends: line and extra
// frontmatter lines.
func eqAgent(desc, extends, extra string) string {
	src := "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n"
	if extends != "" {
		src += "extends: " + extends + "\n"
	}
	return src + extra + "---\n\n" + desc + "\n"
}

// writeLayeredRegistry writes a multi-layer filesystem registry with the
// given layer order, every layer public so the server source's anonymous
// identity sees what the filesystem source walks. Each artifacts key is
// "<layer>/<canonical ID>".
func writeLayeredRegistry(t *testing.T, layers []string, artifacts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	opts := []testharness.WriteTreeOption{{
		Path:    ".registry-config",
		Content: "multi_layer: true\nlayer_order:\n  - " + strings.Join(layers, "\n  - ") + "\n",
	}}
	for _, l := range layers {
		opts = append(opts, testharness.WriteTreeOption{Path: l + "/.layer-config", Content: "visibility:\n  public: true\n"})
	}
	for rel, src := range artifacts {
		opts = append(opts, testharness.WriteTreeOption{Path: rel + "/ARTIFACT.md", Content: src})
	}
	testharness.WriteTree(t, dir, opts...)
	return dir
}

// lockLayers returns the layer the lock file at target records for each
// artifact ID it lists.
func lockLayers(t *testing.T, target string) map[string]string {
	t.Helper()
	lock, err := sync.ReadLock(target)
	if err != nil {
		t.Fatalf("reading the lock at %s: %v", target, err)
	}
	out := make(map[string]string, len(lock.Artifacts))
	for _, a := range lock.Artifacts {
		out[a.ID] = a.Layer
	}
	return out
}

// executableDrop is the drop the §4.6 executable-field rule reports for id.
func executableDrop(id string, fields ...string) layer.Drop {
	ie := &layer.ExecutableInheritance{ArtifactID: id, Fields: fields}
	return layer.Drop{ArtifactID: id, Code: layer.InvalidArtifactCode, Reason: ie.Reason()}
}

// Spec: §11 (filesystem ↔ server equivalence) / §2.2 (shared library), §4.6
// executable fields across layers, §13.11.3 — a child that leaves an
// executable field to a parent in another layer is refused in both modes, and
// the two modes materialize byte-identical trees without it. The filesystem
// source drops the child and reports one ingest.invalid_artifact drop, and the
// server source never stored it and reports nothing, which is the same split
// TestSyncEquivalence_LayerCollisionIsByteIdentical pins for a collision.
//
// Every fixture is one where both modes select the same parent, because the
// two modes do not always do so for a same-ID pin (§13.11.3). The subcases are
// a different-ID child beside an unrelated kept artifact, a same-ID overlay
// that falls back to the lower copy, a child whose field path would name its
// parent and is refused with the shared unreadable-fields reason, and a
// higher-layer record whose collision with the refused record is retracted so
// both modes serve it.
func TestSyncEquivalence_ExecutableInheritanceDropIsByteIdentical(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		layers    []string
		artifacts map[string]string
		// wantLayers is the layer each lock entry records, in both modes, so
		// the case pins which copy each tree carries and that the refused
		// child is absent.
		wantLayers map[string]string
		wantDrops  []layer.Drop
	}{
		{
			name:   "different-id child",
			layers: []string{"base", "top"},
			artifacts: map[string]string{
				"base/shared/server": eqMCPServer("1.0.0", "the base server", "", "base-id"),
				"top/team/server":    eqMCPServer("2.0.0", "the team server", "shared/server@1.x", ""),
				"top/team/notes":     eqAgent("the team notes", "", ""),
			},
			wantLayers: map[string]string{"shared/server": "base", "team/notes": "top"},
			wantDrops:  []layer.Drop{executableDrop("team/server", "server_identifier")},
		},
		{
			name:   "same-id overlay",
			layers: []string{"base", "top"},
			artifacts: map[string]string{
				"base/shared/tool": eqMCPServer("1.0.0", "from-base", "", "base-tool"),
				"top/shared/tool":  eqMCPServer("2.0.0", "from-top", "shared/tool@1.x", ""),
			},
			wantLayers: map[string]string{"shared/tool": "base"},
			wantDrops:  []layer.Drop{executableDrop("shared/tool", "server_identifier")},
		},
		{
			// The parent sets an undeclared key named with its own ID, so the
			// child's field path would disclose the parent and both modes
			// refuse it with the shared reason, which names no field.
			name:   "disclosing name",
			layers: []string{"L1", "L2"},
			artifacts: map[string]string{
				"L1/shared/base": eqAgent("the base agent", "", "shared/base: secret\n"),
				"L2/team/child":  eqAgent("the child agent", "shared/base", ""),
			},
			wantLayers: map[string]string{"shared/base": "L1"},
			wantDrops: []layer.Drop{{
				ArtifactID: "team/child", Code: layer.InvalidArtifactCode, Reason: layer.UnreadableExecutableFieldsReason,
			}},
		},
		{
			// l3's x collides only with l2's refused x. A server never stores
			// l2's x and accepts l3's, so the filesystem composition retracts
			// the collision and reports the refusal alone.
			name:   "retracted collision",
			layers: []string{"l1", "l2", "l3"},
			artifacts: map[string]string{
				"l1/p": eqMCPServer("1.0.0", "the parent server", "", "parent-id"),
				"l2/x": eqMCPServer("1.0.0", "the refused server", "p", ""),
				"l3/x": eqMCPServer("1.0.0", "the kept server", "", "l3-id"),
			},
			wantLayers: map[string]string{"p": "l1", "x": "l3"},
			wantDrops:  []layer.Drop{executableDrop("x", "server_identifier")},
		},
	}
	for _, tc := range cases {
		tc := tc
		dir := writeLayeredRegistry(t, tc.layers, tc.artifacts)
		for _, adapterID := range []string{"none", "claude-code"} {
			adapterID := adapterID
			t.Run(tc.name+"/"+adapterID, func(t *testing.T) {
				t.Parallel()
				fsTarget, fsRes, srvTarget, srvRes := syncBothModes(t, dir, adapterID)

				fsTree := materializedTree(t, fsTarget)
				if len(fsTree) == 0 {
					t.Fatalf("filesystem sync materialized nothing")
				}
				assertTreesEqual(t, fsTree, materializedTree(t, srvTarget))
				if got, want := artifactKeys(srvRes), artifactKeys(fsRes); !equalStringSlices(got, want) {
					t.Errorf("artifacts list mismatch:\n filesystem=%v\n server=    %v", want, got)
				}
				assertLockArtifactsEqual(t, fsTarget, srvTarget)
				if got := lockLayers(t, fsTarget); !reflect.DeepEqual(got, tc.wantLayers) {
					t.Errorf("lock layers = %v, want %v", got, tc.wantLayers)
				}

				if !reflect.DeepEqual(fsRes.Dropped, tc.wantDrops) {
					t.Errorf("filesystem Dropped = %+v, want %+v", fsRes.Dropped, tc.wantDrops)
				}
				if len(srvRes.Dropped) != 0 {
					t.Errorf("server Dropped = %+v, want none", srvRes.Dropped)
				}
			})
		}
	}
}

// syncBothModes runs `podium sync` with the given adapter against dir as a
// filesystem source and against a server built from dir, and returns each
// target and result.
func syncBothModes(t *testing.T, dir, adapterID string) (string, *sync.Result, string, *sync.Result) {
	t.Helper()
	fsTarget := t.TempDir()
	fsRes, err := sync.Run(sync.Options{
		Delivery:     neverDelivery,
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
		Delivery:     neverDelivery,
		RegistryPath: ts.URL,
		Target:       srvTarget,
		AdapterID:    adapterID,
	})
	if err != nil {
		t.Fatalf("server sync.Run: %v", err)
	}
	return fsTarget, fsRes, srvTarget, srvRes
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
