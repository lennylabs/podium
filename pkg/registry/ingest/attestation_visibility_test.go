package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// bootstrapRow stores the fixture's layer as a layer that --layer-path
// bootstraps: with mode, the fixture's signer, the visibility vis, and
// visibility_source source.
func (f *attestFixture) bootstrapRow(mode string, vis store.LayerVisibility, source string) {
	f.t.Helper()
	ctx := context.Background()
	lc, err := f.st.GetLayerConfig(ctx, "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	lc.TrustMode = mode
	lc.SetVisibility(vis)
	lc.VisibilitySource = source
	if err := f.st.PutLayerConfig(ctx, lc); err != nil {
		f.t.Fatal(err)
	}
}

// layerConfig writes the layer's .layer-config, or removes it for "".
func (f *attestFixture) layerConfig(body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, ".layer-config")
	if body == "" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			f.t.Fatal(err)
		}
		return
	}
	f.write(map[string]string{".layer-config": body})
}

// row renders the visibility members of the stored layer.
func (f *attestFixture) row() string {
	f.t.Helper()
	lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	pending := "-"
	if p := lc.VisibilityPending; p != nil {
		pending = fmt.Sprintf("%v/%v/%v/%v", p.Public, p.Organization, p.Groups, p.Users)
	}
	return fmt.Sprintf("%v/%v/%v/%v %s %s", lc.Public, lc.Organization, lc.Groups, lc.Users, lc.VisibilitySource, pending)
}

// visibilityEvents returns the cause, the earlier visibility, and the new
// visibility of each layer.config_changed the cycles recorded.
func (f *attestFixture) visibilityEvents() []string {
	var out []string
	for _, e := range f.events {
		if e.typ == "layer.config_changed" {
			out = append(out, e.fields["cause"]+" "+e.fields["visibility_before"]+" -> "+e.fields["visibility"]+" pending="+e.fields["visibility_pending"])
		}
	}
	return out
}

// TestBootstrapVisibility_NarrowerFileAppliesWhateverTheLayerStep covers a
// .layer-config that admits fewer callers than the row: a cycle whose layer
// step fails applies it in its record write, before it stores an artifact,
// under warn, and a refused cycle under require applies it as well.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Failed layer step)
func TestBootstrapVisibility_NarrowerFileAppliesWhateverTheLayerStep(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{layer.TrustModeWarn, layer.TrustModeRequire} {
		f := newAttestFixture(t, mode)
		f.bootstrapRow(mode, store.LayerVisibility{Organization: true}, store.VisibilitySourceDeclared)
		f.layerConfig("visibility:\n  groups: [acme-finance]\n")
		res, err := f.cycle()
		if mode == layer.TrustModeRequire {
			refusal(t, err)
		} else if err != nil || res.Accepted != 2 {
			t.Fatalf("warn cycle: %v, %+v", err, res)
		}
		if got := f.row(); got != "false/false/[acme-finance]/[] declared -" {
			t.Errorf("%s: row = %s", mode, got)
		}
		want := []string{`cycle {"public":false,"organization":true,"groups":null,"users":null} -> {"public":false,"organization":false,"groups":["acme-finance"],"users":null} pending=`}
		if got := f.visibilityEvents(); !slices.Equal(got, want) {
			t.Errorf("%s: events = %q, want %q", mode, got, want)
		}
	}
}

