package filesystem

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
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
	hook := func(desc, extends, action string) string {
		s := "---\ntype: hook\nversion: 1.0.0\ndescription: " + desc + "\nhook_event: stop\n"
		if extends != "" {
			s += "extends: " + extends + "\n"
		}
		if action != "" {
			s += "hook_action: " + action + "\n"
		}
		return s + "---\n\nBody.\n"
	}
	reg := openDropFixture(t, []string{"base", "team"}, map[string]string{
		"base/core/notify": hook("parent", "", "./notify.sh"),
		"team/team/notify": hook("child", "core/notify", ""),
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
