package ingest_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// sbAgent is execAgent with a sandbox_profile line when profile is non-empty.
func sbAgent(version, extends, profile string) string {
	extra := ""
	if profile != "" {
		extra = "sandbox_profile: " + profile + "\n"
	}
	return execAgent(version, extends, extra)
}

// restrictingProfiles are the §4.4.1 values a non-disclosure assertion
// searches for. unrestricted is absent because SandboxProfileConflictReason
// contains that word itself.
var restrictingProfiles = []string{"read-only-fs", "network-isolated", "seccomp-strict"}

// assertSandboxConflict fails unless res holds exactly one rejection, of id,
// under the §4.6 sandbox-profile rule, with a reason that names no
// restricting profile and not parentID, and the details the rule defines.
func assertSandboxConflict(t *testing.T, res *ingest.Result, id, parentID string) {
	t.Helper()
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one rejection of %s", res.Rejected, id)
	}
	rj := res.Rejected[0]
	if rj.ArtifactID != id || rj.Code != layer.InvalidArtifactCode || rj.Reason != layer.SandboxProfileConflictReason {
		t.Errorf("rejection = %+v, want %s %s %q", rj, id, layer.InvalidArtifactCode, layer.SandboxProfileConflictReason)
	}
	for _, withheld := range append([]string{parentID}, restrictingProfiles...) {
		if strings.Contains(rj.Reason, withheld) {
			t.Errorf("reason %q contains %q", rj.Reason, withheld)
		}
	}
	want := map[string]any{"constraint": layer.SandboxProfileConflictConstraint}
	if !reflect.DeepEqual(rj.Details, want) {
		t.Errorf("details = %#v, want %#v", rj.Details, want)
	}
}

// assertChainUnresolved fails unless res holds exactly one chain-unresolved
// rejection without details.
func assertChainUnresolved(t *testing.T, res *ingest.Result) {
	t.Helper()
	if len(res.Rejected) != 1 || res.Rejected[0].Reason != "extends: parent chain does not resolve" ||
		res.Rejected[0].Code != layer.InvalidArtifactCode || res.Rejected[0].Details != nil {
		t.Fatalf("rejected = %+v, want one chain-unresolved rejection without details", res.Rejected)
	}
}

// assertServedProfile fails unless the merged load of id@version serves
// exactly one sandbox_profile line, set to want.
func assertServedProfile(t *testing.T, st store.Store, id, version, want string) {
	t.Helper()
	fm := string(loadMerged(t, st, id, version).Frontmatter)
	if strings.Count(fm, "sandbox_profile:") != 1 || !strings.Contains(fm, "sandbox_profile: "+want+"\n") {
		t.Errorf("merged frontmatter of %s@%s = %q, want sandbox_profile: %s", id, version, fm, want)
	}
}

// A child whose restricting sandbox_profile differs from its parent's is
// rejected on a same-layer edge and on a cross-layer edge alike, with a
// reason that names neither profile and no parent, and is never stored.
// Spec: §4.6, §7.3.1
// Matrix: §6.10 (ingest.invalid_artifact)
func TestIngest_SandboxConflictRejectsOnEveryEdge(t *testing.T) {
	t.Parallel()
	// The parent ID sorts before the child's, so ingest stores it before the
	// child resolves its pin within the same snapshot; a child that sorted
	// first would be refused for an unresolved reference instead.
	st := newStore(t)
	same := ingestSnapshot(t, st, "L1", fstest.MapFS{
		"a/parent/ARTIFACT.md": artifactFile(sbAgent("1.0.0", "", "read-only-fs")),
		"b/child/ARTIFACT.md":  artifactFile(sbAgent("2.0.0", "a/parent@1.x", "network-isolated")),
	})
	if same.Accepted != 1 {
		t.Errorf("same-layer accepted = %d, want the parent alone", same.Accepted)
	}
	assertSandboxConflict(t, same, "b/child", "a/parent")
	assertNotStored(t, st, "b/child", "2.0.0")

	ingestOne(t, st, "L1", "shared/parent", sbAgent("1.0.0", "", "read-only-fs"))
	cross := ingestOne(t, st, "L2", "finance/child", sbAgent("2.0.0", "shared/parent@1.x", "seccomp-strict"))
	if cross.Accepted != 0 {
		t.Errorf("cross-layer accepted = %d, want 0", cross.Accepted)
	}
	assertSandboxConflict(t, cross, "finance/child", "shared/parent")
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// A pair where either side is unset or unrestricted, or both sides are equal,
// is accepted on both edges, and the read path serves the merged value: the
// parent's value unless it is unset or unrestricted, else the child's.
// Spec: §4.6
func TestIngest_SandboxConflictAcceptsAndServesMergedValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, parent, child, want string
	}{
		{"unrestricted parent, restricting child", "unrestricted", "read-only-fs", "read-only-fs"},
		{"unset parent, restricting child", "", "network-isolated", "network-isolated"},
		{"restricting parent, unset child", "read-only-fs", "", "read-only-fs"},
		{"restricting parent, unrestricted child", "seccomp-strict", "unrestricted", "seccomp-strict"},
		{"equal restricting values", "network-isolated", "network-isolated", "network-isolated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newStore(t)
			// The parent ID sorts before the child's, so it is stored before
			// the child resolves its pin within the same snapshot.
			same := ingestSnapshot(t, st, "L1", fstest.MapFS{
				"a/parent/ARTIFACT.md": artifactFile(sbAgent("1.0.0", "", tc.parent)),
				"b/child/ARTIFACT.md":  artifactFile(sbAgent("2.0.0", "a/parent@1.x", tc.child)),
			})
			if same.Accepted != 2 || len(same.Rejected) != 0 {
				t.Fatalf("same-layer: accepted=%d rejected=%+v, want both accepted", same.Accepted, same.Rejected)
			}
			assertServedProfile(t, st, "b/child", "2.0.0", tc.want)

			ingestOne(t, st, "L1", "shared/parent", sbAgent("1.0.0", "", tc.parent))
			cross := ingestOne(t, st, "L2", "finance/child", sbAgent("2.0.0", "shared/parent@1.x", tc.child))
			if cross.Accepted != 1 || len(cross.Rejected) != 0 {
				t.Fatalf("cross-layer: accepted=%d rejected=%+v, want a clean accept", cross.Accepted, cross.Rejected)
			}
			assertServedProfile(t, st, "finance/child", "2.0.0", tc.want)
		})
	}
}