// TestBootstrapVisibility_WiderFileWaitsForAPassedLayerStep covers a
// .layer-config that admits more callers: an unsigned cycle keeps the row
// and holds the file in visibility_pending, and a cycle that passes the
// layer step over the same file applies it after its removals.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Passed layer step, Pending change)
func TestBootstrapVisibility_WiderFileWaitsForAPassedLayerStep(t *testing.T) {
	t.Parallel()
	f := newAttestFixture(t, layer.TrustModeRequire)
	f.bootstrapRow(layer.TrustModeRequire, store.LayerVisibility{Groups: []string{"acme-finance"}}, store.VisibilitySourceDeclared)
	f.layerConfig("visibility:\n  public: true\n")
	_, err := f.cycle()
	refusal(t, err)
	if got := f.row(); got != "false/false/[acme-finance]/[] declared true/false/[]/[]" {
		t.Fatalf("unsigned: row = %s", got)
	}
	f.signLayer(time.Minute)
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	if got := f.row(); got != "true/false/[]/[] declared -" {
		t.Fatalf("signed: row = %s", got)
	}
	want := []string{
		`cycle {"public":false,"organization":false,"groups":["acme-finance"],"users":null} -> {"public":false,"organization":false,"groups":["acme-finance"],"users":null} pending={"public":true,"organization":false,"groups":null,"users":null}`,
		`cycle {"public":false,"organization":false,"groups":["acme-finance"],"users":null} -> {"public":true,"organization":false,"groups":null,"users":null} pending=`,
	}
	if got := f.visibilityEvents(); !slices.Equal(got, want) {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A further passed cycle over the same state changes nothing and records
	// nothing.
	f.events = nil
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	if got := f.visibilityEvents(); len(got) != 0 {
		t.Errorf("an unchanged cycle recorded %q", got)
	}
}

// TestBootstrapVisibility_SignedFileThatDeclaresNoneTakesTheDefault covers a
// signed state with no .layer-config on a row whose source is declared: the
// passed cycle applies the deployment default and sets the source to
// default, so a later change of the default applies to the row.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Passed layer step)
func TestBootstrapVisibility_SignedFileThatDeclaresNoneTakesTheDefault(t *testing.T) {
	t.Parallel()
	f := newAttestFixture(t, layer.TrustModeRequire)
	f.cfg.DefaultVisibility = layer.Visibility{Organization: true}
	f.bootstrapRow(layer.TrustModeRequire, store.LayerVisibility{Groups: []string{"acme-finance"}}, store.VisibilitySourceDeclared)
	f.signLayer(time.Minute)
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	if got := f.row(); got != "false/true/[]/[] default -" {
		t.Fatalf("row = %s", got)
	}
	// The baseline of the row is now the default, so a narrower default
	// applies in a cycle whose layer step fails.
	f.cfg.DefaultVisibility = layer.Visibility{}
	f.place()
	if _, err := f.cycle(); err == nil {
		t.Fatal("an unsigned cycle under require was accepted")
	}
	if got := f.row(); got != "false/false/[]/[] default -" {
		t.Fatalf("narrower default: row = %s", got)
	}
}

// TestBootstrapVisibility_MalformedFileAdmitsNoCaller covers a .layer-config
// that stops parsing, or that gains an empty users entry, in a later cycle:
// the cycle reads it as a visibility that admits no caller, so the layer
// becomes visible to no caller rather than falling back to the default.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer), §4.6
func TestBootstrapVisibility_MalformedFileAdmitsNoCaller(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"visibility: [\n", "visibility:\n  organization: true\n  users: [\"\"]\n"} {
		f := newAttestFixture(t, layer.TrustModeWarn)
		f.cfg.DefaultVisibility = layer.Visibility{Public: true}
		f.bootstrapRow(layer.TrustModeWarn, store.LayerVisibility{Organization: true}, store.VisibilitySourceDeclared)
		f.layerConfig(body)
		if _, err := f.cycle(); err != nil {
			t.Fatal(err)
		}
		if got := f.row(); got != "false/false/[]/[] declared -" {
			t.Errorf("%q: row = %s", body, got)
		}
		lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []layer.Identity{{}, {IsAuthenticated: true, Sub: "alice"}} {
			if layer.Visible(layer.Layer{ID: "team", Visibility: ingest.FromStoreVisibility(lc.Visibility())}, id) {
				t.Errorf("%q: the layer is visible to %+v", body, id)
			}
		}
	}
}

