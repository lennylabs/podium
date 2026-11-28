package serverboot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// rowView renders the visibility members of a layer row.
func rowView(lc store.LayerConfig) string {
	pending := "-"
	if p := lc.VisibilityPending; p != nil {
		pending = fmt.Sprintf("%v/%v/%v/%v", p.Public, p.Organization, p.Groups, p.Users)
	}
	return fmt.Sprintf("%v/%v/%v/%v %s %s", lc.Public, lc.Organization, lc.Groups, lc.Users, lc.VisibilitySource, pending)
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Start write, Mode
// none) — the start write of a --layer-path layer: under none the directory
// visibility applies; under warn and require a row the store holds takes
// the meet of its baseline and the directory, so a narrower file applies and
// a wider one is held pending, a default change applies in either direction
// to a row whose source is default, a row an earlier release stored is
// treated as declared, a soft-deleted row counts as held, and a row the
// store does not hold is created with no visibility field and the directory
// pending. A change records layer.config_changed with the cause start.
func TestStartVisibility_Rules(t *testing.T) {
	t.Parallel()
	groups := layer.Visibility{Groups: []string{"acme-finance"}}
	org := layer.Visibility{Organization: true}
	pub := layer.Visibility{Public: true}
	cases := []struct {
		name    string
		mode    string
		stored  *store.LayerConfig
		deleted bool
		dir     directoryVisibility
		def     layer.Visibility
		want    string
		created bool
		events  int
	}{
		{"none, new row", layer.TrustModeNone, nil, false, directoryVisibility{groups, true}, org, "false/false/[acme-finance]/[] declared -", true, 0},
		{"none, stored row", layer.TrustModeNone, &store.LayerConfig{Public: true, VisibilitySource: "declared"}, false, directoryVisibility{vis: org}, org, "false/true/[]/[] default -", false, 1},
		{"warn, new row", layer.TrustModeWarn, nil, false, directoryVisibility{groups, true}, org, "false/false/[]/[] default false/false/[acme-finance]/[]", true, 0},
		{"warn, new row, private default", layer.TrustModeWarn, nil, false, directoryVisibility{vis: layer.Visibility{}}, layer.Visibility{}, "false/false/[]/[] default -", true, 0},
		{"warn, narrower file", layer.TrustModeWarn, &store.LayerConfig{Organization: true, VisibilitySource: "declared"}, false, directoryVisibility{groups, true}, pub, "false/false/[acme-finance]/[] declared -", false, 1},
		{"require, wider file", layer.TrustModeRequire, &store.LayerConfig{Groups: []string{"acme-finance"}, VisibilitySource: "declared"}, false, directoryVisibility{pub, true}, org, "false/false/[acme-finance]/[] declared true/false/[]/[]", false, 1},
		{"require, narrower default", layer.TrustModeRequire, &store.LayerConfig{Organization: true, VisibilitySource: "default"}, false, directoryVisibility{vis: layer.Visibility{}}, layer.Visibility{}, "false/false/[]/[] default -", false, 1},
		{"require, wider default", layer.TrustModeRequire, &store.LayerConfig{Organization: true, VisibilitySource: "default"}, false, directoryVisibility{vis: pub}, pub, "true/false/[]/[] default -", false, 1},
		{"require, wider default on a declared row", layer.TrustModeRequire, &store.LayerConfig{Organization: true, VisibilitySource: "declared"}, false, directoryVisibility{vis: pub}, pub, "false/true/[]/[] declared true/false/[]/[]", false, 1},
		{"require, row of an earlier release", layer.TrustModeRequire, &store.LayerConfig{Public: true}, false, directoryVisibility{groups, true}, pub, "false/false/[acme-finance]/[] declared -", false, 1},
		{"require, soft-deleted row", layer.TrustModeRequire, &store.LayerConfig{Groups: []string{"acme-finance"}, VisibilitySource: "declared"}, true, directoryVisibility{pub, true}, pub, "false/false/[acme-finance]/[] declared true/false/[]/[]", false, 1},
		{"require, unchanged", layer.TrustModeRequire, &store.LayerConfig{Groups: []string{"acme-finance"}, VisibilitySource: "declared"}, false, directoryVisibility{groups, true}, pub, "false/false/[acme-finance]/[] declared -", false, 0},
	}
	for _, tc := range cases {
		ctx := context.Background()
		st := newMemoryStoreWithTenant(t)
		cfg := store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", LocalPath: "/x"}
		if tc.stored != nil {
			row := *tc.stored
			row.TenantID, row.ID, row.SourceType = "default", "team", "local"
			if err := st.PutLayerConfig(ctx, row); err != nil {
				t.Fatal(err)
			}
			if tc.deleted {
				if err := st.DeleteLayerConfig(ctx, "default", "team"); err != nil {
					t.Fatal(err)
				}
			}
		}
		var events []map[string]string
		emit := func(typ, target string, fields map[string]string) {
			if typ != "layer.config_changed" || target != "team" || fields["cause"] != "start" || fields["action"] != "visibility" {
				t.Errorf("%s: event %s %s %v", tc.name, typ, target, fields)
			}
			events = append(events, fields)
		}
		got, created, err := startVisibility(ctx, st, cfg, tc.mode, tc.dir, tc.def, emit)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if v := rowView(got); v != tc.want || created != tc.created || len(events) != tc.events {
			t.Errorf("%s: row %s created %v events %d; want %s %v %d", tc.name, v, created, len(events), tc.want, tc.created, tc.events)
		}
	}
	// A nil emitter records nothing and changes nothing else.
	st := newMemoryStoreWithTenant(t)
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", Public: true}); err != nil {
		t.Fatal(err)
	}
	got, _, err := startVisibility(context.Background(), st, store.LayerConfig{TenantID: "default", ID: "team"}, layer.TrustModeWarn, directoryVisibility{groups, true}, pub, nil)
	if err != nil || rowView(got) != "false/false/[acme-finance]/[] declared -" {
		t.Fatalf("nil emitter: %s %v", rowView(got), err)
	}
}

