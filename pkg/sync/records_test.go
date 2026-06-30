package sync

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// rec builds a Record with the given canonical ID and type for the Select tests.
func rec(id, typ string) Record {
	return Record{ID: id, Artifact: &manifest.Artifact{Type: manifest.ArtifactType(typ)}}
}

// Spec: §7.5.1 — ScopeFilter.Select applies include, exclude, and type globs
// over canonical IDs. publish reuses it to intersect a plugin filter with the
// effective view, so it must agree with the sync target scope on every branch.
func TestScopeFilter_Select(t *testing.T) {
	t.Parallel()
	records := []Record{
		rec("finance/ap/pay-invoice", "agent"),
		rec("finance/experimental/draft", "skill"),
		rec("security/baseline/lockdown", "skill"),
		rec("notes/journal", "context"),
	}

	cases := []struct {
		name   string
		filter ScopeFilter
		want   []string
	}{
		{
			name:   "empty selects all",
			filter: ScopeFilter{},
			want:   []string{"finance/ap/pay-invoice", "finance/experimental/draft", "security/baseline/lockdown", "notes/journal"},
		},
		{
			name:   "include narrows",
			filter: ScopeFilter{Include: []string{"finance/**"}},
			want:   []string{"finance/ap/pay-invoice", "finance/experimental/draft"},
		},
		{
			name:   "exclude removes a subtree",
			filter: ScopeFilter{Include: []string{"finance/**"}, Exclude: []string{"finance/experimental/**"}},
			want:   []string{"finance/ap/pay-invoice"},
		},
		{
			name:   "type filters by artifact type",
			filter: ScopeFilter{Types: []string{"skill"}},
			want:   []string{"finance/experimental/draft", "security/baseline/lockdown"},
		},
		{
			name:   "type drops a record with no parsed manifest",
			filter: ScopeFilter{Types: []string{"skill"}},
			want:   []string{"finance/experimental/draft", "security/baseline/lockdown"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := idsOf(tc.filter.Select(records))
			if !sameSet(got, tc.want) {
				t.Errorf("Select() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Spec: §7.5.1 — a record whose manifest did not parse has no type, so a
// non-empty type filter drops it.
func TestScopeFilter_Select_NilArtifactType(t *testing.T) {
	t.Parallel()
	records := []Record{{ID: "broken/manifest"}}
	got := ScopeFilter{Types: []string{"skill"}}.Select(records)
	if len(got) != 0 {
		t.Errorf("a record with a nil Artifact must be dropped by a type filter, got %v", idsOf(got))
	}
}

// Spec: §7.5.2 / §7.8 — FetchRecords returns an error for an unconfigured
// (empty) registry source rather than panicking, matching the sync.Run guard.
func TestFetchRecords_NoRegistry(t *testing.T) {
	t.Parallel()
	_, err := FetchRecords(Options{})
	if err == nil {
		t.Fatalf("FetchRecords with no registry must error")
	}
	if !errors.Is(err, ErrNoRegistry) {
		t.Errorf("error = %v, want ErrNoRegistry", err)
	}
}

// MergeKindForOp maps the adapter file ops to the lock's config-merge kind so a
// publish caller records the same Merge value sync does.
func TestMergeKindForOp(t *testing.T) {
	t.Parallel()
	if got := MergeKindForOp(0); got != "" {
		t.Errorf("OpWrite kind = %q, want empty", got)
	}
}

func idsOf(records []Record) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.ID)
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}
