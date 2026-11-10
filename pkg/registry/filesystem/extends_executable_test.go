package filesystem

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
)

// mcpServerArtifact is an mcp-server manifest with an optional extends: line
// and an optional server_identifier, the executable field these cases leave
// to a parent or restate.
func mcpServerArtifact(desc, extends, serverIdentifier string) string {
	var b strings.Builder
	b.WriteString("---\ntype: mcp-server\nversion: 1.0.0\ndescription: " + desc + "\n")
	if extends != "" {
		b.WriteString("extends: " + extends + "\n")
	}
	if serverIdentifier != "" {
		b.WriteString("server_identifier: " + serverIdentifier + "\n")
	}
	b.WriteString("---\n\nBody.\n")
	return b.String()
}

// hookArtifact is a hook manifest with an optional extends: line and an
// optional hook_action, the executable field the hook cases leave to a parent
// or restate.
func hookArtifact(desc, extends, action string) string {
	var b strings.Builder
	b.WriteString("---\ntype: hook\nversion: 1.0.0\ndescription: " + desc + "\nhook_event: stop\n")
	if extends != "" {
		b.WriteString("extends: " + extends + "\n")
	}
	if action != "" {
		b.WriteString("hook_action: " + action + "\n")
	}
	b.WriteString("---\n\nBody.\n")
	return b.String()
}

// walkDrops walks reg under CollisionPolicyDrop with ResolveExtends and
// returns the records and every drop OnDrop received, in order.
func walkDrops(t *testing.T, reg *Registry) ([]ArtifactRecord, []layer.Drop) {
	t.Helper()
	var drops []layer.Drop
	got, err := reg.Walk(WalkOptions{
		CollisionPolicy: CollisionPolicyDrop,
		ResolveExtends:  true,
		OnDrop:          func(d layer.Drop) { drops = append(drops, d) },
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return got, drops
}

// inheritanceDrop is the drop the executable-field rule reports for id.
func inheritanceDrop(id string, fields ...string) layer.Drop {
	ie := &layer.ExecutableInheritance{ArtifactID: id, Fields: fields}
	return layer.Drop{ArtifactID: id, Code: layer.InvalidArtifactCode, Reason: ie.Reason()}
}

// Spec: §4.6, §13.11.3 — a cross-layer hook child that omits hook_action is
// dropped with ingest.invalid_artifact and a reason that names neither the
// parent nor its layer, and the parent is kept. The filesystem source runs
// no lint, so the hook example reaches the executable-field rule here.
func TestWalk_ExecutableInheritanceDropsCrossLayerChild(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"base", "team"}, map[string]string{
		"base/core/notify": hookArtifact("parent", "", "./notify.sh"),
		"team/team/notify": hookArtifact("child", "core/notify", ""),
	})
	got, drops := walkDrops(t, reg)
	want := []layer.Drop{inheritanceDrop("team/notify", "hook_action")}
	if !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if strings.Contains(drops[0].Reason, "core/notify") || strings.Contains(drops[0].Reason, "base") {
		t.Errorf("reason %q names the parent or its layer", drops[0].Reason)
	}
	if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"core/notify"}) {
		t.Errorf("records = %v, want the parent alone", ids)
	}
}

// Spec: §4.6, §13.11.3 — a child that restates the executable field across
// layers is kept and served with its own value, and a child in its parent's
// layer inherits the field unchanged.
func TestWalk_ExecutableInheritanceKeepsRestatedAndSameLayerChildren(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/p":          mcpServerArtifact("parent", "", "parent-id"),
		"l1/q":          mcpServerArtifact("same-layer", "p", ""),
		"l2/restated":   mcpServerArtifact("restated", "p", "own-id"),
		"l2/unrelated":  mcpServerArtifact("unrelated", "", "u"),
		"l2/z/samelyr":  mcpServerArtifact("same-layer parent", "", "z-id"),
		"l2/z/samelyr2": mcpServerArtifact("same-layer child", "z/samelyr", ""),
	})
	got, drops := walkDrops(t, reg)
	if len(drops) != 0 {
		t.Fatalf("drops = %+v, want none", drops)
	}
	served := map[string]string{}
	for _, rec := range got {
		served[rec.ID] = rec.Artifact.ServerIdentifier
	}
	want := map[string]string{
		"p": "parent-id", "q": "parent-id", "restated": "own-id",
		"unrelated": "u", "z/samelyr": "z-id", "z/samelyr2": "z-id",
	}
	if !reflect.DeepEqual(served, want) {
		t.Errorf("served server_identifier = %v, want %v", served, want)
	}
}