// failingRowReads fails the layer reads the start write makes.
type failingRowReads struct {
	store.Store
	getErr, deletedErr error
}

func (s failingRowReads) GetLayerConfig(ctx context.Context, tenantID, id string) (store.LayerConfig, error) {
	if s.getErr != nil {
		return store.LayerConfig{}, s.getErr
	}
	return s.Store.GetLayerConfig(ctx, tenantID, id)
}

func (s failingRowReads) ListDeletedLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	if s.deletedErr != nil {
		return nil, s.deletedErr
	}
	return s.Store.ListDeletedLayerConfigs(ctx, tenantID)
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Start write) — a
// start that cannot read the stored row, live or soft-deleted, cannot bound
// the directory by it, and fails rather than create the row anew.
func TestStartVisibility_ReadFailure(t *testing.T) {
	t.Parallel()
	cfg := store.LayerConfig{TenantID: "default", ID: "team"}
	for _, st := range []store.Store{
		failingRowReads{Store: newMemoryStoreWithTenant(t), getErr: errors.New("down")},
		failingRowReads{Store: newMemoryStoreWithTenant(t), deletedErr: errors.New("down")},
	} {
		if _, _, err := startVisibility(context.Background(), st, cfg, layer.TrustModeWarn, directoryVisibility{}, layer.Visibility{}, nil); err == nil {
			t.Error("a failed read was no error")
		}
	}
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Start write,
// Failed layer step, Event), §13.10 — a --layer-path start under warn whose
// layer step fails: a stored row takes the narrower file at the start write,
// the startup ingest keeps it and stores the artifact, and both steps record
// their events through the start's emitter; the returned layer carries the
// row's visibility. A first start creates the row bounded by the default.
func TestBootstrapLayerPath_StartVisibilityUnderWarn(t *testing.T) {
	ctx := context.Background()
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order: [one, two]\n"},
		testharness.WriteTreeOption{Path: "one/.layer-config", Content: "visibility:\n  groups: [acme-finance]\n"},
		testharness.WriteTreeOption{Path: "one/a/ARTIFACT.md", Content: artifactBody},
		testharness.WriteTreeOption{Path: "two/.layer-config", Content: "visibility:\n  public: true\n"},
		testharness.WriteTreeOption{Path: "two/b/ARTIFACT.md", Content: artifactBody},
	)
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "default", ID: "one", SourceType: "local", Organization: true, VisibilitySource: "declared"}); err != nil {
		t.Fatal(err)
	}
	def := layer.Visibility{Organization: true}
	att := &ingest.AttestationConfig{Instances: map[string]sign.AttestationInstance{"github": {Name: "github"}}, DefaultMode: "warn", DefaultVisibility: def}
	reqs := []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "i", Identity: "alice@acme.com", Instance: "github"}}}}
	var events []string
	emit := func(typ, target string, fields map[string]string) {
		events = append(events, target+" "+fields["cause"]+" "+fields["visibility"]+" pending="+fields["visibility_pending"])
	}
	layers, err := bootstrapLayerPath(st, "default", root, def, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil,
		bootstrapTrust{file: bootstrapRequirements{path: "f", reqs: reqs}, defaultMode: "warn", att: att, audit: emit})
	if err != nil || len(layers) != 2 {
		t.Fatalf("bootstrapLayerPath: %v, layers %v", err, layers)
	}
	one, _ := st.GetLayerConfig(ctx, "default", "one")
	two, _ := st.GetLayerConfig(ctx, "default", "two")
	if rowView(one) != "false/false/[acme-finance]/[] declared -" || rowView(two) != "false/true/[]/[] declared true/false/[]/[]" {
		t.Fatalf("rows: one %s, two %s", rowView(one), rowView(two))
	}
	if !slices.Equal(layers[0].Visibility.Groups, []string{"acme-finance"}) || !layers[1].Visibility.Organization || layers[1].Visibility.Public {
		t.Fatalf("returned layers %+v", layers)
	}
	manifests, _ := st.ListManifests(ctx, "default")
	if len(manifests) != 2 {
		t.Fatalf("warn stored %d artifacts, want 2", len(manifests))
	}
	want := []string{
		`one start {"public":false,"organization":false,"groups":["acme-finance"],"users":null} pending=`,
		`two cycle {"public":false,"organization":true,"groups":null,"users":null} pending={"public":true,"organization":false,"groups":null,"users":null}`,
	}
	if !slices.Equal(events, want) {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(events, "\n"), strings.Join(want, "\n"))
	}
}

