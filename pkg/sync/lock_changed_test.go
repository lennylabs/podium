package sync

import "testing"

// spec: §11 — a re-sync that materializes the same content reports no change,
// and an edit to any artifact reports one. The comparison keys by (artifact id,
// materialized path), so two artifacts config-merging into one file each
// participate instead of collapsing onto the shared path.
func TestLockChanged_SharedMaterializedPath(t *testing.T) {
	t.Parallel()
	lock := func(hashA, hashB string) *LockFile {
		return &LockFile{Artifacts: []LockArtifact{
			{ID: "acme/mcp/alpha", MaterializedPath: ".mcp.json", ContentHash: hashA},
			{ID: "acme/mcp/beta", MaterializedPath: ".mcp.json", ContentHash: hashB},
		}}
	}
	cases := []struct {
		name        string
		prior, next *LockFile
		want        bool
	}{
		{"identical", lock("a", "b"), lock("a", "b"), false},
		{"first entry edited", lock("a", "b"), lock("a2", "b"), true},
		{"last entry edited", lock("a", "b"), lock("a", "b2"), true},
		{"nil prior", nil, lock("a", "b"), true},
		{"both empty", nil, &LockFile{}, false},
		{
			"entry dropped",
			lock("a", "b"),
			&LockFile{Artifacts: []LockArtifact{
				{ID: "acme/mcp/alpha", MaterializedPath: ".mcp.json", ContentHash: "a"},
			}},
			true,
		},
		{
			"same hash under a different artifact id",
			lock("a", "b"),
			&LockFile{Artifacts: []LockArtifact{
				{ID: "acme/mcp/alpha", MaterializedPath: ".mcp.json", ContentHash: "a"},
				{ID: "acme/mcp/gamma", MaterializedPath: ".mcp.json", ContentHash: "b"},
			}},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := lockChanged(tc.prior, tc.next); got != tc.want {
				t.Errorf("lockChanged = %v, want %v", got, tc.want)
			}
		})
	}
}

// An entry with no materialized path records nothing to compare, and a nil lock
// yields an empty map. spec: §11.
func TestLockEntryHashes_SkipsUnmaterializedAndNil(t *testing.T) {
	t.Parallel()
	if got := lockEntryHashes(nil); len(got) != 0 {
		t.Errorf("lockEntryHashes(nil) = %v, want empty", got)
	}
	got := lockEntryHashes(&LockFile{Artifacts: []LockArtifact{
		{ID: "acme/mcp/alpha", MaterializedPath: ".mcp.json", ContentHash: "a"},
		{ID: "acme/mcp/beta", ContentHash: "b"},
	}})
	want := map[string]string{"acme/mcp/alpha\x00.mcp.json": "a"}
	if len(got) != len(want) {
		t.Fatalf("lockEntryHashes = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("lockEntryHashes[%q] = %q, want %q", k, got[k], v)
		}
	}
}