// Spec: §13.11.3 — a drop cascades: D extends C, C is refused, and no other
// layer contributes C, so D is dropped with the unresolved-parent reason.
// D sorts before C and is refused one round later, and the report still lists
// refusals byte-wise by ID.
func TestWalk_ExecutableInheritanceCascadesInIDOrder(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/p":   mcpServerArtifact("parent", "", "parent-id"),
		"l2/x/c": mcpServerArtifact("refused", "p", ""),
		"l2/a/d": mcpServerArtifact("stranded", "x/c", "d-id"),
	})
	got, drops := walkDrops(t, reg)
	want := []layer.Drop{
		{ArtifactID: "a/d", Code: layer.InvalidArtifactCode, Reason: layer.UnresolvedParentReason("x/c")},
		inheritanceDrop("x/c", "server_identifier"),
	}
	if !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"p"}) {
		t.Errorf("records = %v, want p alone", ids)
	}
}

// Spec: §13.11.3 — a refused same-ID copy lets a higher layer's overlay fall
// through to the next-lower copy, and an overlay with no lower copy left is
// dropped with the unresolved-parent reason. Refusals under one ID are
// reported from the lowest layer to the highest.
func TestWalk_ExecutableInheritanceSameIDFallThroughAndCascade(t *testing.T) {
	t.Parallel()
	t.Run("overlay falls through to the lower copy", func(t *testing.T) {
		t.Parallel()
		reg := openDropFixture(t, []string{"l1", "l2", "l3"}, map[string]string{
			"l1/x": mcpServerArtifact("from-l1", "", "l1-id"),
			"l2/x": mcpServerArtifact("from-l2", "x", ""),
			"l3/x": mcpServerArtifact("from-l3", "x", "l3-id"),
		})
		got, drops := walkDrops(t, reg)
		if want := []layer.Drop{inheritanceDrop("x", "server_identifier")}; !reflect.DeepEqual(drops, want) {
			t.Fatalf("drops = %+v, want %+v", drops, want)
		}
		if len(got) != 1 || got[0].Layer.ID != "l3" || got[0].Artifact.ServerIdentifier != "l3-id" {
			t.Errorf("records = %+v, want l3's x with its own server_identifier", got)
		}
		if strings.Contains(string(got[0].ArtifactBytes), "from-l2") {
			t.Errorf("served bytes carry the dropped l2 copy:\n%s", got[0].ArtifactBytes)
		}
	})
	t.Run("overlay with no lower copy left cascades", func(t *testing.T) {
		t.Parallel()
		reg := openDropFixture(t, []string{"l1", "l2", "l3"}, map[string]string{
			"l1/p": mcpServerArtifact("parent", "", "parent-id"),
			"l2/c": mcpServerArtifact("refused", "p", ""),
			"l3/c": mcpServerArtifact("overlay", "c", "own-id"),
		})
		got, drops := walkDrops(t, reg)
		want := []layer.Drop{
			inheritanceDrop("c", "server_identifier"),
			{ArtifactID: "c", Code: layer.InvalidArtifactCode, Reason: layer.UnresolvedParentReason("c")},
		}
		if !reflect.DeepEqual(drops, want) {
			t.Fatalf("drops = %+v, want %+v", drops, want)
		}
		if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"p"}) {
			t.Errorf("records = %v, want p alone", ids)
		}
	})
}

// Spec: §4.6, §13.11.3, §11 — a higher-precedence record that collided only
// with a refused record is kept, as a server that never stored the refused
// record keeps it, and its first-round collision is not reported.
func TestWalk_ExecutableInheritanceRetractsCollision(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2", "l3"}, map[string]string{
		"l1/p": mcpServerArtifact("parent", "", "parent-id"),
		"l2/x": mcpServerArtifact("refused", "p", ""),
		"l3/x": mcpServerArtifact("kept", "", "l3-id"),
	})
	got, drops := walkDrops(t, reg)
	if want := []layer.Drop{inheritanceDrop("x", "server_identifier")}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	x := recByID(t, got, "x")
	if x.Layer.ID != "l3" {
		t.Errorf("x kept from %q, want l3", x.Layer.ID)
	}
}