// The parent's value is its chain-resolved value, so a grandparent's
// network-isolated that an unset parent inherits conflicts with the child's
// read-only-fs.
// Spec: §4.6
func TestIngest_SandboxConflictFromGrandparent(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	// The IDs sort root, mid, child, so each parent is stored before the
	// artifact that extends it resolves its pin.
	res := ingestSnapshot(t, st, "L1", fstest.MapFS{
		"a/root/ARTIFACT.md":  artifactFile(sbAgent("1.0.0", "", "network-isolated")),
		"b/mid/ARTIFACT.md":   artifactFile(sbAgent("1.0.0", "a/root@1.x", "")),
		"c/child/ARTIFACT.md": artifactFile(sbAgent("1.0.0", "b/mid@1.x", "read-only-fs")),
	})
	if res.Accepted != 2 {
		t.Errorf("accepted = %d, want the root and the intermediate", res.Accepted)
	}
	assertSandboxConflict(t, res, "c/child", "a/root")
	assertNotStored(t, st, "c/child", "1.0.0")
}

// A value §4.4.1 does not list is a restricting value, so it conflicts with
// a parent's known restricting value.
// Spec: §4.6
func TestIngest_SandboxConflictUnknownChildValue(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", sbAgent("1.0.0", "", "read-only-fs"))

	res := ingestOne(t, st, "L2", "finance/child", sbAgent("2.0.0", "shared/parent@1.x", "acme-jail"))
	assertSandboxConflict(t, res, "finance/child", "shared/parent")
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// A cross-layer child that both conflicts on sandbox_profile and leaves an
// executable field to its parent reports the sandbox-profile rejection, the
// precedence both modes apply.
// Spec: §4.6
func TestIngest_SandboxConflictPrecedesExecutableFieldRule(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "", "x_owner: platform\nsandbox_profile: read-only-fs\n"))

	res := ingestOne(t, st, "L2", "finance/child", sbAgent("2.0.0", "shared/parent@1.x", "network-isolated"))
	assertSandboxConflict(t, res, "finance/child", "shared/parent")
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// A same-layer child that authors a restricting profile reads its parent's
// stored chain, so a chain missing an ancestor or a chain that loops rejects
// it with the chain-unresolved reason and no details.
// Spec: §4.6 hidden parents (withheld)
func TestIngest_SandboxConflictSameLayerUnresolvedChainRejects(t *testing.T) {
	t.Parallel()
	t.Run("missing ancestor", func(t *testing.T) {
		t.Parallel()
		st := newStore(t)
		seedChain(t, st, "")
		gone := chainFaultStore{Store: st, ancestorID: "shared/root", err: store.ErrNotFound}
		// shared/parent lives in L1, so this child's edge stays in one layer.
		res := ingestOne(t, gone, "L1", "shared/child", sbAgent("2.0.0", "shared/parent@1.x", "read-only-fs"))
		assertChainUnresolved(t, res)
		assertNotStored(t, st, "shared/child", "2.0.0")
	})
	t.Run("looping chain", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		st := newStore(t)
		for _, row := range []store.ManifestRecord{
			{ArtifactID: "shared/a", ExtendsPin: "shared/b@1.0.0"},
			{ArtifactID: "shared/b", ExtendsPin: "shared/a@1.0.0"},
		} {
			row.TenantID, row.Version, row.Type, row.Layer = "tenant-1", "1.0.0", "agent", "L1"
			row.ContentHash, row.Sensitivity = "sha256:"+strings.Repeat("0", 64), "low"
			if err := st.PutManifest(ctx, row); err != nil {
				t.Fatalf("PutManifest %s: %v", row.ArtifactID, err)
			}
		}
		res := ingestOne(t, st, "L1", "shared/child", sbAgent("2.0.0", "shared/a@1.0.0", "seccomp-strict"))
		assertChainUnresolved(t, res)
		assertNotStored(t, st, "shared/child", "2.0.0")
	})
}

