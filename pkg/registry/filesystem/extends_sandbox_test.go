package filesystem

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

// sandboxAgent is an agent manifest with an optional extends: line and an
// optional sandbox_profile. An agent sets no declared executable field, so a
// cross-layer edge between two of them reaches the sandbox-profile rule alone.
func sandboxAgent(desc, extends, profile string) string {
	var b strings.Builder
	b.WriteString("---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\n")
	if extends != "" {
		b.WriteString("extends: " + extends + "\n")
	}
	if profile != "" {
		b.WriteString("sandbox_profile: " + profile + "\n")
	}
	b.WriteString("---\n\nBody.\n")
	return b.String()
}

// sandboxDrop is the drop the §4.6 sandbox-profile rule reports for id.
func sandboxDrop(id string) layer.Drop {
	return layer.Drop{ArtifactID: id, Code: layer.InvalidArtifactCode, Reason: layer.SandboxProfileConflictReason}
}

// servedProfiles maps each record's ID to the sandbox_profile it serves.
func servedProfiles(recs []ArtifactRecord) map[string]string {
	out := make(map[string]string, len(recs))
	for _, rec := range recs {
		out[rec.ID] = string(rec.Artifact.SandboxProfile)
	}
	return out
}

// Spec: §4.6, §13.11.3 — the sandbox-profile rule holds on a same-layer edge,
// which the executable-field rule never tests. A child that sets
// network-isolated under a read-only-fs parent in the same layer is dropped
// with ingest.invalid_artifact and the shared reason, and the parent is kept.
func TestWalk_SandboxProfileConflictDropsSameLayerChild(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1"}, map[string]string{
		"l1/a/parent": sandboxAgent("parent", "", "read-only-fs"),
		"l1/b/child":  sandboxAgent("child", "a/parent", "network-isolated"),
	})
	got, drops := walkDrops(t, reg)
	if want := []layer.Drop{sandboxDrop("b/child")}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if want := map[string]string{"a/parent": "read-only-fs"}; !reflect.DeepEqual(servedProfiles(got), want) {
		t.Errorf("served profiles = %v, want %v", servedProfiles(got), want)
	}
}

// Spec: §4.6, §13.11.3 — a same-layer drop cascades: a/grand extends the
// refused q/child, and no other layer contributes q/child, so a/grand is
// dropped with the unresolved-parent reason one round later. The report lists
// the refusals byte-wise by ID, so a/grand comes first although its round came
// second.
func TestWalk_SandboxProfileConflictCascadesWithinLayer(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1"}, map[string]string{
		"l1/p/parent":  sandboxAgent("parent", "", "read-only-fs"),
		"l1/q/child":   sandboxAgent("child", "p/parent", "network-isolated"),
		"l1/a/grand":   sandboxAgent("grandchild", "q/child", ""),
		"l1/z/sibling": sandboxAgent("sibling", "", "seccomp-strict"),
	})
	got, drops := walkDrops(t, reg)
	want := []layer.Drop{
		{ArtifactID: "a/grand", Code: layer.InvalidArtifactCode, Reason: layer.UnresolvedParentReason("q/child")},
		sandboxDrop("q/child"),
	}
	if !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"p/parent", "z/sibling"}) {
		t.Errorf("records = %v, want p/parent and z/sibling", ids)
	}
}

// Spec: §4.6, §13.11.3 — the sandbox-profile rule is checked before the
// executable-field rule. A cross-layer child that conflicts on
// sandbox_profile and also omits the server_identifier its parent sets
// reports the sandbox-profile drop, which is the reason the server gives.
func TestWalk_SandboxProfileConflictPrecedesExecutableField(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: mcp-server\nversion: 1.0.0\ndescription: parent\nserver_identifier: parent-id\nsandbox_profile: read-only-fs\n---\n\nBody.\n"
	child := "---\ntype: mcp-server\nversion: 1.0.0\ndescription: child\nextends: p\nsandbox_profile: network-isolated\n---\n\nBody.\n"
	reg := openDropFixture(t, []string{"l1", "l2"}, map[string]string{
		"l1/p": parent,
		"l2/c": child,
	})
	got, drops := walkDrops(t, reg)
	if want := []layer.Drop{sandboxDrop("c")}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v", drops, want)
	}
	if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"p"}) {
		t.Errorf("records = %v, want p alone", ids)
	}
}