// Spec: §13.11.3 — collision drops come first, in dedupe order, followed by
// refusals, although the refused a/child sorts before z/clash. A collision
// whose kept record is not refused stays reported, and a second walk of the
// same registry reports the same slice.
func TestWalk_ExecutableInheritanceReportsCollisionsFirst(t *testing.T) {
	t.Parallel()
	agent := "---\ntype: agent\nversion: 1.0.0\ndescription: clash\n---\n\nBody.\n"
	reg := openDropFixture(t, []string{"L1", "L2"}, map[string]string{
		"L1/P":       mcpServerArtifact("parent", "", "parent-id"),
		"L1/z/clash": agent,
		"L2/a/child": mcpServerArtifact("refused", "P", ""),
		"L2/z/clash": agent,
	})
	_, first := walkDrops(t, reg)
	want := []layer.Drop{
		layer.Collision{ArtifactID: "z/clash", Layer: "L2", ExistingLayer: "L1"}.Drop(),
		inheritanceDrop("a/child", "server_identifier"),
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("drops = %+v, want %+v", first, want)
	}
	if _, second := walkDrops(t, reg); !reflect.DeepEqual(second, first) {
		t.Errorf("second walk drops = %+v, want %+v", second, first)
	}
}

// Spec: §4.6 — without OnDrop, a refused record fails the walk, and the
// error carries the *layer.ExecutableInheritance a caller matches.
func TestWalk_ExecutableInheritanceFailsWithoutOnDrop(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/p": mcpServerArtifact("parent", "", "parent-id"),
		"l2/c": mcpServerArtifact("child", "p", ""),
	})
	for _, policy := range []CollisionPolicy{CollisionPolicyHighestWins, CollisionPolicyError} {
		got, err := reg.Walk(WalkOptions{CollisionPolicy: policy, ResolveExtends: true})
		var ie *layer.ExecutableInheritance
		if !errors.As(err, &ie) {
			t.Fatalf("policy %d: Walk err = %v, want an ExecutableInheritance", policy, err)
		}
		if ie.ArtifactID != "c" || !reflect.DeepEqual(ie.Fields, []string{"server_identifier"}) {
			t.Errorf("policy %d: ExecutableInheritance = %+v", policy, ie)
		}
		if !strings.Contains(err.Error(), layer.InvalidArtifactCode) {
			t.Errorf("policy %d: err = %q, want the %s code", policy, err, layer.InvalidArtifactCode)
		}
		if got != nil {
			t.Errorf("policy %d: records = %v, want nil", policy, idsOf(got))
		}
	}
}

// Spec: §4.6 withheld — a field path that would name a chain parent drops the
// child with the shared unreadable-fields reason before the rewrite pass, so
// the walk succeeds and the reason carries no parent ID.
func TestWalk_ExecutableInheritanceUnreadableFieldsDrop(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"L1", "L2"}, map[string]string{
		"L1/shared/base": "---\ntype: agent\nversion: 1.0.0\ndescription: base\nshared/base: secret\n---\n\nBody.\n",
		"L2/team/child":  "---\ntype: agent\nversion: 1.0.0\ndescription: child\nextends: shared/base\n---\n\nBody.\n",
	})
	got, drops := walkDrops(t, reg)
	want := []layer.Drop{{ArtifactID: "team/child", Code: layer.InvalidArtifactCode, Reason: layer.UnreadableExecutableFieldsReason}}
	if !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"shared/base"}) {
		t.Errorf("records = %v, want shared/base alone", ids)
	}
}

// Spec: §13.11.3 — a reference to an ID no layer ever contributed, and a
// cross-type chain, still fail the walk and report no drop.
func TestWalk_ExecutableInheritanceKeepsResolverFailures(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]string{
		"extends.unresolved": {
			"l1/c": mcpServerArtifact("child", "missing/parent", "id"),
		},
		"extends.type_mismatch": {
			"l1/p": mcpServerArtifact("parent", "", "id"),
			"l2/c": "---\ntype: agent\nversion: 1.0.0\ndescription: child\nextends: p\n---\n\nBody.\n",
		},
	}
	for code, files := range cases {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			reg := openDropFixture(t, []string{"l1", "l2"}, files)
			var drops []layer.Drop
			_, err := reg.Walk(WalkOptions{
				CollisionPolicy: CollisionPolicyDrop,
				ResolveExtends:  true,
				OnDrop:          func(d layer.Drop) { drops = append(drops, d) },
			})
			if err == nil || !strings.Contains(err.Error(), code) {
				t.Fatalf("Walk err = %v, want %s", err, code)
			}
			if len(drops) != 0 {
				t.Errorf("drops = %+v, want none", drops)
			}
		})
	}
}

