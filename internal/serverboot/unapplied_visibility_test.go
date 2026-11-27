package serverboot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// recordLines returns a log function that records each formatted line.
func recordLines(lines *[]string) func(string, ...any) {
	return func(format string, args ...any) {
		*lines = append(*lines, fmt.Sprintf(format, args...))
	}
}

// seedTenantLayers creates the tenant and stores the given layers in it.
func seedTenantLayers(t *testing.T, st store.Store, tenant store.Tenant, layers ...store.LayerConfig) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant %s: %v", tenant.ID, err)
	}
	for i, l := range layers {
		l.TenantID = tenant.ID
		l.SourceType = "local"
		l.LocalPath = "/tmp/" + l.ID
		l.Order = (i + 1) * 10
		if err := st.PutLayerConfig(ctx, l); err != nil {
			t.Fatalf("PutLayerConfig %s: %v", l.ID, err)
		}
	}
}

// Spec: §4.6 (No-identity bypass), §13.10 — the registry applies no layer
// visibility in public mode and whenever no request-time verifier is wired,
// whatever provider value the configuration names, `external` included.
func TestAppliesNoVisibility(t *testing.T) {
	t.Parallel()
	cases := []struct {
		publicMode, verifier, want bool
	}{
		{false, false, true},
		{true, false, true},
		{false, true, false},
	}
	for _, tc := range cases {
		if got := appliesNoVisibility(tc.publicMode, tc.verifier); got != tc.want {
			t.Errorf("appliesNoVisibility(public=%v, verifier=%v) = %v, want %v", tc.publicMode, tc.verifier, got, tc.want)
		}
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — one line names
// how many of the bound tenant's layers set no public grant and lists their
// IDs; a public layer is not listed, and a tenant whose layers are all public
// logs nothing.
func TestLogUnappliedVisibility_ListsRestrictedLayers(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	seedTenantLayers(t, st, store.Tenant{ID: "t1", Name: "default"},
		store.LayerConfig{ID: "open", Public: true},
		store.LayerConfig{ID: "team-finance", Groups: []string{"acme-finance"}},
		store.LayerConfig{ID: "alice-personal", UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"}},
		store.LayerConfig{ID: "sealed"},
	)
	var lines []string
	logUnappliedVisibility(context.Background(), st, "t1", false, recordLines(&lines))
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		"warning: 3 stored layer(s) declare visibility other than public",
		"this registry verifies no caller and applies no layer visibility, so every caller sees them",
		"their declarations take effect once an identity provider is configured: ",
		"team-finance", "alice-personal", "sealed",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "open") {
		t.Errorf("line %q lists the public layer", line)
	}

	public := store.NewMemory()
	seedTenantLayers(t, public, store.Tenant{ID: "t1"}, store.LayerConfig{ID: "open", Public: true})
	lines = nil
	logUnappliedVisibility(context.Background(), public, "t1", false, recordLines(&lines))
	if len(lines) != 0 {
		t.Errorf("an all-public tenant logged %q, want nothing", lines)
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — a public-mode
// registry refuses an identity provider, so its line says the declarations
// take effect once public mode is turned off and a provider is configured.
func TestLogUnappliedVisibility_PublicModeWording(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	seedTenantLayers(t, st, store.Tenant{ID: "t1"}, store.LayerConfig{ID: "team", Organization: true})
	var lines []string
	logUnappliedVisibility(context.Background(), st, "t1", true, recordLines(&lines))
	want := "this registry runs in public mode and applies no layer visibility, so every caller sees them; their declarations take effect once public mode is turned off and an identity provider is configured: team"
	if len(lines) != 1 || !strings.Contains(lines[0], want) {
		t.Errorf("logged %q, want one line containing %q", lines, want)
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — the line reads the
// tenant the registry binds to alone. A registry that applies no visibility is
// single-tenant, so a layer of another tenant the store holds is served to no
// caller and is not listed.
func TestLogUnappliedVisibility_ListsTheBoundTenantAlone(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	seedTenantLayers(t, st, store.Tenant{ID: "id-acme", Name: "acme"}, store.LayerConfig{ID: "acme-team"})
	seedTenantLayers(t, st, store.Tenant{ID: "id-globex", Name: "globex"}, store.LayerConfig{ID: "globex-team", Groups: []string{"globex-eng"}})
	var lines []string
	logUnappliedVisibility(context.Background(), st, "id-acme", false, recordLines(&lines))
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "1 stored layer(s)") || !strings.HasSuffix(lines[0], ": acme-team") || strings.Contains(lines[0], "globex") {
		t.Errorf("line %q, want acme-team alone", lines[0])
	}
}

// Spec: §13.10 (Startup warning for unapplied visibility) — the line lists at
// most 20 IDs and summarizes the rest as "and N more".
func TestLogUnappliedVisibility_CapsTheList(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	var layers []store.LayerConfig
	for i := range unappliedVisibilityListCap + 5 {
		layers = append(layers, store.LayerConfig{ID: fmt.Sprintf("layer-%02d", i)})
	}
	seedTenantLayers(t, st, store.Tenant{ID: "t1"}, layers...)
	var lines []string
	logUnappliedVisibility(context.Background(), st, "t1", false, recordLines(&lines))
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	line := lines[0]
	if !strings.Contains(line, "25 stored layer(s)") || !strings.HasSuffix(line, "layer-19, and 5 more") {
		t.Errorf("line %q, want 25 counted and the list ending at layer-19, and 5 more", line)
	}
	if strings.Contains(line, "layer-20") {
		t.Errorf("line %q lists an ID past the cap", line)
	}
}

// failingLayerStore fails the layer list the startup warning reads.
type failingLayerStore struct {
	store.Store
	err error
}

func (f failingLayerStore) ListLayerConfigs(context.Context, string) ([]store.LayerConfig, error) {
	return nil, f.err
}

// Spec: §13.10 (Startup warning for unapplied visibility) — the warning
// refuses nothing, so a store read that fails is logged as a warning and
// the function returns.
func TestLogUnappliedVisibility_StoreFailureIsLogged(t *testing.T) {
	t.Parallel()
	st := failingLayerStore{Store: store.NewMemory(), err: errors.New("store offline")}
	var lines []string
	logUnappliedVisibility(context.Background(), st, "t1", false, recordLines(&lines))
	if len(lines) != 1 || !strings.Contains(lines[0], "could not list the stored layers") || !strings.Contains(lines[0], "store offline") {
		t.Errorf("logged %q, want one line reporting the store failure", lines)
	}
}