// TestBootstrapVisibility_RowCreatedByAStart covers the startup ingest that
// follows the start write which created the row: a failed layer step sets
// the meet of the default and the file and fixes the source from the file,
// so a later unsigned file that the default admits does not widen the layer.
// The event goes to the emitter AtStart names.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Row created by a start)
func TestBootstrapVisibility_RowCreatedByAStart(t *testing.T) {
	t.Parallel()
	f := newAttestFixture(t, layer.TrustModeWarn)
	f.cfg.DefaultVisibility = layer.Visibility{Organization: true}
	pending := &store.LayerVisibility{Groups: []string{"acme-finance"}}
	ctx := context.Background()
	lc, err := f.st.GetLayerConfig(ctx, "tenant-1", "team")
	if err != nil {
		t.Fatal(err)
	}
	lc.VisibilitySource, lc.VisibilityPending = store.VisibilitySourceDefault, pending
	if err := f.st.PutLayerConfig(ctx, lc); err != nil {
		t.Fatal(err)
	}
	f.layerConfig("visibility:\n  groups: [acme-finance]\n")
	var startEvents []string
	claim, _, claimed, err := ingest.ClaimLayer(ctx, f.st, "tenant-1", "team")
	if err != nil {
		t.Fatal(err)
	}
	att := f.cfg.Cycle(claimed, claim).AtStart(true, func(typ, target string, fields map[string]string) {
		startEvents = append(startEvents, typ+" "+target+" "+fields["cause"]+" "+fields["visibility_source_before"]+"->"+fields["visibility_source"])
	})
	if _, err := ingest.Ingest(ctx, f.st, ingest.Request{
		TenantID: "tenant-1", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(f.dir), Clock: f.clock, Attestation: att,
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.row(); got != "false/false/[acme-finance]/[] declared -" {
		t.Fatalf("row = %s", got)
	}
	if want := []string{"layer.config_changed team cycle default->declared"}; !slices.Equal(startEvents, want) {
		t.Fatalf("start events = %q, want %q", startEvents, want)
	}
	// A later unsigned file that the default admits stays bounded by the
	// row.
	f.layerConfig("visibility:\n  organization: true\n")
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	if got := f.row(); got != "false/false/[acme-finance]/[] declared false/true/[]/[]" {
		t.Fatalf("after an unsigned widening: row = %s", got)
	}
}

// TestBootstrapVisibility_OtherLayersAreUntouched covers a layer whose row
// carries no visibility_source, a registered or declared layer: a cycle
// reads no .layer-config for it and writes no visibility. A cycle under
// none applies nothing either.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Mode none)
func TestBootstrapVisibility_OtherLayersAreUntouched(t *testing.T) {
	t.Parallel()
	f := newAttestFixture(t, layer.TrustModeWarn)
	f.bootstrapRow(layer.TrustModeWarn, store.LayerVisibility{Public: true}, "")
	f.layerConfig("visibility: [\n")
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	if got := f.row(); got != "true/false/[]/[]  -" {
		t.Errorf("registered layer: row = %s", got)
	}
	g := newAttestFixture(t, layer.TrustModeNone)
	g.bootstrapRow(layer.TrustModeNone, store.LayerVisibility{Public: true}, store.VisibilitySourceDeclared)
	g.layerConfig("visibility:\n  groups: [acme-finance]\n")
	if _, err := g.cycle(); err != nil {
		t.Fatal(err)
	}
	if got := g.row(); got != "true/false/[]/[] declared -" {
		t.Errorf("mode none: row = %s", got)
	}
}

// TestBootstrapVisibility_UnreadableFileFailsTheCycle covers a .layer-config
// the cycle cannot read: the cycle fails before its record write and
// changes nothing.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer)
func TestBootstrapVisibility_UnreadableFileFailsTheCycle(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	f := newAttestFixture(t, layer.TrustModeWarn)
	f.bootstrapRow(layer.TrustModeWarn, store.LayerVisibility{Public: true}, store.VisibilitySourceDeclared)
	f.layerConfig("visibility:\n  public: true\n")
	if err := os.Chmod(filepath.Join(f.dir, ".layer-config"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cycle(); err == nil || !strings.Contains(err.Error(), ".layer-config") {
		t.Fatalf("cycle error = %v, want the read of .layer-config", err)
	}
	if got, rec := f.row(), f.record(); got != "true/false/[]/[] declared -" || rec.CheckedAt != nil {
		t.Fatalf("row = %s, record = %+v", got, rec)
	}
}

// failingRemovals fails the cycle's removal write once, which follows the
// cycle's record write and precedes its second visibility write.
type failingRemovals struct {
	store.Store
	failed bool
}

func (s *failingRemovals) SoftDeleteArtifacts(ctx context.Context, tenantID, layerID string, ids []string, at time.Time, claim string) error {
	if !s.failed {
		s.failed = true
		return errors.New("injected removal failure")
	}
	return s.Store.SoftDeleteArtifacts(ctx, tenantID, layerID, ids, at, claim)
}

// TestBootstrapVisibility_WideningFollowsTheRemovals covers a signed state
// that removes an artifact and widens the file, with a failure injected
// after the record write: the row keeps the meet and the wider value in
// visibility_pending, so the artifact the state removes is not served under
// the wider visibility, and the next cycle completes both.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Passed layer step)
func TestBootstrapVisibility_WideningFollowsTheRemovals(t *testing.T) {
	t.Parallel()
	f := newAttestFixture(t, layer.TrustModeRequire)
	f.bootstrapRow(layer.TrustModeRequire, store.LayerVisibility{Groups: []string{"acme-finance"}}, store.VisibilitySourceDeclared)
	f.layerConfig("visibility:\n  groups: [acme-finance]\n")
	f.signLayer(time.Minute)
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.dir, "finance", "close")); err != nil {
		t.Fatal(err)
	}
	f.layerConfig("visibility:\n  public: true\n")
	f.signLayer(2 * time.Minute)
	real := f.st
	f.st = &failingRemovals{Store: real}
	if _, err := f.cycle(); err == nil {
		t.Fatal("the injected failure did not fail the cycle")
	}
	if got := f.row(); got != "false/false/[acme-finance]/[] declared true/false/[]/[]" {
		t.Fatalf("after the failure: row = %s", got)
	}
	if ids := f.stored(); !slices.Contains(ids, "finance/close") {
		t.Fatalf("the failed cycle removed the artifact: %v", ids)
	}
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	if got, ids := f.row(), f.stored(); got != "true/false/[]/[] declared -" || slices.Contains(ids, "finance/close") {
		t.Fatalf("completed: row = %s, stored %v", got, ids)
	}
}

// supersedingStore runs a newer cycle when an older cycle makes its second
// visibility write, so that write meets a claim and a mark it did not
// write.
type supersedingStore struct {
	store.Store
	newer func()
	ran   bool
}

func (s *supersedingStore) WriteLayerVisibility(ctx context.Context, tenantID, layerID, claim string, mark time.Time, commit string, w store.VisibilityWrite) error {
	if !s.ran {
		s.ran = true
		s.newer()
	}
	return s.Store.WriteLayerVisibility(ctx, tenantID, layerID, claim, mark, commit, w)
}

// TestBootstrapVisibility_SupersededCycleDoesNotWiden covers a cycle that
// passed on an older signed state with a wider file and is superseded
// before its second visibility write: the write does not apply, and the row
// keeps the visibility of the newer cycle.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Passed layer step)
func TestBootstrapVisibility_SupersededCycleDoesNotWiden(t *testing.T) {
	t.Parallel()
	f := newAttestFixture(t, layer.TrustModeRequire)
	f.bootstrapRow(layer.TrustModeRequire, store.LayerVisibility{Groups: []string{"acme-finance"}}, store.VisibilitySourceDeclared)
	f.layerConfig("visibility:\n  public: true\n")
	f.signLayer(time.Minute)
	real := f.st
	wrapped := &supersedingStore{Store: real}
	wrapped.newer = func() {
		f.st = real
		f.layerConfig("visibility:\n  groups: [acme-finance]\n")
		f.signLayer(2 * time.Minute)
		if _, err := f.cycle(); err != nil {
			t.Errorf("newer cycle: %v", err)
		}
		f.st = wrapped
	}
	f.st = wrapped
	res, err := f.cycle()
	if err != nil || !res.Superseded {
		t.Fatalf("older cycle: %+v, %v; want a superseded result", res, err)
	}
	if got := f.row(); got != "false/false/[acme-finance]/[] declared -" {
		t.Fatalf("row = %s", got)
	}
}

// TestBootstrapVisibility_ChangeReachesTheEventStream covers the §7.6 event
// of a cycle's visibility change: the scope names the layer and carries the
// visibility it held before the change.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Event), §7.6
func TestBootstrapVisibility_ChangeReachesTheEventStream(t *testing.T) {
	t.Parallel()
	f := newAttestFixture(t, layer.TrustModeWarn)
	f.bootstrapRow(layer.TrustModeWarn, store.LayerVisibility{Organization: true}, store.VisibilitySourceDeclared)
	f.layerConfig("visibility:\n  groups: [acme-finance]\n")
	ctx := context.Background()
	claim, _, claimed, err := ingest.ClaimLayer(ctx, f.st, "tenant-1", "team")
	if err != nil {
		t.Fatal(err)
	}
	var published []string
	if _, err := ingest.Ingest(ctx, f.st, ingest.Request{
		TenantID: "tenant-1", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(f.dir), Clock: f.clock, Attestation: f.cfg.Cycle(claimed, claim),
		PublishEvent: func(_ context.Context, scope core.EventScope, typ string, data map[string]any) {
			if typ == "layer.config_changed" {
				published = append(published, fmt.Sprintf("%s %v prior=%+v %v %v", scope.TenantID, scope.Layers, *scope.Prior, data["action"], data["cause"]))
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"tenant-1 [team] prior={Public:false Organization:true Groups:[] Users:[]} visibility cycle"}
	if !slices.Equal(published, want) {
		t.Fatalf("published %q, want %q", published, want)
	}
}