// racingVisibility lets another writer change the row's visibility before
// the start's conditional write, races times over, as a cycle of another
// replica can.
type racingVisibility struct {
	store.Store
	races  int
	narrow func()
}

func (s *racingVisibility) SetLayerVisibilityIf(ctx context.Context, tenantID, layerID string, expect, next store.VisibilityState) error {
	if s.races > 0 {
		s.races--
		s.narrow()
	}
	return s.Store.SetLayerVisibilityIf(ctx, tenantID, layerID, expect, next)
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Start write) — the
// start write changes the visibility members only while they hold the values
// it read: a narrowing another writer lands between its read and its write
// is read again and kept, and a row that keeps changing fails the start
// rather than be written blind.
func TestStartVisibility_ConditionalOnTheMembersItRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := newMemoryStoreWithTenant(t)
	if err := mem.PutLayerConfig(ctx, store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", Public: true, VisibilitySource: "declared"}); err != nil {
		t.Fatal(err)
	}
	narrowed := 0
	st := &racingVisibility{Store: mem, races: 1}
	st.narrow = func() {
		narrowed++
		cur, _ := mem.GetLayerConfig(ctx, "default", "team")
		next := cur.VisibilityState()
		next.Visibility = store.LayerVisibility{Users: []string{fmt.Sprintf("alice-%d", narrowed)}}
		if err := mem.SetLayerVisibilityIf(ctx, "default", "team", cur.VisibilityState(), next); err != nil {
			t.Fatal(err)
		}
	}
	cfg := store.LayerConfig{TenantID: "default", ID: "team", SourceType: "local", LocalPath: "/x", Order: 3}
	got, _, err := startVisibility(ctx, st, cfg, layer.TrustModeRequire, directoryVisibility{layer.Visibility{Public: true}, true}, layer.Visibility{}, nil)
	if err != nil || rowView(got) != "false/false/[]/[alice-1] declared true/false/[]/[]" || got.Order != 3 {
		t.Fatalf("after one race: %s order %d, %v", rowView(got), got.Order, err)
	}
	st.races = startVisibilityRetries
	if _, _, err := startVisibility(ctx, st, cfg, layer.TrustModeRequire, directoryVisibility{layer.Visibility{Users: []string{"bob"}}, true}, layer.Visibility{}, nil); err == nil || !strings.Contains(err.Error(), "changed during each") {
		t.Fatalf("a row that keeps changing: %v", err)
	}
}