// Spec: §4.6, §13.11.3 — a two-layer same-ID overlay that omits the executable
// field is dropped, and x serves L1's record with L1's authored bytes, which
// hold none of the dropped overlay's values.
func TestWalk_ExecutableInheritanceDropsSameIDOverlay(t *testing.T) {
	t.Parallel()
	base := mcpServerArtifact("from-l1", "", "l1-id")
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/x": base,
		"l2/x": mcpServerArtifact("from-l2", "x", ""),
	})
	got, drops := walkDrops(t, reg)
	if want := []layer.Drop{inheritanceDrop("x", "server_identifier")}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	x := recByID(t, got, "x")
	if x.Layer.ID != "l1" || len(got) != 1 {
		t.Fatalf("records = %v, x from %q, want l1's x alone", idsOf(got), x.Layer.ID)
	}
	if string(x.ArtifactBytes) != base {
		t.Errorf("served bytes = %q, want l1's authored bytes %q", x.ArtifactBytes, base)
	}
}

// Spec: §4.6, §13.11.3 — the filesystem side of the same-ID pin layout. L2's
// x 1.2.0 declares an unpinned extends: x and omits the undeclared executable
// key acme_hook. A filesystem layer holds one record per ID, so the resolver
// takes L1's x 1.0.0 as the parent, tests a cross-layer edge, and drops the
// overlay, and x serves L1's 1.0.0.
//
// A server that holds a stored x 1.1.0 from L2 resolves the same 1.2.0's pin
// to that within-layer version and accepts it (D-5; the ingest case TEST-2 (n)
// pins that outcome). A later change to same-ID parent selection shows here as
// a test change.
func TestWalk_ExecutableInheritanceDropsUnpinnedSameIDOverlay(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/x": "---\ntype: agent\nversion: 1.0.0\ndescription: base\nacme_hook: run.sh\n---\n\nBody.\n",
		"l2/x": "---\ntype: agent\nversion: 1.2.0\ndescription: overlay\nextends: x\n---\n\nBody.\n",
	})
	got, drops := walkDrops(t, reg)
	if want := []layer.Drop{inheritanceDrop("x", "acme_hook")}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if !strings.Contains(drops[0].Reason, "acme_hook") {
		t.Errorf("reason %q does not name acme_hook", drops[0].Reason)
	}
	x := recByID(t, got, "x")
	if x.Layer.ID != "l1" || x.Artifact.Version != "1.0.0" {
		t.Errorf("x served from %q at %q, want l1's 1.0.0", x.Layer.ID, x.Artifact.Version)
	}
}

// Spec: §4.6, §13.11.3 — the rule compares the cross-layer child against its
// parent's chain-resolved values, so a field the parent inherits within its
// own layer counts as set by the parent. B (L2) extends A (L1), which extends
// A0 (L1) and leaves acme_hook to it, so B is dropped for omitting acme_hook,
// and A keeps the value it inherits within L1.
func TestWalk_ExecutableInheritanceDropsChildOfInheritedField(t *testing.T) {
	t.Parallel()
	agent := func(desc, extends, hook string) string {
		s := "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\n"
		if extends != "" {
			s += "extends: " + extends + "\n"
		}
		if hook != "" {
			s += "acme_hook: " + hook + "\n"
		}
		return s + "---\n\nBody.\n"
	}
	reg := openDropFixture(t, []string{"L1", "L2"}, map[string]string{
		"L1/a0": agent("grandparent", "", "run.sh"),
		"L1/a":  agent("parent", "a0", ""),
		"L2/b":  agent("child", "a", ""),
	})
	got, drops := walkDrops(t, reg)
	if want := []layer.Drop{inheritanceDrop("b", "acme_hook")}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"a", "a0"}) {
		t.Fatalf("records = %v, want a and a0", ids)
	}
	if a := recByID(t, got, "a"); !strings.Contains(string(a.ArtifactBytes), "acme_hook: run.sh") {
		t.Errorf("a serves no inherited acme_hook:\n%s", a.ArtifactBytes)
	}
}