// The walk is gated on a cross-layer edge or a restricting child, so a
// same-layer child that leaves sandbox_profile unset or unrestricted reads no
// chain and is accepted over a parent whose stored chain is missing an
// ancestor.
// Spec: §4.6
func TestIngest_SandboxConflictGateSkipsSameLayerWalk(t *testing.T) {
	t.Parallel()
	for _, profile := range []string{"", "unrestricted"} {
		t.Run("profile="+profile, func(t *testing.T) {
			t.Parallel()
			st := newStore(t)
			seedChain(t, st, "")
			gone := chainFaultStore{Store: st, ancestorID: "shared/root", err: store.ErrNotFound}
			res := ingestOne(t, gone, "L1", "shared/child", sbAgent("2.0.0", "shared/parent@1.x", profile))
			if res.Accepted != 1 || len(res.Rejected) != 0 {
				t.Errorf("accepted=%d rejected=%+v, want the child accepted without a chain walk", res.Accepted, res.Rejected)
			}
		})
	}
}

// A store failure other than absence during a same-layer restricting child's
// walk aborts the cycle with a message that names the child alone, and the
// store error stays reachable through the chain.
// Spec: §4.6 hidden parents (withheld)
func TestIngest_SandboxConflictSameLayerChainReadFailureAborts(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	seedChain(t, st, "")
	boom := errors.New("connection reset")
	failing := chainFaultStore{Store: st, ancestorID: "shared/root", err: boom}

	_, err := ingest.Ingest(context.Background(), failing, ingest.Request{
		TenantID: "tenant-1", LayerID: "L1", Files: fstest.MapFS{
			"shared/child/ARTIFACT.md": artifactFile(sbAgent("2.0.0", "shared/parent@1.x", "read-only-fs")),
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Ingest error = %v, want it to wrap the store failure", err)
	}
	if got, want := err.Error(), "ingest: resolve extends chain for shared/child: store read failed"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	assertNotStored(t, st, "shared/child", "2.0.0")
}

// A stored version is never re-judged: a conflicting child written to the
// store directly, as an earlier release stored it, is idempotent on an
// unchanged reingest, and the read path serves the parent's sandbox_profile.
// Spec: §4.6, §7.3.1
func TestIngest_SandboxConflictStoredChildIsNotRejudged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	child := sbAgent("2.0.0", "shared/parent@1.x", "seccomp-strict")

	// A first store, whose parent leaves the profile unrestricted, yields the
	// record ingest computes for the child, so the row written below carries
	// the content hash a reingest compares against.
	scratch := newStore(t)
	ingestOne(t, scratch, "L1", "shared/parent", sbAgent("1.0.0", "", "unrestricted"))
	if res := ingestOne(t, scratch, "L2", "finance/child", child); res.Accepted != 1 {
		t.Fatalf("scratch child not accepted: %+v", res)
	}
	rec, err := scratch.GetManifest(ctx, "tenant-1", "finance/child", "2.0.0")
	if err != nil {
		t.Fatalf("GetManifest scratch child: %v", err)
	}

	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", sbAgent("1.0.0", "", "read-only-fs"))
	if err := st.PutManifest(ctx, rec); err != nil {
		t.Fatalf("PutManifest conflicting child: %v", err)
	}

	same := ingestOne(t, st, "L2", "finance/child", child)
	if same.Idempotent != 1 || len(same.Rejected) != 0 {
		t.Errorf("unchanged: idempotent=%d rejected=%+v, want idempotent with no rejection", same.Idempotent, same.Rejected)
	}
	assertServedProfile(t, st, "finance/child", "2.0.0", "read-only-fs")
}
