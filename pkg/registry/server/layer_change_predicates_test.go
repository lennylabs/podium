package server

import (
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// baseLayerConfig is the record every predicate case mutates one field of.
func baseLayerConfig() store.LayerConfig {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return store.LayerConfig{
		TenantID:        "t",
		ID:              "team",
		SourceType:      "git",
		Repo:            "git@github.com:acme/team.git",
		Ref:             "main",
		Root:            "artifacts",
		LocalPath:       "",
		Order:           10,
		UserDefined:     false,
		Owner:           "alice",
		Public:          true,
		Organization:    false,
		Groups:          []string{"eng"},
		Users:           []string{"alice"},
		WebhookSecret:   "s3cret",
		GitProvider:     "github",
		LastIngestedRef: "abc123",
		ForcePushPolicy: "tolerant",
		LastIngestedAt:  &at,
		CreatedAt:       at,
		DeletedAt:       nil,
	}
}

// Spec: §7.3.1, §7.5.4 — the wake predicate. A visibility, order, or source
// location difference can alter what a watcher's re-resolve produces; a
// webhook secret, a force-push policy, a git provider, the ingest bookkeeping,
// and the timestamps cannot. The table names every field of store.LayerConfig,
// and the coverage assertion below fails when a field is added until the field
// is classified here.
func TestWakesWatchers_EveryField(t *testing.T) {
	t.Parallel()
	other := time.Date(2027, 6, 7, 8, 9, 10, 0, time.UTC)
	cases := []struct {
		field  string
		mutate func(*store.LayerConfig)
		wake   bool
	}{
		{"TenantID", func(c *store.LayerConfig) { c.TenantID = "other" }, false},
		{"ID", func(c *store.LayerConfig) { c.ID = "other" }, false},
		{"SourceType", func(c *store.LayerConfig) { c.SourceType = "local" }, true},
		{"Repo", func(c *store.LayerConfig) { c.Repo = "git@github.com:acme/other.git" }, true},
		{"Ref", func(c *store.LayerConfig) { c.Ref = "release" }, true},
		{"Root", func(c *store.LayerConfig) { c.Root = "other" }, true},
		{"LocalPath", func(c *store.LayerConfig) { c.LocalPath = "/tmp/other" }, true},
		{"Order", func(c *store.LayerConfig) { c.Order = 20 }, true},
		{"UserDefined", func(c *store.LayerConfig) { c.UserDefined = true }, false},
		{"Owner", func(c *store.LayerConfig) { c.Owner = "bob" }, false},
		{"Public", func(c *store.LayerConfig) { c.Public = false }, true},
		{"Organization", func(c *store.LayerConfig) { c.Organization = true }, true},
		{"Groups", func(c *store.LayerConfig) { c.Groups = nil }, true},
		{"Users", func(c *store.LayerConfig) { c.Users = []string{"alice", "bob"} }, true},
		{"WebhookSecret", func(c *store.LayerConfig) { c.WebhookSecret = "rotated" }, false},
		{"GitProvider", func(c *store.LayerConfig) { c.GitProvider = "gitlab" }, false},
		{"LastIngestedRef", func(c *store.LayerConfig) { c.LastIngestedRef = "def456" }, false},
		{"ForcePushPolicy", func(c *store.LayerConfig) { c.ForcePushPolicy = "strict" }, false},
		{"LastIngestedAt", func(c *store.LayerConfig) { c.LastIngestedAt = &other }, false},
		{"CreatedAt", func(c *store.LayerConfig) { c.CreatedAt = other }, false},
		{"DeletedAt", func(c *store.LayerConfig) { c.DeletedAt = &other }, false},
	}

	tabled := map[string]bool{}
	for _, tc := range cases {
		tabled[tc.field] = true
		t.Run(tc.field, func(t *testing.T) {
			before := baseLayerConfig()
			after := baseLayerConfig()
			tc.mutate(&after)
			if layerConfigEqual(before, after) {
				t.Fatalf("%s: the case mutated nothing, so it tests nothing", tc.field)
			}
			if got := wakesWatchers(before, after); got != tc.wake {
				t.Errorf("wakesWatchers on a %s difference = %v, want %v", tc.field, got, tc.wake)
			}
		})
	}

	typ := reflect.TypeOf(store.LayerConfig{})
	for i := range typ.NumField() {
		if name := typ.Field(i).Name; !tabled[name] {
			t.Errorf("store.LayerConfig.%s is not classified by this table; add it with the §7.5.4 reading that applies", name)
		}
	}
	if len(tabled) != typ.NumField() {
		t.Errorf("the table names %d fields, store.LayerConfig has %d", len(tabled), typ.NumField())
	}
}

// Spec: §7.5.4 — an unchanged record wakes nothing, which is what makes the
// wake predicate safe to evaluate on every write.
func TestWakesWatchers_IdenticalRecordsDoNotWake(t *testing.T) {
	t.Parallel()
	if wakesWatchers(baseLayerConfig(), baseLayerConfig()) {
		t.Error("two identical records reported a wake")
	}
}

// Spec: §7.3.1 — the unchanged-write comparison treats a nil slice and an
// empty one as equal. A client echoing a layer object back sends `null` on an
// empty list, the decode normalizes it to nil, and reflect.DeepEqual would
// report a change on a record that stores the same visibility.
func TestLayerConfigEqual_NilAndEmptySlices(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		a, b  store.LayerConfig
		equal bool
	}{
		{
			name:  "nil and empty groups",
			a:     store.LayerConfig{ID: "team", Groups: nil},
			b:     store.LayerConfig{ID: "team", Groups: []string{}},
			equal: true,
		},
		{
			name:  "nil and empty users",
			a:     store.LayerConfig{ID: "team", Users: nil},
			b:     store.LayerConfig{ID: "team", Users: []string{}},
			equal: true,
		},
		{
			name:  "a populated list against an empty one",
			a:     store.LayerConfig{ID: "team", Groups: []string{"eng"}},
			b:     store.LayerConfig{ID: "team", Groups: []string{}},
			equal: false,
		},
		{
			name:  "the same members in a different order",
			a:     store.LayerConfig{ID: "team", Users: []string{"alice", "bob"}},
			b:     store.LayerConfig{ID: "team", Users: []string{"bob", "alice"}},
			equal: false,
		},
		{
			name:  "identical records",
			a:     baseLayerConfig(),
			b:     baseLayerConfig(),
			equal: true,
		},
		{
			name:  "equal timestamps held at different addresses",
			a:     store.LayerConfig{ID: "team", LastIngestedAt: timePtr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))},
			b:     store.LayerConfig{ID: "team", LastIngestedAt: timePtr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))},
			equal: true,
		},
		{
			name:  "one timestamp set and the other absent",
			a:     store.LayerConfig{ID: "team", DeletedAt: timePtr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))},
			b:     store.LayerConfig{ID: "team"},
			equal: false,
		},
		{
			name:  "a differing scalar",
			a:     store.LayerConfig{ID: "team", ForcePushPolicy: "strict"},
			b:     store.LayerConfig{ID: "team", ForcePushPolicy: "tolerant"},
			equal: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := layerConfigEqual(tc.a, tc.b); got != tc.equal {
				t.Errorf("layerConfigEqual = %v, want %v", got, tc.equal)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// Spec: §7.3.1 — the reorder event follows the tenant's precedence sequence,
// which is the layer identifiers in ascending order with ties broken by
// identifier, rather than the stored order integers the handler renumbers.
func TestPrecedenceSequence(t *testing.T) {
	t.Parallel()
	got := precedenceSequence([]store.LayerConfig{
		{ID: "c", Order: 30},
		{ID: "a", Order: 10},
		{ID: "b", Order: 10},
	})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("precedenceSequence = %v, want %v", got, want)
	}
}