// Spec: §4.6 — without OnDrop, a sandbox-profile refusal fails the walk with
// an error that matches layer.ErrSandboxProfileConflict, carries the
// ingest.invalid_artifact code, and names neither profile value.
func TestWalk_SandboxProfileConflictFailsWithoutOnDrop(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1"}, map[string]string{
		"l1/a/parent": sandboxAgent("parent", "", "read-only-fs"),
		"l1/b/child":  sandboxAgent("child", "a/parent", "network-isolated"),
	})
	for _, policy := range []CollisionPolicy{CollisionPolicyHighestWins, CollisionPolicyError} {
		got, err := reg.Walk(WalkOptions{CollisionPolicy: policy, ResolveExtends: true})
		if !errors.Is(err, layer.ErrSandboxProfileConflict) {
			t.Fatalf("policy %d: Walk err = %v, want ErrSandboxProfileConflict", policy, err)
		}
		if !strings.Contains(err.Error(), layer.InvalidArtifactCode) {
			t.Errorf("policy %d: err = %q, want the %s code", policy, err, layer.InvalidArtifactCode)
		}
		for _, value := range []string{"read-only-fs", "network-isolated", "a/parent"} {
			if strings.Contains(err.Error(), value) {
				t.Errorf("policy %d: err = %q names %q", policy, err, value)
			}
		}
		if got != nil {
			t.Errorf("policy %d: records = %v, want nil", policy, idsOf(got))
		}
	}
}

// Spec: §4.6, §13.11.3 — chains that obey the rule settle with no refusal.
// In the first chain the root is unrestricted, the parent tightens it to
// read-only-fs, and the child leaves the field unset, so the parent and the
// child serve read-only-fs. In the second the root is network-isolated, the
// parent leaves the field unset, and the child restates network-isolated, so
// every member serves network-isolated.
//
// Within each chain the IDs sort root, parent, child, so resolveExtends
// rewrites the parent before it reaches the child and the child's edge is
// checked again against the parent's merged record. A merged parent never
// conflicts with the profile it was merged under, so the rewrite pass raises
// no refusal; a resolver that exempted rewritten records would hide a break
// in that property, and this order is what makes the case observe it.
func TestWalk_SandboxProfileChainsSettleWithoutRefusal(t *testing.T) {
	t.Parallel()
	reg := openDropFixture(t, []string{"l1"}, map[string]string{
		"l1/ro/a-root":   sandboxAgent("root", "", "unrestricted"),
		"l1/ro/b-parent": sandboxAgent("parent", "ro/a-root", "read-only-fs"),
		"l1/ro/c-child":  sandboxAgent("child", "ro/b-parent", ""),
		"l1/ni/a-root":   sandboxAgent("root", "", "network-isolated"),
		"l1/ni/b-parent": sandboxAgent("parent", "ni/a-root", ""),
		"l1/ni/c-child":  sandboxAgent("child", "ni/b-parent", "network-isolated"),
	})
	got, drops := walkDrops(t, reg)
	if len(drops) != 0 {
		t.Fatalf("drops = %+v, want none", drops)
	}
	want := map[string]string{
		"ro/a-root": "unrestricted", "ro/b-parent": "read-only-fs", "ro/c-child": "read-only-fs",
		"ni/a-root": "network-isolated", "ni/b-parent": "network-isolated", "ni/c-child": "network-isolated",
	}
	if got := servedProfiles(got); !reflect.DeepEqual(got, want) {
		t.Errorf("served profiles = %v, want %v", got, want)
	}
	for _, id := range []string{"ro/c-child", "ni/b-parent"} {
		rec := recByID(t, got, id)
		if n := strings.Count(string(rec.ArtifactBytes), "sandbox_profile:"); n != 1 {
			t.Errorf("%s: served bytes carry %d sandbox_profile lines, want 1:\n%s", id, n, rec.ArtifactBytes)
		}
	}
}