// Spec: §4.6, §13.11.3 — inheritance within one layer is unchanged: a chain in
// L2 whose members leave hook_action to the chain root is kept, and every
// member serves the root's value. The filesystem source runs no lint, so the
// hook children reach the resolver without the hook_action that server lint
// requires on the authored record.
func TestWalk_ExecutableInheritanceKeepsWithinLayerHookChain(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/other":     hookArtifact("unrelated", "", "./other.sh"),
		"l2/core/base": hookArtifact("root", "", "./notify.sh"),
		"l2/core/mid":  hookArtifact("mid", "core/base", ""),
		"l2/core/leaf": hookArtifact("leaf", "core/mid", ""),
	})
	got, drops := walkDrops(t, reg)
	if len(drops) != 0 {
		t.Fatalf("drops = %+v, want none", drops)
	}
	served := map[string]string{}
	for _, rec := range got {
		served[rec.ID] = rec.Artifact.HookAction
	}
	want := map[string]string{
		"other": "./other.sh", "core/base": "./notify.sh",
		"core/mid": "./notify.sh", "core/leaf": "./notify.sh",
	}
	if !reflect.DeepEqual(served, want) {
		t.Errorf("served hook_action = %v, want %v", served, want)
	}
}

// Spec: §4.6, §13.11.3 — the divergent-parent fixture. L1 holds p 2.0.0, and
// L2 holds an overlay of p that restates server_identifier and a child c with
// an unpinned extends: p that omits it, where c sorts before p. The filesystem
// resolver takes the highest-precedence p, L2's overlay, so the edge it tests
// is L2 to L2 and c is kept with the overlay's value.
//
// A server ingesting L2 processes c before p in canonical-ID order and
// resolves c's pin against stored rows alone, so it selects L1's p 2.0.0,
// tests a cross-layer edge, and rejects c. This is the parent-selection
// divergence §13.11.3 states (SPEC-3, D-5). The fixture records the
// filesystem outcome so a later change to parent selection shows here as a
// test change.
func TestWalk_ExecutableInheritanceDivergentParentKeepsChild(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/p": "---\ntype: mcp-server\nversion: 2.0.0\ndescription: base\nserver_identifier: l1-id\n---\n\nBody.\n",
		"l2/p": "---\ntype: mcp-server\nversion: 2.1.0\ndescription: overlay\nextends: p\nserver_identifier: l2-id\n---\n\nBody.\n",
		"l2/c": mcpServerArtifact("child", "p", ""),
	})
	got, drops := walkDrops(t, reg)
	if len(drops) != 0 {
		t.Fatalf("drops = %+v, want none", drops)
	}
	served := map[string]string{}
	for _, rec := range got {
		served[rec.ID] = rec.Layer.ID + ":" + rec.Artifact.ServerIdentifier
	}
	if want := map[string]string{"c": "l2:l2-id", "p": "l2:l2-id"}; !reflect.DeepEqual(served, want) {
		t.Errorf("served = %v, want %v", served, want)
	}
}

// Spec: §4.6, §13.11.3 — a plain-skill parent sets no executable field, so a
// cross-layer extends: child that restates none is kept. The parent's chain
// block is empty in the executable-field comparison, as the server's empty
// stored Frontmatter is, even though its SKILL.md carries allowed-tools and a
// metadata mapping. The walk then synthesizes the parent's ARTIFACT.md.
func TestWalk_ExecutableInheritancePlainSkillParentSetsNoField(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - L1\n  - L2\n"},
		testharness.WriteTreeOption{Path: "L1/ops/base/SKILL.md", Content: "---\nname: base\ndescription: Base skill.\n" +
			"allowed-tools:\n  - Bash\nmetadata:\n  owner: ops\n---\n\nBase body.\n"},
		testharness.WriteTreeOption{Path: "L2/ops/child/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\nextends: ops/base\n---\n"},
		testharness.WriteTreeOption{Path: "L2/ops/child/SKILL.md", Content: "---\nname: child\ndescription: Child skill.\n---\n\nChild body.\n"},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, drops := walkDrops(t, reg)
	if len(drops) != 0 {
		t.Fatalf("drops = %+v, want none", drops)
	}
	var child, base *ArtifactRecord
	for i := range got {
		switch got[i].ID {
		case "ops/child":
			child = &got[i]
		case "ops/base":
			base = &got[i]
		}
	}
	if child == nil || base == nil {
		t.Fatalf("walk returned %d records, want ops/base and ops/child", len(got))
	}
	if string(child.ArtifactBytes) == string(child.AuthoredBytes) {
		t.Errorf("ops/child ArtifactBytes = authored bytes, want the merged document")
	}
	want, err := manifest.SynthesizedSkillDocument(manifest.AutoVersion(0))
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument: %v", err)
	}
	if string(base.ArtifactBytes) != string(want) {
		t.Errorf("ops/base ArtifactBytes = %q, want %q", base.ArtifactBytes, want)
	}
}
